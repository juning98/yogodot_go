package engine

import (
	"math"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/theme"
)

// 图案单选接管点阵：矩阵必须等于图案本身的编码，背光仍走主题。
func TestThemeScenePatternOverridesDot(t *testing.T) {
	p, ok := theme.PatternByID("pikachu")
	if !ok {
		t.Fatal("测试假设图案 pikachu 存在")
	}
	th := theme.Default()

	sc := NewThemeScene(th, true)
	sc.Pattern = &p

	r := sc.Frame(0, Channels{true, true})
	if r.Matrix == nil {
		t.Fatal("图案模式下点阵帧不应为空")
	}
	want := theme.DotFromArt(p.Art)
	if *r.Matrix != want {
		t.Error("图案模式的点阵帧 ≠ 图案的编码结果")
	}
	if r.Keys == nil {
		t.Error("图案模式下键盘背光仍应推主题配色")
	}

	// 动态开启时背光应随时间演化（取一个非零时刻对比）
	k0 := sc.Frame(0, Channels{true, true}).Keys
	k1 := sc.Frame(time.Second, Channels{true, true}).Keys
	if th.Dynamic() && k0 != nil && k1 != nil && *k0 == *k1 {
		t.Error("动态主题的背光应随时间变化")
	}

	// 置 nil 恢复主题默认点阵
	sc.Pattern = nil
	r2 := sc.Frame(0, Channels{true, true})
	want2 := th.DotAt(0)
	if *r2.Matrix != want2 {
		t.Error("清除图案后点阵应恢复主题默认")
	}

	// 关闭动态后继续使用普通编码，不引入空间量化。
	sc.Dynamic = false
	wantStatic := th.DotFrame()
	if got := sc.Frame(time.Second, Channels{true, true}); *got.Matrix != wantStatic {
		t.Error("静态主题的点阵编码被动画量化改变")
	}
}

// 低亮度下鲸身不能整片同步跳红，同时保留原色比和 22% 呼吸幅度。
func TestDeepSeekLowBrightnessBreathing(t *testing.T) {
	th, _ := theme.ByID("deepseek")
	sc := NewThemeScene(th, true)
	const dotBright = 10
	var prev frame.Matrix
	var prevGrid frame.Grid
	minB, maxB := 255.0, 0.0
	for at := time.Duration(0); at < 8*time.Second; at += 67 * time.Millisecond {
		m, g := sc.Frame(at, Channels{Matrix: true}).Matrix.EncodeAnimated(color.DefaultCalibration(), dotBright, false), th.DotAt(at.Seconds())
		var actual, ideal [3]float64
		body, redChanges := 0, 0
		for r := range g {
			for c, px := range g[r] {
				if px[0] == 0 || (r == 3 && c == 1) {
					continue
				}
				body++
				off := (r*frame.DotCols + c) * 3
				for ch := range actual {
					actual[ch] += float64(m[off+ch])
					ideal[ch] += px[ch] * dotBright / 100
				}
				if at > 0 && prevGrid[r][c][0] > 0 && m[off] != prev[off] {
					redChanges++
				}
			}
		}
		for ch := range actual {
			if err := math.Abs(actual[ch]-ideal[ch]) / float64(body); err > .16 {
				t.Fatalf("%s 通道 %d 鲸身平均误差=%g", at, ch, err)
			}
		}
		if redChanges > 4 {
			t.Fatalf("%s 同步跳红像素过多：%d", at, redChanges)
		}
		meanB := actual[2] / float64(body)
		minB, maxB = math.Min(minB, meanB), math.Max(maxB, meanB)
		prev, prevGrid = m, g
	}
	if amplitude := 1 - minB/maxB; math.Abs(amplitude-.22) > .02 {
		t.Fatalf("22%% 的呼吸幅度改变：%g", amplitude)
	}
}
