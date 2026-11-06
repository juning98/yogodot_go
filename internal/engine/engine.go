// Package engine 是帧引擎 —— 整个程序的心脏。
//
// 它解决的是旧架构最根本的那个问题：**时钟必须和 IO 解耦**。
//
//	旧版：浏览器 setTimeout ──HTTP──▶ Python ──HID──▶ 键盘
//	      一次请求悬挂 = 该场景永久停止出帧，而且不报错
//
//	新版：Ticker（永不被阻塞）──▶ 1 槽邮箱 ──▶ 写设备 goroutine ──▶ 键盘
//	      设备慢只会掉帧，不会停止出帧
//
// 场景只产出"这一帧想显示什么"，不认识 HID 也不认识 HTTP；
// 熄屏策略、liveness 对账都在这一层统一决策。
package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
)

// Render 是一帧的渲染结果。
//
// 字段为 nil 表示"这一帧不动它"——比如 Coding 伴侣模式只动点阵，
// 键盘背光保持当前主题不变。
type Render struct {
	Matrix *frame.Matrix
	Keys   *frame.Keys
	// PrevMatrix/PrevKeys 是给控制台预览的满亮度版本：亮度是设备端的事
	// （电流、灯珠、屏幕观感永远对不齐），页面只显示原始颜色。
	// nil 时预览复用 Matrix/Keys（熄屏黑帧这类亮度无关的画面）。
	PrevMatrix *frame.Matrix
	PrevKeys   *frame.Keys
}

// Scene 是场景。实现者只描述"这一刻显示什么"，不碰设备。
type Scene interface {
	// Name 是场景标识。
	Name() string
	// Frame 是纯函数：给定场景本地时钟，返回这一帧。
	Frame(t time.Duration) Render
	// Enter 放副作用：切点阵模式、写配置、点亮。
	// 必须带 ctx，超时就放弃 —— 绝不能让场景切换卡住引擎。
	Enter(ctx context.Context, ctrl *device.Controller) error
	// Exit 在离开场景时调用。
	Exit(ctx context.Context, ctrl *device.Controller)
}

// Options 是引擎的可调参数。
type Options struct {
	// Tick 是渲染节拍。30Hz 是为了让动画的时间分辨率平滑，
	// 不是为了让设备收 30 帧 —— 设备侧的限速在 device 层。
	Tick time.Duration
	// WriteTimeout 是单次写设备的超时。
	WriteTimeout time.Duration
	// OnLiveness 在判定设备失联时调用（用于重进模式）。
	OnLiveness func()
	// Logf 是日志出口。
	Logf func(format string, args ...any)
}

func (o *Options) fill() {
	if o.Tick <= 0 {
		o.Tick = time.Second / 30
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 300 * time.Millisecond
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// Sink 是引擎写设备的那一半。
//
// 抽成接口是为了让帧泵可以在没有键盘的机器上被测试 ——
// 旧版的引擎和 HID 焊死在一起，结果"卡死"这件事只能靠人肉复现。
type Sink interface {
	SendMatrix(ctx context.Context, m frame.Matrix) (device.Result, error)
	SendKeys(ctx context.Context, k frame.Keys) (device.Result, error)
	FlushPending(ctx context.Context)
}

// Engine 拥有场景与帧泵。
type Engine struct {
	opts Options
	dev  *device.Controller
	sink Sink

	// OnLiveness 在判定设备失联时调用。
	OnLiveness func()

	mu    sync.RWMutex
	scene Scene
	// transMu 串行化 SetScene 的 Exit/Enter 全程（并发切换会倒挂）。
	transMu sync.Mutex
	wake    chan struct{}
	power   *PowerMonitor

	// 最后一帧渲染结果，供控制台预览。
	lastMatrix frame.Matrix
	lastKeys   frame.Keys
	haveMatrix bool
	haveKeys   bool

	// out 是 1 槽邮箱：Drain 慢的时候新帧直接覆盖旧帧。
	out chan Render

	// 统计
	sent    atomic.Uint64
	dropped atomic.Uint64

	// lastAlive：最近一次设备层被证明活着（真正写出帧，或帧被正常
	// 去重/限速/打字让路）。静态输出是合法零流量，不算失联。
	lastSent atomic.Int64 // unix nano
}

// New 创建一个引擎。
func New(dev *device.Controller, opts Options) *Engine {
	e := newEngine(opts)
	e.dev = dev
	e.sink = dev
	return e
}

// NewWithSink 用一个假的 Sink 建引擎，供测试用（不需要真键盘）。
func NewWithSink(s Sink, opts Options) *Engine {
	e := newEngine(opts)
	e.sink = s
	return e
}

func newEngine(opts Options) *Engine {
	opts.fill()
	e := &Engine{
		opts: opts,
		wake: make(chan struct{}, 1),
		out:  make(chan Render, 1),
	}
	e.lastSent.Store(time.Now().UnixNano())
	return e
}

// SetScene 切换当前场景。会先 Exit 旧的、再 Enter 新的。
//
// 注意：Enter/Exit 是同步执行的（要写设备配置），调用方会被阻塞到配置写完。
// 这个调用只应该来自主流程或后台 goroutine，不要放在 HTTP handler 的请求路径上。
// 真正"切换即成帧"的部分是异步的：场景一换就把帧泵的时钟踢回零。
//
// 整个切换由 transMu 串行：HTTP、托盘菜单、番茄钟到点、链路恢复都会并发调这里，
// 不串行的话 Exit/Enter 可以倒挂 —— 最终 scene 是 B、设备模式却是 A 的。
func (e *Engine) SetScene(ctx context.Context, s Scene) error {
	e.transMu.Lock()
	defer e.transMu.Unlock()

	e.mu.Lock()
	old := e.scene
	e.scene = s
	e.mu.Unlock()

	if old != nil {
		old.Exit(ctx, e.dev)
	}
	if s != nil {
		if err := s.Enter(ctx, e.dev); err != nil {
			e.opts.Logf("场景 %s 进入失败：%v", s.Name(), err)
			// 回滚场景指针：Pump 继续用旧场景出帧，设备模式的半旧半新
			// 由下一次切换或 liveness 对账收拾。不回滚的话 API 报着错、
			// 引擎却拿新场景配旧模式渲染，是半切换状态。
			e.mu.Lock()
			e.scene = old
			e.mu.Unlock()
			return err
		}
		e.opts.Logf("场景 -> %s", s.Name())
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}

// Scene 返回当前场景。
func (e *Engine) Scene() Scene {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scene
}

// Sent 返回真正写进设备的帧数。
func (e *Engine) Sent() uint64 { return e.sent.Load() }

// Dropped 返回被限速/去重挡下的帧数。
func (e *Engine) Dropped() uint64 { return e.dropped.Load() }

// Pump 是主循环：时钟。它永远不被 IO 拖住。
//
// 每 tick 取一帧渲染结果，丢进 1 槽邮箱；邮箱满了就用新帧覆盖旧帧。
// 这样即使写设备卡了半秒，动画也不会"欠债"——
// 画面永远是最新意图，而不是排队等着补发的历史。
//
// 省电闸门在这里生效：熄屏时场景照常推进时钟（所以恢复时看到的是最新画面），
// 但输出被换成全黑帧，并且定期补黑。
func (e *Engine) Pump(ctx context.Context) error {
	ticker := time.NewTicker(e.opts.Tick)
	defer ticker.Stop()

	var clock time.Duration
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.wake:
			clock = 0 // 场景切换，本地时钟归零
		case now := <-ticker.C:
			clock += e.opts.Tick
			s := e.Scene()
			if s == nil {
				continue
			}

			var r Render
			act := KeepOn
			if e.power != nil {
				act = e.power.Action()
			}
			switch {
			case act == KeepOn:
				r = s.Frame(clock)
			case e.power.ShouldEmit(now):
				r = blankFor(act)
			default:
				continue // 熄着，且没到补黑时间：什么都不发
			}

			select {
			case e.out <- r:
			default:
				// 邮箱里还有没发出去的帧：覆盖它。
				select {
				case <-e.out:
				default:
				}
				select {
				case e.out <- r:
				default:
				}
				e.dropped.Add(1)
			}
		}
	}
}

// blankFor 构造一帧全黑。
//
// 熄屏一定是"推一帧全黑"，**不是停止推送**——
// 停推的话最后一帧会一直亮着，等于没省电（旧版第一版就栽在这）。
func blankFor(a Action) Render {
	var r Render
	if a != BlankKeys {
		m := frame.Matrix{}
		r.Matrix = &m
	}
	if a != BlankMatrix {
		k := frame.Keys{}
		r.Keys = &k
	}
	return r
}

// SetPower 挂上省电闸门。传 nil 表示不做熄屏。
func (e *Engine) SetPower(p *PowerMonitor) { e.power = p }

// Drain 是写设备循环。它慢就慢，只影响自己，不影响上面的时钟。
func (e *Engine) Drain(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-e.out:
			e.write(ctx, r)
			// 补发也属于设备 IO，必须与正常帧共用这个写循环。
			// 从 Pump 补发会阻塞时钟，并让两帧的 HID 分包交错。
			// 熄屏期间不补旧画面，避免覆盖黑帧。
			if e.power == nil || e.power.Action() == KeepOn {
				e.sink.FlushPending(ctx)
			}
		}
	}
}

func (e *Engine) write(ctx context.Context, r Render) {
	// 记下"最后一次真正渲染出来的画面"，供控制台预览。
	// 注意这是**渲染意图**，不是设备回读 —— 用来看"引擎在出什么帧"足够。
	// 预览优先用满亮度版本：亮度只压设备，不压页面。
	e.mu.Lock()
	if r.Matrix != nil {
		if r.PrevMatrix != nil {
			e.lastMatrix = *r.PrevMatrix
		} else {
			e.lastMatrix = *r.Matrix
		}
		e.haveMatrix = true
	}
	if r.Keys != nil {
		if r.PrevKeys != nil {
			e.lastKeys = *r.PrevKeys
		} else {
			e.lastKeys = *r.Keys
		}
		e.haveKeys = true
	}
	e.mu.Unlock()

	wctx, cancel := context.WithTimeout(ctx, e.opts.WriteTimeout)
	defer cancel()

	anySent := false
	// healthy：设备层正常裁决了这帧（发出/去重/限速/打字让路）——
	// 都说明链路活着、在收帧。静态主题（动态关 / 静态图案）全靠 dedup，
	// 失联判定如果把 dedup 当失联，会每 10 秒空对账一次。
	healthy := false
	if r.Keys != nil {
		res, err := e.sink.SendKeys(wctx, *r.Keys)
		if err != nil {
			e.opts.Logf("写键盘灯失败：%v", err)
		} else if res.Reason == "" || res.Reason == "deduped" || res.Reason == "throttled" || res.Reason == "typing" {
			healthy = true
		}
		if res.Sent {
			anySent = true
		}
	}
	if r.Matrix != nil {
		res, err := e.sink.SendMatrix(wctx, *r.Matrix)
		if err != nil {
			e.opts.Logf("写点阵失败：%v", err)
		} else if res.Reason == "" || res.Reason == "deduped" || res.Reason == "throttled" || res.Reason == "typing" {
			healthy = true
		}
		if res.Sent {
			anySent = true
		}
	}
	if anySent {
		e.sent.Add(1)
		e.lastSent.Store(time.Now().UnixNano())
	} else if healthy {
		e.lastSent.Store(time.Now().UnixNano())
	} else {
		e.dropped.Add(1)
	}
}

// WatchLiveness 定期检查"设备层是否还活着"。
//
// 这是对写失败（HID 层报错、持续被断开拒绝）的后备判定：
// 连续 N 秒没有任何一次正常裁决，就触发 OnLiveness 重进场景重推画面。
// 注意 dedup/throttle/typing 都算活着 —— 它们证明设备在正常收帧，
// 静态主题（动态关、静态图案）就是靠 dedup 零流量运行的。
func (e *Engine) WatchLiveness(ctx context.Context, after time.Duration) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 熄屏期间不判：补黑 15 秒一次、且本就是要省电，
			// 用 10 秒的写成功窗口去量它必然误报。
			// 顺手把时间戳刷到当前，恢复后从一个干净的窗口重新算。
			if e.power != nil && e.power.Action() != KeepOn {
				e.lastSent.Store(time.Now().UnixNano())
				continue
			}
			last := time.Unix(0, e.lastSent.Load())
			if time.Since(last) > after {
				e.opts.Logf("连续 %s 写失败，判定链路异常，重新对账", time.Since(last).Round(time.Second))
				e.lastSent.Store(time.Now().UnixNano()) // 重置，别刷屏
				switch {
				case e.OnLiveness != nil:
					e.OnLiveness()
				case e.opts.OnLiveness != nil:
					e.opts.OnLiveness()
				}
			}
		}
	}
}

// LastFrame 返回最近一次渲染出的点阵与键盘帧，供控制台做实时预览。
//
// 返回的是"引擎想显示什么"，不是"设备回读到什么" —— 预览要的就是前者。
func (e *Engine) LastFrame() (matrix frame.Matrix, keys frame.Keys, hasMatrix, hasKeys bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastMatrix, e.lastKeys, e.haveMatrix, e.haveKeys
}
