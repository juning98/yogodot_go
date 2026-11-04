package theme

import (
	"testing"

	"github.com/wednesque/yogod/internal/frame"
)

// TestFireAnimationIsSane 对火焰动画只断言结构性质。
//
// 它是对拍不了的：`ANIMS.fire` 每帧都在调 `Math.random()`，
// 而且 `fireBuf` 跨帧演化 —— 它不是一个 f(t)。
// 能保证的是"它看起来像火并且一直在动"。
func TestFireAnimationIsSane(t *testing.T) {
	fire := NewFire()
	if fire == nil {
		t.Fatal("NewFire 返回 nil")
	}

	var prev frame.Grid
	distinct := 0
	for i := 0; i < 10; i++ {
		g := fire(float64(i) / 6)
		if i > 0 && g != prev {
			distinct++
		}
		prev = g
	}
	if distinct < 5 {
		t.Fatalf("10 帧里只有 %d 次变化，火焰基本没在动", distinct)
	}
}

// TestFireWarmup 记录并锁定火焰的"预热"特性。
//
// fireBuf 初值全零，而每一行只从下面一行取热源（r 读 r+1），
// 所以火苗是**一帧往上爬一格**：第一帧全黑，之后逐行点亮，约 7 帧铺满。
// 旧版 UI 里那 28 帧预热就是为这个 —— 不预热的话切到岩浆主题头几帧是黑的。
//
// 这条测试的意义：如果有人"优化"成第一帧就亮，说明算法被改坏了。
func TestFireWarmup(t *testing.T) {
	fire := NewFire()

	if g := fire(0); g.Any() {
		t.Error("第一帧应该全黑（缓冲区初值全零），实际有亮格")
	}

	var prev frame.Grid
	lit := make([]int, 0, 8)
	for i := 0; i < 8; i++ {
		g := fire(float64(i) / 6)
		n := 0
		for r := 0; r < frame.DotRows; r++ {
			for c := 0; c < frame.DotCols; c++ {
				if g[r][c].Sum() > 8 {
					n++
				}
			}
		}
		lit = append(lit, n)
		prev = g
	}
	_ = prev
	t.Logf("逐帧点亮格数：%v", lit)

	if lit[len(lit)-1] == 0 {
		t.Fatal("预热 8 帧后仍然是全黑，火焰算法没起作用")
	}
	// 应该是单调爬升的（允许个别抖动由随机数造成）
	if lit[len(lit)-1] <= lit[0] {
		t.Errorf("灯数没有增长：%v", lit)
	}
}

// TestAnimsAreDeterministic 确认除 fire 之外的程序动画都是纯函数。
//
// 引擎会在任意时刻重复取同一时刻的帧（重绘、切场景回来），
// 如果动画不是纯函数，画面就会莫名其妙地漂。
func TestAnimsAreDeterministic(t *testing.T) {
	cases := map[string]DotAnim{
		"plasma":    Plasma,
		"comet":     Comet,
		"heartbeat": Heartbeat,
		"rain":      Rain,
		"breathe":   Breathe,
		"spiral":    Spiral,
	}
	for name, fn := range cases {
		if fn == nil {
			t.Errorf("%s 没有导出", name)
			continue
		}
		for _, tt := range []float64{0, 0.37, 1.5, 7.25} {
			if a, b := fn(tt), fn(tt); a != b {
				t.Errorf("%s(t=%v) 两次调用结果不同 —— 不是纯函数", name, tt)
			}
		}
		// 不同时刻应该产出不同画面（除非动画恰好是常数）
		if fn(0) == fn(3.7) {
			t.Errorf("%s 在 t=0 与 t=3.7 产出相同画面，动画可能没实现", name)
		}
	}
}
