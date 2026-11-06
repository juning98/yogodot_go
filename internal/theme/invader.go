package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════ 太空侵略者（Go 原生主题）══════

   6×6 本来是它的原生尺寸。绿身子 + 两只亮绿触角，动作只有"横移"
   和"触角闪"，不需要更多 —— 街机里的它本来就这么动。

   键盘上排两只，整排一起横移；亮绿电流在形状里流，像老 CRT 的扫描。
   战机以外的键铺一层紫色星云，整块键盘都有波动，不再是黑的。 */

// invaderArt 是静态帧。g = 身体，G = 触角（会闪）。
var invaderArt = [frame.DotRows]string{
	".G..G.",
	"..gg..",
	".gggg.",
	"gggggg",
	"g.gg.g",
	"..gg..",
}

// invaderMarch 是横移节奏：sin 取 ±1 时整只挪一格。
const invaderMarch = 2.2

func invaderDot(t float64) frame.Grid {
	var g frame.Grid
	dx := int(math.Round(math.Sin(t * invaderMarch)))
	// 触角亮度：余弦从 1 开始，t=0 与静态帧一致。
	shine := 1 - 0.35*(1-math.Cos(t*3.1))/2
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			ch := invaderArt[r][c]
			if ch == '.' {
				continue
			}
			cc := c + dx
			if cc < 0 || cc >= frame.DotCols {
				continue
			}
			col := DotColor(ch)
			if ch == 'G' {
				col = color.MixFloat(color.RGBFloat{}, col, shine)
			}
			g[r][cc] = col
		}
	}
	return g
}

// MInvaders 是键盘上的两只侵略者：6×16，两段 6 列中间隔 4 列。
var MInvaders = [frame.KBRows]string{
	".X..X......X..X.",
	"..XX........XX..",
	".XXXX......XXXX.",
	"XXXXXX....XXXXXX",
	"X.XX.X....X.XX.X",
	"..XX........XX..",
}

var (
	invaderGreen = color.RGB{40, 220, 60}
	invaderLime  = color.RGB{150, 255, 120}
)

// invaderSpace 是键盘背景的星云：深紫 → 靛蓝 → 星空蓝，缓慢横流。
var invaderSpace = []color.RGB{
	{18, 0, 48},
	{40, 10, 110},
	{20, 30, 170},
	{10, 0, 60},
}

// invaderKB 让两只侵略者一起横移，形状里跑一道亮绿扫描；
// 非战机的键铺一层紫色星云，整块键盘都有波动，不再是黑的。
func invaderKB(t float64) frame.LEDs {
	dx := int(math.Round(math.Sin(t * invaderMarch)))
	bg := kbFlow(invaderSpace, FlowOpts{
		DX: Num(1), DY: Num(0.6), Speed: Num(0.16), Wobble: 0.08,
	})(t)
	return eachKey(func(r, c int) color.RGB {
		if !maskAt(MInvaders, r, c-dx) {
			return bg.Get(r, c)
		}
		k := 0.5 + 0.5*math.Sin(t*3.1+float64(c)*0.45+float64(r)*0.2)
		return fullExact(color.Mix(invaderGreen, invaderLime, k))
	})
}

func invaderTheme() Theme {
	return Theme{
		ID: "invader", Name: "太空侵略者", Light: invaderGreen,
		Dot: invaderArt, KB: blankKB, DotAnim: invaderDot, KBAnim: invaderKB,
	}
}
