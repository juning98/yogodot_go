package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════ DeepSeek 鲸（Go 原生主题，不来自 yogo.html）══════

   6×6 只有 36 格，画不出一整条鲸鱼。取舍是只留三件识别物：
   右上竖起的尾鳍、白眼睛、最后一行左半段的胸鳍；身体用品牌蓝
   #4D6BFE 铺满 —— 少任何一件，剩下的就是一块蓝色方块。

   动画三样，都不改轮廓：
     ① 尾鳍：左尖在 2..4 之间扫，右尖固定，2.9 秒一个来回；
     ② 眨眼：3.4 秒一次，末尾眼睛熄一下；
     ③ 呼吸：整条鲸的亮度起伏 22%（余弦从 1 开始，t=0 与静态帧一致）。
   眼睛不跟着呼吸变暗 —— 它是唯一的高光点，暗了整块就像"熄了"。
   三条节奏各自独立（2.9s / 3.4s / 3.7s），都是纯函数，任意 t 可重放。 */

// deepseekWhale 是静态帧。D = 品牌蓝，w = 白（眼睛）。
//
// 顶上是尾鳍：两个尖 + 中间一格凹口；身体从第二行起，
// 最后一行左半段是胸鳍。尾鳍不能画平，否则整块就是"蓝色方块"。
var deepseekWhale = [frame.DotRows]string{
	"...D.D",
	"..DDD.",
	".DDDD.",
	"DwDDD.",
	"DDDDD.",
	".DDD..",
}

// deepseekDot 是点阵动画。纯函数：同一 t 永远同一帧。
func deepseekDot(t float64) frame.Grid {
	g := DotFromArt(deepseekWhale)

	// 尾鳍：左尖在 2..4 之间扫，右尖固定在 5。t=0 时左尖正好在 3，
	// 与静态帧一致；扫到 4 时凹口合上，扫到 2 时尾鳍张开。
	lc := int(math.Round(3 + 1.2*math.Sin(t*2.2)))
	if lc != 3 {
		g[0][3] = color.RGBFloat{}
	}
	if lc >= 0 && lc < frame.DotCols {
		g[0][lc] = DotColor('D')
	}

	// 眨眼：3.4 秒一次，末尾 0.12 秒。t=0 时睁着。
	if math.Mod(t, 3.4) > 3.28 {
		g[3][1] = color.RGBFloat{}
	}

	// 呼吸：余弦从 1 开始，所以 t=0 的帧与静态图逐格一致。
	// 眼睛单独存一份、缩完再放回去 —— 它是全屏唯一的白，不能跟着暗。
	eye := g[3][1]
	g = scaled(g, 1-0.22*(1-math.Cos(t*1.7))/2)
	g[3][1] = eye
	return g
}

// deepseekCurrent 是键盘上的那条洋流：深海 → 下层 → 品牌蓝（双份）→ 浅滩 → 浪尖。
//
// 键盘出帧会把每颗灯归一到最大通道 255（亮度交给全局滑杆），
// 所以"深海"不能靠压暗，只能靠饱和度和明度差拉开。
var deepseekCurrent = []color.RGB{
	{10, 24, 92},    // 深海
	{30, 60, 200},   // 下层
	{77, 107, 254},  // #4D6BFE
	{77, 107, 254},  // 品牌蓝多占一段，让它成为画面主体
	{130, 175, 255}, // 浅滩
	{235, 244, 255}, // 浪尖
}

// deepseekKB 是一条横向的慢流：白浪从右往左推，整块键盘没有硬边。
func deepseekKB(t float64) frame.LEDs {
	return kbFlow(deepseekCurrent, FlowOpts{
		DX: Num(1), DY: Num(0.4), Speed: Num(0.22), Wobble: 0.05,
		Repeat: Num(0.72),
	})(t)
}

// deepseekTheme 组装这套原生主题。buildAll 把它接在 24 套移植主题之后。
func deepseekTheme() Theme {
	return Theme{
		ID:      "deepseek",
		Name:    "DeepSeek 鲸",
		Light:   color.RGB{77, 107, 254},
		Dot:     deepseekWhale,
		KB:      blankKB,
		DotAnim: deepseekDot,
		KBAnim:  deepseekKB,
	}
}
