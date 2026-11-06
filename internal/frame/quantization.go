package frame

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
)

// 8×8 Bayer 矩阵左上 6×6 的相对顺序，重新编号为 0..35。
// 阈值只由屏幕位置决定：静止的颜色不会因时间、帧率或重绘而变化。
// 三个通道共用阈值，灰色仍为灰色；每通道误差小于一个 RGB888 档位。
var dotQuantizationRanks = [DotRows][DotCols]uint8{
	{0, 18, 5, 23, 2, 20},
	{27, 9, 32, 14, 29, 11},
	{7, 25, 4, 22, 8, 26},
	{34, 16, 31, 13, 35, 17},
	{3, 21, 6, 24, 1, 19},
	{30, 12, 33, 15, 28, 10},
}

func quantizeAnimated(in color.RGBFloat, row, col int) color.RGB {
	active := 0
	for ch := range in {
		in[ch] = color.ClampFloat(in[ch])
		if in[ch] > 0 {
			active++
		}
	}
	if active < 2 {
		// 单色没有混色偏移，保留原来的取整和亮度范围。
		return color.FromFloat(in)
	}
	threshold := (float64(dotQuantizationRanks[row][col]) + .5) / (DotRows * DotCols)
	var out color.RGB
	for ch, v := range in {
		out[ch] = uint8(math.Floor(v + threshold))
	}
	return out
}
