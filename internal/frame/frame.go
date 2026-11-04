// Package frame 定义要写进设备的两种帧。
//
// 渲染层只产出"意图"（Grid / LEDs），这一层负责把它们编码成设备认识的字节。
// 尺寸是硬约束，写错一个字节设备就什么都不显示，所以全部用定长数组。
package frame

import "github.com/wednesque/yogod/internal/color"

// 屏幕与键盘的几何。
const (
	DotRows = 6
	DotCols = 6
	KBRows  = 6
	KBCols  = 16
	// LEDCount 是这把键盘实际的灯珠数（96 个格子里只有 84 个有灯）。
	LEDCount = 84
)

// MatrixLen 是点阵帧长度：6×6×3 字节 RGB888，行主序。
const MatrixLen = DotRows * DotCols * 3

// KeysLen 是逐键帧长度：84 颗灯 × 2 字节 RGB565 小端。
const KeysLen = LEDCount * 2

// Grid 是一帧点阵的渲染结果。行 0 在上、列 0 在左。
type Grid [DotRows][DotCols]color.RGB

// LEDs 是一帧键盘灯的渲染结果，按 LED 索引 0..83 排列（不是按行列）。
type LEDs [LEDCount]color.RGB

// Matrix 是可直接写进设备的 108 字节点阵帧。
type Matrix [MatrixLen]byte

// Keys 是可直接写进设备的 168 字节逐键帧。
type Keys [KeysLen]byte

// BlankGrid 返回一帧全黑的点阵。
func BlankGrid() Grid { return Grid{} }

// BlankLEDs 返回一帧全灭的键盘灯。
func BlankLEDs() LEDs { return LEDs{} }

// Any 报告这一帧里是否有任何一格亮着。
func (g Grid) Any() bool {
	for r := range g {
		for c := range g[r] {
			if g[r][c].Sum() > 0 {
				return true
			}
		}
	}
	return false
}

// Any 报告这一帧里是否有任何一颗灯亮着。
func (l LEDs) Any() bool {
	for i := range l {
		if l[i].Sum() > 0 {
			return true
		}
	}
	return false
}

// Encode 把渲染结果编码成设备帧。points 是用户设置的点阵亮度（0..100），
// applyCalib 决定是否套用键盘那套校正（默认 false，见 color.Calibration.Dot）。
func (g Grid) Encode(cal color.Calibration, dotBright float64, applyCalib bool) Matrix {
	var m Matrix
	i := 0
	for r := 0; r < DotRows; r++ {
		for c := 0; c < DotCols; c++ {
			px := cal.Dot(g[r][c], dotBright, applyCalib)
			m[i], m[i+1], m[i+2] = px[0], px[1], px[2]
			i += 3
		}
	}
	return m
}

// Encode 把渲染结果编码成设备帧。kbBright 是用户设置的键盘亮度（0..100）。
func (l LEDs) Encode(cal color.Calibration, kbBright float64) Keys {
	var k Keys
	for i := 0; i < LEDCount; i++ {
		c := cal.Keys(l[i], kbBright)
		v := color.RGB565(c)
		k[i*2] = byte(v & 0xFF)
		k[i*2+1] = byte(v >> 8)
	}
	return k
}

// RawKeys 跳过校正直接编码（校准界面用）。
func RawKeys(l LEDs, brightness float64) Keys {
	var k Keys
	for i := 0; i < LEDCount; i++ {
		v := color.RGB565(color.KeysRaw(l[i], brightness))
		k[i*2] = byte(v & 0xFF)
		k[i*2+1] = byte(v >> 8)
	}
	return k
}

// KBMap 是物理布局：6 行 × 16 列，值是 LED 索引，255 表示该位置没有灯。
//
// 84 颗灯只占 96 个格子里的一部分。写帧时必须按 LED 索引排，
// 按行列排会让整块键盘错位。
var KBMap = [KBRows][KBCols]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 255, 255},
	{30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 255},
	{45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 255, 255},
	{59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 255, 255},
	{73, 74, 75, 255, 76, 255, 255, 255, 255, 77, 78, 79, 80, 81, 82, 83},
}

// HasLED 报告某个格子是否真的有灯。
func HasLED(r, c int) bool {
	if r < 0 || r >= KBRows || c < 0 || c >= KBCols {
		return false
	}
	return KBMap[r][c] != 255
}

// EachCell 按行优先遍历所有有灯的位置，回调收到 (行, 列, LED 索引)。
func EachCell(fn func(r, c, led int)) {
	for r := 0; r < KBRows; r++ {
		for c := 0; c < KBCols; c++ {
			if led := KBMap[r][c]; led != 255 {
				fn(r, c, int(led))
			}
		}
	}
}

// Get 读取某个格子的灯色（空格返回全黑）。
func (l LEDs) Get(r, c int) color.RGB {
	if !HasLED(r, c) {
		return color.RGB{}
	}
	return l[KBMap[r][c]]
}

// Set 写入某个格子的灯色（空格忽略）。
func (l *LEDs) Set(r, c int, v color.RGB) {
	if !HasLED(r, c) {
		return
	}
	l[KBMap[r][c]] = v
}

// Fill 把整块键盘铺成一个颜色（"只亮点阵屏"或专注模式用）。
func (l *LEDs) Fill(v color.RGB) {
	for i := range l {
		l[i] = v
	}
}
