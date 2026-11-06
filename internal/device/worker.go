package device

import (
	"context"
	"fmt"
	"maps"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// Mode 描述两路输出的目标模式。关闭时写一次黑帧，随后停止该路刷新。
// Effect=-1 表示软件键盘配色；其它值是固件效果号。
type Mode struct {
	Matrix     bool
	Keys       bool
	Effect     int
	Light      color.RGB
	Brightness byte
}

func (m Mode) Patch() proto.Patch {
	p := proto.Patch{}
	// 日常开关保留底层模式。固件提交配置会重启两路灯效，不能用来独立开关。
	p[proto.OffTFTOnOff] = 0
	p[proto.OffDotMode] = proto.DotModeCustom
	p[proto.OffDotBright] = 90
	p[proto.OffDotSpeed] = 3
	p[proto.OffBLMode], p[proto.OffBLBright] = proto.BLModeStream, 90
	p[proto.OffBLColorful] = 0
	if m.Effect >= 0 {
		p[proto.OffBLBright] = m.Brightness
		p[proto.OffBLMode] = byte(m.Effect)
		p[proto.OffBLR], p[proto.OffBLG], p[proto.OffBLB] = m.Light[0], m.Light[1], m.Light[2]
		if eff, ok := EffectByID(m.Effect); ok && eff.Colorful {
			p[proto.OffBLColorful] = 1
		}
	}
	return p
}

// call 把完整操作交给唯一设备工作循环，包括配置读改写、整帧分包、
// 电量查询和重连。调用者取消等待后，结果仍可安全投递到私有单槽通道。
func call[T any](c *Controller, ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	c.workOnce.Do(func() {
		c.work = make(chan func(), 16)
		c.workStop = make(chan struct{})
		go func() {
			for {
				select {
				case <-c.workStop:
					return
				case work := <-c.work:
					work()
				}
			}
		}()
	})
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	job := func() {
		if err := ctx.Err(); err != nil {
			done <- result{zero, err}
			return
		}
		v, err := fn()
		done <- result{v, err}
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-c.workStop:
		return zero, ErrNotConnected
	case c.work <- job:
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-c.workStop:
		return zero, ErrNotConnected
	case r := <-done:
		return r.value, r.err
	}
}

func command(c *Controller, ctx context.Context, fn func() error) error {
	_, err := call(c, ctx, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

func (c *Controller) Close() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil
	}
	err := command(c, context.Background(), c.close)
	c.stopOnce.Do(func() { close(c.workStop) })
	return err
}

func (c *Controller) Reopen(ctx context.Context) error {
	return command(c, ctx, func() error { return c.reopen(ctx) })
}

func (c *Controller) Generation() uint64 { return c.generation.Load() }

func (c *Controller) ReadConfig(ctx context.Context) (proto.Config, error) {
	return call(c, ctx, func() (proto.Config, error) { return c.readConfig(ctx) })
}

func (c *Controller) WriteConfig(ctx context.Context, cfg proto.Config) error {
	return command(c, ctx, func() error { c.appliedMode = nil; c.generation.Add(1); return c.writeConfig(ctx, cfg) })
}

func (c *Controller) PatchConfig(ctx context.Context, p proto.Patch) (proto.Config, error) {
	return call(c, ctx, func() (proto.Config, error) { c.appliedMode = nil; c.generation.Add(1); return c.patchConfig(ctx, p) })
}

func (c *Controller) Power(ctx context.Context, force bool) (*Power, error) {
	return call(c, ctx, func() (*Power, error) { return c.powerInfo(ctx, force) })
}

func (c *Controller) EndDotSync(ctx context.Context) error {
	return command(c, ctx, func() error { c.appliedMode = nil; c.generation.Add(1); return c.endDotSync(ctx) })
}

// ApplyMode 只有模式变化、重连或实读发现配置漂移时才操作设备配置。
func (c *Controller) ApplyMode(ctx context.Context, m Mode) error {
	return command(c, ctx, func() error {
		if c.appliedMode != nil && *c.appliedMode == m {
			return nil
		}
		previous := c.appliedMode
		c.appliedMode = nil // 写到一半失败后，旧模式也不能继续当作已确认状态。
		configChanged := previous == nil || !maps.Equal(previous.Patch(), m.Patch())
		if previous == nil {
			if err := c.endDotSync(ctx); err != nil {
				return err
			}
		}
		if configChanged {
			patch := m.Patch()
			if _, err := c.patchConfig(ctx, patch); err != nil {
				return err
			}
			cfg, err := c.readConfig(ctx)
			if err != nil {
				return err
			}
			for off, want := range patch {
				if cfg[off] != want {
					return fmt.Errorf("device: 配置尚未生效（offset %d）", off)
				}
			}
		}
		// 同一事务内熄灯。只在开关边沿或配置被重新应用后写一次，关闭期间零推帧。
		if !m.Matrix && (configChanged || previous.Matrix) {
			if err := c.writeMatrix(ctx, frame.Matrix{}); err != nil {
				return err
			}
		}
		if !m.Keys && (configChanged || previous.Keys) {
			if err := c.writeKeys(ctx, frame.Keys{}); err != nil {
				return err
			}
		}
		// 黑帧临时接管固件背光；恢复固件效果只退出逐键推流，不改共享配置。
		if m.Keys && m.Effect >= 0 && !configChanged && !previous.Keys {
			if _, err := c.transact(ctx, proto.New(proto.CmdEndSyncLED, 0, nil), false); err != nil {
				return err
			}
		}
		c.appliedMode = &m
		return nil
	})
}

func (c *Controller) SendMatrix(ctx context.Context, m frame.Matrix) error {
	return command(c, ctx, func() error { return c.writeMatrix(ctx, m) })
}

func (c *Controller) SendKeys(ctx context.Context, k frame.Keys) error {
	return command(c, ctx, func() error { return c.writeKeys(ctx, k) })
}

// Reconcile 保留实际读配置的慢速检查，不使用帧计数推断链路健康。
func (c *Controller) Reconcile(ctx context.Context) error {
	return command(c, ctx, func() error {
		if c.appliedMode == nil {
			return nil
		}
		cfg, err := c.readConfig(ctx)
		if err != nil {
			return err
		}
		for off, want := range c.appliedMode.Patch() {
			if cfg[off] != want {
				c.appliedMode = nil
				c.generation.Add(1)
				break
			}
		}
		return nil
	})
}

// ResetOutput 在设备重新应答时使模式和成功帧基线失效。
func (c *Controller) ResetOutput(ctx context.Context) error {
	return command(c, ctx, func() error { c.appliedMode = nil; c.generation.Add(1); return nil })
}
