package frame

import (
	"testing"

	"github.com/wednesque/yogod/internal/color"
)

// TestKBMapIsAPermutation 是这块键盘最重要的几何约束：
//
// KBMap 里 96 个格子里恰好 84 个有灯，而且 84 个索引必须**不重不漏**地覆盖 0..83。
// 写错一个数字，整块键盘就会错位一颗灯 —— 而且很难看出来。
func TestKBMapIsAPermutation(t *testing.T) {
	seen := make(map[uint8]bool)
	for r := 0; r < KBRows; r++ {
		for c := 0; c < KBCols; c++ {
			v := KBMap[r][c]
			if v == 255 {
				continue // 空格
			}
			if v >= LEDCount {
				t.Fatalf("(%d,%d) 的 LED 索引 %d 越界（应 < %d）", r, c, v, LEDCount)
			}
			if seen[v] {
				t.Fatalf("LED 索引 %d 重复出现在 (%d,%d)", v, r, c)
			}
			seen[v] = true
		}
	}
	if len(seen) != LEDCount {
		t.Fatalf("KBMap 覆盖了 %d 个灯，应为 %d", len(seen), LEDCount)
	}
	for i := uint8(0); i < LEDCount; i++ {
		if !seen[i] {
			t.Fatalf("LED 索引 %d 在 KBMap 里找不到", i)
		}
	}
}

// TestEachCellVisitsEveryLED 确认遍历辅助函数也只访问有灯的格子。
func TestEachCellVisitsEveryLED(t *testing.T) {
	count := 0
	seen := make(map[int]bool)
	EachCell(func(r, c, led int) {
		count++
		if seen[led] {
			t.Fatalf("LED %d 被访问了两次", led)
		}
		seen[led] = true
		if KBMap[r][c] != uint8(led) {
			t.Fatalf("(%d,%d) 回调给的 led=%d 与 KBMap=%d 不符", r, c, led, KBMap[r][c])
		}
	})
	if count != LEDCount {
		t.Fatalf("访问了 %d 个格子，应为 %d", count, LEDCount)
	}
}

// TestGridIsRowMajor 确认点阵帧是行主序的 108 字节。
func TestGridIsRowMajor(t *testing.T) {
	var g Grid
	// 只点亮 (2,3)，它在字节偏移 (2*6+3)*3 = 45
	g[2][3] = color.RGBFloat{255, 0, 0}

	cal := color.DefaultCalibration()
	m := g.Encode(cal, 100, false)

	if m[45] != 255 {
		t.Fatalf("(2,3) 的 R 应在字节 45，实际 m[45]=%d", m[45])
	}
	// 其余全黑
	for i := 0; i < MatrixLen; i++ {
		if i >= 45 && i < 48 {
			continue
		}
		if m[i] != 0 {
			t.Fatalf("字节 %d 应为 0，实际 %d（帧没有清干净？）", i, m[i])
		}
	}
}

// TestKeysAreLedIndexed 确认逐键帧按 LED 索引排列，不是按行列。
//
// 这条很容易搞错：字符画是按 6×16 网格写的，但设备的 168 字节是按
// LED 索引 0..83 排的。中间必须过 KBMap 这一层。
func TestKeysAreLedIndexed(t *testing.T) {
	var l LEDs
	// LED 5 在 KBMap 的 (0,5)
	l[5] = color.RGB{0, 255, 0}

	// 用 RawKeys：这里验的是**字节布局**，不是调色管线。
	// 走 Encode 的话纯绿会被校正成 157（红:绿:蓝 ≈ 1:4:14 的补偿），
	// 那是 color 包的测试该管的事。
	k := RawKeys(l, 100)

	// RGB565 绿色 = 0x07E0，小端写 [0]=0xE0 [1]=0x07，位置在 5*2=10,11
	if k[10] != 0xE0 || k[11] != 0x07 {
		t.Fatalf("LED 5 应在字节 10,11 处为 E0 07，实际 %02X %02X", k[10], k[11])
	}
	for i := 0; i < KeysLen; i++ {
		if i == 10 || i == 11 {
			continue
		}
		if k[i] != 0 {
			t.Fatalf("字节 %d 应为 0，实际 %02X", i, k[i])
		}
	}
}

// TestSetGetRespectGaps 确认往空位写颜色不会污染任何灯。
func TestSetGetRespectGaps(t *testing.T) {
	var l LEDs
	// 第 5 行第 3 列、第 5 行第 5 列在 KBMap 里都是 255（没有灯）
	if HasLED(5, 3) {
		t.Fatal("(5,3) 应该没有灯")
	}
	if HasLED(5, 5) {
		t.Fatal("(5,5) 应该没有灯")
	}
	l.Set(5, 3, color.RGB{255, 255, 255})
	l.Set(5, 5, color.RGB{255, 255, 255})
	if l.Any() {
		t.Fatal("往空位写颜色不该点亮任何灯")
	}
	if got := l.Get(5, 3); got != (color.RGB{}) {
		t.Fatalf("空位读到 %v，应为全黑", got)
	}
	if got := l.Get(99, 99); got != (color.RGB{}) {
		t.Fatal("越界读应返回全黑")
	}
}

// TestEncodeRespectsBrightness 确认亮度是线性缩放（点阵默认不校正）。
func TestEncodeRespectsBrightness(t *testing.T) {
	var g Grid
	g[0][0] = color.RGBFloat{200, 100, 50}

	full := g.Encode(color.DefaultCalibration(), 100, false)
	half := g.Encode(color.DefaultCalibration(), 50, false)

	if full[0] != 200 {
		t.Fatalf("亮度 100 时 R 应为 200，实际 %d", full[0])
	}
	if half[0] != 100 {
		t.Fatalf("亮度 50 时 R 应为 100，实际 %d", half[0])
	}
}

// TestBlankIsAllZero 确认全黑帧真的是全零（熄屏靠它）。
func TestBlankIsAllZero(t *testing.T) {
	g := BlankGrid()
	if g.Any() {
		t.Fatal("BlankGrid 应该全黑")
	}
	l := BlankLEDs()
	if l.Any() {
		t.Fatal("BlankLEDs 应该全灭")
	}
	if m := g.Encode(color.DefaultCalibration(), 100, false); m != (Matrix{}) {
		t.Fatal("全黑网格编码后应仍是全零")
	}
	if k := l.Encode(color.DefaultCalibration(), 100); k != (Keys{}) {
		t.Fatal("全灭灯编码后应仍是全零")
	}
}

// TestFillLightsEverything 确认"整块铺一个色"真的铺满了 84 颗灯。
func TestFillLightsEverything(t *testing.T) {
	var l LEDs
	l.Fill(color.RGB{1, 2, 3})
	n := 0
	for i := range l {
		if l[i] != (color.RGB{1, 2, 3}) {
			t.Fatalf("LED %d 没被铺到", i)
		}
		n++
	}
	if n != LEDCount {
		t.Fatalf("铺了 %d 个，应为 %d", n, LEDCount)
	}
}
