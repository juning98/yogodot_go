package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
	"github.com/wednesque/yogod/internal/sys"
	"github.com/wednesque/yogod/internal/theme"
)

// runEngine 是阶段 3 的验收路径：
//
//	关掉浏览器，键盘照常按主题动；锁屏几秒内全灭；解锁自动恢复。
//
// 整条链路上没有任何 HTTP、没有任何浏览器进程。
func runEngine(ctx context.Context) error {
	ctrl, err := device.Open(ctx)
	if err != nil {
		return err
	}
	defer ctrl.Close()

	// 打字让路：最近 250ms 内按过键，非必要帧先等着
	ctrl.Typing = func() bool { return sys.RecentKeyInput(sys.TypingGuardMS) }
	if err := sys.StartKeyHook(); err != nil {
		logf("键盘钩子没装上，打字检测退回键鼠一起算：%v", err)
	}

	st := ctrl.Status()
	logf("已连接 %q（%s）应答=%v", st.Product, wiredName(st.Wired), st.Awake)

	id := "pikachu"
	if len(theme.All()) > 0 {
		id = theme.Default().ID
	}
	t, ok := theme.ByID(id)
	if !ok {
		return fmt.Errorf("默认主题 %q 不存在", id)
	}

	eng := engine.New(ctrl, engine.Options{
		Tick: time.Second / 30,
		Logf: logf,
	})

	// 省电闸门：锁屏 / 离开 / 低电量
	pm := &engine.PowerMonitor{
		LockFn: sys.Locked,
		IdleFn: func() float64 {
			return sys.IdleSeconds(engine.IdleBlankAfter.Seconds())
		},
		PowerFn: func() *device.Power {
			p, err := ctrl.Power(ctx, false)
			if err != nil {
				return nil
			}
			return p
		},
		Logf: logf,
	}
	eng.SetPower(pm)

	scene := engine.NewThemeScene(t, true, deviceCal())
	// liveness：连续 10 秒没真正写成功，就重进场景模式、重推当前画面。
	// 这是对旧版"键盘睡了页面还在空转"的直接修补。
	eng.SetScene(ctx, scene)
	eng.OnLiveness = func() {
		logf("重新对账：重写设备模式")
		ctrl.Invalidate()
		if err := eng.SetScene(ctx, scene); err != nil {
			logf("重进场景失败：%v", err)
		}
	}

	go pm.Run(ctx, 2*time.Second)
	go func() { _ = eng.Pump(ctx) }()
	go func() { _ = eng.Drain(ctx) }()
	go eng.WatchLiveness(ctx, 10*time.Second)

	logf("引擎已启动：主题 %s（%s）", t.ID, t.Name)
	<-ctx.Done()
	logf("退出：共写入 %d 帧，丢弃/去重 %d 帧", eng.Sent(), eng.Dropped())
	return nil
}

// logFile 非空时 logf 同时往文件里写一份 ——
// 托盘后台运行没有终端,排查问题全靠 ~/.yogo/yogod.log。
var logFile *os.File

// setupLogFile 打开日志文件（追加）;太大就先截断,不让它无限长。
func setupLogFile(dir string) {
	ensureDir(dir)
	p := filepath.Join(dir, "yogod.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		_ = os.Truncate(p, 0)
	}
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		logFile = f
	}
}

func closeLogFile() {
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

func logf(format string, args ...any) {
	line := fmt.Sprintf("%s  %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	if logFile != nil {
		fmt.Fprintln(logFile, line)
	}
	// windowsgui 且无控制台时,这句静默失败,不影响运行
	fmt.Println(line)
}

func wiredName(wired bool) string {
	if wired {
		return "数据线"
	}
	return "2.4G"
}
