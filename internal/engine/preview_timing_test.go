package engine

import (
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/device"
)

func TestPowerPollDoesNotBlockAnimation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m := &PowerMonitor{PowerFn: func() *device.Power {
		close(entered)
		<-release
		return &device.Power{Percent: 5}
	}}
	polled := make(chan Action, 1)
	go func() { polled <- m.Poll() }()
	defer func() {
		close(release)
		if got := <-polled; got != BlankAll {
			t.Errorf("电量返回后仍应执行全灭策略，got %v", got)
		}
	}()
	<-entered

	action := make(chan Action, 1)
	go func() { action <- m.Action() }()
	select {
	case got := <-action:
		if got != KeepOn {
			t.Fatalf("电量返回前应沿用原状态，got %v", got)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("电量读取持有状态锁，帧泵无法继续读取省电状态")
	}
}

// 发布状态后，后台日志等后续工作不应阻塞读取最新结果。
func TestPowerStateReadDuringLogging(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m := &PowerMonitor{
		PowerFn: func() *device.Power { return &device.Power{Percent: 5} },
		Logf: func(string, ...any) {
			close(entered)
			<-release
		},
	}
	polled := make(chan struct{})
	go func() { m.Poll(); close(polled) }()
	defer func() { close(release); <-polled }()
	<-entered

	type result struct {
		action Action
	}
	state := make(chan result, 1)
	go func() { state <- result{m.Action()} }()
	select {
	case got := <-state:
		if got.action != BlankAll {
			t.Fatalf("应读到最新状态并立即允许熄屏，got %+v", got)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("读取省电状态仍在等待后台工作")
	}
}
