package engine

import (
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
)

// TestDecide 锁定熄屏策略。
//
// 这些数字不是随便定的：键盘自己的休眠是 900 秒，我们在 180 秒就熄；
// 20% 关背光、8% 全灭，都是实测调出来的。
func TestDecide(t *testing.T) {
	pw := func(pct int, charging bool) *device.Power {
		return &device.Power{Percent: pct, Charging: charging}
	}

	cases := []struct {
		name string
		in   Inputs
		want Action
	}{
		{"正常", Inputs{Idle: 10 * time.Second, Power: pw(80, false)}, KeepOn},
		{"锁屏立即全灭", Inputs{Locked: true, Idle: time.Second, Power: pw(80, false)}, BlankAll},
		{"离开三分钟全灭", Inputs{Idle: IdleBlankAfter, Power: pw(80, false)}, BlankAll},
		{"差一秒还没到", Inputs{Idle: IdleBlankAfter - time.Second, Power: pw(80, false)}, KeepOn},
		{"电量 20% 关背光", Inputs{Idle: time.Second, Power: pw(20, false)}, BlankKeys},
		{"电量 8% 全灭", Inputs{Idle: time.Second, Power: pw(8, false)}, BlankAll},
		{"电量 21% 不动", Inputs{Idle: time.Second, Power: pw(21, false)}, KeepOn},
		{"充电时不因电量降级", Inputs{Idle: time.Second, Power: pw(5, true)}, KeepOn},
		{"读不到电量不降级", Inputs{Idle: time.Second, Power: nil}, KeepOn},
		{"锁屏优先于电量", Inputs{Locked: true, Power: pw(80, true)}, BlankAll},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.in); got != c.want {
				t.Errorf("Decide(%+v) = %v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

// TestPowerMonitorDebouncesLock 确认锁屏判定要去抖。
//
// UAC 弹窗的安全桌面也会让 OpenInputDesktop 失败，
// 一次命中就熄屏会导致"每次弹权限框键盘就黑一下"。
func TestPowerMonitorDebouncesLock(t *testing.T) {
	hits := 0
	m := &PowerMonitor{
		LockFn: func() bool { return hits > 0 },
		IdleFn: func() float64 { return 0 },
	}

	hits = 1 // 只有一次"锁屏"
	if got := m.Poll(); got != KeepOn {
		t.Fatalf("单次命中不该熄屏，实际 %v", got)
	}

	// 连续命中才当真
	m.Poll()
	m.Poll()
	if got := m.Poll(); got != BlankAll {
		t.Fatalf("连续命中应熄屏，实际 %v", got)
	}

	hits = 0 // 恢复
	for i := 0; i < LockDebounce; i++ {
		m.Poll()
	}
	if got := m.Poll(); got != KeepOn {
		t.Fatalf("解锁后应恢复，实际 %v", got)
	}
}

// TestShouldEmitReblanks 确认熄屏期间会定期补一帧全黑。
//
// 固件在一段时间收不到点阵帧后会自己退出同步态、把存储的画面重画回来。
// 只在刚熄灭那一刻发一帧，管不住整夜。
func TestShouldEmitReblanks(t *testing.T) {
	m := &PowerMonitor{
		LockFn: func() bool { return false },
		IdleFn: func() float64 { return IdleBlankAfter.Seconds() + 1 }, // 已离开
	}
	m.Poll()
	m.Poll()
	if m.Action() != BlankAll {
		t.Fatalf("应处于全灭状态，实际 %v", m.Action())
	}

	now := time.Now()
	if !m.ShouldEmit(now) {
		t.Fatal("刚进入熄屏应立刻发一帧")
	}
	if m.ShouldEmit(now.Add(time.Second)) {
		t.Fatal("一秒后不该再发")
	}
	if !m.ShouldEmit(now.Add(ReblankEvery + time.Second)) {
		t.Fatal("超过补黑间隔应再发一帧")
	}
}

// TestBlankFor 确认每种处置灭的是对的那一半。
func TestBlankFor(t *testing.T) {
	cases := []struct {
		a          Action
		matrix, kb bool
	}{
		{BlankAll, true, true},
		{BlankMatrix, true, false},
		{BlankKeys, false, true},
	}
	for _, c := range cases {
		r := blankFor(c.a)
		if (r.Matrix != nil) != c.matrix {
			t.Errorf("%v: Matrix 是否存在应为 %v", c.a, c.matrix)
		}
		if (r.Keys != nil) != c.kb {
			t.Errorf("%v: Keys 是否存在应为 %v", c.a, c.kb)
		}
		if r.Matrix != nil && *r.Matrix != (frame.Matrix{}) {
			t.Errorf("%v: 点阵帧不是全黑", c.a)
		}
		if r.Keys != nil && *r.Keys != (frame.Keys{}) {
			t.Errorf("%v: 键盘帧不是全灭", c.a)
		}
	}
}
