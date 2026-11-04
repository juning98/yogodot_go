package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
)

// countingScene 每被取一帧就 +1，并把计数编码进帧里，
// 方便断言"引擎推进了几帧"和"设备最后收到哪一帧"。
type countingScene struct {
	n atomic.Int64
}

func (s *countingScene) Name() string { return "counting" }

func (s *countingScene) Frame(time.Duration) Render {
	var k frame.Keys
	// 直接把计数塞进第 0 字节，不走编码 —— 这是测试替身，
	// 目的只是让"设备最后收到哪一帧"可观测（编码本身在 color/frame 里另有测试）。
	k[0] = byte(s.n.Add(1))
	return Render{Keys: &k}
}

func (s *countingScene) Enter(context.Context, *device.Controller) error { return nil }
func (s *countingScene) Exit(context.Context, *device.Controller)        {}

// sceneFunc 用一个函数快速造一个 Scene。
type sceneFunc struct {
	name string
	f    func(t time.Duration) Render
}

func (s *sceneFunc) Name() string                                    { return s.name }
func (s *sceneFunc) Frame(t time.Duration) Render                    { return s.f(t) }
func (s *sceneFunc) Enter(context.Context, *device.Controller) error { return nil }
func (s *sceneFunc) Exit(context.Context, *device.Controller)        {}

// slowSink 是一个"写得很慢"的设备，用来证明帧泵不会被它拖住。
type slowSink struct {
	delay time.Duration
	mu    sync.Mutex
	seen  []byte // 每次收到帧的第一个字节（即场景计数）
}

func (s *slowSink) SendMatrix(context.Context, frame.Matrix) (device.Result, error) {
	time.Sleep(s.delay)
	return device.Result{Sent: true}, nil
}

func (s *slowSink) SendKeys(_ context.Context, k frame.Keys) (device.Result, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.seen = append(s.seen, k[0])
	s.mu.Unlock()
	return device.Result{Sent: true}, nil
}

func (s *slowSink) FlushPending(context.Context) {}

func (s *slowSink) frames() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.seen))
	copy(out, s.seen)
	return out
}

// TestPumpIsNeverBlockedBySlowIO 是整个重写的核心断言。
//
// 旧版：一次 fetch 悬挂 → busy 永久 true → 该场景再不出帧，且不报错。
// 新版：写设备慢 100ms 时，帧泵仍然按节拍推进 —— 只会掉帧，不会停钟。
func TestPumpIsNeverBlockedBySlowIO(t *testing.T) {
	scene := &countingScene{}
	sink := &slowSink{delay: 100 * time.Millisecond}

	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	e.scene = scene

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() { _ = e.Pump(ctx) }()
	go func() { _ = e.Drain(ctx) }()

	<-ctx.Done()
	got := scene.n.Load()

	// 200ms / 5ms = 40 个 tick。如果帧泵被 100ms 的写操作阻塞，
	// 最多只能推进 2 帧。这里要求至少推进一半。
	if got < 20 {
		t.Fatalf("200ms 内只推进了 %d 帧 —— 帧泵被 IO 阻塞了（旧版的老毛病）", got)
	}
	t.Logf("推进 %d 帧，写设备只有 %d 次（其余被 1 槽邮箱覆盖）",
		got, len(sink.frames()))
}

// TestMailboxKeepsLatest 确认邮箱保留的是"最新意图"而不是历史队列。
//
// 画面是状态不是队列：写设备慢的时候，补发一帧三秒前的画面毫无意义，
// 用户看到的应该是"现在"。
func TestMailboxKeepsLatest(t *testing.T) {
	scene := &countingScene{}
	sink := &slowSink{delay: 60 * time.Millisecond}

	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	e.scene = scene

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() { _ = e.Pump(ctx) }()
	go func() { _ = e.Drain(ctx) }()
	<-ctx.Done()

	frames := sink.frames()
	if len(frames) < 2 {
		t.Fatalf("只写出了 %d 帧，样本太少", len(frames))
	}
	skipped := false
	for i := 1; i < len(frames); i++ {
		if frames[i] <= frames[i-1] {
			t.Fatalf("帧顺序倒退：%v", frames)
		}
		if frames[i] > frames[i-1]+1 {
			skipped = true
		}
	}
	if !skipped {
		t.Log("没有跳号（写设备刚好跟得上），这也正常")
	}
	t.Logf("实际写出 %d 帧，最后一帧计数 %d（场景共推进 %d）",
		len(frames), frames[len(frames)-1], scene.n.Load())
}

// TestSceneClockResets 确认切换场景时本地时钟归零。
//
// 旧版的动画 t0 用 performance.now()，切模式再切回来时间已经跑了几分钟，
// 画面上就是"一进来就跳到了中途"。
func TestSceneClockResets(t *testing.T) {
	var mu sync.Mutex
	var times []time.Duration

	scene := &sceneFunc{
		name: "probe",
		f: func(t time.Duration) Render {
			mu.Lock()
			times = append(times, t)
			mu.Unlock()
			return Render{}
		},
	}

	e := NewWithSink(&slowSink{}, Options{Tick: 5 * time.Millisecond})
	e.scene = scene

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	done1 := make(chan struct{})
	go func() { defer close(done1); _ = e.Pump(ctx) }()
	<-ctx.Done()
	<-done1 // 必须等旧泵真的退出，否则它会在我们清零之后又追加一帧

	mu.Lock()
	if len(times) == 0 {
		mu.Unlock()
		t.Fatal("帧泵没有产出任何帧")
	}
	before := times[len(times)-1]
	times = nil
	mu.Unlock()
	if before <= 0 {
		t.Fatalf("时钟没有推进：%v", before)
	}

	// 踢一下 wake，模拟场景切换
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { defer close(done2); _ = e.Pump(ctx2) }()
	time.Sleep(15 * time.Millisecond)
	e.wake <- struct{}{}
	<-ctx2.Done()
	<-done2

	mu.Lock()
	defer mu.Unlock()
	if len(times) == 0 {
		t.Fatal("切换后没有产出帧")
	}
	// 归零之后应该出现一帧很小的 t
	minT := times[0]
	for _, v := range times {
		if v < minT {
			minT = v
		}
	}
	if minT > 20*time.Millisecond {
		t.Fatalf("场景切换后时钟没有归零：最小 t=%v（切换前跑到 %v）", minT, before)
	}
}

// dedupSink 永远回报"去重挡下" —— 模拟静态主题（动态关/静态图案）的
// 合法零流量：设备在正常裁决，只是画面没变。
type dedupSink struct{}

func (dedupSink) SendMatrix(context.Context, frame.Matrix) (device.Result, error) {
	return device.Result{Reason: "deduped"}, nil
}
func (dedupSink) SendKeys(context.Context, frame.Keys) (device.Result, error) {
	return device.Result{Reason: "deduped"}, nil
}
func (dedupSink) FlushPending(context.Context) {}

// 静态输出绝不能触发失联对账 —— 否则每 10 秒重写一次设备配置（日志里
// 4356 条"判定失联"就是这么来的：dedup 被误读成失联）。
func TestLivenessIgnoresDedupSilence(t *testing.T) {
	eng := NewWithSink(dedupSink{}, Options{})
	fired := make(chan struct{}, 4)
	eng.OnLiveness = func() { fired <- struct{}{} }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go eng.WatchLiveness(ctx, 120*time.Millisecond)

	// 持续喂静态帧（全部被 dedup）
	m := frame.Matrix{}
	k := frame.Keys{}
	for i := 0; i < 8; i++ {
		eng.write(ctx, Render{Matrix: &m, Keys: &k})
		time.Sleep(60 * time.Millisecond)
	}
	select {
	case <-fired:
		t.Fatal("dedup 静默被误判成失联")
	case <-time.After(400 * time.Millisecond):
	}
}

// 对照组：写持续报错（设备层真死）时，失联判定必须响。
func TestLivenessFiresOnWriteErrors(t *testing.T) {
	eng := NewWithSink(errSink{}, Options{})
	fired := make(chan struct{}, 4)
	eng.OnLiveness = func() { fired <- struct{}{} }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go eng.WatchLiveness(ctx, 120*time.Millisecond)

	m := frame.Matrix{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-fired:
			return // 期望的行为
		case <-deadline:
			t.Fatal("写持续报错却未触发失联判定")
		default:
			eng.write(ctx, Render{Matrix: &m})
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// errSink 每次都写失败 —— 模拟 HID 层真死（拔线、句柄失效）。
type errSink struct{ dedupSink }

func (errSink) SendMatrix(context.Context, frame.Matrix) (device.Result, error) {
	return device.Result{Reason: "error"}, errors.New("hid: WriteFile 失败")
}
func (errSink) SendKeys(context.Context, frame.Keys) (device.Result, error) {
	return device.Result{Reason: "error"}, errors.New("hid: WriteFile 失败")
}

// 熄屏期间 liveness 必须闭嘴：补黑 15 秒一帧，10 秒窗口必然误报
// （"连续 10s 写失败"刷屏的一半现场就是这么来的）。
func TestLivenessSkipsWhenBlanked(t *testing.T) {
	eng := NewWithSink(errSink{}, Options{})
	pm := &PowerMonitor{}
	pm.action = BlankAll
	eng.SetPower(pm)
	fired := make(chan struct{}, 1)
	eng.OnLiveness = func() { fired <- struct{}{} }

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go eng.WatchLiveness(ctx, 120*time.Millisecond)

	select {
	case <-fired:
		t.Fatal("熄屏期间不应触发失联判定")
	case <-time.After(500 * time.Millisecond):
	}
}

// TestPreviewPrefersFullBrightVariant 钉住"预览帧与设备帧分离"这条约定：
// 亮度只压设备，LastFrame 给页面的必须是满亮度版本（PrevMatrix/PrevKeys），
// 没有预览版本时才回落到设备帧（熄屏黑帧这类亮度无关的画面）。
func TestPreviewPrefersFullBrightVariant(t *testing.T) {
	var m, pm frame.Matrix
	m[0], pm[0] = 10, 20 // 设备帧被亮度压过，预览帧满亮度
	var k, pk frame.Keys
	k[0], pk[0] = 30, 40

	eng := NewWithSink(dedupSink{}, Options{})
	eng.write(context.Background(), Render{Matrix: &m, Keys: &k, PrevMatrix: &pm, PrevKeys: &pk})
	gm, gk, hasM, hasK := eng.LastFrame()
	if !hasM || !hasK {
		t.Fatal("LastFrame 应该有帧")
	}
	if gm[0] != 20 || gk[0] != 40 {
		t.Fatalf("预览应取满亮度版本，got m[0]=%d k[0]=%d", gm[0], gk[0])
	}

	eng2 := NewWithSink(dedupSink{}, Options{})
	eng2.write(context.Background(), Render{Matrix: &m, Keys: &k})
	gm2, gk2, _, _ := eng2.LastFrame()
	if gm2[0] != 10 || gk2[0] != 30 {
		t.Fatalf("无预览版本时应回落设备帧，got m[0]=%d k[0]=%d", gm2[0], gk2[0])
	}
}
