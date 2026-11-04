package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ════════════ 构图工具 ════════════
   这块键盘的【亮度差肉眼分辨不出来】，所以区分一律靠色相：
   所有键跑满亮度，整体明暗交给全局亮度滑杆。
   fullExact() 兜底（即 JS 的 full()）：任何颜色进来先归一到最大通道 255。
   下面每个函数是一种不同的摆法，主题各用各的，不复用同一种。       */

// 摆法分两种形态：
//
//	静态：func() frame.LEDs —— 与时间无关，渲染一次就够
//	动画：KBAnim            —— 给定秒数返回这一帧
//
// Go 的函数类型不能做并集，所以所有摆法统一返回 KBAnim，静态摆法用
// Static() 固化成 func() frame.LEDs。JS 里 `kb: kbMask(...)` 这种直接
// 写函数的地方，渲染器是拿 `kb(0)` 取的（yogo.html:1969），
// 所以「静态 = t=0」不是偷懒，就是原行为。

// Stop 是渐变表上的一个锚点。对应 JS 的 [位置, 颜色] 二元组。
type Stop struct {
	Pos float64
	Col color.RGB
}

// Rect 是 kbRect 的矩形块。对应 JS 的 [c0,c1,r0,r1,颜色]，全部闭区间。
type Rect struct {
	C0, C1 int
	R0, R1 int
	Col    color.RGB
}

// Num 返回 v 的地址。用来给 FlowOpts 这类「0 也是有效值」的选项赋值。
func Num(v float64) *float64 { return &v }

// FlowOpts 对应 JS 的 {dx,dy,speed,wobble,repeat}。
//
// 默认值（JS 用 o.dx==null 之类判断）：dx=1、dy=0.35、speed=0.22、wobble=0、repeat=0.5。
// dx / dy / speed 在 LAYOUTS 里都出现过显式 0（dx:0 竖向流动、speed:0 定格），
// 所以这三个字段必须用指针区分「没写」和「写了 0」。
type FlowOpts struct {
	DX     *float64
	DY     *float64
	Speed  *float64
	Wobble float64 // JS 是 o.wobble||0，0 就是关掉抖动
	Repeat *float64
}

// FlowRadialOpts 对应 JS 的 {speed, scale}，默认 0.3 / 0.75。
// 0 在这里表示「省略」，因为 JS 用 ==null 判断，而 LAYOUTS 里没人显式传 0。
type FlowRadialOpts struct {
	Speed float64
	Scale float64
}

// SweepSoftOpts 对应 JS 的 {speed, width}，默认 0.45 / 4.5。0 表示「省略」。
type SweepSoftOpts struct {
	Speed float64
	Width float64
}

// RadialOpts 对应 JS 的 {power, pulse}。power 默认 1（JS 用 o.power||1，
// 所以 0 也等于 1）；pulse 为 0 表示不脉动。
type RadialOpts struct {
	Power float64
	Pulse float64
}

// ShapeOpts 对应 JS 的 {sweep}。sweep=0 表示不做扫描。
type ShapeOpts struct {
	Sweep float64
}

// CapsOpts 对应 JS 的 {cycle}。
//
// TODO(port): 原 JS 的 kbCaps 只实现了 cycle，主题里传的 {breathe:…} 和
// {wave:…} 在实现里根本没读 —— 那是没写完的选项。这里照样不实现，
// 免得凭空造出一种原版没有的效果。
type CapsOpts struct {
	Cycle float64
}

// KEY 是几个有名字的灯位 —— 注意是 LED 索引，不是行列。
// 硬件上的固定位置，改这里等于改键位表。
const (
	KeyEsc    = 0
	KeyDel    = 15
	KeyTab    = 30
	KeyCaps   = 45
	KeySpace  = 76
	KeyLShift = 59
	KeyRShift = 70
	KeyUp     = 71
	KeyLeft   = 80
	KeyDown   = 81
	KeyRight  = 82
	KeyW      = 32
	KeyA      = 46
	KeyS      = 47
	KeyD      = 48
	KeyEnter  = 57
)

// ZONE 是键帽三分区：字母区 / 修饰键 / 点缀键（ESC + 方向键）。
// 只留给真正适合分区的主题用。
var (
	zoneAlphas = []int{17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
		31, 32, 33, 34, 35, 36, 37, 38, 39, 40,
		46, 47, 48, 49, 50, 51, 52, 53, 54,
		60, 61, 62, 63, 64, 65, 66}
	zoneAccent = []int{0, 71, 80, 81, 82}
	// zoneMods 由排除法算出来，不手抄 —— 手抄一定会漏。
	zoneMods = func() []int {
		var mods []int
		for i := 0; i < frame.LEDCount; i++ {
			if !containsInt(zoneAlphas, i) && !containsInt(zoneAccent, i) {
				mods = append(mods, i)
			}
		}
		return mods
	}()
)

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// jsFrac 复刻 JS 的 (((x % 1) + 1) % 1)。
//
// 不能写成 x-math.Floor(x)：中间那步 +1 再 %1 会动到最低位，
// 而最低位恰好决定 65.5 这种 .5 边界往哪边舍入 —— 对拍时会成片差 1。
func jsFrac(x float64) float64 { return math.Mod(math.Mod(x, 1)+1, 1) }

// jsWrap 复刻 JS 的 (((x % m) + m) % m)：负数也折回 0..m。
func jsWrap(x, m float64) float64 { return math.Mod(math.Mod(x, m)+m, m) }

// fullExact 是构图内部用的 full()，比 palette.Full 少一次舍入误差。
//
// JS 写的是 c*255/mx（先乘后除）；palette.Full 先算 255/mx 再乘。
// 161*255/210 精确等于 195.5，而 161*(255/210) = 195.49999999999997 ——
// clamp 一个进 196、一个进 195。这类边界在 24 套主题里会零星冒出来。
func fullExact(c color.RGB) color.RGB {
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
	return color.RGB{
		color.Clamp(float64(c[0]) * 255 / float64(mx)),
		color.Clamp(float64(c[1]) * 255 / float64(mx)),
		color.Clamp(float64(c[2]) * 255 / float64(mx)),
	}
}

// hueShiftExact 是构图内部用的 hueShift()。
//
// 和 palette.HueShift 只差一处：JS 的相位是 (h*60+deg+360)%360，
// 那个 +360 再 %360 会把结果挪动一个 ulp。差值本身看不见，
// 但 43.49999999999995 和 43.500000000000064 的 clamp 结果差 1。
func hueShiftExact(c color.RGB, deg float64) color.RGB {
	r := float64(c[0]) / 255
	g := float64(c[1]) / 255
	b := float64(c[2]) / 255
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	d := mx - mn
	if d <= 0 {
		return fullExact(c)
	}
	var h float64
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h = math.Mod(h*60+deg+360, 360)

	cc := d / mx
	x := cc * (1 - math.Abs(math.Mod(h/60, 2)-1))
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

// idx 是 JS 里 ((i%n)+n)%n 的写法：把任意整数索引折到 0..n-1。
func idx(i, n int) int {
	if n <= 0 {
		return 0
	}
	m := i % n
	if m < 0 {
		m += n
	}
	return m
}

// kbConst 把 6×16 字符画直接固化成静态帧。
func kbConst(rows [frame.KBRows]string) func() frame.LEDs {
	a := KBFromArt(rows)
	return func() frame.LEDs { return a }
}

// eachKey 按物理格子遍历整块键盘，回调收到 (行, 列)，返回该键的颜色。
func eachKey(fn func(r, c int) color.RGB) frame.LEDs {
	var out frame.LEDs
	frame.EachCell(func(r, c, led int) { out[led] = fn(r, c) })
	return out
}

// Static 把一个摆法在 t=0 固化下来 —— 对应 JS 里 kb() 的空参调用。
func Static(a KBAnim) func() frame.LEDs {
	return func() frame.LEDs { return a(0) }
}

// gradAt 在渐变表上取 x 处的颜色（线性插值 + Full 归一）。
func gradAt(stops []Stop, x float64) color.RGB {
	a, b := stops[0], stops[len(stops)-1]
	for i := 0; i < len(stops)-1; i++ {
		if x >= stops[i].Pos && x <= stops[i+1].Pos {
			a, b = stops[i], stops[i+1]
			break
		}
	}
	var k float64
	if b.Pos != a.Pos {
		k = (x - a.Pos) / (b.Pos - a.Pos)
	}
	return fullExact(color.Mix(a.Col, b.Col, k))
}

// bandAt 把相位映射成颜色。用【三角波往返】而不是循环：0→1→0。
//
// 循环色带的问题是最后一个色直接跳回第一个色，会留一道硬接缝
// （实测相邻键色差能到 595，看着就是一条裂缝在跑）。往返则首尾天然接上。
func bandAt(cols []color.RGB, ph float64) color.RGB {
	n := len(cols)
	if n == 0 {
		return color.RGB{}
	}
	if n == 1 {
		return fullExact(cols[0])
	}
	// JS: (((ph%1)+1)%1)*2 —— 负数相位也要落在 0..1；必须用 jsFrac 复刻它的最低位
	x := jsFrac(ph) * 2
	if x > 1 {
		x = 2 - x
	}
	f := x * float64(n-1)
	i := int(math.Floor(math.Min(float64(n-2), f)))
	return fullExact(color.Mix(cols[i], cols[i+1], color.Smoothstep(f-float64(i))))
}

// maskAt 报告掩码在 (r,c) 是否为 'X'。越界 = 掩码外。
// 对应 JS 的 `(mask[r]||"")[c]!=="X"`。
func maskAt(mask [frame.KBRows]string, r, c int) bool {
	if r < 0 || r >= len(mask) {
		return false
	}
	row := mask[r]
	return c >= 0 && c < len(row) && row[c] == 'X'
}

/* ── 摆法 1：横向渐变（16 列足够平滑）── */
func kbGrad(stops []Stop) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB { return gradAt(stops, float64(c)/15) })
	}
}

// kbGradMove 同样走往返，避免渐变首尾跳回的接缝。
func kbGradMove(stops []Stop, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			x := jsFrac(float64(c)/15+t*speed) * 2
			if x > 1 {
				x = 2 - x
			}
			return gradAt(stops, x)
		})
	}
}

/* ── 摆法 2：纵向分层（6 行各一个色相，天空 / 上下身）── */
func kbRows(cols []color.RGB) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			i := r
			if i >= len(cols) {
				i = len(cols) - 1 // JS: cols[r]||cols[cols.length-1]
			}
			if i < 0 {
				i = 0
			}
			return fullExact(cols[i])
		})
	}
}

func kbRowsMove(cols []color.RGB, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			x := math.Mod(float64(r)/5+t*speed, 1) // JS 是裸的 %1，没有 +1/-1 那一步
			i := int(math.Min(float64(len(cols)-2), math.Floor(x*float64(len(cols)-1))))
			k := x*float64(len(cols)-1) - float64(i)
			return fullExact(color.Mix(cols[i], cols[i+1], k))
		})
	}
}

/* ── 摆法 3：棋盘交错（两个色相互咬，纹理感最强）── */
func kbCheck(a, b color.RGB, size int, speed float64) KBAnim {
	sz := size
	if sz == 0 {
		sz = 2
	}
	return func(t float64) frame.LEDs {
		ph := 0
		if speed != 0 {
			ph = int(math.Floor(t * speed))
		}
		return eachKey(func(r, c int) color.RGB {
			if (c/sz+r/sz+ph)%2 != 0 {
				return fullExact(b)
			}
			return fullExact(a)
		})
	}
}

/* ── 摆法 4：竖条纹（垄 / 均衡器 / 条纹袖）── */
func kbStripes(cols []color.RGB, w int, speed float64) KBAnim {
	ww := w
	if ww == 0 {
		ww = 2
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			i := int(math.Floor(float64(c)/float64(ww) + t*speed))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 5：中央竖带（企鹅肚子 / 跑道）── */
//
// c0/c1 是带子的左右列。JS 里省略时默认 5/10；LAYOUTS 只用过 6/9，
// 所以这里要求调用方显式给，免得用 0 当哨兵把「从 0 列开始」吃掉。
func kbBand(inner, outer color.RGB, c0, c1 int) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if c >= c0 && c <= c1 {
				return fullExact(inner)
			}
			return fullExact(outer)
		})
	}
}

/* ── 摆法 6：随机点缀（星空 / 萤火）── */
func kbScatter(base, acc color.RGB, thresh, speed float64) KBAnim {
	th := thresh
	if th == 0 {
		th = 0.86
	}
	sp := speed
	if sp == 0 {
		sp = 0.9
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			s := math.Sin(t*sp + float64(r*16+c)*2.399)
			if s > th {
				return fullExact(acc)
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 7：同心环（水母 / 涟漪）── */
func kbRings(cols []color.RGB, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			d := math.Hypot((float64(c)-7.5)/2.9, float64(r)-2.5) - t*speed
			i := int(math.Floor(math.Abs(d)))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 8：显式矩形块（腮红这种"长在特定位置"的特征）── */
func kbRect(base color.RGB, rects []Rect) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			for _, rc := range rects {
				if c >= rc.C0 && c <= rc.C1 && r >= rc.R0 && r <= rc.R1 {
					return fullExact(rc.Col)
				}
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 9：径向色相推移（墨在水里化开）── */
func kbRadial(core, edge color.RGB, opts RadialOpts) KBAnim {
	power := opts.Power
	if power == 0 {
		power = 1
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			d := math.Hypot((float64(c)-7.5)/7.5, (float64(r)-2.5)/2.5)
			if opts.Pulse != 0 {
				d = d / (1 + 0.18*math.Sin(t*opts.Pulse))
			}
			return fullExact(color.Mix(core, edge, math.Min(1, math.Pow(d, power))))
		})
	}
}

/* ── 摆法 10：左右分割（可移动的分界线 = 被吃掉的进度）── */
//
// at 默认 7.5、blend 默认 2.2、speed 省略则不动 —— JS 用 ==null 判断，
// 这里由调用方显式传（LAYOUTS 两处都传齐了）。speed=0 与省略等价。
func kbSplit(left, right color.RGB, at, blend, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		a := at
		if speed != 0 {
			a += math.Mod(t*speed, 20) - 6
		}
		return eachKey(func(r, c int) color.RGB {
			k := math.Max(0, math.Min(1, (float64(c)-a+blend/2)/blend))
			return fullExact(color.Mix(left, right, k))
		})
	}
}

/* ── 摆法 11：对角扫（熔岩裂缝）── */
func kbDiag(base, hi color.RGB, speed, width float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			x := float64(c)/15*0.75 + (1-float64(r)/5)*0.25
			d := math.Abs(x - (math.Mod(t*speed, 1.6) - 0.3))
			if d < width {
				return fullExact(color.Mix(base, hi, math.Pow(1-d/width, 1.8)))
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 12：对角渐变（从一角铺到另一角）── */
func kbCorner(a, b color.RGB) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			return fullExact(color.Mix(a, b, float64(c)/15*0.72+(float64(r)/5)*0.28))
		})
	}
}

/* ── 摆法 13：竖向下落（数字雨）── */
func kbFall(base, drop color.RGB, speed, density float64) KBAnim {
	var seed [frame.KBCols]float64
	for i := range seed {
		seed[i] = math.Mod(float64(i)*2.399, 6)
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			d := math.Mod(t*speed+seed[c], 9) - 1.5 - float64(r)
			if d >= 0 && d < density {
				return fullExact(color.Mix(base, drop, math.Pow(1-d/density, 1.6)))
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 14：底色 + 标志形状（心电图）── */
func kbShape(base color.RGB, mask [frame.KBRows]string, hi color.RGB, opts ShapeOpts) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if !maskAt(mask, r, c) {
				return fullExact(base)
			}
			if opts.Sweep != 0 {
				head := math.Mod(t*opts.Sweep, 1.6) - 0.3
				k := 0.45 + 0.55*math.Max(0, 1-math.Abs(float64(c)/15-head)*3.2)
				return fullExact(color.Mix(base, hi, k))
			}
			return fullExact(hi)
		})
	}
}

/* ── 摆法 15：键帽三分区（只留给真正适合分区的主题）── */
func kbCaps(alpha, mod, accent color.RGB, opts CapsOpts) KBAnim {
	var m [frame.LEDCount]color.RGB
	for _, i := range zoneMods {
		m[i] = mod
	}
	for _, i := range zoneAlphas {
		m[i] = alpha
	}
	for _, i := range zoneAccent {
		// TODO(port): JS 是 `accent||mod`，accent 若给全黑也会被当成"有值"。
		// 这里用零色当"没给"，因为没有调用方会传全黑点缀色。
		if accent.Sum() != 0 {
			m[i] = accent
		} else {
			m[i] = mod
		}
	}
	return func(t float64) frame.LEDs {
		spin := 0.0
		if opts.Cycle != 0 {
			spin = t * opts.Cycle * 60
		}
		return eachKey(func(r, c int) color.RGB {
			col := m[frame.KBMap[r][c]]
			if col.Sum() == 0 {
				col = mod // JS: map[led]||mod
			}
			if spin != 0 {
				return fullExact(hueShiftExact(col, spin))
			}
			return fullExact(col)
		})
	}
}

/* ── 摆法 16：四象限（四块各一个色相）── */
func kbQuad(a, b, c2, d color.RGB) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if r < 3 {
				if c < 8 {
					return fullExact(a)
				}
				return fullExact(b)
			}
			if c < 8 {
				return fullExact(c2)
			}
			return fullExact(d)
		})
	}
}

/* ── 摆法 17：斜条纹（对角方向的色带，跟横竖都不一样）── */
func kbDiagStripes(cols []color.RGB, w int, speed float64) KBAnim {
	ww := w
	if ww == 0 {
		ww = 3
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			i := int(math.Floor((float64(c)+float64(r)*2.2)/float64(ww) + t*speed))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 18：扇形（从中心放射的角度分区 = 触手 / 风车）── */
func kbSector(cols []color.RGB, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			a := math.Atan2((float64(r)-2.5)*2.6, float64(c)-7.5) + t*speed
			i := int(math.Floor(jsFrac(a/(math.Pi*2)) * float64(len(cols))))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 19：X 交叉（两条对角线用重点色）── */
func kbCross(base, hi color.RGB, w float64) KBAnim {
	ww := w
	if ww == 0 {
		ww = 1.6
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			x := float64(c) / 15 * 5
			d1 := math.Abs(x - float64(r))
			d2 := math.Abs(x - (5 - float64(r)))
			if math.Min(d1, d2) < ww {
				return fullExact(hi)
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 20：马赛克（固定伪随机的色块，有肌理但不闪）── */
func kbMosaic(cols []color.RGB, size int, speed float64) KBAnim {
	sz := size
	if sz == 0 {
		sz = 2
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			bx := c / sz
			by := r / sz
			h := math.Sin(float64(bx)*12.9898+float64(by)*78.233) * 43758.5453
			i := int(math.Floor(jsFrac(h)*float64(len(cols)) + t*speed))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 21：波浪竖条纹（条纹本身是弯的，像垂下来的触手）── */
func kbWave(cols []color.RGB, w, amp, speed float64) KBAnim {
	ww := w
	if ww == 0 {
		ww = 2.5
	}
	am := amp
	if am == 0 {
		am = 2
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			off := am * math.Sin(float64(r)*0.9+t*speed)
			i := int(math.Floor((float64(c) + off) / ww))
			return fullExact(cols[idx(i, len(cols))])
		})
	}
}

/* ── 摆法 22：斜向分割（一条斜线把键盘切成两半）── */
//
// at 默认 4、blend 默认 2.5（JS 用 ==null 判断），这里由调用方显式传。
func kbDiagSplit(a, b color.RGB, at, blend float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			x := float64(c) - float64(r)*1.8
			k := math.Max(0, math.Min(1, (x-at+blend/2)/blend))
			return fullExact(color.Mix(a, b, k))
		})
	}
}

/* ── 摆法 23：逐列色相（16 列扫过一段色环）── */
func kbColumnHue(base color.RGB, span, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			return fullExact(hueShiftExact(base, float64(c)/15*span+t*speed*60))
		})
	}
}

/* ── 形状掩码：这些不是装饰，是各自主题的标志物 ── */

// kbMask 掩码上色：形状一个色，底一个色。
// 两色必须差 90° 以上，否则等于没画。
func kbMask(base color.RGB, mask [frame.KBRows]string, hi color.RGB) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if maskAt(mask, r, c) {
				return fullExact(hi)
			}
			return fullExact(base)
		})
	}
}

// kbMaskFlow 掩码 + 沿形状流动的电流。
func kbMaskFlow(base color.RGB, mask [frame.KBRows]string, hi, hi2 color.RGB, speed float64) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if !maskAt(mask, r, c) {
				return fullExact(base)
			}
			k := 0.5 + 0.5*math.Sin(t*speed-float64(c)*0.55-float64(r)*0.4)
			return fullExact(color.Mix(hi, hi2, k))
		})
	}
}

// kbSpiral 螺旋臂：柴犬的卷尾。
func kbSpiral(base, hi color.RGB, turns, speed float64) KBAnim {
	tn := turns
	if tn == 0 {
		tn = 3.2
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			dx := (float64(c) - 7.5) / 2.9
			dy := float64(r) - 2.5
			d := math.Hypot(dx, dy)
			a := math.Atan2(dy, dx)
			if jsFrac(a/(math.Pi*2)-d/tn+t*speed) < 0.34 {
				return fullExact(hi)
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 24：菱形阵（A.T. 立场那种六边形障壁的感觉）── */
func kbDiamond(base, hi color.RGB, size int, speed float64) KBAnim {
	sz := size
	if sz == 0 {
		sz = 4
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			fx := math.Abs(jsWrap(float64(c)+t*speed, float64(2*sz)) - float64(sz))
			fy := math.Abs(math.Mod(float64(r)*2.2, float64(2*sz)) - float64(sz)) // JS 这里只有单层 %
			if fx+fy < float64(sz)*0.95 {
				return fullExact(hi)
			}
			return fullExact(base)
		})
	}
}

/* ── 摆法 25：单列扫描（独眼来回扫）── */
func kbScanCol(base, hi color.RGB, speed, width float64) KBAnim {
	sp := speed
	if sp == 0 {
		sp = 0.5
	}
	ww := width
	if ww == 0 {
		ww = 1.6
	}
	return func(t float64) frame.LEDs {
		ph := math.Mod(t*sp, 2)
		x := ph
		if ph >= 1 {
			x = 2 - ph
		}
		x *= 15 // 来回，不是循环跳回
		return eachKey(func(r, c int) color.RGB {
			if math.Abs(float64(c)-x) < ww {
				return fullExact(hi)
			}
			return fullExact(base)
		})
	}
}

/* ════ 流光引擎 ════
   原厂灯效好看的关键不是图案，是【连续的场】：颜色随位置平滑变化、整体朝一个方向推。
   离散格子（棋盘/条纹/菱形/硬边扫描）看着是"色块在跳"，不是"光在流"。
   下面这套把颜色排成一条首尾相接的色环，用一个连续相位场在键盘上推移，
   相邻键之间用 smoothstep 过渡，所以没有任何硬边。                        */

// kbFlow 线性流光。dx/dy 定方向（1,0=横；0,1=纵；1,0.6=斜），
// wobble 给一点有机的摆动。
//
// rep 默认 0.5：三角波往返会把色带在板上走两遍，所以位置项要减半，
// 让整块键盘只铺【一条完整的色带】—— 相位梯度小了才像"流"，大了就是"跳"。
func kbFlow(cols []color.RGB, opts FlowOpts) KBAnim {
	dx, dy := 1.0, 0.35
	if opts.DX != nil {
		dx = *opts.DX
	}
	if opts.DY != nil {
		dy = *opts.DY
	}
	sp := 0.22
	if opts.Speed != nil {
		sp = *opts.Speed
	}
	rep := 0.5
	if opts.Repeat != nil {
		rep = *opts.Repeat
	}
	wob := opts.Wobble
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			ph := (float64(c)/15*dx+float64(r)/5*dy)*rep - t*sp
			if wob != 0 {
				ph += wob * math.Sin(float64(r)*1.9+t*1.25)
			}
			return bandAt(cols, ph)
		})
	}
}

// kbFlowRadial 径向流光：一圈圈从中心荡出去。
func kbFlowRadial(cols []color.RGB, opts FlowRadialOpts) KBAnim {
	sp := opts.Speed
	if sp == 0 {
		sp = 0.3
	}
	sc := opts.Scale
	if sc == 0 {
		sc = 0.75
	}
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			return bandAt(cols, math.Hypot((float64(c)-7.5)/7.5, (float64(r)-2.5)/2.5)*sc-t*sp)
		})
	}
}

// kbSweepSoft 软边扫描：一道有渐隐拖尾的光带来回扫，不是硬边的一列。
func kbSweepSoft(base, hi color.RGB, opts SweepSoftOpts) KBAnim {
	sp := opts.Speed
	if sp == 0 {
		sp = 0.45
	}
	w := opts.Width
	if w == 0 {
		w = 4.5
	}
	return func(t float64) frame.LEDs {
		ph := math.Mod(t*sp, 2)
		x := ph
		if ph >= 1 {
			x = 2 - ph
		}
		x *= 15
		return eachKey(func(r, c int) color.RGB {
			k := math.Max(0, 1-math.Abs(float64(c)-x)/w)
			return fullExact(color.Mix(base, hi, color.Smoothstep(k)))
		})
	}
}

// kbField 单色场 + 重点键。accents 按 LED 索引给重点色，nil 表示全铺底色。
func kbField(base color.RGB, accents map[int]color.RGB) KBAnim {
	return func(t float64) frame.LEDs {
		return eachKey(func(r, c int) color.RGB {
			if a, ok := accents[int(frame.KBMap[r][c])]; ok {
				return fullExact(a)
			}
			return fullExact(base)
		})
	}
}
