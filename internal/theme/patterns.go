package theme

import "github.com/wednesque/yogod/internal/frame"

// statics 是 48 张单独的点阵图案。键是 id，值是 6×6 字符画。
//
// 字符含义见 DotPalette；'.' 表示熄灭。
// 这里的顺序和 JS 源码里的 STATICS 一致（图库展示顺序另见 gallery）。
var statics = map[string][frame.DotRows]string{
	"tomato":  {"..gg..", ".rrrr.", "rrrrrr", "rrrrrr", ".rrrr.", "..rr.."},
	"heart":   {"rr..rr", "rrrrrr", "rrrrrr", ".rrrr.", "..rr..", "......"},
	"pikachu": {".y..y.", "yyyyyy", "y.yy.y", "ryyyyr", "yy..yy", ".yyyy."},
	"cat":     {"o....o", "oooooo", "o.oo.o", "ooppoo", "oooooo", ".oooo."},
	"skull":   {".llll.", "llllll", "l.ll.l", "ll..ll", ".llll.", ".l.l.l"},
	"ghost":   {".cccc.", "cccccc", "c.cc.c", "cccccc", "cccccc", "c.c.c."},
	"invader": {"g.g..g", ".gggg.", "gg..gg", "gggggg", ".g..g.", "g.gg.g"},
	"moon":    {"..yyy.", ".yy...", "yy....", "yy....", ".yy...", "..yyy."},
	"coffee":  {".s.s..", "..s.s.", "lllll.", "llllll", "lllll.", ".lll.."},
	"bolt":    {"...yy.", "..yy..", ".yyyy.", "..yy..", ".yy...", ".y...."},
	"wave":    {"......", ".c..c.", "c.cc.c", "......", ".b..b.", "b.bb.b"},
	"prompt":  {"gg....", "..gg..", "....gg", "..gg..", "gg....", ".gggg."},
	/* ── 蔬菜 ── */
	"carrot":   {".g.g..", "..g.g.", ".oooo.", ".oooo.", "..oo..", "..o..."},
	"broccoli": {".gggg.", "gggggg", "gggggg", "..GG..", "..GG..", "..GG.."},
	"eggplant": {"..g...", ".gvg..", ".vvvv.", "vvvvvv", "vvvvvv", ".vvvv."},
	"corn":     {"..yy..", ".yyyy.", "gyyyyg", "gyyyyg", ".yyyy.", "..yy.."},
	"chili":    {"..gg..", "..rr..", "..rr..", ".rr...", ".rr...", ".r...."},
	"pumpkin":  {"..gg..", ".mmmm.", "mmmmmm", "mmmmmm", "mmmmmm", ".mmmm."},
	"pea":      {"....g.", "...gg.", ".gggg.", "gGGGGg", ".gggg.", "......"},
	/* ── 水果 ── */
	"apple":   {"..g...", ".rrrr.", "rrrrrr", "rrrrrr", "rrrrrr", ".rrrr."},
	"banana":  {"....s.", "...yy.", "..yy..", ".yy...", ".yy...", "..yy.."},
	"berry":   {".g.g..", ".gggg.", "rrrrrr", "rrrrrr", ".rrrr.", "..rr.."},
	"grape":   {"...g..", ".v.v.v", "vvvvvv", ".vvvv.", "..vv..", "......"},
	"orange":  {"...gg.", ".mmmm.", "mmmmmm", "mmmmmm", "mmmmmm", ".mmmm."},
	"melon":   {".rrrr.", "rrrrrr", "rr.rrr", "rrr.rr", "GGGGGG", "gggggg"},
	"pineapp": {".g.g..", "..gg..", ".yyyy.", "yy..yy", ".yyyy.", "yy..yy"},
	"cherry":  {"..gg..", ".g..g.", ".g..g.", "rr..rr", "rr..rr", "......"},
	"lemon":   {"......", "..yy..", ".yyyy.", "yyyyyy", ".yyyy.", "..yy.."},
	/* ── 动物 ── */
	"rabbit": {".p..p.", ".p..p.", ".pppp.", "p.pp.p", "pppppp", ".p..p."},
	"frog":   {".g..g.", "gGggGg", "gggggg", "gggggg", "g.gg.g", ".gggg."},
	"chick":  {"..yy..", ".yyyy.", "y.yy.y", "yymmyy", "yyyyyy", ".m..m."},
	"pig":    {"p....p", "pppppp", "p.pp.p", "pprrpp", "pprrpp", ".pppp."},
	"fox":    {"o....o", ".oooo.", "o.oo.o", "oooooo", ".OrrO.", "..OO.."},
	"whale":  {".a....", "bbbb.b", "bbbbbb", "bbbbbb", ".bbbb.", "......"},
	"bee":    {".s..s.", ".yyyy.", "y....y", ".yyyy.", "s.ss.s", ".yyyy."},
	/* ── EVA ── */
	"eva01":   {"..LL..", ".vvvv.", "vvvvvv", "vLvvLv", "vvvvvv", ".vvvv."},
	"eva00":   {"..oo..", ".oooo.", "oooooo", "o.aa.o", "oooooo", ".oooo."},
	"atfield": {"..LL..", ".L..L.", "L....L", "L....L", ".L..L.", "..LL.."},
	/* ── 我的世界 ── */
	"grass":    {"gggggg", "gLggLg", "BBBBBB", "BnBBnB", "BBBnBB", "BBBBBB"},
	"creeper":  {"gggggg", "kkggkk", "kkggkk", "ggkkgg", "gkkkkg", "gkggkg"},
	"diamond":  {"..aa..", ".aaaa.", "aaaaaa", "aaaaaa", ".aaaa.", "..aa.."},
	"tnt":      {"nnnnnn", "rrrrrr", "YYYYYY", "rrrrrr", "rrrrrr", "nnnnnn"},
	"enderman": {".ssss.", "ssssss", "vssssv", "ssssss", ".ssss.", ".s..s."},
	"torch":    {"..yy..", ".yYYy.", "..yy..", "..BB..", "..BB..", "..BB.."},
	"redstone": {"..r...", "..r...", "rrrrrr", "..r...", "..r...", "..r..."},
	"sword":    {"....aa", "...aa.", "..aa..", ".aa...", "BB....", "B....."},
	"jackol":   {"..gg..", "mmmmmm", "m.mm.m", "mmmmmm", "m.mm.m", ".mmmm."},
	"emerald":  {"..gg..", ".gLLg.", "gLLLLg", "gLLLLg", ".gLLg.", "..gg.."},
}

// gallery 是图库分组 —— 平铺一大片找不到东西，所以按题材分开。
//
// 顺序即 UI 里的展示顺序。
var gallery = []Group{
	{Name: "角色 / 符号", IDs: []string{"pikachu", "cat", "ghost", "invader", "skull",
		"moon", "bolt", "heart", "prompt", "coffee", "wave", "tomato"}},
	{Name: "蔬菜", IDs: []string{"carrot", "broccoli", "eggplant", "corn", "chili", "pumpkin", "pea"}},
	{Name: "水果", IDs: []string{"apple", "banana", "berry", "grape", "orange", "melon",
		"pineapp", "cherry", "lemon"}},
	{Name: "动物", IDs: []string{"rabbit", "frog", "chick", "pig", "fox", "whale", "bee"}},
	{Name: "EVA", IDs: []string{"eva01", "eva00", "atfield"}},
	{Name: "我的世界", IDs: []string{"grass", "creeper", "diamond", "tnt", "enderman",
		"torch", "redstone", "sword", "jackol", "emerald"}},
}

// Pattern 是一张 6×6 的点阵图案。
type Pattern struct {
	ID   string
	Name string // JS 里 STATICS 没有名字字段，一律用 id
	Art  [frame.DotRows]string
}

// Group 是图库分组：组名 + 组里的图案 id，顺序与 JS 的 GALLERY 一致。
type Group struct {
	Name string
	IDs  []string
}

// patterns 按图库顺序摊平后的 48 张图案，包初始化时算一次。
var patterns = buildPatterns()

func buildPatterns() []Pattern {
	out := make([]Pattern, 0, len(statics))
	for _, g := range gallery {
		for _, id := range g.IDs {
			art, ok := statics[id]
			if !ok {
				panic("theme: GALLERY 引用了不存在的图案 id: " + id)
			}
			out = append(out, Pattern{ID: id, Name: id, Art: art})
		}
	}
	return out
}

// Patterns 按 GALLERY 的分组顺序返回全部 48 张图案。
//
// 注意不是 STATICS 的字面量顺序：图库要按题材排，
// 一个 map 的遍历顺序在 Go 里是随机的，所以顺序必须由 gallery 决定。
func Patterns() []Pattern {
	out := make([]Pattern, len(patterns))
	copy(out, patterns)
	return out
}

// Groups 返回图案的分组名与其中的 id 列表，顺序与 GALLERY 一致。
func Groups() []Group {
	out := make([]Group, len(gallery))
	copy(out, gallery)
	return out
}
