package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
)

type testScene struct {
	dynamic      bool
	matrix, keys atomic.Int64
	elapsed      atomic.Int64
	slow         time.Duration
}

func (*testScene) Name() string          { return "test" }
func (s *testScene) Animation() Channels { return Channels{s.dynamic, s.dynamic} }
func (*testScene) Mode() device.Mode     { return device.Mode{Matrix: true, Keys: true, Effect: -1} }
func (s *testScene) Frame(at time.Duration, ch Channels) Picture {
	s.elapsed.Store(int64(at))
	if s.slow > 0 {
		time.Sleep(s.slow)
	}
	var r Picture
	v := byte(180)
	if s.dynamic {
		v = byte(at / time.Millisecond)
	}
	if ch.Matrix {
		s.matrix.Add(1)
		g := frame.Grid{}
		g[0][0] = color.RGBFloat{float64(v), 90, 40}
		r.Matrix = &g
	}
	if ch.Keys {
		s.keys.Add(1)
		k := frame.LEDs{}
		k[0] = color.RGB{v, 90, 40}
		r.Keys = &k
	}
	return r
}

type testSink struct {
	failMode   int
	mu         sync.Mutex
	modes      []device.Mode
	matrices   []frame.Matrix
	keys       []frame.Keys
	failKeys   int
	attempts   int
	block      chan struct{}
	entered    chan struct{}
	once       sync.Once
	generation atomic.Uint64
}

func (s *testSink) Generation() uint64 { return s.generation.Load() }
func (s *testSink) ApplyMode(_ context.Context, m device.Mode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modes = append(s.modes, m)
	if s.failMode > 0 {
		s.failMode--
		return errors.New("mode failed")
	}
	return nil
}
func (s *testSink) SendMatrix(_ context.Context, m frame.Matrix) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matrices = append(s.matrices, m)
	return nil
}
func (s *testSink) SendKeys(ctx context.Context, k frame.Keys) error {
	if s.block != nil {
		s.once.Do(func() {
			close(s.entered)
			select {
			case <-s.block:
			case <-ctx.Done():
			}
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.failKeys > 0 {
		s.failKeys--
		return errors.New("write failed")
	}
	s.keys = append(s.keys, k)
	return nil
}
func (s *testSink) counts() (m, k, mode, attempts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.matrices), len(s.keys), len(s.modes), s.attempts
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func runTestEngine(t *testing.T, e *Engine, s Scene) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.SetScene(ctx, s); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = e.Pump(ctx) }()
	go func() { defer wg.Done(); _ = e.Drain(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
}

func TestStaticOutputSendsOnceAndReplaysAfterReconnect(t *testing.T) {
	sink := &testSink{}
	scene := &testScene{}
	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	runTestEngine(t, e, scene)
	eventually(t, func() bool { m, k, _, _ := sink.counts(); return m == 1 && k == 1 })
	time.Sleep(220 * time.Millisecond)
	if m, k, mode, _ := sink.counts(); m != 1 || k != 1 || mode != 1 {
		t.Fatalf("static output repeats: %d %d %d", m, k, mode)
	}
	if scene.matrix.Load() != 1 || scene.keys.Load() != 1 {
		t.Fatal("static scene rendered repeatedly")
	}
	sink.generation.Add(1)
	eventually(t, func() bool { m, k, _, _ := sink.counts(); return m == 2 && k == 2 })
}

func TestChannelsStopIndependentlyAndResumeCurrentFrame(t *testing.T) {
	sink := &testSink{}
	scene := &testScene{dynamic: true}
	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	runTestEngine(t, e, scene)
	eventually(t, func() bool { m, k, _, _ := sink.counts(); return m > 0 && k > 0 })
	o := DefaultOutput()
	o.Enabled.Keys = false
	e.SetOutput(o)
	eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.modes) > 1 && !sink.modes[len(sink.modes)-1].Keys
	})
	time.Sleep(40 * time.Millisecond)
	kBefore := scene.keys.Load()
	mBefore := scene.matrix.Load()
	_, writes, modeBefore, _ := sink.counts()
	time.Sleep(220 * time.Millisecond)
	if scene.keys.Load() != kBefore || scene.matrix.Load() <= mBefore {
		t.Fatal("disabled keys render or matrix freezes")
	}
	if _, k, mode, _ := sink.counts(); k != writes || mode != modeBefore {
		t.Fatal("disabled channel still writes")
	}
	o.Enabled.Keys = true
	o.Enabled.Matrix = false
	e.SetOutput(o)
	eventually(t, func() bool { _, k, _, _ := sink.counts(); return k > writes })
	time.Sleep(40 * time.Millisecond)
	mBefore = scene.matrix.Load()
	kBefore = scene.keys.Load()
	time.Sleep(160 * time.Millisecond)
	if scene.matrix.Load() != mBefore || scene.keys.Load() <= kBefore {
		t.Fatal("disabled matrix renders or keys freeze")
	}
	o.Enabled = Channels{}
	e.SetOutput(o)
	eventually(t, func() bool { return !e.Status().Pending })
	mBefore = scene.matrix.Load()
	kBefore = scene.keys.Load()
	time.Sleep(80 * time.Millisecond)
	if scene.matrix.Load() != mBefore || scene.keys.Load() != kBefore {
		t.Fatal("fully off scene renders")
	}
}

type firmwareTestScene struct {
	testScene
	effect int
}

func (s *firmwareTestScene) Mode() device.Mode {
	return device.Mode{Matrix: true, Keys: true, Effect: s.effect, Light: color.RGB{77, 107, 254}}
}

func TestOutputSwitchPreservesFirmwareModeAndConfigChangeReplaysStaticMatrix(t *testing.T) {
	sink := &testSink{}
	e := NewWithSink(sink, Options{})
	runTestEngine(t, e, &firmwareTestScene{effect: 3})
	eventually(t, func() bool { return !e.Status().Pending })
	o := DefaultOutput()
	o.Enabled.Keys = false
	e.SetOutput(o)
	eventually(t, func() bool { return !e.Status().Pending })
	sink.mu.Lock()
	off := sink.modes[len(sink.modes)-1]
	matrixCount := len(sink.matrices)
	sink.mu.Unlock()
	if off.Keys || off.Effect != 3 || off.Brightness != byte(o.KBBright) || matrixCount != 1 {
		t.Fatalf("switch changed firmware mode or refreshed static matrix: mode=%+v matrices=%d", off, matrixCount)
	}
	o.Enabled.Keys = true
	e.SetOutput(o)
	eventually(t, func() bool { return !e.Status().Pending })
	if m, _, _, _ := sink.counts(); m != 1 {
		t.Fatal("key restore refreshed unaffected matrix")
	}
	if err := e.SetScene(context.Background(), &firmwareTestScene{effect: 4}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return !e.Status().Pending })
	if m, _, _, _ := sink.counts(); m != 2 {
		t.Fatal("configuration change did not restore static matrix")
	}
}

func TestFailedStaticFrameRetriesAndOtherChannelContinues(t *testing.T) {
	sink := &testSink{failKeys: 1}
	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	runTestEngine(t, e, &testScene{})
	eventually(t, func() bool { m, _, _, a := sink.counts(); return m == 1 && a == 1 })
	time.Sleep(200 * time.Millisecond)
	if _, _, _, a := sink.counts(); a != 1 {
		t.Fatal("failure busy retries")
	}
	eventually(t, func() bool { m, k, _, a := sink.counts(); return m == 1 && k == 1 && a == 2 })
	eventually(t, func() bool { return !e.Status().Pending && e.Status().Error == "" })
}

func TestPreviewAndClockContinueDuringSlowDevice(t *testing.T) {
	sink := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	e := NewWithSink(sink, Options{Tick: 5 * time.Millisecond})
	scene := &testScene{dynamic: true}
	runTestEngine(t, e, scene)
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("no write")
	}
	_, before, _, _ := e.LastFrame()
	time.Sleep(80 * time.Millisecond)
	_, after, _, _ := e.LastFrame()
	if before == after {
		t.Fatal("preview waits for device")
	}
	close(sink.block)
	eventually(t, func() bool { m, k, _, _ := sink.counts(); return m > 0 && k > 0 })
}

func TestClockUsesElapsedTimeAndParameterChangesKeepPhase(t *testing.T) {
	e := NewWithSink(&testSink{}, Options{Tick: 5 * time.Millisecond})
	scene := &testScene{dynamic: true, slow: 50 * time.Millisecond}
	runTestEngine(t, e, scene)
	eventually(t, func() bool { return scene.elapsed.Load() > int64(120*time.Millisecond) })
	before := scene.elapsed.Load()
	o := DefaultOutput()
	o.DotBright = 10
	e.SetOutput(o)
	eventually(t, func() bool { return scene.elapsed.Load() > before })
	if err := e.SetScene(context.Background(), scene); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if scene.elapsed.Load() < before {
		t.Fatal("parameter rebuild reset clock")
	}
}

func TestGlobalPipelineChangesDeviceButNotPreview(t *testing.T) {
	sink := &testSink{}
	e := NewWithSink(sink, Options{})
	runTestEngine(t, e, &testScene{})
	eventually(t, func() bool { return !e.Status().Pending })
	pm, pk, _, _ := e.LastFrame()
	sink.mu.Lock()
	m, k := sink.matrices[0], sink.keys[0]
	sink.mu.Unlock()
	o := DefaultOutput()
	o.DotBright = 10
	o.KBBright = 10
	e.SetOutput(o)
	eventually(t, func() bool { m, k, _, _ := sink.counts(); return m == 2 && k == 2 })
	pm2, pk2, _, _ := e.LastFrame()
	if pm2 != pm || pk2 != pk {
		t.Fatal("brightness changed original-color preview")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.matrices[1] == m || sink.keys[1] == k {
		t.Fatal("global brightness not applied")
	}
	if len(sink.modes) != 1 {
		t.Fatal("software brightness rewrote hardware mode")
	}
}

// 中途写失败后设备可能已经收到半帧，即使目标回到旧成功帧也必须重放。
func TestPartialFailureInvalidatesSuccessfulFrameBaseline(t *testing.T) {
	sink := &testSink{}
	e := NewWithSink(sink, Options{})
	runTestEngine(t, e, &testScene{})
	eventually(t, func() bool { return !e.Status().Pending })
	sink.mu.Lock()
	sink.failKeys = 1
	sink.mu.Unlock()
	o := DefaultOutput()
	o.KBBright = 10
	e.SetOutput(o)
	eventually(t, func() bool { _, _, _, a := sink.counts(); return a == 2 })
	e.SetOutput(DefaultOutput())
	eventually(t, func() bool { _, k, _, a := sink.counts(); return k == 2 && a == 3 })
}

func TestFailedModeCanReturnToPreviousTarget(t *testing.T) {
	sink := &testSink{}
	e := NewWithSink(sink, Options{})
	runTestEngine(t, e, &testScene{})
	eventually(t, func() bool { return !e.Status().Pending })
	sink.mu.Lock()
	sink.failMode = 1
	sink.mu.Unlock()
	off := DefaultOutput()
	off.Enabled.Keys = false
	e.SetOutput(off)
	eventually(t, func() bool { return e.Status().Error != "" })
	e.SetOutput(DefaultOutput())
	eventually(t, func() bool { return !e.Status().Pending && e.Status().Error == "" })
	if _, _, modes, _ := sink.counts(); modes < 3 {
		t.Fatal("old cached mode bypassed recovery")
	}
}
