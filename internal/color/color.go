// Package color 实现这台键盘灯珠的调色管线。
//
// 这是整个项目里**唯一无法从协议反推**的业务逻辑，必须原样移植：
//
//	ledize()  保持明度、把饱和度推上去（低饱和色会被灯珠压成白）
//	gr()      通道增益 + 校正强度 + 感知光量水位
//	亮度      软件缩放（推流走 RAM，改亮度立刻生效，不写 flash）
//	RGB565    逐键推流的编码
//
// 硬件事实：这批灯珠的发光效率约为 红:绿:蓝 = 1:4:14。
// 等值混合时红被压过去 —— 黄看着像绿、青看着像蓝。所以必须校正。
package color

import "math"

// RGB 是一个 0..255 的颜色。
type RGB [3]uint8

// Clamp 把浮点通道夹到 0..255 并四舍五入。
func Clamp(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

// Float 转成浮点三元组，供内部计算用。
func (c RGB) Float() [3]float64 { return [3]float64{float64(c[0]), float64(c[1]), float64(c[2])} }

// FromFloat 把浮点通道夹到位。
func FromFloat(f [3]float64) RGB {
	return RGB{Clamp(f[0]), Clamp(f[1]), Clamp(f[2])}
}

// Sum 返回三通道之和，用于"这颗灯亮不亮"的粗判。
func (c RGB) Sum() int { return int(c[0]) + int(c[1]) + int(c[2]) }

// Calibration 是用户可调的校准参数。默认值来自原项目的实测标定。
type Calibration struct {
	// Gain 是通道增益（白平衡）。绿、蓝的发光效率远高于红，所以压它们。
	Gain [3]float64
	// Corr 是校正强度 0..1：暖色亮度 ↔ 色相准确度，二选一。
	Corr float64
	// Sat 是饱和度倍数。低饱和色在灯珠上会发白。
	Sat float64
	// EQL 是感知光量水位。低于水位的不动，高于水位的等比压下来。
	EQL float64
}

// DefaultCalibration 是实测的甜点值：亮 1.5 倍且黄还够黄。
func DefaultCalibration() Calibration {
	return Calibration{
		Gain: [3]float64{1.0, 0.50, 0.42},
		Corr: 0.70,
		Sat:  1.80,
		EQL:  255,
	}
}

// ledize 保持明度、只把饱和度往上推。纯灰/纯白不动（主题要的就是它）。
func (c Calibration) ledize(in [3]float64) [3]float64 {
	mx := math.Max(in[0], math.Max(in[1], in[2]))
	mn := math.Min(in[0], math.Min(in[1], in[2]))
	if mx <= 0 {
		return [3]float64{}
	}
	s := (mx - mn) / mx
	if s <= 0.001 {
		return in // 纯灰/纯白：不碰
	}
	ratio := math.Min(1, s*c.Sat) / s
	return [3]float64{
		mx - (mx-in[0])*ratio,
		mx - (mx-in[1])*ratio,
		mx - (mx-in[2])*ratio,
	}
}

// gr 应用通道增益、按最大通道归一、再按感知光量水位压制。
func (c Calibration) gr(in [3]float64) [3]float64 {
	g := [3]float64{
		math.Pow(c.Gain[0], c.Corr),
		math.Pow(c.Gain[1], c.Corr),
		math.Pow(c.Gain[2], c.Corr),
	}
	out := [3]float64{in[0] * g[0], in[1] * g[1], in[2] * g[2]}

	mx := math.Max(out[0], math.Max(out[1], out[2]))
	if mx <= 0 {
		return out
	}
	// 归一到最大通道：保住色相比例，也不丢亮度
	k := 255 / mx
	out[0] *= k
	out[1] *= k
	out[2] *= k

	// 感知光量权重由增益反推：红=1、绿≈4、蓝≈14
	var w [3]float64
	w[0] = 1
	if g[1] != 0 {
		w[1] = 1 / g[1]
	}
	if g[2] != 0 {
		w[2] = 1 / g[2]
	}
	l0 := out[0]*w[0] + out[1]*w[1] + out[2]*w[2]
	if l0 <= 0 {
		return out
	}
	if l0 > c.EQL {
		t := c.EQL / l0
		out[0] *= t
		out[1] *= t
		out[2] *= t
	}
	// 低于水位的一律不动 —— 混白会让红发粉、橙发白
	return out
}

// Keys 返回一颗键灯实际要写进设备的颜色。
// brightness 是 0..100 的软件亮度（旧版 UI 上的滑杆）。
func (c Calibration) Keys(in RGB, brightness float64) RGB {
	f := c.ledize(in.Float())
	f = c.gr(f)
	k := brightness / 100
	return FromFloat([3]float64{f[0] * k, f[1] * k, f[2] * k})
}

// KeysRaw 跳过校正，直接把原色乘亮度（校准界面"铺纯色"时用）。
func KeysRaw(in RGB, brightness float64) RGB {
	k := brightness / 100
	return FromFloat([3]float64{float64(in[0]) * k, float64(in[1]) * k, float64(in[2]) * k})
}

// Dot 返回点阵屏一格实际要写进设备的颜色。
//
// 注意：点阵屏实测混色本来就准，默认**不做**校正 —— 套键盘那套反而搞坏。
// 只有 apply 为 true（旧版那个永远存不住的"点阵也套用"勾选）才走同一套管线。
func (c Calibration) Dot(in RGB, brightness float64, apply bool) RGB {
	var f [3]float64
	if apply {
		f = c.gr(c.ledize(in.Float()))
	} else {
		f = in.Float()
	}
	k := brightness / 100
	return FromFloat([3]float64{f[0] * k, f[1] * k, f[2] * k})
}

// RGB565 把颜色编码成逐键推流用的 16 位小端值。
//
//	R 取高 5 位、G 取高 6 位、B 取高 5 位。
func RGB565(c RGB) uint16 {
	r := uint16(c[0]>>3) & 0x1F
	g := uint16(c[1]>>2) & 0x3F
	b := uint16(c[2]>>3) & 0x1F
	return r<<11 | g<<5 | b
}

// HSV 生成一个颜色。h 单位为度，s/v 为 0..1。
func HSV(h, s, v float64) RGB {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	cc := v * s
	x := cc * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - cc
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = cc, x, 0
	case h < 120:
		r, g, b = x, cc, 0
	case h < 180:
		r, g, b = 0, cc, x
	case h < 240:
		r, g, b = 0, x, cc
	case h < 300:
		r, g, b = x, 0, cc
	default:
		r, g, b = cc, 0, x
	}
	return FromFloat([3]float64{(r + m) * 255, (g + m) * 255, (b + m) * 255})
}

// Mix 在两色之间线性插值，k=0 返回 a、k=1 返回 b。
func Mix(a, b RGB, k float64) RGB {
	if k <= 0 {
		return a
	}
	if k >= 1 {
		return b
	}
	return FromFloat([3]float64{
		float64(a[0]) + (float64(b[0])-float64(a[0]))*k,
		float64(a[1]) + (float64(b[1])-float64(a[1]))*k,
		float64(a[2]) + (float64(b[2])-float64(a[2]))*k,
	})
}

// Scale 按比例缩放颜色。
func Scale(c RGB, k float64) RGB {
	return FromFloat([3]float64{float64(c[0]) * k, float64(c[1]) * k, float64(c[2]) * k})
}

// Smoothstep 是 0..1 之间的平滑过渡。
func Smoothstep(k float64) float64 {
	if k <= 0 {
		return 0
	}
	if k >= 1 {
		return 1
	}
	return k * k * (3 - 2*k)
}
