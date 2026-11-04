package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/api"
	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/store"
	"github.com/wednesque/yogod/internal/sys"
	"github.com/wednesque/yogod/internal/theme"
)

// bindConsole 绑定控制台地址。端口固定，被占就直接报错，不再顺延 ——
// 端口一换，hook 和页面都得跟着找，反而更难排查。
func bindConsole(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("控制台端口 %s 用不了（可能已被别的程序占用）：%w　换一个端口：yogod serve 127.0.0.1:8899", addr, err)
	}
	return ln, nil
}

// app 是装配好的整套服务。
type app struct {
	dir  string
	st   *store.Settings
	dev  *device.Controller
	eng  *engine.Engine
	pomo *engine.Pomodoro
	ag   *agent.Store
	api  *api.Server

	mu          sync.Mutex
	sceneID     string
	themeScene  *engine.ThemeScene
	comp        *engine.CompanionScene
	cal         color.Calibration
	typingGuard atomic.Bool
}

// runServe 是 `yogod serve`：托盘 + 本地 API + 帧引擎，常驻。
func runServe(addr string, noTray, noConsole bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{dir: store.DefaultDir()}

	// 单实例：两个实例会抢同一把键盘，也会各开一个托盘图标
	mu, err := sys.AcquireSingleInstance("yogod")
	if err != nil {
		return err
	}
	if !mu.Acquired() {
		// 已经有一个实例在跑：双击又来一次不是错误,
		// 把老实例的控制台唤出来就行,悄悄退出。
		openConsole("http://" + readConsoleAddr(a.dir))
		return nil
	}
	defer mu.Close()

	// 托盘后台运行时没有终端,日志落到文件,排查「键盘不动」时用得上
	setupLogFile(a.dir)
	defer closeLogFile()

	// ── 端口先绑下来 ──
	// 绑不上就立刻失败：以前是丢到后台 goroutine 里 net.Listen，
	// 结果端口被占也照样往下跑 —— 引擎在推、托盘起不来、控制台打不开，
	// 变成一个既看不见又关不掉的进程。这是最糟的一种失败方式。
	ln, err := bindConsole(addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	writeConsoleAddr(a.dir, addr)

	// 子进程挂进 Job Object：父进程一退，内核负责清理，不用 PowerShell 去杀
	if job, err := sys.NewJob(); err == nil {
		_ = job.Assign()
		defer job.Close()
	}

	if a.st, err = store.Open(a.dir); err != nil {
		return err
	}
	a.cal = color.Calibration{
		Gain: a.st.Calib.Gain, Corr: a.st.Calib.Corr, Sat: a.st.Calib.Sat, EQL: a.st.Calib.EQL,
	}
	logf("配置目录 %s", a.dir)

	if a.dev, err = device.Open(ctx); err != nil {
		return err
	}
	defer a.dev.Close()
	// 打字保护可关：开着时敲键盘暂停推帧、停手 250ms 补发;
	// 关了动画不被打字打断（连接质量好才建议关，2.4G 下开着稳）。
	// 用 atomic 是因为引擎的写 goroutine 和 HTTP 的 POST 都会碰它。
	a.typingGuard.Store(a.st.TypingGuard)
	a.dev.Typing = func() bool {
		return a.typingGuard.Load() && sys.RecentKeyInput(sys.TypingGuardMS)
	}
	// 打字保护默认关，全局键盘钩子也只在保护开启时才装 ——
	// 一个默认不用的功能不该常驻全局钩子。打开保护时再装（幂等）;
	// 装不上就退回 GetLastInputInfo（那时鼠标也会算"在打字"）。
	if a.st.TypingGuard {
		a.startKeyHook()
	}
	// 推帧档位（默认/流畅/极速），热切换链路后依然生效
	a.dev.SetRateTier(device.RateTier(a.st.RateTier))

	st := a.dev.Status()
	logf("键盘 %q（%s）应答=%v", st.Product, wiredName(st.Wired), st.Awake)

	// ── 引擎 ──
	a.eng = engine.New(a.dev, engine.Options{Tick: time.Second / 30, Logf: logf})
	pm := &engine.PowerMonitor{
		LockFn: sys.Locked,
		IdleFn: func() float64 { return sys.IdleSeconds(engine.IdleBlankAfter.Seconds()) },
		PowerFn: func() *device.Power {
			p, err := a.dev.Power(ctx, false)
			if err != nil {
				return nil
			}
			return p
		},
		Logf: logf,
	}
	// 熄屏阈值来自配置（控制台"熄屏策略"页的输入）；0/空值回退包级默认。
	applyIdlePolicy := func(idle store.Idle) {
		pm.SetPolicy(engine.Policy{
			BlankAfter: time.Duration(idle.BlankSeconds) * time.Second,
			KeysOffPct: idle.KeysOffPercent,
			AllOffPct:  idle.AllOffPercent,
		})
	}
	a.st.View(func(st *store.Settings) { applyIdlePolicy(st.IdlePolicy) })
	a.eng.SetPower(pm)
	a.eng.OnLiveness = func() {
		logf("liveness：重写设备模式并重推画面")
		a.dev.Invalidate()
		a.mu.Lock()
		id := a.sceneID
		sc := a.themeScene
		a.mu.Unlock()
		if id == "theme" && sc != nil {
			_ = a.eng.SetScene(ctx, sc)
		}
	}

	// ── 伴侣与番茄钟 ──
	a.ag = agent.NewStore()
	a.pomo = engine.NewPomodoro(engine.DefaultPresets[0])
	comp := &engine.CompanionScene{Store: a.ag}
	comp.SetIdleAgent(a.st.IdleAgent)
	a.comp = comp

	// ── 场景切换 ──
	themeID := a.st.Theme
	if _, ok := theme.ByID(themeID); !ok {
		themeID = theme.Default().ID
	}
	a.themeScene = a.newThemeScene(themeID, a.st.Dynamic)
	// 伴侣只接管点阵：背光帧从主题场景透传，切进伴侣不丢键盘灯效。
	comp.SetKeys(a.themeScene)
	a.sceneID = a.st.Mode

	if err := a.switchScene(a.st.Mode, comp); err != nil {
		logf("进入初始场景失败：%v", err)
	}

	// ── API ──
	a.api = api.New(api.Deps{
		Engine:   a.eng,
		Device:   a.dev,
		Settings: a.st,
		Agent:    a.ag,
		Pomodoro: a.pomo,
		Console:  api.ConsoleFS(),
		Logf:     logf,
		SwitchScene: func(id string) error {
			if err := a.switchScene(id, comp); err != nil {
				return err
			}
			a.st.Update(func(st *store.Settings) { st.Mode = id })
			return a.st.Save()
		},
		SwitchTheme: func(id string, dynamic bool) error {
			// 页面点主题 = 明确要看这套：必须切进主题场景（含点阵）。
			return a.switchTheme(id, dynamic, true)
		},
		SetPattern: func(id string) error {
			return a.applyPattern(id)
		},
		SetKBEffect: a.setKBEffect,
		SetTypingGuard: func(on bool) {
			a.typingGuard.Store(on)
			if on {
				a.startKeyHook()
			}
		},
		SetRateTier: func(t int) {
			a.dev.SetRateTier(device.RateTier(t))
		},
		SetIdleAgent: func(id string) {
			comp.SetIdleAgent(id)
		},
		SetIdlePolicy: applyIdlePolicy,
		RebuildTheme: func() error {
			a.mu.Lock()
			sc := a.themeScene
			a.mu.Unlock()
			if sc == nil {
				return nil
			}
			return a.switchTheme(sc.Theme.ID, sc.Dynamic, false)
		},
		SetCalib: func(c store.Calibration) error {
			a.mu.Lock()
			a.cal = color.Calibration{Gain: c.Gain, Corr: c.Corr, Sat: c.Sat, EQL: c.EQL}
			sc := a.themeScene
			a.mu.Unlock()
			if sc == nil {
				return nil
			}
			return a.switchTheme(sc.Theme.ID, sc.Dynamic, false)
		},
		ExePath: exePath(),
		Version: version,
		Save:    func() error { return a.st.Save() },
	})

	// ── 后台循环 ──
	go pm.Run(ctx, 2*time.Second)
	go func() { _ = a.eng.Pump(ctx) }()
	go func() { _ = a.eng.Drain(ctx) }()
	go a.eng.WatchLiveness(ctx, 10*time.Second)
	go a.api.RunPreview(ctx, 100*time.Millisecond)
	go a.pomodoroLoop(ctx)
	go a.watchDevice(ctx, comp, pm)

	go func() {
		// Serve 只在监听器出错时返回；ctx 取消后关 ln 会让它正常退出
		if err := a.api.Serve(ln); err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
			logf("HTTP 服务退出：%v", err)
		}
	}()
	logf("控制台 http://%s", addr)
	if !noConsole {
		openConsole("http://" + addr)
	}

	// ── 托盘（阻塞主线程的消息循环）──
	if noTray {
		<-ctx.Done()
	} else {
		var tr *sys.Tray
		tr = &sys.Tray{
			Tip:      "yogod —— 点阵屏与背光",
			IconPath: iconPath(a.dir),
			Items: func() []sys.MenuItem {
				a.mu.Lock()
				cur := a.sceneID
				a.mu.Unlock()
				return []sys.MenuItem{
					{ID: 1, Label: "打开控制台"},
					{ID: 2, Label: "专注（全灭）", Checked: cur == "focus"},
					{ID: 3, Label: "番茄钟", Checked: cur == "pomodoro"},
					{ID: 4, Label: "Coding 伴侣", Checked: cur == "companion"},
					{Separator: true},
					{ID: 5, Label: "开机自启", Checked: sys.AutostartEnabled()},
					{Separator: true},
					{ID: 9, Label: "退出"},
				}
			},
			OnClick: func() { openConsole("http://" + addr) },
			OnMenu: func(id int) {
				switch id {
				case 1:
					openConsole("http://" + addr)
				case 2:
					_ = a.switchScene("focus", comp)
				case 3:
					_ = a.switchScene("pomodoro", comp)
				case 4:
					_ = a.switchScene("companion", comp)
				case 5:
					if err := sys.SetAutostart(!sys.AutostartEnabled()); err != nil {
						logf("开机自启切换失败：%v", err)
					}
				case 9:
					tr.Quit()
				}
			},
			OnExit: stop,
		}
		go func() { <-ctx.Done(); tr.Quit() }()
		if err := tr.Run(ctx); err != nil {
			logf("托盘不可用（%v），继续以无界面方式运行", err)
			<-ctx.Done()
		}
	}

	logf("退出：写入 %d 帧，丢弃/去重 %d 帧", a.eng.Sent(), a.eng.Dropped())
	return nil
}

// startKeyHook 装全局键盘钩子（只认按键、不认鼠标的打字检测）。
// 幂等：sys 内部已判断装没装过，重复调用直接返回。
func (a *app) startKeyHook() {
	if err := sys.StartKeyHook(); err != nil {
		logf("键盘钩子没装上，打字检测退回键鼠一起算：%v", err)
	}
}

// watchDevice 盯 HID 拓扑与链路健康。
//
// 键盘在有线/2.4G 之间切换时，旧接口从系统里消失、另一个接口后面才有键盘；
// 不跟过去，程序就捏着死句柄对着空气推帧（表现为"看着连接正常，键盘一动不动"）。
// 两路信号：
//
//   - 拓扑变化（接口出现/消失）→ 直接重新选口
//   - 链路报错/不应答 → 先探活，应了就当瞬时故障，不应才重新选口
//
// 注意不在"看起来健康"时主动探活：键盘自己的 900 秒休眠不该被我们戳醒。
// reconcileEvery 是配置对账周期：一分钟一次，只读不写（漂移了才重推）。
const reconcileEvery = 60 * time.Second

func (a *app) watchDevice(ctx context.Context, comp *engine.CompanionScene, pm *engine.PowerMonitor) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	known, _ := device.VendorPaths()
	lastTry := time.Time{}
	lastReconcile := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		a.reconcileConfigIfDue(ctx, comp, pm, &lastReconcile)

		cur, err := device.VendorPaths()
		topoChanged := err == nil && !sameStrings(cur, known)
		if topoChanged {
			known = cur
		}
		st := a.dev.Status()
		suspect := st.Error != "" || !st.Awake
		if !topoChanged && !suspect {
			continue
		}

		// 纯嫌疑先探活：应了就没事（回包成功会顺手清掉 Error）；
		// 应了但 awake 还是 false，说明设备之前不应答、刚回来，要重推画面。
		if suspect && !topoChanged {
			if _, err := a.dev.Power(ctx, true); err == nil {
				if !st.Awake {
					logf("键盘恢复应答（%s）", wiredName(a.dev.Wired()))
					a.dev.Invalidate()
					a.reenterScene(comp)
					a.api.Broadcast()
				}
				continue
			}
		}

		if time.Since(lastTry) < 8*time.Second {
			continue
		}
		lastTry = time.Now()
		oldWired, oldProduct := a.dev.Wired(), a.dev.Product()
		if err := a.dev.Reopen(ctx); err != nil {
			continue
		}
		if a.dev.Wired() != oldWired || a.dev.Product() != oldProduct {
			logf("链路切换：%s → %s（%q）",
				wiredName(oldWired), wiredName(a.dev.Wired()), a.dev.Product())
		}
		if a.dev.Awake() {
			a.dev.Invalidate()
			a.reenterScene(comp)
		}
		a.api.Broadcast()
	}
}

// reconcileConfigIfDue 慢速配置对账。
//
// 固件可能自己退回原厂模式（休眠、Fn 键切灯效、被官方 HUB 抢），
// 那时推帧照样"写成功"但进了关闭的通道 —— 只有回读配置能发现漂移。
// 修复失联误报之前，这件事是靠误报每 11 秒重写配置"歪打正着"兜底的；
// 误报修掉之后，用一分钟一次的只读对账把它正确地补上。
func (a *app) reconcileConfigIfDue(ctx context.Context, comp *engine.CompanionScene, pm *engine.PowerMonitor, last *time.Time) {
	if time.Since(*last) < reconcileEvery {
		return
	}
	*last = time.Now()

	a.mu.Lock()
	id := a.sceneID
	sc := a.themeScene
	a.mu.Unlock()
	if id != "theme" && id != "" {
		return // 别的场景有自己的配置语义（专注会写 blMode=0）
	}
	if sc == nil || !a.dev.Awake() {
		return
	}
	if pm != nil && pm.Action() != engine.KeepOn {
		return // 熄屏期间键盘显示的是我们推的黑帧，对账无意义还可能戳醒它
	}

	wantBL := expectedBLMode(sc)
	cfg, err := a.dev.ReadConfig(ctx)
	if err != nil {
		return // 读不到由上面的快路径（探活/重选口）去管
	}
	if cfg.DotMode() != proto.DotModeCustom || cfg.BLMode() != wantBL {
		logf("设备模式漂移（blMode=%d 期望 %d，dotMode=%d），重新对账",
			cfg.BLMode(), wantBL, cfg.DotMode())
		a.dev.Invalidate()
		a.reenterScene(comp)
	}
}

// expectedBLMode 是主题场景期望的背光模式值：选了原厂灯效就是效果号，
// 否则是逐键推流（254）。
func expectedBLMode(sc *engine.ThemeScene) byte {
	if sc != nil && sc.KBFirmware != nil {
		return byte(sc.KBFirmware.ID)
	}
	return proto.BLModeStream
}

// reenterScene 在链路恢复后重进当前场景：重写设备模式、重推当前画面。
func (a *app) reenterScene(comp *engine.CompanionScene) {
	a.mu.Lock()
	id := a.sceneID
	a.mu.Unlock()
	if err := a.switchScene(id, comp); err != nil {
		logf("链路恢复后重进场景失败：%v", err)
	}
}

// sameStrings 按集合语义比较两个字符串切片（拓扑对比不看顺序）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[s]++
	}
	for _, s := range b {
		if set[s] == 0 {
			return false
		}
		set[s]--
	}
	return true
}

// switchScene 是所有场景切换的唯一入口。
func (a *app) switchScene(id string, comp *engine.CompanionScene) error {
	// 先校验再动记账：未知 id 或引擎切换失败都不能把 sceneID 弄脏，
	// 否则托盘勾选项和 /api/state 撒谎。
	switch id {
	case "theme", "", "focus", "pomodoro", "companion":
	default:
		return fmt.Errorf("未知场景 %q", id)
	}

	a.mu.Lock()
	sc := a.themeScene
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var err error
	switch id {
	case "theme", "":
		if sc == nil {
			sc = a.newThemeScene(theme.Default().ID, true)
		}
		err = a.eng.SetScene(ctx, sc)
	case "focus":
		err = a.eng.SetScene(ctx, &engine.FocusScene{})
	case "pomodoro":
		err = a.eng.SetScene(ctx, a.pomo)
	case "companion":
		err = a.eng.SetScene(ctx, comp)
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.sceneID = id
	a.mu.Unlock()
	if a.api != nil {
		a.api.SetSceneState(id)
	}
	return nil
}

// switchTheme 重建主题场景。亮度、校准变了也走这里。
//
// enter 区分两种意图：页面点主题（enter=true）是"我要看这个主题"，
// 必须把引擎切进主题场景；内部重建（换灯效/点图案/伴侣透传跟随）
// 只换素材，不打断用户正在看的场景。
// 旧 bug：enter 语义混在 active 里 —— 场景不在 theme 时只把 sceneID
// 记成 theme 却不切引擎，点阵要点第二下才动。
func (a *app) switchTheme(id string, dynamic bool, enter bool) error {
	if _, ok := theme.ByID(id); !ok {
		return fmt.Errorf("没有主题 %q", id)
	}
	sc := a.newThemeScene(id, dynamic)

	a.mu.Lock()
	a.themeScene = sc
	if a.comp != nil {
		a.comp.SetKeys(sc) // 伴侣透传的背光跟着新主题走
	}
	if enter {
		a.sceneID = "theme"
	}
	active := a.sceneID == "theme" || a.sceneID == ""
	a.mu.Unlock()

	if !active {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := a.eng.SetScene(ctx, sc); err != nil {
		return err
	}
	a.api.SetSceneState("theme")
	return nil
}

func (a *app) newThemeScene(id string, dynamic bool) *engine.ThemeScene {
	t, ok := theme.ByID(id)
	if !ok {
		t = theme.Default()
	}
	// 配置读取全部在锁内：本函数会被 HTTP handler 的 goroutine 并发调
	// （switchTheme / RebuildTheme / SetCalib），裸读 Settings 是数据竞争。
	a.mu.Lock()
	cal := a.cal
	a.mu.Unlock()
	sc := engine.NewThemeScene(t, dynamic, cal)
	sc.KBLight = t.Light
	a.st.View(func(st *store.Settings) {
		sc.DotBright = st.Bright.Dot
		sc.KBBright = st.Bright.Keys
		sc.ApplyDotCalib = st.Calib.DotCalib
		// 图案库单选：非空时点阵显示这张图案，背光仍走主题。
		if st.Pattern != "" {
			if p, ok := theme.PatternByID(st.Pattern); ok {
				pp := p
				sc.Pattern = &pp
			}
		}
		// 选了原厂灯效就别推逐键配色：背光交给键盘自己的芯片，我们只推点阵。
		if st.KBEffect != nil {
			if eff, ok := device.EffectByID(*st.KBEffect); ok {
				sc.KBFirmware = &eff
			}
		}
	})
	return sc
}

// applyPattern 应用图案库单选（"" 还原主题默认点阵）。
// 场景不在主题页时只更新配置，回到主题时自然带上。
func (a *app) applyPattern(id string) error {
	a.mu.Lock()
	a.st.Update(func(st *store.Settings) { st.Pattern = id })
	sc := a.themeScene
	a.mu.Unlock()
	if sc == nil {
		return nil
	}
	return a.switchTheme(sc.Theme.ID, sc.Dynamic, false)
}

// setKBEffect 切换原厂键盘灯效。id < 0 表示回到主题的逐键配色。
func (a *app) setKBEffect(id int) error {
	a.mu.Lock()
	a.st.Update(func(st *store.Settings) {
		if id < 0 {
			st.KBEffect = nil
		} else {
			n := id
			st.KBEffect = &n
		}
	})
	var sc *engine.ThemeScene
	sc = a.themeScene
	a.mu.Unlock()

	if err := a.st.Save(); err != nil {
		logf("保存配置失败：%v", err)
	}
	if sc == nil {
		return nil
	}
	return a.switchTheme(sc.Theme.ID, sc.Dynamic, false)
}

// pomodoroLoop 每秒推进一次番茄钟。
//
// 阶段切换时自动把场景切到番茄钟 —— 到点了你总得看见，
// 旧版在别的页面上会静默走完 30 秒提醒，等于没提醒。
func (a *app) pomodoroLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !a.pomo.Advance(time.Now()) {
				continue
			}
			// 阶段变了就要推状态：页面时钟、阶段文案、按钮全靠 SSE 驱动，
			// 只在切场景时广播的话，页面时钟会冻在 00:00。
			if a.pomo.IsAlerting() {
				a.mu.Lock()
				cur := a.sceneID
				a.mu.Unlock()
				if cur != "pomodoro" {
					_ = a.switchScene("pomodoro", a.comp)
				}
			}
			a.api.Broadcast()
		}
	}
}

func openConsole(url string) {
	_ = sys.OpenBrowser(url)
}

// iconPath 返回托盘图标路径：内嵌图标落盘到数据目录,保证有 logo。
func iconPath(dir string) string {
	return ensureIcon(dir)
}

// exePath 是自己的绝对路径（正斜杠）。控制台把它写进给 Agent 的接入指令，
// hook 配置里的命令也得指向这个 exe。
func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "yogod.exe"
	}
	return filepath.ToSlash(exe)
}
