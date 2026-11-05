// Command yogod 是键盘的控制程序。
//
// 用法：
//
//	yogod probe            列出 HID 接口，并对厂商口握手一次
//	yogod status           打印连接、电量、配置
//	yogod theme [id]       把某套主题推到键盘（不给 id 就列出全部）
//	yogod run              启动引擎，按当前场景持续驱动键盘
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/hid"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/sys"
	"github.com/wednesque/yogod/internal/theme"
)

// version 是发布版本号。zip 文件名、控制台页脚、/api/state 都用它 ——
// 改版本只动这一处。
const version = "1.0.3"

func main() {
	// 双击启动（没有任何参数）等价于 `yogod serve`：
	// 后台常驻 + 托盘图标 + 打开控制台,这是普通用户唯一该用的入口。
	args := os.Args[1:]
	attached := false
	if len(args) == 0 {
		args = []string{"serve"}
	} else if args[0] != "hook" {
		// 终端里敲的子命令需要把输出接回父控制台（windowsgui 程序天生没有）。
		// hook 是 AI 工具在后台静默调起的,绝不能去抢它宿主的控制台。
		attached = attachConsole()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	switch args[0] {
	case "probe":
		err = cmdProbe(ctx)
	case "status":
		err = cmdStatus(ctx)
	case "theme":
		err = cmdTheme(ctx, args[1:])
	case "run":
		err = cmdRun(ctx)
	case "bench":
		err = cmdBench(ctx, args[1:])
	case "hook":
		err = cmdHook(args[1:])
	case "serve":
		addr := defaultAddr()
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			addr = args[1]
		}
		err = runServe(addr, hasFlag("-no-tray"), hasFlag("-no-console"))
	case "-v", "--version", "version":
		fmt.Println("yogod", version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", args[0])
		usage()
		os.Exit(2)
	}
	if err != nil {
		if !attached {
			// 没有控制台可看（双击启动的 serve）:弹系统错误框,
			// 绝不能「双击没反应」地死掉。
			sys.Alert("yogod 启动失败", err.Error())
		}
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "yogod v%s —— ATK YOGO 75 PRO 点阵屏与背光控制\n\n", version)
	fmt.Fprint(os.Stderr, `  （双击 yogod.exe）      后台常驻：托盘图标 + 控制台，等价于 yogod serve
  yogod probe            列出 HID 接口，并对厂商口握手一次
  yogod status           打印连接、电量、配置
  yogod theme [id]       推送主题（不给 id 就列出全部）
  yogod run              启动引擎持续驱动键盘（前台，Ctrl+C 退出）
  yogod bench            链路压力测试：爬坡帧率，找 2.4G/有线的真实极限
  yogod serve [addr]     托盘 + 本地控制台（固定 127.0.0.1:25264）
  yogod hook             AI 工具的 hook 适配器（读 stdin，见 yogod hook --help）

默认端口固定为 25264，被别的程序占用会直接报错，不再顺延。
确需换端口时显式指定（hook 要相应设置 --url 或 YOGO_COMPANION_URL）：
  set YOGO_PORT=8899          (PowerShell: $env:YOGO_PORT='8899')
  yogod serve 127.0.0.1:8899
`)
}

// defaultAddr 是控制台的默认监听地址。
//
// 端口固定 25264：选一个不常见的高位端口，避免和常见本机服务撞车。
// YOGO_PORT 只作调试覆盖，正常不需要动。
func defaultAddr() string {
	port := strings.TrimSpace(os.Getenv("YOGO_PORT"))
	if port == "" {
		port = "25264"
	}
	return "127.0.0.1:" + port
}

// cmdProbe 是排查用的：能分清"键盘不回包"和"程序写错了"。
func cmdProbe(ctx context.Context) error {
	all, err := hid.Enumerate()
	if err != nil {
		return err
	}
	fmt.Printf("枚举到 %d 个 HID 接口\n\n", len(all))
	for _, it := range all {
		mark := "  "
		if it.VID == hid.VID {
			mark = ">>"
		}
		fmt.Printf("%s vid=%04X pid=%04X page=%04X usage=%04X rx=%d tx=%d  %q\n",
			mark, it.VID, it.PID, it.UsagePage, it.Usage, it.RxLen, it.TxLen, it.Product)
		fmt.Printf("      %s\n", it.Path)
	}

	vendor, err := hid.VendorInterfaces()
	if err != nil {
		return err
	}
	fmt.Printf("\n厂商通道口：%d 个\n", len(vendor))
	if len(vendor) == 0 {
		fmt.Println("没有找到 ATK 厂商口（VID 373B, FF60/61）。")
		fmt.Println("检查：键盘是否开机、是否在 2.4G 档、接收器是否插好。")
		return nil
	}

	c, err := device.Open(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	st := c.Status()
	fmt.Printf("已打开：%q  有线=%v  应答=%v\n", st.Product, st.Wired, st.Awake)
	if !st.Awake {
		fmt.Println("键盘没应答（按一下任意键唤醒）——", st.Error)
		return nil
	}
	cfg, err := c.ReadConfig(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("配置读到 %d 字节：blMode=%d dotMode=%d dotBright=%d 休眠=%d 分钟\n",
		len(cfg), cfg.BLMode(), cfg.DotMode(), cfg.DotBright(), cfg.SleepMinutes())
	return nil
}

func cmdStatus(ctx context.Context) error {
	c, err := device.Open(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	st := c.Status()
	fmt.Printf("产品      %q\n", st.Product)
	fmt.Printf("连接      %v\n", st.Connected)
	fmt.Printf("应答      %v\n", st.Awake)
	fmt.Printf("链路      %s\n", linkName(st.Wired))
	if st.Power != nil {
		fmt.Printf("电量      %d%%%s\n", st.Power.Percent, chargingName(st.Power.Charging))
	}
	if cfg, ok := c.Config(); ok {
		fmt.Printf("背光模式  %d\n", cfg.BLMode())
		fmt.Printf("点阵模式  %d（亮度 %d）\n", cfg.DotMode(), cfg.DotBright())
	}
	if st.Error != "" {
		fmt.Printf("最近错误  %s\n", st.Error)
	}
	return nil
}

func linkName(wired bool) string {
	if wired {
		return "数据线（等于在充电）"
	}
	return "2.4G 接收器"
}

func chargingName(charging bool) string {
	if charging {
		return "，充电中"
	}
	return ""
}

func cmdTheme(ctx context.Context, args []string) error {
	all := theme.All()
	if len(args) == 0 {
		fmt.Printf("%d 套主题：\n", len(all))
		for _, t := range all {
			dyn := ""
			if t.Dynamic() {
				dyn = "  [动态]"
			}
			fmt.Printf("  %-10s %s%s\n", t.ID, t.Name, dyn)
		}
		return nil
	}

	id := args[0]
	t, ok := theme.ByID(id)
	if !ok {
		return fmt.Errorf("没有主题 %q（用 `yogod theme` 列出全部）", id)
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

	// 静态推送：点阵 + 键盘各一帧就够了，之后零流量。
	//
	// 两路都要先切到"接受推帧"的模式：
	//   点阵 → mode 6（自定义推帧），背光 → 254（逐键推流）。
	// 少写任何一个，对应的那一路会被固件直接忽略 —— 看上去就是"点了没反应"。
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
		return fmt.Errorf("切设备模式失败：%w", err)
	}
	// 用 *_At(0) 而不是 DotFrame()/KBFrames()：
	// 移植主题的键盘配色全部来自构图函数（LAYOUTS），
	// 字符画字段是空的 —— 直接读 KBFrames() 会推一块全黑的键盘。
	dot := t.DotAt(0).Encode(deviceCal(), 50, false)
	if _, err := c.SendMatrix(ctx, dot); err != nil {
		return err
	}
	keys := t.KBAt(0).Encode(deviceCal(), 70)
	if _, err := c.SendKeys(ctx, keys); err != nil {
		return err
	}
	fmt.Printf("已推送主题 %s（%s）\n", t.ID, t.Name)
	time.Sleep(200 * time.Millisecond)
	return nil
}

func cmdRun(ctx context.Context) error {
	return runEngine(ctx)
}

// hasFlag 判断命令行里有没有某个开关。
func hasFlag(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}
