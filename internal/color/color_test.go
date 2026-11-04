package color

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// fixture 是 testdata/color_fixtures.json 的结构。
//
// 这份数据由 testdata/gen_color_fixtures.js **直接执行 yogo.html 里的原始 JS**
// 生成，不是手算的。所以这个测试是真正的对拍：
// Go 侧算错一位，这里就会红。
type fixture struct {
	Params struct {
		Gain [3]float64 `json:"GAIN"`
		Sat  float64    `json:"SAT"`
		EQL  float64    `json:"EQL"`
		Corr float64    `json:"CORR"`
	} `json:"params"`
	Rows []struct {
		In     [3]int `json:"in"`
		Ledize [3]int `json:"ledize"`
		Gr     [3]int `json:"gr"`
	} `json:"rows"`
	Brightness []struct {
		K    int   `json:"k"`
		Dot  []int `json:"dot"`
		Keys []int `json:"keys"`
	} `json:"brightness"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "color_fixtures.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到对拍数据（先跑 node testdata/gen_color_fixtures.js）：%v", err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析对拍数据失败：%v", err)
	}
	return f
}

func rgbOf(v [3]int) RGB {
	return RGB{uint8(v[0]), uint8(v[1]), uint8(v[2])}
}

// TestDefaultMatchesJS 确认 Go 的默认校准参数与原始 JS 完全一致。
func TestDefaultMatchesJS(t *testing.T) {
	f := loadFixture(t)
	c := DefaultCalibration()

	if c.Gain != f.Params.Gain {
		t.Errorf("Gain = %v，JS 是 %v", c.Gain, f.Params.Gain)
	}
	if c.Sat != f.Params.Sat {
		t.Errorf("Sat = %v，JS 是 %v", c.Sat, f.Params.Sat)
	}
	if c.EQL != f.Params.EQL {
		t.Errorf("EQL = %v，JS 是 %v", c.EQL, f.Params.EQL)
	}
	if math.Abs(c.Corr-f.Params.Corr) > 1e-9 {
		t.Errorf("Corr = %v，JS 是 %v", c.Corr, f.Params.Corr)
	}
}

// TestKeysPipelineMatchesJS 是核心对拍：ledize + gr 的完整链路。
//
// brightness 传 100，所以等于只走 ledize+gr，与 fixture 的 gr 列同口径。
func TestKeysPipelineMatchesJS(t *testing.T) {
	f := loadFixture(t)
	c := DefaultCalibration()

	for _, row := range f.Rows {
		in := rgbOf(row.In)
		got := c.Keys(in, 100)
		want := rgbOf(row.Gr)
		if got != want {
			t.Errorf("Keys(%v, 100) = %v，JS 得到 %v", row.In, got, row.Gr)
		}
	}
}

// TestLedizeMatchesJS 直接验证饱和度校正那一步。
//
// 用 Keys(...,100) 反推不方便，这里直接调内部函数 —— 同一个包，可以。
func TestLedizeMatchesJS(t *testing.T) {
	f := loadFixture(t)
	c := DefaultCalibration()

	for _, row := range f.Rows {
		var in [3]float64
		for i := range in {
			in[i] = float64(row.In[i])
		}
		got := c.ledize(in)
		want := row.Ledize
		for i := 0; i < 3; i++ {
			if int(math.Round(got[i])) != want[i] {
				t.Errorf("ledize(%v)[%d] = %.1f，JS 得到 %d（rounded）",
					row.In, i, got[i], want[i])
			}
		}
	}
}

// TestBrightnessScaling 验证亮度是纯粹的线性缩放，点阵默认不校正。
func TestBrightnessScaling(t *testing.T) {
	f := loadFixture(t)
	c := DefaultCalibration()

	for _, b := range f.Brightness {
		got := c.Dot(RGB{255, 195, 0}, float64(b.K), false)
		want := rgbOf([3]int{b.Dot[0], b.Dot[1], b.Dot[2]})
		if got != want {
			t.Errorf("Dot(255,195,0 @%d%%) = %v，JS 得到 %v", b.K, got, b.Dot)
		}
	}
}

// TestRGB565 验证逐键推流的编码。位布局写错会整块键盘错色。
func TestRGB565(t *testing.T) {
	cases := []struct {
		in   RGB
		want uint16
	}{
		{RGB{0, 0, 0}, 0x0000},
		{RGB{255, 255, 255}, 0xFFFF},
		{RGB{255, 0, 0}, 0xF800},
		{RGB{0, 255, 0}, 0x07E0},
		{RGB{0, 0, 255}, 0x001F},
		// 0x68,0x6C,0x74 → R=13 G=27 B=14
		{RGB{0x68, 0x6C, 0x74}, uint16(13)<<11 | uint16(27)<<5 | uint16(14)},
	}
	for _, c := range cases {
		if got := RGB565(c.in); got != c.want {
			t.Errorf("RGB565(%v) = 0x%04X，期望 0x%04X", c.in, got, c.want)
		}
	}
}

// TestGrayAndBlackUnchanged 验证边界：纯黑归零，纯灰不被饱和度校正动。
func TestGrayAndBlackUnchanged(t *testing.T) {
	c := DefaultCalibration()

	if got := c.Dot(RGB{0, 0, 0}, 100, true); got != (RGB{}) {
		t.Errorf("纯黑应保持全零，实际 %v", got)
	}
	// 灰度 s=0，ledize 不该改动它
	led := c.ledize([3]float64{128, 128, 128})
	if led != [3]float64{128, 128, 128} {
		t.Errorf("纯灰不该被 ledize 改动，实际 %v", led)
	}
}

// TestCalibrationIsPure 确认同一输入永远同一输出（引擎会并发调用）。
func TestCalibrationIsPure(t *testing.T) {
	c := DefaultCalibration()
	in := RGB{217, 119, 87}
	first := c.Keys(in, 70)
	for i := 0; i < 100; i++ {
		if got := c.Keys(in, 70); got != first {
			t.Fatalf("第 %d 次调用结果不同：%v vs %v", i, got, first)
		}
	}
}
