package main

import (
	"testing"

	"github.com/wednesque/yogod/internal/frame"
)

// 爬坡帧必须帧帧不同 —— 全相同的帧既测不出撕裂，
// 也看不出设备是不是还活着（画面定格和帧相同无法区分）。
func TestBenchKeysGenConsecutiveFramesDiffer(t *testing.T) {
	prev := genBenchKeys(0)
	for i := 1; i < 84; i++ {
		cur := genBenchKeys(i)
		if cur == prev {
			t.Fatalf("第 %d 帧与前一帧完全相同", i)
		}
		prev = cur
	}
}

func TestBenchKeysGenLightsEightKeys(t *testing.T) {
	k := genBenchKeys(0)
	lit := 0
	for i := 0; i < frame.LEDCount; i++ {
		if k[i*2] != 0 || k[i*2+1] != 0 {
			lit++
		}
	}
	if lit != 8 {
		t.Fatalf("应点亮 8 颗灯，实际 %d", lit)
	}
}

func TestBenchMatrixGenConsecutiveFramesDiffer(t *testing.T) {
	prev := genBenchMatrix(0)
	for i := 1; i < 36; i++ {
		cur := genBenchMatrix(i)
		if cur == prev {
			t.Fatalf("第 %d 帧与前一帧完全相同", i)
		}
		prev = cur
	}
}

// hsvRGB 的六个分段端点必须准 —— 它要是偏色，爬坡时看到的颜色就没参考价值。
func TestHSVPrimaryHues(t *testing.T) {
	cases := []struct {
		h       float64
		r, g, b byte
	}{
		{0, 255, 0, 0},     // 红
		{60, 255, 255, 0},  // 黄
		{120, 0, 255, 0},   // 绿
		{180, 0, 255, 255}, // 青
		{240, 0, 0, 255},   // 蓝
		{300, 255, 0, 255}, // 品红
	}
	for _, c := range cases {
		r, g, b := hsvRGB(c.h, 1, 1)
		if r != c.r || g != c.g || b != c.b {
			t.Errorf("hsvRGB(%v) = (%d,%d,%d)，期望 (%d,%d,%d)", c.h, r, g, b, c.r, c.g, c.b)
		}
	}
}

// RGB565 编码必须与 frame 包的线上格式一致（小端、5-6-5 位段）。
func TestRGB565LELayout(t *testing.T) {
	if v := rgb565LE(255, 0, 0); v != 0xF800 {
		t.Errorf("纯红 = %04X，期望 F800", v)
	}
	if v := rgb565LE(0, 255, 0); v != 0x07E0 {
		t.Errorf("纯绿 = %04X，期望 07E0", v)
	}
	if v := rgb565LE(0, 0, 255); v != 0x001F {
		t.Errorf("纯蓝 = %04X，期望 001F", v)
	}
}
