// Package theme 保存点阵屏与键盘背光的全部美术资产：
// 调色板、构图原语、24 套移植主题 + 4 套原生、48 张单独图案。
//
// 这里的定论来自旧版 yogo.html，逐字符、逐参数搬过来。
// 改这些值不是"重构"，是"改设计"—— 必须对着实物键盘验证。
package theme

import "github.com/wednesque/yogod/internal/color"

// DotPalette 是点阵屏的字符画调色板。
//
// 点阵屏是真正的全彩 LED，混色准，所以这些值可以按设计稿填。
// 空格 / '.' 表示熄灭。
var DotPalette = map[byte]color.RGB{
	'.': {0, 0, 0},
	'r': {255, 40, 30},   // 红
	'o': {255, 120, 0},   // 橙
	'y': {255, 210, 0},   // 黄
	'g': {40, 220, 60},   // 绿
	'c': {0, 200, 255},   // 青
	'b': {40, 90, 255},   // 蓝
	'D': {77, 107, 254},  // DeepSeek 品牌蓝 #4D6BFE
	'p': {255, 60, 170},  // 粉
	'v': {160, 60, 255},  // 紫
	'w': {255, 255, 255}, // 白
	'd': {90, 90, 110},   // 暗灰（勾边用）
	'k': {26, 20, 10},    // 近黑
	'a': {0, 190, 240},
	'A': {125, 228, 248},
	'l': {110, 168, 218},
	's': {62, 62, 74},
	'S': {135, 135, 150},
	'O': {245, 165, 120},
	'n': {118, 52, 32},
	'm': {255, 120, 0},
	'Y': {255, 228, 95},
	'G': {115, 238, 125},
	'e': {8, 10, 16},
	't': {0, 235, 205},
	'L': {60, 255, 0},
	'B': {165, 72, 0},
}

// KBPalette 是键盘背光的字符画调色板。
//
// 刻意不含纯白：84 颗灯在亮度 90 下铺白会非常晃眼，
// 所有"浅色"都带住色相，最亮的几个也压在 245 以内。
// '.' 表示这一格不点亮。
var KBPalette = map[byte]color.RGB{
	'k': {20, 12, 4},
	'e': {6, 8, 14},
	's': {62, 62, 74},
	'S': {132, 132, 148},
	'y': {255, 195, 0},
	'Y': {255, 225, 90},
	'd': {112, 86, 26},
	'r': {255, 40, 35},
	'R': {250, 110, 72},
	'n': {118, 52, 32},
	'o': {217, 119, 87},
	'O': {245, 165, 120},
	'm': {255, 120, 0},
	'g': {25, 190, 45},
	'G': {110, 235, 120},
	'b': {25, 95, 225},
	'l': {108, 162, 212},
	'a': {0, 185, 235},
	'A': {118, 222, 244},
	'p': {232, 95, 158},
	'v': {140, 65, 240},
	'L': {60, 255, 0},
	'B': {165, 72, 0},
}

// DotColor 查点阵调色板。未知字符按熄灭处理。
func DotColor(ch byte) color.RGBFloat {
	if c, ok := DotPalette[ch]; ok {
		return c.Float()
	}
	return color.RGBFloat{}
}

// KBColor 查键盘调色板。未知字符与 '.' 一样按熄灭处理。
func KBColor(ch byte) color.RGB {
	if c, ok := KBPalette[ch]; ok {
		return c
	}
	return color.RGB{}
}

// Full 把颜色归一到最大通道 255。
//
// 这块键盘的亮度差肉眼分辨不出来，所以区分一律靠色相：
// 所有键跑满亮度，整体明暗交给全局亮度滑杆。
func Full(c color.RGB) color.RGB {
	mx := c[0]
	if c[1] > mx {
		mx = c[1]
	}
	if c[2] > mx {
		mx = c[2]
	}
	if mx == 0 {
		return color.RGB{}
	}
	k := 255.0 / float64(mx)
	return color.RGB{
		color.Clamp(float64(c[0]) * k),
		color.Clamp(float64(c[1]) * k),
		color.Clamp(float64(c[2]) * k),
	}
}

// HueShift 旋转色相，保持满饱和满亮度 —— 用来从主色自动生出有对比的第二色。
func HueShift(c color.RGB, deg float64) color.RGB {
	r := float64(c[0]) / 255
	g := float64(c[1]) / 255
	b := float64(c[2]) / 255
	mx := r
	if g > mx {
		mx = g
	}
	if b > mx {
		mx = b
	}
	mn := r
	if g < mn {
		mn = g
	}
	if b < mn {
		mn = b
	}
	d := mx - mn
	if d <= 0 {
		return Full(c)
	}
	var h float64
	switch mx {
	case r:
		h = (g - b) / d
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h = h*60 + deg
	for h < 0 {
		h += 360
	}
	h = mod(h, 360)

	cc := d / mx
	x := cc * (1 - abs(mod(h/60, 2)-1))
	m := 1 - cc
	var o [3]float64
	switch {
	case h < 60:
		o = [3]float64{cc, x, 0}
	case h < 120:
		o = [3]float64{x, cc, 0}
	case h < 180:
		o = [3]float64{0, cc, x}
	case h < 240:
		o = [3]float64{0, x, cc}
	case h < 300:
		o = [3]float64{x, 0, cc}
	default:
		o = [3]float64{cc, 0, x}
	}
	return color.RGB{
		color.Clamp((o[0] + m) * 255),
		color.Clamp((o[1] + m) * 255),
		color.Clamp((o[2] + m) * 255),
	}
}

func mod(a, b float64) float64 {
	m := a - b*float64(int(a/b))
	if m < 0 {
		m += b
	}
	return m
}

func abs(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}
