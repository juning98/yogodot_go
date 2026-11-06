package device

import (
	"context"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/proto"
)

// openOrSkip 连上键盘，没有就跳过。
//
// 这些是**只读**的集成测试：只握手、只读配置、只读电量，
// 不写任何字节，所以不会把键盘留在奇怪的状态里。
func openOrSkip(t *testing.T) *Controller {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := Open(ctx)
	if err != nil {
		t.Skipf("没有键盘/接收器：%v", err)
	}
	t.Cleanup(func() { c.Close() })

	if !c.Awake() {
		if _, err := c.ReadConfig(ctx); err != nil {
			t.Skipf("键盘没应答（可能没开机 / 不在 2.4G 档）：%v", err)
		}
	}
	return c
}

// TestLiveReadConfig 验证整条读配置链路：握手 → 三段读 → 拼成 64 字节。
//
// 这是阶段 2 的核心验收：能读到配置，就说明报文编码、
// seq 分发、overlapped 超时、回包解析全部对得上。
func TestLiveReadConfig(t *testing.T) {
	c := openOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := c.ReadConfig(ctx)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}

	t.Logf("blMode=%d blBright=%d dotMode=%d dotBright=%d 休眠=%d 分钟",
		cfg.BLMode(), cfg.At(proto.OffBLBright),
		cfg.DotMode(), cfg.DotBright(), cfg.SleepMinutes())

	// 配置区有意义的部分不该全是 0 或全是 0xFF（那是没读到）
	allZero, allFF := true, true
	for _, b := range cfg {
		if b != 0 {
			allZero = false
		}
		if b != 0xFF {
			allFF = false
		}
	}
	if allZero || allFF {
		t.Fatalf("配置区全是 0x%02X，多半没真读到", cfg[0])
	}

	// 读两次应该一样（配置没变）
	again, err := c.ReadConfig(ctx)
	if err != nil {
		t.Fatalf("第二次 ReadConfig: %v", err)
	}
	if again != cfg {
		t.Logf("两次读到的配置不同（设备可能在自己更新），不视为失败")
	}
}

// TestLivePower 验证电量读取。
func TestLivePower(t *testing.T) {
	c := openOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := c.Power(ctx, true)
	if err != nil {
		t.Fatalf("Power: %v", err)
	}
	if p.Percent < 0 || p.Percent > 100 {
		t.Fatalf("电量越界：%d", p.Percent)
	}
	t.Logf("电量 %d%%，充电=%v 有线=%v 原始标志=%d",
		p.Percent, p.Charging, p.Wired, p.Flag)

	// 30 秒缓存：第二次不该再问设备
	start := time.Now()
	p2, err := c.Power(ctx, false)
	if err != nil {
		t.Fatalf("第二次 Power: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("第二次读电量花了 %s —— 缓存没生效", elapsed)
	}
	if p2.Percent != p.Percent {
		t.Errorf("缓存值不一致：%d vs %d", p2.Percent, p.Percent)
	}
}

// TestLiveWiredFlagMatchesProduct 验证有线/无线判定。
//
// 依据是产品名里有没有 "Dongle"：接收器报的名字带，键盘直连不带。
func TestLiveWiredFlagMatchesProduct(t *testing.T) {
	c := openOrSkip(t)
	st := c.Status()
	t.Logf("产品=%q 有线=%v", st.Product, st.Wired)

	if st.Product == "" {
		t.Skip("读不到产品名，无法判定")
	}
	wantWired := !containsDongle(st.Product)
	if st.Wired != wantWired {
		t.Errorf("有线判定错了：产品名 %q，Wired=%v", st.Product, st.Wired)
	}
}

func containsDongle(s string) bool {
	lower := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower[i] = c
	}
	for i := 0; i+6 <= len(lower); i++ {
		if string(lower[i:i+6]) == "dongle" {
			return true
		}
	}
	return false
}
