package device

import (
	"testing"
	"time"
)

// 档位表的数值就是控制台文案里承诺的那些 fps —— 改数先改文案。
func TestRateTierGaps(t *testing.T) {
	cases := []struct {
		tier   RateTier
		matrix time.Duration
		keys   time.Duration
	}{
		{RateTierDefault, 160 * time.Millisecond, 100 * time.Millisecond}, // 6.25 / 10 fps
		{RateTierSmooth, 100 * time.Millisecond, 50 * time.Millisecond},   // 10 / 20 fps
		{RateTierExtreme, 67 * time.Millisecond, 40 * time.Millisecond},   // 15 / 25 fps
	}
	for _, c := range cases {
		m, k := c.tier.Gaps()
		if m != c.matrix || k != c.keys {
			t.Errorf("tier %d = (%v, %v)，期望 (%v, %v)", c.tier, m, k, c.matrix, c.keys)
		}
	}
}

// 未知档位值必须回落到默认档 —— 配置文件可能被手改出界。
func TestRateTierUnknownFallsBackToDefault(t *testing.T) {
	for _, bad := range []RateTier{-1, 3, 99} {
		m, k := bad.Gaps()
		if m != MinMatrixGap || k != MinKeysGap {
			t.Errorf("非法档位 %d 应回落默认档", bad)
		}
	}
}
