package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

// 这份数据由 testdata/gen_theme_fixtures.js **直接执行 yogo.html 里的原始 JS** 生成：
// 它把调色板、构图原语、24 套主题、48 张图案全部求值，
// 再把每套主题在若干时刻的渲染结果导出来。
//
// 所以下面的测试是真正的对拍 —— Go 侧把美术资产搬错一个字符、一个颜色值、
// 一个动画参数，这里就会红。不是"看起来差不多"。
type themeFixture struct {
	ThemeOrder []string              `json:"themeOrder"`
	Themes     map[string]themeEntry `json:"themes"`
	Statics    map[string][]string   `json:"statics"`
	Gallery    map[string][]string   `json:"gallery"`
}

type themeEntry struct {
	Name       string               `json:"name"`
	Light      []int                `json:"light"`
	Dot        []string             `json:"dot"`
	KB         [][]int              `json:"kb"`
	KBAt       map[string][][]int   `json:"kbAt"`
	DotAt      map[string][][][]int `json:"dotAt"`
	HasKBAnim  bool                 `json:"hasKbAnim"`
	HasDotAnim bool                 `json:"hasDotAnim"`
}

func loadThemeFixture(t *testing.T) themeFixture {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "theme_fixtures.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到对拍数据（先跑 node testdata/gen_theme_fixtures.js）：%v", err)
	}
	var f themeFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析对拍数据失败：%v", err)
	}
	return f
}

// nativeThemeIDs 是本仓库新增、不来自 yogo.html 的主题，接在 24 套移植主题之后。
//
// 对拍只覆盖从原项目逐位搬来的 24 套；新主题没有 JS 对照物，
// 必须在这里显式登记 —— 否则主题表里悄悄多出一套、没人核对，
// 这组测试会把它当成"野主题"报出来。
var nativeThemeIDs = []string{"deepseek"}

// nativeThemes 是同一个集合的查表版本，给对拍过滤用。
var nativeThemes = func() map[string]bool {
	m := make(map[string]bool, len(nativeThemeIDs))
	for _, id := range nativeThemeIDs {
		m[id] = true
	}
	return m
}()

// ported 返回对拍数据覆盖的主题，并校验表里没有未登记的原生主题。
func ported(t *testing.T, f themeFixture) []Theme {
	t.Helper()
	var out []Theme
	for _, th := range All() {
		if _, ok := f.Themes[th.ID]; ok {
			out = append(out, th)
			continue
		}
		if !nativeThemes[th.ID] {
			t.Errorf("主题 %q 不在对拍数据里，也没登记为原生主题", th.ID)
		}
	}
	if len(out) != len(f.ThemeOrder) {
		t.Fatalf("对拍主题数：Go %d，JS %d", len(out), len(f.ThemeOrder))
	}
	return out
}

func ledsOf(t *testing.T, rows [][]int) frame.LEDs {
	t.Helper()
	if len(rows) != frame.LEDCount {
		t.Fatalf("期望 84 个灯，实际 %d", len(rows))
	}
	var l frame.LEDs
	for i, c := range rows {
		if len(c) < 3 {
			t.Fatalf("第 %d 个灯只有 %d 个通道", i, len(c))
		}
		l[i] = color.RGB{uint8(c[0]), uint8(c[1]), uint8(c[2])}
	}
	return l
}

func gridOf(t *testing.T, rows [][][]int) frame.Grid {
	t.Helper()
	if len(rows) != frame.DotRows {
		t.Fatalf("期望 %d 行，实际 %d", frame.DotRows, len(rows))
	}
	var g frame.Grid
	for r, row := range rows {
		if len(row) != frame.DotCols {
			t.Fatalf("第 %d 行有 %d 列", r, len(row))
		}
		for c, px := range row {
			g[r][c] = color.RGB{uint8(px[0]), uint8(px[1]), uint8(px[2])}
		}
	}
	return g
}

func key(t float64) string { return strconv.FormatFloat(t, 'f', -1, 64) }

// TestThemeRegistryMatchesJS 验证移植主题的数量、顺序、id、名字全部一致。
//
// 原生主题（当前只有 deepseek）不在 fixture 里，由 ported() 过滤掉；
// 顺序是 UI 的展示顺序，必须是 JS 对象字面量的顺序，不能是 map 的随机序。
func TestThemeRegistryMatchesJS(t *testing.T) {
	f := loadThemeFixture(t)
	all := ported(t, f)

	if len(all) != len(f.ThemeOrder) {
		t.Fatalf("主题数量：Go %d，JS %d", len(all), len(f.ThemeOrder))
	}
	for i, want := range f.ThemeOrder {
		if all[i].ID != want {
			t.Errorf("第 %d 套主题：Go=%q，JS=%q", i, all[i].ID, want)
		}
		entry := f.Themes[want]
		if all[i].Name != entry.Name {
			t.Errorf("%s 的名字：Go=%q，JS=%q", want, all[i].Name, entry.Name)
		}
	}
}

// TestThemeLightMatchesJS 验证每套主题的主色。
func TestThemeLightMatchesJS(t *testing.T) {
	f := loadThemeFixture(t)
	for _, th := range ported(t, f) {
		entry, ok := f.Themes[th.ID]
		if !ok {
			t.Errorf("JS 里没有主题 %q", th.ID)
			continue
		}
		want := color.RGB{uint8(entry.Light[0]), uint8(entry.Light[1]), uint8(entry.Light[2])}
		if th.Light != want {
			t.Errorf("%s 的主色：Go=%v，JS=%v", th.ID, th.Light, want)
		}
	}
}

// TestThemeDotArtMatchesJS 验证 6×6 字符画逐字符相同。
func TestThemeDotArtMatchesJS(t *testing.T) {
	f := loadThemeFixture(t)
	for _, th := range ported(t, f) {
		entry := f.Themes[th.ID]
		if len(entry.Dot) != frame.DotRows {
			t.Errorf("%s 的点阵图有 %d 行", th.ID, len(entry.Dot))
			continue
		}
		for r := 0; r < frame.DotRows; r++ {
			if th.Dot[r] != entry.Dot[r] {
				t.Errorf("%s 点阵第 %d 行：Go=%q，JS=%q", th.ID, r, th.Dot[r], entry.Dot[r])
			}
		}
	}
}

// TestKeyboardFramesMatchJS 是重头戏：84 颗灯的颜色逐个对拍。
//
// 静态主题比 kb 字段，动态主题比 t=0 的那一帧。
func TestKeyboardFramesMatchJS(t *testing.T) {
	f := loadThemeFixture(t)
	for _, th := range ported(t, f) {
		entry := f.Themes[th.ID]

		var wantRows [][]int
		if entry.HasKBAnim {
			wantRows = entry.KBAt["0"]
		} else {
			wantRows = entry.KB
		}
		if wantRows == nil {
			t.Errorf("%s：JS 没有键盘帧数据", th.ID)
			continue
		}

		got := th.KBAt(0)
		want := ledsOf(t, wantRows)
		if got != want {
			diff := 0
			for i := range got {
				if got[i] != want[i] {
					if diff < 5 {
						t.Errorf("%s LED %d：Go=%v，JS=%v", th.ID, i, got[i], want[i])
					}
					diff++
				}
			}
			t.Errorf("%s 键盘帧有 %d 个灯不一致", th.ID, diff)
		}
	}
}

// TestKeyboardAnimationMatchesJS 验证动画在不同时刻也对得上。
func TestKeyboardAnimationMatchesJS(t *testing.T) {
	f := loadThemeFixture(t)
	for _, th := range ported(t, f) {
		entry := f.Themes[th.ID]
		if !entry.HasKBAnim {
			continue
		}
		for _, tt := range []float64{0.37, 1.5} {
			wantRows, ok := entry.KBAt[key(tt)]
			if !ok || wantRows == nil {
				continue
			}
			got := th.KBAt(tt)
			want := ledsOf(t, wantRows)
			if got != want {
				diff := 0
				for i := range got {
					if got[i] != want[i] {
						if diff < 3 {
							t.Errorf("%s t=%v LED %d：Go=%v，JS=%v", th.ID, tt, i, got[i], want[i])
						}
						diff++
					}
				}
				t.Errorf("%s t=%v 有 %d 个灯不一致", th.ID, tt, diff)
			}
		}
	}
}

// TestDotAnimationMatchesJS 验证点阵动画在不同时刻对得上。
//
// **例外：lava 不可对拍。** 它的 dotAnim 是 `ANIMS.fire`，而那个动画
// 每帧都在调 `Math.random()`（yogo.html:1712-1713），而且是跨帧演化的
// 细胞自动机 —— 它根本不是一个 f(t)。这类动画只能断言结构性质，
// 见 TestFireAnimationIsSane。
func TestDotAnimationMatchesJS(t *testing.T) {
	f := loadThemeFixture(t)
	for _, th := range ported(t, f) {
		entry := f.Themes[th.ID]
		if !entry.HasDotAnim {
			continue
		}
		if th.ID == "lava" {
			continue // 随机 + 有状态，见上面的说明
		}
		for _, tt := range []float64{0.37, 1.5} {
			wantRows, ok := entry.DotAt[key(tt)]
			if !ok || wantRows == nil {
				continue
			}
			got := th.DotAt(tt)
			want := gridOf(t, wantRows)
			if got != want {
				diff := 0
				for r := 0; r < frame.DotRows; r++ {
					for c := 0; c < frame.DotCols; c++ {
						if got[r][c] != want[r][c] {
							if diff < 5 {
								t.Errorf("%s t=%v (%d,%d)：Go=%v，JS=%v",
									th.ID, tt, r, c, got[r][c], want[r][c])
							}
							diff++
						}
					}
				}
				t.Errorf("%s t=%v 点阵有 %d 个格子不一致", th.ID, tt, diff)
			}
		}
	}
}

// TestPatternsMatchJS 验证 48 张图案逐字符相同。
func TestPatternsMatchJS(t *testing.T) {
	f := loadThemeFixture(t)
	pats := Patterns()

	if len(pats) != len(f.Statics) {
		t.Fatalf("图案数量：Go %d，JS %d", len(pats), len(f.Statics))
	}
	for _, p := range pats {
		want, ok := f.Statics[p.ID]
		if !ok {
			t.Errorf("JS 里没有图案 %q", p.ID)
			continue
		}
		for r := 0; r < frame.DotRows; r++ {
			if r >= len(want) {
				break
			}
			if p.Art[r] != want[r] {
				t.Errorf("%s 第 %d 行：Go=%q，JS=%q", p.ID, r, p.Art[r], want[r])
			}
		}
	}
}

// TestGroupsMatchJS 验证图案分组的名字与成员顺序。
func TestGroupsMatchJS(t *testing.T) {
	f := loadThemeFixture(t)
	groups := Groups()

	if len(groups) != len(f.Gallery) {
		t.Fatalf("分组数量：Go %d，JS %d", len(groups), len(f.Gallery))
	}
	for _, g := range groups {
		want, ok := f.Gallery[g.Name]
		if !ok {
			t.Errorf("JS 里没有分组 %q（Go 的分组名可能改了）", g.Name)
			continue
		}
		if len(g.IDs) != len(want) {
			t.Errorf("分组 %q 成员数：Go %d，JS %d", g.Name, len(g.IDs), len(want))
			continue
		}
		for i := range want {
			if g.IDs[i] != want[i] {
				t.Errorf("分组 %q 第 %d 个：Go=%q，JS=%q", g.Name, i, g.IDs[i], want[i])
			}
		}
	}
}
