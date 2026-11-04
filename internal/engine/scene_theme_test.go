package engine

import (
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/theme"
)

// 图案单选接管点阵：矩阵必须等于图案本身的编码，背光仍走主题。
func TestThemeScenePatternOverridesDot(t *testing.T) {
	p, ok := theme.PatternByID("pikachu")
	if !ok {
		t.Fatal("测试假设图案 pikachu 存在")
	}
	th := theme.Default()

	sc := NewThemeScene(th, true, color.Calibration{Gain: [3]float64{1, 1, 1}, Sat: 1, EQL: 255})
	sc.Pattern = &p

	r := sc.Frame(0)
	if r.Matrix == nil {
		t.Fatal("图案模式下点阵帧不应为空")
	}
	want := theme.DotFromArt(p.Art).Encode(sc.Cal, sc.DotBright, sc.ApplyDotCalib)
	if *r.Matrix != want {
		t.Error("图案模式的点阵帧 ≠ 图案的编码结果")
	}
	if r.Keys == nil {
		t.Error("图案模式下键盘背光仍应推主题配色")
	}

	// 动态开启时背光应随时间演化（取一个非零时刻对比）
	k0 := sc.Frame(0).Keys
	k1 := sc.Frame(time.Second).Keys
	if th.Dynamic() && k0 != nil && k1 != nil && *k0 == *k1 {
		t.Error("动态主题的背光应随时间变化")
	}

	// 置 nil 恢复主题默认点阵
	sc.Pattern = nil
	r2 := sc.Frame(0)
	want2 := th.DotAt(0).Encode(sc.Cal, sc.DotBright, sc.ApplyDotCalib)
	if *r2.Matrix != want2 {
		t.Error("清除图案后点阵应恢复主题默认")
	}
}
