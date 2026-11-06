package color

import "math"

// RGBFloat 保留动画计算的小数，直到设备编码时才量化为 RGB888。
type RGBFloat [3]float64

func (c RGBFloat) Sum() float64 { return c[0] + c[1] + c[2] }

// ClampFloat 只限制范围，不提前取整。
func ClampFloat(v float64) float64 { return math.Max(0, math.Min(255, v)) }

func ScaleFloat(c RGBFloat, k float64) RGBFloat {
	return RGBFloat{ClampFloat(c[0] * k), ClampFloat(c[1] * k), ClampFloat(c[2] * k)}
}

func MixFloat(a, b RGBFloat, k float64) RGBFloat {
	k = math.Max(0, math.Min(1, k))
	return RGBFloat{
		a[0] + (b[0]-a[0])*k,
		a[1] + (b[1]-a[1])*k,
		a[2] + (b[2]-a[2])*k,
	}
}

// DotFloat 把校准和用户亮度都应用到浮点颜色，交给最终编码统一量化。
func (c Calibration) DotFloat(in RGBFloat, brightness float64, apply bool) RGBFloat {
	f := in
	if apply {
		f = c.gr(c.ledize(f))
	}
	return ScaleFloat(f, brightness/100)
}
