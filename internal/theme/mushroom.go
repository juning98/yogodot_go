package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════ 蘑菇（Go 原生主题）══════

   红帽、白点、白柄、绿草。6×6 上不画静态蘑菇，画它"长出来"的过程：
   先是一根矮柄顶着三格宽的小帽，八秒里柄长高、帽抬到顶、白点浮出来；
   长满后停一会儿，再从土里重新长。

   键盘是同一片草地：上三排红帽带白点（帽上有横向波、白点呼吸），
   下三排绿草（草里也有波）。 */

const mushroomCycle = 8.0

// mushroomArt 是长满时的静态帧（t=0 正好是长满的这一刻）。
var mushroomArt = [frame.DotRows]string{
	".rrr..",
	"rwrwr.",
	"rrrrr.",
	"..ww..",
	"..ww..",
	".gggg.",
}

// mushroomGrowth 返回 0..1 的生长度：先缩回土里，再慢慢长满。
func mushroomGrowth(t float64) float64 {
	p := math.Mod(t, mushroomCycle) / mushroomCycle
	switch {
	case p < 0.08:
		return 1
	case p < 0.18:
		return 1 - color.Smoothstep((p-0.08)/0.10)
	case p < 0.75:
		return color.Smoothstep((p - 0.18) / 0.57)
	default:
		return 1
	}
}

func mushroomDot(t float64) frame.Grid {
	g := frame.BlankGrid()
	gr := mushroomGrowth(t)

	// 草地
	for c := 1; c <= 4; c++ {
		g[5][c] = DotColor('g')
	}

	// 菌柄：1..2 行，两列宽
	stemH := 1 + int(math.Round(gr))
	bottom := 4 - stemH
	for r := bottom + 1; r <= 4; r++ {
		g[r][2] = DotColor('w')
		g[r][3] = DotColor('w')
	}

	// 菌盖：1..3 行，越往下越宽；红帽轻微呼吸，白点不跟着暗。
	capH := 1 + int(math.Round(gr*2))
	top := bottom - capH + 1
	b := 1 - 0.12*(1-math.Cos(t*1.6))/2
	for i := 0; i < capH; i++ {
		width := 3
		if capH > 1 {
			width = 3 + 2*int(math.Round(float64(i)/float64(capH-1)))
		}
		left := (6 - width) / 2
		for c := left; c < left+width; c++ {
			g[top+i][c] = color.Scale(DotColor('r'), b)
		}
	}

	// 白点只在长满前三层、并且快长成时浮出来。
	if capH == 3 && gr > 0.85 {
		k := math.Min(1, (gr-0.85)/0.15)
		spot := color.Scale(DotColor('w'), k)
		g[top+1][1] = spot
		g[top+1][3] = spot
	}
	return g
}

// mushroomSpot 是键盘红帽上的白点位置。
func mushroomSpot(r, c int) bool {
	switch r {
	case 0:
		return c == 2 || c == 9 || c == 13
	case 1:
		return c == 5 || c == 11
	case 2:
		return c == 0 || c == 7 || c == 15
	}
	return false
}

// mushroomKB 是"红帽 + 白点 + 绿草"：帽上跑横向红波，白点呼吸，草里有绿波；
// 另外每 5 秒有一道暖色生长带从草底扫到帽顶，像养分往上走。
func mushroomKB(t float64) frame.LEDs {
	band := 5 - math.Mod(t/5.0, 1)*7
	return eachKey(func(r, c int) color.RGB {
		var col color.RGB
		switch {
		case r >= 3:
			k := 0.5 + 0.5*math.Sin(t*1.8+float64(c)*0.55+float64(r)*0.3)
			col = color.Mix(color.RGB{25, 120, 35}, color.RGB{80, 230, 70}, k)
		case mushroomSpot(r, c):
			k := 0.75 + 0.25*math.Sin(t*1.6+float64(c)*0.5)
			col = color.Scale(color.RGB{255, 255, 255}, k)
		default:
			k := 0.5 + 0.5*math.Sin(t*2.0-float64(c)*0.35+float64(r)*0.4)
			col = color.Mix(color.RGB{80, 0, 0}, color.RGB{255, 60, 50}, k)
		}
		if d := math.Abs(float64(r) - band); d < 0.9 {
			col = color.Mix(col, color.RGB{255, 235, 170}, (1-d/0.9)*0.6)
		}
		return fullExact(col)
	})
}

func mushroomTheme() Theme {
	return Theme{
		ID: "mushroom", Name: "蘑菇", Light: color.RGB{255, 60, 50},
		Dot: mushroomArt, KB: blankKB, DotAnim: mushroomDot, KBAnim: mushroomKB,
	}
}
