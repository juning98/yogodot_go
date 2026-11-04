package engine

import (
	"context"
	"sync"
	"time"

	"github.com/wednesque/yogod/internal/device"
)

// Action 是本时刻对输出的处置。
type Action uint8

const (
	// KeepOn 正常显示场景内容。
	KeepOn Action = iota
	// BlankMatrix 只灭点阵（电量低，把电留给键盘灯）。
	BlankMatrix
	// BlankKeys 只灭键盘灯（电量更低一档）。
	BlankKeys
	// BlankAll 全灭（锁屏或离开）。
	BlankAll
)

func (a Action) String() string {
	switch a {
	case BlankMatrix:
		return "灭点阵"
	case BlankKeys:
		return "灭键盘灯"
	case BlankAll:
		return "全灭"
	default:
		return "正常"
	}
}

// 省电策略参数。全部来自旧版实测，改动前先想清楚。
const (
	// IdleBlankAfter 是"多久没碰键鼠就熄屏"。键盘自己的休眠是 900 秒，我们比它早一步。
	IdleBlankAfter = 180 * time.Second
	// RebBlankEvery 是熄着的时候定期补一帧全黑：
	// 固件在一段时间收不到点阵帧后会退出同步态、把存储的画面重画回来。
	ReblankEvery = 15 * time.Second
	// BattKeysOff 是低于这个电量就不再推键盘灯（84 颗灯是耗电大头）。
	BattKeysOff = 20
	// BattAllOff 是低于这个电量连点阵也停，把电留给打字。
	BattAllOff = 8
	// LockDebounce 是锁屏判定的去抖次数：UAC 弹窗的安全桌面也会让检测失败。
	LockDebounce = 2
)

// Inputs 是熄屏决策的全部输入。
type Inputs struct {
	// Locked 是系统是否锁屏（已去抖）。
	Locked bool
	// Idle 是距离上次键鼠输入的时长。
	Idle time.Duration
	// Power 是电量。nil 表示读不到（读不到就不要因为电量降级）。
	Power *device.Power
}

// Policy 是省电阈值。零值 = 用包级默认（全部来自旧版实测）。
// Settings.IdlePolicy 通过 SetPolicy 热更新 —— 控制台"熄屏策略"页的三个
// 输入就是写到这里；不接的话滑杆"已保存"但行为永远不变。
type Policy struct {
	// BlankAfter 是"多久没碰键鼠就熄屏"。
	BlankAfter time.Duration
	// KeysOffPercent / AllOffPercent 是两级电量阈值（百分比）。
	KeysOffPct int
	AllOffPct  int
}

func (p Policy) blankAfter() time.Duration {
	if p.BlankAfter <= 0 {
		return IdleBlankAfter
	}
	return p.BlankAfter
}
func (p Policy) keysOff() int {
	if p.KeysOffPct <= 0 {
		return BattKeysOff
	}
	return p.KeysOffPct
}
func (p Policy) allOff() int {
	if p.AllOffPct <= 0 {
		return BattAllOff
	}
	return p.AllOffPct
}

// Away 报告是不是"人不在"。
func (in Inputs) Away() bool { return in.Locked || in.Idle >= IdleBlankAfter }

// Decide 是唯一的熄屏判断入口（零阈值默认版，测试用）。
//
// 场景层只描述"我想显示什么"，这里决定"能不能显示 / 要不要盖黑"。
// 旧版把这两件事缠在 kbd._idle_loop 和 _saved_mx 里，是状态打架的根源。
func Decide(in Inputs) Action { return DecidePolicy(in, Policy{}) }

// DecidePolicy 带阈值版本：运行期阈值来自 Settings.IdlePolicy。
func DecidePolicy(in Inputs, p Policy) Action {
	away := in.Locked || in.Idle >= p.blankAfter()

	lowKeys, lowAll := false, false
	// 读不到电量 / 在充电：不因电量降级
	if in.Power != nil && !in.Power.Charging {
		lowKeys = in.Power.Percent <= p.keysOff()
		lowAll = in.Power.Percent <= p.allOff()
	}

	matrixOff := away || lowAll
	keysOff := away || lowKeys

	switch {
	case matrixOff && keysOff:
		return BlankAll
	case keysOff:
		return BlankKeys
	case matrixOff:
		return BlankMatrix
	default:
		return KeepOn
	}
}

// PowerMonitor 轮询系统状态并给出当前处置。
//
// 它不负责"怎么灭"——那由引擎把 Render 换成全黑帧完成。
type PowerMonitor struct {
	// LockFn 报告系统是否锁屏。注入是为了可测。
	LockFn func() bool
	// IdleFn 报告空闲秒数。
	IdleFn func() float64
	// PowerFn 读电量，可能返回 nil。
	PowerFn func() *device.Power
	// Logf 是日志出口。
	Logf func(format string, args ...any)

	mu          sync.Mutex
	lockHits    int
	locked      bool
	action      Action
	pol         Policy
	lastReblank time.Time
}

// SetPolicy 热更新省电阈值（控制台"熄屏策略"改动时调用）。
func (m *PowerMonitor) SetPolicy(p Policy) {
	m.mu.Lock()
	m.pol = p
	m.mu.Unlock()
}

// Poll 更新一次状态。
func (m *PowerMonitor) Poll() Action {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.LockFn != nil {
		if m.LockFn() {
			m.lockHits++
		} else {
			m.lockHits = 0
		}
		// 连续命中才当真：UAC 弹窗也会让检测失败
		m.locked = m.lockHits >= LockDebounce
	}

	var idle time.Duration
	if m.IdleFn != nil {
		idle = time.Duration(m.IdleFn() * float64(time.Second))
	}
	var pw *device.Power
	if m.PowerFn != nil {
		pw = m.PowerFn()
	}

	next := DecidePolicy(Inputs{Locked: m.locked, Idle: idle, Power: pw}, m.pol)
	if next != m.action {
		if m.Logf != nil {
			m.Logf("省电状态 %v -> %v（锁屏=%v 空闲=%s）", m.action, next, m.locked, idle.Round(time.Second))
		}
		m.action = next
		m.lastReblank = time.Time{} // 状态刚变，立刻执行一次
	}
	return m.action
}

// Action 返回上一次 Poll 的结果。
func (m *PowerMonitor) Action() Action {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.action
}

// Run 以固定间隔轮询，直到 ctx 取消。
//
// 单独一个 goroutine 轮询是刻意的：读电量要走 HID，会阻塞几百毫秒。
// 如果让帧泵自己去读，动画就跟着卡 —— 旧版正是这么卡的。
func (m *PowerMonitor) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	m.Poll() // 先来一次，别等第一个 tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Poll()
		}
	}
}

// ShouldEmit 报告这一 tick 是否该输出（而不是被熄屏吞掉）。
//
// 熄屏期间每隔 ReblankEvery 放一帧全黑出去 —— 目的不是画面，
// 而是防止固件自己把存储的画面重画回来。
func (m *PowerMonitor) ShouldEmit(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.action == KeepOn {
		return true
	}
	if now.Sub(m.lastReblank) >= ReblankEvery {
		m.lastReblank = now
		return true
	}
	return false
}
