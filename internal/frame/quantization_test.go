package frame

import (
	"math"
	"testing"

	"github.com/wednesque/yogod/internal/color"
)

func TestAnimatedQuantizationColorAndBrightness(t *testing.T) {
	for _, base := range []color.RGBFloat{
		{77, 107, 254}, {245, 165, 120}, {255, 40, 30}, {60, 240, 120},
		{255, 210, 0}, {1, 254, 0}, {255, 255, 255},
	} {
		var prev [DotRows][DotCols]color.RGB
		for step := 0; step <= 1000; step++ {
			in := color.ScaleFloat(base, float64(step)/1000)
			var sum [3]float64
			for r := 0; r < DotRows; r++ {
				for c := 0; c < DotCols; c++ {
					out := quantizeAnimated(in, r, c)
					for ch, v := range out {
						if math.Abs(float64(v)-in[ch]) >= 1 {
							t.Fatalf("%v -> %v: (%d,%d) 通道 %d 误差达到一个档位", in, out, r, c, ch)
						}
						if v < prev[r][c][ch] {
							t.Fatalf("单调变亮时出现反向跳变：%v -> %v", prev[r][c], out)
						}
						if in[ch] == 0 && v != 0 {
							t.Fatalf("关闭的通道被点亮：%v -> %v", in, out)
						}
						sum[ch] += float64(v)
					}
					if base[0] == base[1] && base[1] == base[2] && (out[0] != out[1] || out[1] != out[2]) {
						t.Fatalf("灰色被染色：%v -> %v", in, out)
					}
					prev[r][c] = out
				}
			}
			for ch, total := range sum {
				// 同色的 36 个点平均后，应达到 1/36 档的精度。
				if math.Abs(total/36-in[ch]) > .5/36+1e-9 {
					t.Fatalf("%v: 通道 %d 平均值 %v 偏离目标", in, ch, total/36)
				}
			}
		}
	}
}

func TestAnimatedQuantizationPureChannelsAndIntegers(t *testing.T) {
	for r := 0; r < DotRows; r++ {
		for c := 0; c < DotCols; c++ {
			for ch := 0; ch < 3; ch++ {
				for _, v := range []float64{0, .1, .49, .5, .51, 1, 6.5, 127.5, 254.9, 255} {
					var in color.RGBFloat
					in[ch] = v
					if got := quantizeAnimated(in, r, c); got != color.FromFloat(in) {
						t.Fatalf("单色取整改变：%v -> %v", in, got)
					}
				}
			}
			for _, in := range []color.RGBFloat{{}, {77, 107, 254}, {255, 255, 255}} {
				if got := quantizeAnimated(in, r, c); got != color.FromFloat(in) {
					t.Fatalf("整数颜色改变：%v -> %v", in, got)
				}
			}
		}
	}
}
