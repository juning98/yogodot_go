package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════ 橡皮鸭（Go 原生主题）══════

   黄鸭、橙嘴、黑眼，浮在一条蓝水线上。6×6 只留这三件套，
   动作是眨眼、张嘴和"水面呼吸"—— 鸭子本身不挪，水在动。

   键盘取它的世界：上面是黄天（有太阳和两朵飘动的云），下面两排是蓝水，
   水里有波纹。 */

var duckArt = [frame.DotRows]string{
	".yy...",
	".yky..",
	".yyyo.",
	"yyyyy.",
	".yyy..",
	"bbbbbb",
}

const duckCycle = 11.0

// duckDrift 返回整只鸭的横向偏移：0..6 秒内右漂一格再回来，潜水时回中。
func duckDrift(t float64) int {
	p := math.Mod(t, duckCycle)
	if p >= 6 {
		return 0
	}
	return int(math.Round(0.5 * (1 - math.Cos(p*1.05))))
}

// duckSink 返回鸭身下沉的格数：6 秒开始下潜，7.2 秒全没，9 秒开始上浮。
func duckSink(t float64) int {
	p := math.Mod(t, duckCycle)
	switch {
	case p < 6:
		return 0
	case p < 7.2:
		return int(math.Round(5 * color.Smoothstep((p-6)/1.2)))
	case p < 9:
		return 5
	case p < 10.2:
		return int(math.Round(5 * (1 - color.Smoothstep((p-9)/1.2))))
	default:
		return 0
	}
}

func duckDot(t float64) frame.Grid {
	dx := duckDrift(t)
	dy := duckSink(t)
	g := frame.BlankGrid()

	// 鸭身（第 0..4 行）按 dx/dy 平移；水面单独画，所以潜下去时会被水盖住。
	for r := 0; r < 5; r++ {
		for c := 0; c < frame.DotCols; c++ {
			ch := duckArt[r][c]
			if ch == '.' {
				continue
			}
			rr, cc := r+dy, c+dx
			if rr < 0 || rr >= frame.DotRows || cc < 0 || cc >= frame.DotCols {
				continue
			}
			g[rr][cc] = DotColor(ch)
		}
	}

	// 只在浮在水面时做表情和翅膀；潜水时不做。
	if dy == 0 {
		// 眨眼：3.6 秒一次，末尾 0.15 秒。t=0 时睁着。
		if math.Mod(t, 3.6) > 3.45 {
			if cc := 2 + dx; cc < frame.DotCols {
				g[1][cc] = DotColor('y')
			}
		}
		// 张嘴：每 3.2 秒把橙嘴伸出去一格；横向漂到边缘时抬上嘴。
		if q := math.Mod(t, 3.2); q > 0.4 && q < 0.58 {
			if dx == 0 {
				g[2][5] = DotColor('o')
			} else {
				g[1][5] = DotColor('o')
			}
		}
		// 翅膀：一道浅黄从身体左侧扫到右侧再扫回来。
		if w := 0.5 * (1 - math.Cos(t*2.4)); w > 0.15 {
			wc := 1 + int(math.Round(2*w))
			if cc := wc + dx; cc < frame.DotCols {
				g[3][cc] = DotColor('Y')
			}
		}
	}

	// 水面：从中间向两边荡的行波，t=0 时整条水线还是静态蓝。
	for c := 0; c < frame.DotCols; c++ {
		k := 0.5 * (math.Sin(t*2.2+float64(c)*0.9) - math.Sin(float64(c)*0.9))
		if k >= 0 {
			g[5][c] = color.MixFloat(DotColor('b'), color.RGBFloat{120, 220, 255}, k)
		} else {
			g[5][c] = color.MixFloat(DotColor('b'), color.RGBFloat{15, 40, 130}, -k)
		}
	}

	// 下潜/上浮时水线两侧溅水花，水下冒气泡。
	p := math.Mod(t, duckCycle)
	if (p > 6 && p < 6.6) || (p > 9.6 && p < 10.4) {
		g[4][0] = color.RGBFloat{220, 245, 255}
		g[4][5] = color.RGBFloat{220, 245, 255}
	}
	if p > 6.2 && p < 9.8 {
		for i, off := range []float64{0, 0.8, 1.6} {
			y := 5 - math.Mod((p-6.2)*0.9+off, 2.8)
			yy := int(y)
			if yy >= 3 && yy <= 5 {
				g[yy][[]int{1, 3, 4}[i]] = color.RGBFloat{200, 240, 255}
			}
		}
	}
	return g
}

// duckCloud 是一朵在天空中横向飘的云。
type duckCloud struct {
	off float64 // 初始相位
	y   float64 // 所在行
	w   float64 // 半径（格）
}

var duckClouds = []duckCloud{
	{off: 4, y: 0.7, w: 3.1},
	{off: 12, y: 1.9, w: 2.7},
}

// duckKB 是"黄天 + 蓝水"：天上有一个会呼吸的太阳、两朵横着飘的云，
// 下面两排蓝水有波纹。
func duckKB(t float64) frame.LEDs {
	return eachKey(func(r, c int) color.RGB {
		if r >= 4 {
			k := 0.5 + 0.5*math.Sin(t*2.2+float64(c)*0.55)
			water := color.Mix(color.RGB{20, 60, 200}, color.RGB{0, 200, 255}, k)

			// 太阳在水里的倒影：右半边一条晃动的金色光带。
			if d := math.Abs(float64(c) - 13); d < 2.6 {
				shimmer := 0.35 + 0.35*math.Sin(t*2.4+float64(c)*0.8)
				water = color.Mix(water, color.RGB{255, 200, 80}, (1-d/2.6)*shimmer)
			}
			// 水面顶行：一道道移动的白浪。
			if r == 4 && math.Sin(t*1.6+float64(c)*0.7) > 0.72 {
				water = color.Mix(water, color.RGB{240, 252, 255}, 0.75)
			}
			return fullExact(water)
		}

		// 天空：从顶部的金黄到底部的暖橙，白云和太阳才压得住。
		k := float64(r) / 3
		sky := color.Mix(color.RGB{255, 205, 50}, color.RGB{255, 150, 0}, k)

		// 太阳：右上角，半径缓慢呼吸。
		sunR := 2.2 + 0.35*math.Sin(t*1.2)
		if d := math.Hypot(float64(c)-13, float64(r)-0.5); d < sunR {
			sky = color.Mix(sky, color.RGB{255, 255, 235}, 1-d/sunR)
		}

		// 云：从右往左飘，菱形柔边。
		for _, cl := range duckClouds {
			cx := math.Mod(t*2.0+cl.off, 22) - 3
			if math.Abs(float64(c)-cx)+0.9*math.Abs(float64(r)-cl.y) < cl.w {
				sky = color.Mix(sky, color.RGB{255, 250, 235}, 0.85)
			}
		}
		return fullExact(sky)
	})
}

func duckTheme() Theme {
	return Theme{
		ID: "duck", Name: "橡皮鸭", Light: color.RGB{255, 210, 0},
		Dot: duckArt, KB: blankKB, DotAnim: duckDot, KBAnim: duckKB,
	}
}
