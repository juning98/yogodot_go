package engine

import (
	"context"
	"testing"
	"time"
)

type pendingSink struct {
	dedupSink
	flushed chan struct{}
}

func (s *pendingSink) FlushPending(ctx context.Context) {
	select {
	case s.flushed <- struct{}{}:
	default:
	}
	<-ctx.Done() // 模拟一笔慢的补发；帧泵仍须继续推进。
}

func TestPendingWritesStayInDrain(t *testing.T) {
	sink := &pendingSink{flushed: make(chan struct{}, 1)}
	scene := &countingScene{}
	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	e.scene = scene
	ctx, cancel := context.WithCancel(context.Background())
	pumpDone, drainDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(pumpDone); _ = e.Pump(ctx) }()
	defer func() { cancel(); <-pumpDone; <-drainDone }()
	go func() {
		defer close(drainDone)
		// 先不启动写循环：帧泵必须能独立推进，且不能触发设备补发。
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		_ = e.Drain(ctx)
	}()
	select {
	case <-sink.flushed:
		if scene.n.Load() < 10 {
			t.Fatal("帧泵在写循环启动之前就调用了设备补发")
		}
	case <-time.After(time.Second):
		t.Fatal("写循环没有处理待补帧")
	}
	before := scene.n.Load()
	time.Sleep(100 * time.Millisecond)
	if scene.n.Load()-before < 10 {
		t.Fatal("慢补发阻塞了动画时钟")
	}
}

func TestDrainSkipsPendingWhileBlanked(t *testing.T) {
	sink := &pendingSink{flushed: make(chan struct{}, 1)}
	e := NewWithSink(sink, Options{})
	e.SetPower(&PowerMonitor{action: BlankAll})
	e.out <- blankFor(BlankAll)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = e.Drain(ctx)
	select {
	case <-sink.flushed:
		t.Fatal("熄屏时补发了旧画面")
	default:
	}
}
