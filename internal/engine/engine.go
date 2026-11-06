// Package engine 分离画面时钟、输出策略和设备 IO。
package engine

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
)

type Channels struct{ Matrix, Keys bool }
type Picture struct {
	Matrix *frame.Grid
	Keys   *frame.LEDs
}
type Scene interface {
	Name() string
	Frame(time.Duration, Channels) Picture
	Animation() Channels
	Mode() device.Mode
}

// Output 是所有场景共用的电气输出参数；预览始终使用原色。
type Output struct {
	Cal                 color.Calibration
	DotBright, KBBright float64
	DotCalib            bool
	Enabled             Channels
	Rate                device.RateTier
}

func DefaultOutput() Output {
	return Output{Cal: color.DefaultCalibration(), DotBright: 50, KBBright: 70, Enabled: Channels{true, true}}
}

type Options struct {
	Tick, WriteTimeout time.Duration
	Typing             func() bool
}
type Sink interface {
	ApplyMode(context.Context, device.Mode) error
	SendMatrix(context.Context, frame.Matrix) error
	SendKeys(context.Context, frame.Keys) error
}
type rendered struct {
	mode                 device.Mode
	matrix               frame.Matrix
	keys                 frame.Keys
	haveMatrix, haveKeys bool
	rev                  uint64
	rate                 device.RateTier
}

// OutputStatus 区分已接受的设置与设备实际完成的设置，不伪装同步成功。
type OutputStatus struct {
	Pending bool   `json:"pending"`
	Error   string `json:"error"`
}
type Engine struct {
	opts                 Options
	sink                 Sink
	mu                   sync.RWMutex
	scene                Scene
	epoch                time.Time
	revision             uint64
	output               Output
	power                *PowerMonitor
	lastMatrix           frame.Matrix
	lastKeys             frame.Keys
	haveMatrix, haveKeys bool
	status               OutputStatus
	wake, ready          chan struct{}
	latest               atomic.Pointer[rendered]
}

func New(dev *device.Controller, opts Options) *Engine { return NewWithSink(dev, opts) }
func NewWithSink(s Sink, opts Options) *Engine {
	if opts.Tick <= 0 {
		opts.Tick = time.Second / 30
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 3 * time.Second
	}

	return &Engine{sink: s, opts: opts, output: DefaultOutput(), wake: make(chan struct{}, 1), ready: make(chan struct{}, 1)}
}
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
func (e *Engine) Notify() {
	e.mu.Lock()
	e.revision++
	e.status.Pending = true
	e.mu.Unlock()
	signal(e.wake)
}
func (e *Engine) SetOutput(o Output) {
	e.mu.Lock()
	e.output = o
	e.revision++
	e.status.Pending = true
	e.mu.Unlock()
	signal(e.wake)
}
func (e *Engine) SetScene(ctx context.Context, s Scene) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.scene == nil || s == nil || e.scene.Name() != s.Name() {
		e.epoch = time.Now()
	}
	e.scene = s
	e.revision++
	e.status.Pending = true
	e.mu.Unlock()
	signal(e.wake)
	return nil
}
func (e *Engine) Scene() Scene         { e.mu.RLock(); defer e.mu.RUnlock(); return e.scene }
func (e *Engine) Status() OutputStatus { e.mu.RLock(); defer e.mu.RUnlock(); return e.status }
func (e *Engine) SetPower(p *PowerMonitor) {
	e.mu.Lock()
	e.power = p
	e.mu.Unlock()
	if p != nil {
		p.OnChange = e.Notify
	}
}

// Pump 只读取内存。静态/关闭的通道不重复渲染；两路均静态时停止动画定时器。
func (e *Engine) Pump(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var prev rendered
	var prevMode device.Mode
	var revision uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.wake:
		case <-timer.C:
		}
		e.mu.RLock()
		s, epoch, rev, o, p := e.scene, e.epoch, e.revision, e.output, e.power
		e.mu.RUnlock()
		if s == nil {
			continue
		}
		mode := s.Mode()
		anim := s.Animation()
		mode.Matrix = mode.Matrix && o.Enabled.Matrix && o.DotBright > 0
		mode.Keys = mode.Keys && o.Enabled.Keys && o.KBBright > 0
		if mode.Effect >= 0 {
			mode.Brightness = byte(o.KBBright)
		}
		if p != nil {
			switch p.Action() {
			case BlankAll:
				mode.Matrix = false
				mode.Keys = false
			case BlankKeys:
				mode.Keys = false
			case BlankMatrix:
				mode.Matrix = false
			}
		}
		dirty := rev != revision
		ch := Channels{mode.Matrix && (dirty || !prevMode.Matrix || anim.Matrix), mode.Keys && mode.Effect < 0 && (dirty || !prevMode.Keys || anim.Keys)}
		var pic Picture
		if ch.Matrix || ch.Keys {
			pic = s.Frame(time.Since(epoch), ch)
		}
		r := prev
		r.mode = mode
		r.rev = rev
		r.rate = o.Rate
		r.haveMatrix = mode.Matrix
		r.haveKeys = mode.Keys && mode.Effect < 0
		var pm frame.Matrix
		var pk frame.Keys
		if pic.Matrix != nil {
			r.matrix = pic.Matrix.Encode(o.Cal, o.DotBright, o.DotCalib)
			if anim.Matrix {
				r.matrix = pic.Matrix.EncodeAnimated(o.Cal, o.DotBright, o.DotCalib)
			}
			pm = pic.Matrix.Encode(o.Cal, 100, false)
		}
		if pic.Keys != nil {
			r.keys = pic.Keys.Encode(o.Cal, o.KBBright)
			pk = frame.RawKeys(*pic.Keys, 100)
		}
		e.mu.Lock()
		if rev != e.revision {
			e.mu.Unlock()
			signal(e.wake)
			continue
		}
		if pic.Matrix != nil || !mode.Matrix {
			e.lastMatrix = pm
			e.haveMatrix = true
		}
		if pic.Keys != nil || !mode.Keys || mode.Effect >= 0 {
			e.lastKeys = pk
			e.haveKeys = true
		}
		e.latest.Store(&r)
		e.mu.Unlock()
		signal(e.ready)
		prev = r
		prevMode = mode
		revision = rev
		if (mode.Matrix && anim.Matrix) || (mode.Keys && mode.Effect < 0 && anim.Keys) {
			timer.Reset(e.opts.Tick)
		} else {
			timer.Stop()
		}
	}
}

// Drain 只保留最新目标。两路各有发送期限；仅成功写入后更新去重基线。
// 静态画面零流量，失败保留目标并退避，重新连接后重放当前目标。
func (e *Engine) Drain(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var mode device.Mode
	var haveMode bool
	var matrix frame.Matrix
	var keys frame.Keys
	var haveM, haveK bool
	var nextM, nextK, retryMode time.Time
	var generation, mRev, kRev uint64
	var modeErr, mErr, kErr error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.ready:
		case <-timer.C:
		}
		r := e.latest.Load()
		if r == nil {
			continue
		}
		if g, ok := e.sink.(interface{ Generation() uint64 }); ok {
			if n := g.Generation(); n != generation {
				generation = n
				haveMode = false
				haveM = false
				haveK = false
				mRev = 0
				kRev = 0
				retryMode = time.Time{}
				nextM = time.Time{}
				nextK = time.Time{}
			}
		}
		now := time.Now()
		if (!haveMode || mode != r.mode) && !now.Before(retryMode) {
			wctx, cancel := context.WithTimeout(ctx, e.opts.WriteTimeout)
			modeErr = e.sink.ApplyMode(wctx, r.mode)
			cancel()
			if modeErr == nil {
				// 真正的配置提交会重置两路显示；单纯开关则只使该路首帧失效。
				configChanged := !haveMode || !maps.Equal(mode.Patch(), r.mode.Patch())
				if configChanged || mode.Matrix != r.mode.Matrix {
					haveM = false
					mRev = 0
					nextM = time.Time{}
				}
				if configChanged || mode.Keys != r.mode.Keys {
					haveK = false
					kRev = 0
					nextK = time.Time{}
				}
				mode = r.mode
				haveMode = true
			} else {
				haveMode = false
				haveM = false
				haveK = false
				mRev = 0
				kRev = 0
				retryMode = time.Now().Add(time.Second)
			}
		}
		if modeErr == nil && haveMode && mode == r.mode && e.current(r.rev) {
			// 配置写入期间可能已产生更新的动画帧，直接使用最新帧。
			latest := e.latest.Load()
			if latest != nil && latest.rev == r.rev && latest.mode == mode {
				r = latest
			}
			mg, kg := r.rate.Gaps()
			typing := e.opts.Typing != nil && e.opts.Typing()
			if !r.haveKeys {
				kErr = nil
				kRev = r.rev
			} else if haveK && keys == r.keys {
				kRev = r.rev
			} else if !typing && !time.Now().Before(nextK) {
				wctx, cancel := context.WithTimeout(ctx, e.opts.WriteTimeout)
				started := time.Now()
				kErr = e.sink.SendKeys(wctx, r.keys)
				cancel()
				if kErr == nil {
					keys = r.keys
					haveK = true
					kRev = r.rev
					nextK = started.Add(kg)
				} else {
					haveK = false
					kRev = 0
					nextK = time.Now().Add(time.Second)
				}
			}
			if !r.haveMatrix {
				mErr = nil
				mRev = r.rev
			} else if haveM && matrix == r.matrix {
				mRev = r.rev
			} else if !typing && !time.Now().Before(nextM) && e.current(r.rev) {
				wctx, cancel := context.WithTimeout(ctx, e.opts.WriteTimeout)
				started := time.Now()
				mErr = e.sink.SendMatrix(wctx, r.matrix)
				cancel()
				if mErr == nil {
					matrix = r.matrix
					haveM = true
					mRev = r.rev
					nextM = started.Add(mg)
				} else {
					haveM = false
					mRev = 0
					nextM = time.Now().Add(time.Second)
				}
			}
		}
		e.mu.Lock()
		if r.rev == e.revision {
			if err := errors.Join(modeErr, mErr, kErr); err != nil {
				e.status.Error = err.Error()
			} else {
				e.status.Error = ""
			}
			e.status.Pending = modeErr != nil || !haveMode || mode != r.mode || mRev != r.rev || kRev != r.rev
		}
		e.mu.Unlock()
		delay := time.Second
		if modeErr != nil {
			delay = max(10*time.Millisecond, time.Until(retryMode))
		} else {
			if r.haveMatrix && (!haveM || matrix != r.matrix) {
				delay = min(delay, max(10*time.Millisecond, time.Until(nextM)))
			}
			if r.haveKeys && (!haveK || keys != r.keys) {
				delay = min(delay, max(10*time.Millisecond, time.Until(nextK)))
			}
			if e.opts.Typing != nil && e.opts.Typing() {
				delay = max(delay, 50*time.Millisecond)
			}
		}
		timer.Reset(delay)
	}
}
func (e *Engine) current(rev uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.revision == rev
}
func (e *Engine) LastFrame() (frame.Matrix, frame.Keys, bool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastMatrix, e.lastKeys, e.haveMatrix, e.haveKeys
}

// Enabled 是当前设置与省电策略合并后的通道开关，也约束兼容 API 的单帧写入。
func (e *Engine) Enabled() Channels {
	e.mu.RLock()
	defer e.mu.RUnlock()
	o := e.output
	c := o.Enabled
	c.Matrix = c.Matrix && o.DotBright > 0
	c.Keys = c.Keys && o.KBBright > 0
	if e.scene != nil {
		m := e.scene.Mode()
		c.Matrix = c.Matrix && m.Matrix
		c.Keys = c.Keys && m.Keys
	}
	if e.power != nil {
		switch e.power.Action() {
		case BlankAll:
			c = Channels{}
		case BlankKeys:
			c.Keys = false
		case BlankMatrix:
			c.Matrix = false
		}
	}
	return c
}
