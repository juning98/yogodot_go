package theme

import (
	"math"
	"math/rand"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

// ring 是点阵屏外圈的 20 个格子，顺时针：上边 → 右边 → 下边 → 左边。
// 彗星动画沿着它跑。
var ring = func() [][2]int {
	var p [][2]int
	for c := 0; c < frame.DotCols; c++ {
		p = append(p, [2]int{0, c})
	}
	for r := 1; r < frame.DotRows; r++ {
		p = append(p, [2]int{r, frame.DotCols - 1})
	}
	for c := frame.DotCols - 2; c >= 0; c-- {
		p = append(p, [2]int{frame.DotRows - 1, c})
	}
	for r := frame.DotRows - 2; r >= 1; r-- {
		p = append(p, [2]int{r, 0})
	}
	return p
}()

// Plasma 是流动的等离子：三条不同速度的正弦叠加成相位，色相跟着相位转。
func Plasma(t float64) frame.Grid {
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			v := math.Sin(float64(c)*0.9+t*2) +
				math.Sin(float64(r)*0.9-t*1.5) +
				math.Sin(float64(c+r)*0.6+t*2.5)
			g[r][c] = color.HSVFloat(v*55+t*45, 1, 0.45+0.55*math.Abs(math.Sin(v*1.2)))
		}
	}
	return g
}

// Comet 是一颗拖着 9 格尾巴的彗星绕外圈跑，中心 2×2 同时呼吸。
func Comet(t float64) frame.Grid {
	g := frame.BlankGrid()
	n := len(ring)
	for k := 0; k < 9; k++ {
		p := ring[idx(int(math.Floor(t*15))-k, n)]
		g[p[0]][p[1]] = color.HSVFloat(190+float64(k)*14, 1, math.Pow(1-float64(k)/9, 2.2))
	}
	b := 0.25 + 0.75*math.Abs(math.Sin(t*3.5))
	for _, p := range [][2]int{{2, 2}, {2, 3}, {3, 2}, {3, 3}} {
		g[p[0]][p[1]] = color.RGBFloat{
			color.ClampFloat(90 * b),
			color.ClampFloat(200 * b),
			color.ClampFloat(255 * b),
		}
	}
	return g
}

// Heartbeat 是一颗按心电图节奏收缩的心（和 heartbeat 主题里的点阵动画同源，
// 但这里用的是 STATICS.heart 的图，节奏也更快）。
func Heartbeat(t float64) frame.Grid {
	ph := math.Mod(t*2, 1)
	var b float64
	switch {
	case ph < 0.14:
		b = 0.35 + 0.65*(ph/0.14)
	case ph < 0.3:
		b = 1 - 0.45*((ph-0.14)/0.16)
	case ph < 0.42:
		b = 0.55 + 0.45*((ph-0.3)/0.12)
	default:
		b = 0.35 + 0.65*math.Max(0, 1-(ph-0.42)/0.35)
	}
	g := frame.BlankGrid()
	art := statics["heart"]
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols && c < len(art[r]); c++ {
			if art[r][c] == 'r' {
				g[r][c] = color.RGBFloat{
					color.ClampFloat(255 * b),
					color.ClampFloat(35 * b),
					color.ClampFloat(45 * b),
				}
			}
		}
	}
	return g
}

// Fire 是唯一有状态的动画：火苗每一帧都从上一帧的缓冲区推出来。
//
// 缓冲多留一行（行 6）当热源，热量往上走，每帧叠一点随机扰动再衰减。
// 无锁 —— 引擎在一帧里只会从同一个 goroutine 调它。
// 需要两处各自动，就各自 NewFire()，绝不能共用同一个实例。
type Fire struct {
	buf [frame.DotRows + 1][frame.DotCols]float64
}

// Step 推进一帧火并返回这一帧。时间参数对火没有意义 —— 火自己演化。
func (f *Fire) Step() frame.Grid {
	for r := 0; r < frame.DotRows; r++ {
		bl := &f.buf[r+1]
		for c := 0; c < frame.DotCols; c++ {
			v := (bl[(c+5)%6]+bl[c]*2+bl[(c+1)%6])/4 - 0.055 - rand.Float64()*0.045
			if v < 0 {
				v = 0
			}
			f.buf[r][c] = v
		}
	}
	for c := 0; c < frame.DotCols; c++ {
		f.buf[frame.DotRows][c] = 0.75 + rand.Float64()*0.25
	}
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			v := math.Min(1, f.buf[r][c])
			g[r][c] = color.RGBFloat{
				color.ClampFloat(v * 3 * 255),
				color.ClampFloat(math.Max(0, v-0.34) * 3.4 * 255),
				color.ClampFloat(math.Max(0, v-0.78) * 4.5 * 255),
			}
		}
	}
	return g
}

// NewFire 开一团新的火。
//
// 这是唯一需要这样处理的动画：闭包里带着自己的缓冲区，
// 所以两个调用方各烧各的，不会互相踩。
func NewFire() DotAnim {
	f := &Fire{}
	return func(t float64) frame.Grid { return f.Step() }
}

// Rain 是数字雨：每列一根三段拖尾的雨滴，列间错开相位。
func Rain(t float64) frame.Grid {
	g := frame.BlankGrid()
	for c := 0; c < frame.DotCols; c++ {
		off := math.Mod(float64(c)*2.7, 6)
		y := math.Mod(t*4+off, 8)
		for k := 0; k < 3; k++ {
			r := int(math.Floor(y)) - k
			if r >= 0 && r < frame.DotRows {
				g[r][c] = color.RGBFloat{
					0,
					color.ClampFloat(120 * (1 - float64(k)/3)),
					color.ClampFloat(255 * (1 - float64(k)/3)),
				}
			}
		}
	}
	return g
}

// Breathe 是整屏同色呼吸，色相随时间慢慢转。
func Breathe(t float64) frame.Grid {
	b := 0.15 + 0.85*(0.5+0.5*math.Sin(t*1.6))
	col := color.HSVFloat(t*24, 0.85, b)
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			g[r][c] = col
		}
	}
	return g
}

// Spiral 是绕中心的螺旋，角度与半径一起决定相位，所以整屏都在转。
func Spiral(t float64) frame.Grid {
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			dx := float64(c) - 2.5
			dy := float64(r) - 2.5
			a := math.Atan2(dy, dx)
			d := math.Hypot(dx, dy)
			phase := math.Abs(math.Mod(d*1.6-t*2.4, 2.2) - 1.1)
			g[r][c] = color.HSVFloat(a*57+d*70-t*160, 1, math.Max(0, 1-phase))
		}
	}
	return g
}

// AnimNames 是 ANIMS 的键顺序，与 JS 对象字面量一致。
var AnimNames = []string{"plasma", "comet", "heartbeat", "fire", "rain", "breathe", "spiral"}

// ANIMS 按名字查动画。
//
// 注意 "fire"：它是唯一有内部状态的动画，这里的条目是一份共享实例
// （对应 JS 里全局只有一个 fireBuf），单帧预览够用；
// 两处要各自动起来时必须各自 NewFire()，否则会互相踩缓冲。
var ANIMS = map[string]DotAnim{
	"plasma":    Plasma,
	"comet":     Comet,
	"heartbeat": Heartbeat,
	"fire":      NewFire(),
	"rain":      Rain,
	"breathe":   Breathe,
	"spiral":    Spiral,
}
