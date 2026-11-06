package device

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// fakeLink 模拟协议应答，离线验证完整事务与配置分段，不触碰真实 HID。
type fakeLink struct {
	c            *Controller
	mu           sync.Mutex
	cfg          proto.Config
	packets      []proto.Packet
	ignoreWrites bool
	staging      *proto.Config
}

func (f *fakeLink) Write(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, _ := proto.Unmarshal(raw)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets = append(f.packets, p)
	var reply []byte
	switch p.Cmd {
	case proto.CmdWriteConfig:
		if !f.ignoreWrites {
			if f.staging == nil {
				cfg := f.cfg
				f.staging = &cfg
			}
			end := int(p.Offset) + int(p.Length)
			copy(f.staging[p.Offset:end], p.Data[:p.Length])
			if end == proto.ConfigLen {
				f.cfg = *f.staging
				f.staging = nil
			}
		}
	case proto.CmdReadConfig:
		reply = append([]byte{}, f.cfg[p.Offset:int(p.Offset)+int(p.Length)]...)
	case proto.CmdPowerInfo:
		reply = []byte{80, 0}
	case proto.CmdEndSyncDot:
		reply = []byte{0}
	}
	if reply != nil {
		f.c.mu.Lock()
		ch := f.c.pending[p.Seq]
		f.c.mu.Unlock()
		if ch != nil {
			ch <- reply
		}
	}
	return nil
}
func (*fakeLink) Read(context.Context, time.Duration) ([]byte, error) { return nil, nil }
func (*fakeLink) Close() error                                        { return nil }
func fakeController(t *testing.T) (*Controller, *fakeLink) {
	t.Helper()
	done := make(chan struct{})
	close(done)
	c := &Controller{pending: map[byte]chan []byte{}, stopRead: make(chan struct{}), readDone: done, lastHand: time.Now()}
	f := &fakeLink{c: c}
	c.link = f
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}
func TestDeviceTransactionsDoNotInterleaveFramePackets(t *testing.T) {
	c, f := fakeController(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := c.SendKeys(ctx, frame.Keys{}); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := c.SendMatrix(ctx, frame.Matrix{}); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := c.Power(ctx, true); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[proto.Cmd]bool{}
	var last proto.Cmd
	for _, p := range f.packets {
		if p.Cmd != last {
			if seen[p.Cmd] {
				t.Fatalf("transaction packets interleaved at %v", p.Cmd)
			}
			seen[p.Cmd] = true
			last = p.Cmd
		}
	}
	if !seen[proto.CmdSyncLED] || !seen[proto.CmdMatrix] || !seen[proto.CmdPowerInfo] {
		t.Fatalf("missing operation: %v", seen)
	}
}
func TestOutputSwitchesOnlyWriteOneBlackFrame(t *testing.T) {
	for _, channel := range []string{"matrix", "keys"} {
		t.Run(channel, func(t *testing.T) {
			c, f := fakeController(t)
			ctx := context.Background()
			mode := Mode{Matrix: true, Keys: true, Effect: -1}
			if err := c.ApplyMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.packets = nil
			f.mu.Unlock()
			cmd, count := proto.CmdMatrix, MatrixPacketCount
			if channel == "matrix" {
				mode.Matrix = false
			} else {
				mode.Keys = false
				cmd, count = proto.CmdSyncLED, KeysPacketCount
			}
			if err := c.ApplyMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			if err := c.ApplyMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			if len(f.packets) != count {
				t.Fatalf("want one black frame, got %d packets", len(f.packets))
			}
			for _, p := range f.packets {
				if p.Cmd != cmd || p.Data != [proto.PayloadLen]byte{} {
					t.Fatalf("switch reconfigures device or touches other channel: %+v", p)
				}
			}
			// 恢复软件动画不重配模式、不等待回读；引擎紧接着发当前帧。
			mode.Matrix, mode.Keys = true, true
			if err := c.ApplyMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			if len(f.packets) != count {
				t.Fatal("restore performed configuration IO")
			}
		})
	}
}

func TestConfigurationChangesCommitFinalSegment(t *testing.T) {
	c, f := fakeController(t)
	if _, err := c.PatchConfig(context.Background(), proto.Patch{proto.OffDotBright: 90}); err != nil {
		t.Fatal(err)
	}
	committed := false
	for _, p := range f.packets {
		if p.Cmd == proto.CmdEndSyncLED || p.Cmd == proto.CmdWriteConfig && p.Offset == 0 {
			t.Fatalf("matrix configuration touches keys: %+v", p)
		}
		if p.Cmd == proto.CmdWriteConfig && int(p.Offset)+int(p.Length) == proto.ConfigLen {
			committed = true
		}
	}
	if !committed || f.cfg[proto.OffDotBright] != 90 {
		t.Fatal("configuration was not committed")
	}
}

func TestFirmwareOutputSwitchKeepsConfiguration(t *testing.T) {
	c, f := fakeController(t)
	ctx := context.Background()
	mode := Mode{Matrix: true, Keys: true, Effect: 3, Brightness: 70}
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	f.packets = nil
	mode.Keys = false
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	mode.Keys = true
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	if len(f.packets) != KeysPacketCount+1 || f.packets[KeysPacketCount].Cmd != proto.CmdEndSyncLED {
		t.Fatal("firmware restore should only release key stream")
	}
	for _, p := range f.packets[:KeysPacketCount] {
		if p.Cmd != proto.CmdSyncLED || p.Data != [proto.PayloadLen]byte{} {
			t.Fatalf("unexpected packet: %+v", p)
		}
	}
}
func TestModeReadbackMustMatchBeforeCaching(t *testing.T) {
	c, f := fakeController(t)
	ctx := context.Background()
	mode := Mode{Matrix: true, Keys: true, Effect: -1}
	f.ignoreWrites = true
	if err := c.ApplyMode(ctx, mode); err == nil {
		t.Fatal("unapplied config reported success")
	}
	if c.appliedMode != nil {
		t.Fatal("failed config cached")
	}
	f.ignoreWrites = false
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	before := len(f.packets)
	f.mu.Unlock()
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.packets) != before {
		t.Fatal("unchanged mode performed IO")
	}
}

func TestFailedModeInvalidatesPreviousMode(t *testing.T) {
	c, f := fakeController(t)
	ctx := context.Background()
	mode := Mode{Matrix: true, Keys: true, Effect: -1}
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	f.ignoreWrites = true
	off := mode
	off.Effect = 3
	off.Brightness = 70
	if err := c.ApplyMode(ctx, off); err == nil {
		t.Fatal("ignored write succeeded")
	}
	if c.appliedMode != nil {
		t.Fatal("old mode survived partially failed update")
	}
	f.ignoreWrites = false
	if err := c.ApplyMode(ctx, mode); err != nil {
		t.Fatal(err)
	}
	if c.appliedMode == nil || *c.appliedMode != mode {
		t.Fatal("old desired mode not restored")
	}
}
