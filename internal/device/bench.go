package device

import (
	"context"
	"time"

	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// BenchStep 是爬坡测试一档的统计。
type BenchStep struct {
	FPS      float64       // 目标帧率
	Frames   int           // 成功写出的帧数
	Failed   int           // 写失败（含超时）的帧数
	Elapsed  time.Duration // 档位总耗时
	MinWrite time.Duration // 单帧最快
	AvgWrite time.Duration
	MaxWrite time.Duration // 单帧最慢（卡顿尖刺）
	ProbeOK  bool          // 档位结束后设备是否仍应答命令
}

// ActualFPS 是这一档真实达到的帧率 —— 写设备本身的耗时会拉低它，
// 它明显低于目标 FPS 时，说明写路径本身已经到顶。
func (s BenchStep) ActualFPS() float64 {
	if s.Elapsed <= 0 {
		return 0
	}
	return float64(s.Frames) / s.Elapsed.Seconds()
}

// maxBenchFailStreak 是单档内允许的连续写失败次数。
// 连续失败说明链路已经堵死，没必要把这一档熬完（每次超时 800ms，熬完要几分钟）。
const maxBenchFailStreak = 3

// BenchKeys 以目标帧率推 frames 帧键盘灯。
//
// 它刻意绕过 SendKeys 的限速/去重/打字让路 —— 这是压力测试，
// 量的就是链路与固件的真实承受力，不是日常行为。
// gen(i) 生成第 i 帧，应当帧帧不同，方便肉眼判断哪一档开始撕裂。
func (c *Controller) BenchKeys(ctx context.Context, fps float64, frames int, gen func(i int) frame.Keys) BenchStep {
	return c.bench(ctx, fps, frames, func(i int) error {
		return c.writeKeys(ctx, gen(i))
	})
}

// BenchMatrix 同 BenchKeys，推点阵帧。
func (c *Controller) BenchMatrix(ctx context.Context, fps float64, frames int, gen func(i int) frame.Matrix) BenchStep {
	return c.bench(ctx, fps, frames, func(i int) error {
		return c.writeMatrix(ctx, gen(i))
	})
}

func (c *Controller) bench(ctx context.Context, fps float64, frames int, write func(i int) error) BenchStep {
	st := BenchStep{FPS: fps, MinWrite: time.Hour}
	interval := time.Duration(float64(time.Second) / fps)
	start := time.Now()
	next := start
	failStreak := 0
	for i := 0; i < frames; i++ {
		if i > 0 {
			next = next.Add(interval)
			select {
			case <-ctx.Done():
				st.Elapsed = time.Since(start)
				return st
			case <-time.After(time.Until(next)):
			}
		}
		t0 := time.Now()
		err := write(i)
		d := time.Since(t0)
		if err != nil {
			st.Failed++
			failStreak++
			if failStreak >= maxBenchFailStreak {
				break // 链路已堵死，提前结束这一档
			}
		} else {
			failStreak = 0
			st.Frames++
		}
		if d < st.MinWrite {
			st.MinWrite = d
		}
		if d > st.MaxWrite {
			st.MaxWrite = d
		}
		st.AvgWrite += d
	}
	st.Elapsed = time.Since(start)
	if st.Frames+st.Failed > 0 {
		st.AvgWrite /= time.Duration(st.Frames + st.Failed)
	}
	if st.MinWrite == time.Hour {
		st.MinWrite = 0
	}

	// 档位结束探活：设备还接得上话吗？
	// 写是单向的，固件被灌死时写调用照样返回成功，只有要回包的命令能试出来。
	b, err := c.transact(ctx, proto.NewLen(proto.CmdPowerInfo, 0, 0, nil), true)
	st.ProbeOK = err == nil && b != nil
	return st
}
