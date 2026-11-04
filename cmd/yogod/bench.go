package main

import (
	"context"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/store"
)

// bench 是链路压力测试：绕过限速，按阶梯帧率往键盘灌帧，
// 找出 2.4G 与有线各自的真实承受力。
// 报告同时写 stdout 和 yogod.log —— ConPTY 终端里 stdout 可能接不回去，
// 文件（exe 旁边的 yogod.log）兜底保证结果一定看得到。
//
// 用法：
//
//	yogod bench            键灯 + 点阵都测
//	yogod bench -keys      只测键灯（cmd 46 推流）
//	yogod bench -matrix    只测点阵（cmd 59 推流）
//	yogod bench -sec 5     每档跑 5 秒（默认 3 秒）
//
// 跑之前必须先退出常驻的 yogod（托盘右键 → 退出），
// 否则两个进程一起推帧，数据没意义。
//
// 测完会恢复键盘原有配置。哪一档开始撕裂/定格/按键发飘，
// 肉眼观察比数据更直接 —— 跑的时候请看着键盘。

var benchKeysLadder = []float64{2, 4, 6, 8, 10, 12, 15, 20, 25, 30}
var benchMatrixLadder = []float64{4, 6, 8, 10, 12, 15}

func cmdBench(ctx context.Context, args []string) error {
	sec := 3
	testKeys, testMatrix := true, true
	for _, a := range args {
		switch {
		case a == "-keys":
			testMatrix = false
		case a == "-matrix":
			testKeys = false
		case len(a) > 5 && a[:5] == "-sec=":
			if v, err := fmt.Sscanf(a[5:], "%d", &sec); v != 1 || err != nil {
				return fmt.Errorf("-sec= 后面要跟秒数，比如 -sec=5")
			}
		default:
			return fmt.Errorf("未知参数 %q（支持 -keys / -matrix / -sec=N）", a)
		}
	}
	if sec < 1 || sec > 30 {
		return fmt.Errorf("每档秒数应在 1..30，实际 %d", sec)
	}

	// 报告用 logf 双写 stdout + yogod.log：
	// windowsgui 程序在 ConPTY 终端（Windows Terminal / 各类集成终端）里
	// stdout 接不回父终端（AttachConsole 对伪终端无效），文件兜底保证结果一定看得到。
	setupLogFile(store.DefaultDir())
	defer closeLogFile()

	// 常驻实例会先占着设备推帧，必须让它先退。
	// console.addr 可能是死了的实例留下的，所以以"端口真的通"为准。
	addr := readConsoleAddr(store.DefaultDir())
	if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		conn.Close()
		err := fmt.Errorf("有 yogod 正在 %s 运行，会同时推帧干扰测量。\n请先在托盘里退出它（右键图标 → 退出），再跑 bench", addr)
		logf("错误：%v", err)
		return err
	}

	c, err := device.Open(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if !c.Awake() {
		if _, err := c.ReadConfig(ctx); err != nil {
			return fmt.Errorf("键盘没应答：%w", err)
		}
	}

	link := "2.4G 接收器"
	if c.Wired() {
		link = "数据线（有线）"
	}
	logf("链路：%s　产品：%q", link, c.Product())
	if !c.Wired() {
		logf("注意：2.4G 下高档位可能挤掉按键上报 —— 爬坡时请同时打几个字，")
		logf("按键开始发飘的那一档就是这条链路的实用上限。")
	}
	logf("每档 %d 秒。测试期间请看键盘：哪一档开始撕裂/定格，就是肉眼上限。", sec)

	// 备份原配置，测试结束原样写回（Ctrl+C 也回写，用全新的 ctx）。
	orig, err := c.ReadConfig(ctx)
	if err != nil {
		return fmt.Errorf("读配置失败：%w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := c.WriteConfig(rctx, orig); err != nil {
			logf("恢复原配置失败（%v）—— 请开关一次键盘或重跑 yogod theme 恢复", err)
			return
		}
		logf("已恢复键盘原配置。")
	}()

	// 切到推流模式：背光 254 逐键推流，点阵 6 自定义推帧。
	patch := proto.Patch{
		proto.OffBLMode:     proto.BLModeStream,
		proto.OffBLBright:   90,
		proto.OffBLColorful: 0,
	}
	for k, v := range proto.DotModePatch(
		proto.DotModeCustom, u8(90), u8(3), nil, nil, nil) {
		patch[k] = v
	}
	if _, err := c.PatchConfig(ctx, patch); err != nil {
		return fmt.Errorf("切推流模式失败：%w", err)
	}

	if testKeys {
		logf("── 键灯推流（cmd 46，168B/帧，7 包）──")
		runBenchLadder(ctx, c, benchKeysLadder, sec,
			func(fps float64, frames int) device.BenchStep {
				return c.BenchKeys(ctx, fps, frames, genBenchKeys)
			})
	}
	if testMatrix {
		logf("── 点阵推帧（cmd 59，108B/帧，5 包）──")
		runBenchLadder(ctx, c, benchMatrixLadder, sec,
			func(fps float64, frames int) device.BenchStep {
				return c.BenchMatrix(ctx, fps, frames, genBenchMatrix)
			})
	}
	return nil
}

// runBenchLadder 跑一个阶梯，任一档位出现写失败或探活失败就停止爬坡。
func runBenchLadder(ctx context.Context, c *device.Controller,
	ladder []float64, sec int, run func(fps float64, frames int) device.BenchStep) {

	for _, fps := range ladder {
		frames := int(fps * float64(sec))
		st := run(fps, frames)
		probe := "✓"
		if !st.ProbeOK {
			probe = "✗ 设备失联"
		}
		line := fmt.Sprintf("  %5.1f fps | 实发 %d/%d | 实测 %5.1f fps | 单帧 %s~%s 均值 %s | 探活 %s",
			fps, st.Frames, frames, st.ActualFPS(),
			st.MinWrite.Round(time.Millisecond), st.MaxWrite.Round(time.Millisecond),
			st.AvgWrite.Round(time.Millisecond), probe)
		if st.Failed > 0 {
			line += fmt.Sprintf(" | 写失败 %d", st.Failed)
		}
		logf("%s", line)
		if st.Failed > 0 || !st.ProbeOK {
			logf("  ↑ 这一档开始出问题，停止爬坡。")
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
}

// genBenchKeys 第 i 帧：8 键宽的亮条以每帧 1 键的速度扫过 84 键，
// 色相每帧转 3°。撕裂表现为亮条断成几截，定格表现为亮条停住。
func genBenchKeys(i int) frame.Keys {
	var k frame.Keys
	head := i % 84
	r, g, b := hsvRGB(float64((i*3)%360), 1, 1)
	for j := 0; j < 8; j++ {
		idx := (head + j) % 84
		scale := 1 - float64(j)/8
		v := rgb565LE(scaleByte(r, scale), scaleByte(g, scale), scaleByte(b, scale))
		k[idx*2] = byte(v)
		k[idx*2+1] = byte(v >> 8)
	}
	return k
}

// genBenchMatrix 第 i 帧：亮点绕 6×6 点阵逐格走，带两格拖尾，色相旋转。
func genBenchMatrix(i int) frame.Matrix {
	var m frame.Matrix
	r, g, b := hsvRGB(float64((i*5)%360), 1, 1)
	for j := 0; j < 3; j++ {
		pos := (i + 36 - j) % 36
		scale := 1 - float64(j)/3
		m[pos*3] = scaleByte(r, scale)
		m[pos*3+1] = scaleByte(g, scale)
		m[pos*3+2] = scaleByte(b, scale)
	}
	return m
}

func scaleByte(v byte, s float64) byte { return byte(float64(v) * s) }

// rgb565LE 把 RGB888 压成 RGB565（小端存放时的 uint16 值）。
func rgb565LE(r, g, b byte) uint16 {
	return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
}

// hsvRGB：h ∈ [0,360)，s、v ∈ [0,1]，返回 RGB888。
func hsvRGB(h, s, v float64) (byte, byte, byte) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return byte((r + m) * 255), byte((g + m) * 255), byte((b + m) * 255)
}
