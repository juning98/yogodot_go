package theme

import (
	"testing"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

// themeIDs 是 JS 里 THEMES 对象字面量的键顺序，也是 UI 里的展示顺序。
// 顺序错了 UI 的排列就变了，所以当数据锁住。
var themeIDs = []string{
	"pikachu", "claude", "pacman", "doraemon", "pokeball", "lava",
	"aurora", "sunset", "matrix", "emoji", "weather", "shiba",
	"panda", "penguin", "jelly", "starry", "miku", "veggie",
	"fruit", "animal", "eva01", "eva00", "minecraft", "heartbeat",
}

func blankKBRows() [frame.KBRows]string {
	var a [frame.KBRows]string
	for i := range a {
		a[i] = "................"
	}
	return a
}

func setKBChar(a *[frame.KBRows]string, r, c int, ch byte) {
	b := []byte(a[r])
	b[c] = ch
	a[r] = string(b)
}

func countLit(l frame.LEDs) int {
	n := 0
	for i := range l {
		if l[i].Sum() > 0 {
			n++
		}
	}
	return n
}

func TestThemeCount(t *testing.T) {
	if got := len(All()); got != 24 {
		t.Fatalf("主题数 = %d，要 24", got)
	}
	seen := make(map[string]bool)
	for _, th := range All() {
		if th.ID == "" {
			t.Error("有空 id 的主题")
		}
		if seen[th.ID] {
			t.Errorf("重复主题 id: %q", th.ID)
		}
		seen[th.ID] = true
	}
}

func TestThemeOrder(t *testing.T) {
	all := All()
	if len(all) != len(themeIDs) {
		t.Fatalf("主题数 = %d，期望 %d", len(all), len(themeIDs))
	}
	for i, id := range themeIDs {
		if all[i].ID != id {
			t.Errorf("第 %d 套 = %q，期望 %q（顺序必须与 JS 对象字面量一致）", i, all[i].ID, id)
		}
	}
}

func TestDefaultTheme(t *testing.T) {
	def := Default()
	if def.ID != "pikachu" {
		t.Fatalf("Default().ID = %q，要 pikachu", def.ID)
	}
	if All()[0].ID != "pikachu" {
		t.Fatalf("All()[0].ID = %q，要 pikachu", All()[0].ID)
	}
	if def.Name != "皮卡丘" {
		t.Errorf("Default().Name = %q，要 皮卡丘", def.Name)
	}
	if def.Light != (color.RGB{255, 200, 0}) {
		t.Errorf("Default().Light = %v，要 [255 200 0]", def.Light)
	}
}

func TestByID(t *testing.T) {
	th, ok := ByID("heartbeat")
	if !ok {
		t.Fatal("ByID(heartbeat) 没找到")
	}
	if th.Name != "心跳" {
		t.Errorf("heartbeat 的名字 = %q，要 心跳", th.Name)
	}
	if _, ok := ByID("no-such-theme"); ok {
		t.Error("ByID 对不存在的 id 返回了 ok=true")
	}
}

// 每套主题的点阵字符画必须是 6 行 × 6 列。
func TestDotArtShape(t *testing.T) {
	for _, th := range All() {
		if len(th.Dot) != frame.DotRows {
			t.Errorf("%s: Dot 有 %d 行，要 %d", th.ID, len(th.Dot), frame.DotRows)
			continue
		}
		for r, row := range th.Dot {
			if len(row) != frame.DotCols {
				t.Errorf("%s: Dot 第 %d 行有 %d 个字符，要 %d", th.ID, r, len(row), frame.DotCols)
			}
		}
	}
}

// 每套主题的键盘字符画必须是 6 行 × 16 列。
//
// 注意：JS 的 THEMES 里 24 套的 kb 全是构造函数调用，一套字面量字符画都没有，
// 所以 eva01 / eva00 / minecraft 也拿不到"内联字符画"——
// 这里锁的是"留空的 6×16"，真正的摆法由 KBAnim 提供（见 TestLayoutsCoverKeyboard）。
func TestKBArtShape(t *testing.T) {
	for _, id := range []string{"eva01", "eva00", "minecraft"} {
		th, ok := ByID(id)
		if !ok {
			t.Fatalf("找不到主题 %q", id)
		}
		for r, row := range th.KB {
			if len(row) != frame.KBCols {
				t.Errorf("%s: KB 第 %d 行有 %d 个字符，要 %d", id, r, len(row), frame.KBCols)
			}
		}
		if th.KB != blankKB {
			t.Errorf("%s: KB 应该留空（字符画不存在），实际是 %v", id, th.KB)
		}
	}
	// 所有主题的形状都得对 —— 数据写错一个点都不该过。
	for _, th := range All() {
		for r, row := range th.KB {
			if len(row) != frame.KBCols {
				t.Errorf("%s: KB 第 %d 行有 %d 个字符，要 %d", th.ID, r, len(row), frame.KBCols)
			}
		}
	}
}

// 逐格核对颜色：期望值是人对着字符画 + DotPalette 手算出来的。
func TestDotFromArtColours(t *testing.T) {
	pika := DotFromArt(Default().Dot)
	cases := []struct {
		r, c int
		want color.RGB
		why  string
	}{
		{0, 1, color.RGB{255, 210, 0}, ".y..y. 里的 y"},
		{0, 0, color.RGB{0, 0, 0}, ".y..y. 里的 ."},
		{2, 1, color.RGB{0, 0, 0}, "y.yy.y 里挖掉的眼珠"},
		{3, 0, color.RGB{255, 40, 30}, "ryyyyr 里的 r（腮红）"},
		{3, 5, color.RGB{255, 40, 30}, "ryyyyr 右边的 r"},
		{5, 0, color.RGB{0, 0, 0}, ".yyyy. 左边的 ."},
		{5, 1, color.RGB{255, 210, 0}, ".yyyy. 里的 y"},
	}
	for _, tc := range cases {
		if got := pika[tc.r][tc.c]; got != tc.want {
			t.Errorf("pikachu(%d,%d) = %v，要 %v（%s）", tc.r, tc.c, got, tc.want, tc.why)
		}
	}

	// 换两套验证新增调色板字符：'L' = [60,255,0]，'g' = [40,220,60]，'m' = [255,120,0]。
	eva, _ := ByID("eva01")
	evaG := DotFromArt(eva.Dot)
	if got, want := evaG[3][1], (color.RGB{60, 255, 0}); got != want {
		t.Errorf("eva01(3,1) = %v，要 %v（字符 L）", got, want)
	}
	mc, _ := ByID("minecraft")
	mcG := DotFromArt(mc.Dot)
	if got, want := mcG[1][1], (color.RGB{60, 255, 0}); got != want {
		t.Errorf("minecraft(1,1) = %v，要 %v（字符 L）", got, want)
	}
	if got, want := mcG[1][0], (color.RGB{40, 220, 60}); got != want {
		t.Errorf("minecraft(1,0) = %v，要 %v（字符 g）", got, want)
	}
	lava, _ := ByID("lava")
	lavaG := DotFromArt(lava.Dot)
	if got, want := lavaG[0][3], (color.RGB{255, 120, 0}); got != want {
		t.Errorf("lava(0,3) = %v，要 %v（字符 m）", got, want)
	}
}

// KBFromArt 写的是 LED 索引，不是行列坐标。
func TestKBFromArtUsesLEDIndex(t *testing.T) {
	art := blankKBRows()
	setKBChar(&art, 0, 0, 'y') // KBMap[0][0] = 0
	l := KBFromArt(art)
	if got, want := l[0], (color.RGB{255, 195, 0}); got != want {
		t.Errorf("LED[0] = %v，要 %v（字符 y）", got, want)
	}
	if n := countLit(l); n != 1 {
		t.Errorf("点亮了 %d 颗灯，要 1 颗", n)
	}

	// 第 5 行第 0 列在 KBMap 里是 LED 73，不是 5*16+0=80。
	art = blankKBRows()
	setKBChar(&art, 5, 0, 'y')
	l = KBFromArt(art)
	if l[73].Sum() == 0 {
		t.Error("LED[73] 没亮：KBMap[5][0] = 73")
	}
	if l[80].Sum() != 0 {
		t.Error("LED[80] 亮了：按行列坐标写就会错到这里")
	}

	// 最后一颗：第 5 行第 15 列 = LED 83。
	art = blankKBRows()
	setKBChar(&art, 5, 15, 'y')
	l = KBFromArt(art)
	if l[83].Sum() == 0 {
		t.Error("LED[83] 没亮：KBMap[5][15] = 83")
	}
}

// 空格位（KBMap 里 255）就算画了颜色也不该点亮任何灯。
func TestKBFromArtSkipsGaps(t *testing.T) {
	// KBMap 里 12 个没有灯的位置。
	for _, pos := range [][2]int{
		{1, 14}, {1, 15}, {2, 15}, {3, 14}, {3, 15}, {4, 14}, {4, 15},
		{5, 3}, {5, 5}, {5, 6}, {5, 7}, {5, 8},
	} {
		art := blankKBRows()
		setKBChar(&art, pos[0], pos[1], 'y')
		l := KBFromArt(art)
		if l.Any() {
			t.Errorf("KBMap[%d][%d] 是空位（255），却点亮了灯", pos[0], pos[1])
		}
		if frame.HasLED(pos[0], pos[1]) {
			t.Errorf("HasLED(%d,%d) = true，要 false", pos[0], pos[1])
		}
	}
}

// 动画必须是纯函数：同样的 t 给同样的帧。
func TestAnimationDeterminism(t *testing.T) {
	a := Plasma(0)
	b := Plasma(0)
	if a != b {
		t.Error("plasma(0) 两次结果不同：动画里混进了状态")
	}
	if a == Plasma(1) {
		t.Error("plasma(0) 与 plasma(1) 相同：时间参数没起作用")
	}

	// 主题的点阵动画同样要纯（fire 是唯一例外，单独测）。
	pika, _ := ByID("pikachu")
	if pika.DotAnim == nil {
		t.Fatal("pikachu 没有 DotAnim")
	}
	if pika.DotAnim(0.5) != pika.DotAnim(0.5) {
		t.Error("pikachu.DotAnim(0.5) 两次结果不同")
	}
	// t=2.4 落在眨眼的窗口（2.30..2.54）里，眼睛会被填上颜色。
	if pika.DotAnim(0) == pika.DotAnim(2.4) {
		t.Error("pikachu.DotAnim 对时间不敏感")
	}
}

// fire 是唯一有状态的动画，但两个实例各烧各的，不共享缓冲区。
//
// 判据：缓冲区冷的时候第一帧必然是纯黑（底部热源要等 Step 末尾才填），
// 所以「新实例的第一帧是全黑」就证明它没吃到别人的历史。
func TestFireIsolation(t *testing.T) {
	a := &Fire{}
	if a.Step().Any() {
		t.Error("冷缓冲的第一帧应该全黑")
	}
	if !a.Step().Any() {
		t.Error("第二帧应该已经有火了")
	}

	b := &Fire{}
	if b.Step().Any() {
		t.Error("新实例的第一帧被 A 的历史污染了：两个 Fire 共享了缓冲")
	}

	// NewFire 返回的闭包也必须各自独立。
	x := NewFire()
	y := NewFire()
	x(0)
	x(0)
	x(0)
	if y(0).Any() {
		t.Error("两个 NewFire 实例共享了缓冲")
	}
}

// lava 主题用 fire 动画，但不该和 ANIMS["fire"] 那份共享实例。
func TestLavaDotAnimWorks(t *testing.T) {
	lava, ok := ByID("lava")
	if !ok {
		t.Fatal("找不到 lava")
	}
	if lava.DotAnim == nil {
		t.Fatal("lava 没有 DotAnim")
	}
	// 火无论从哪一帧进来都会继续烧：连推两帧之后不可能还是全黑。
	lava.DotAnim(0)
	lava.DotAnim(1)
	if !lava.DotAnim(2).Any() {
		t.Error("lava 的点阵动画没烧起来")
	}
}

// 21 套由 LAYOUTS 覆盖，3 套自带摆法；24 套都必须能点亮键盘。
func TestLayoutsCoverKeyboard(t *testing.T) {
	if got := len(layoutTable); got != 21 {
		t.Errorf("LAYOUTS 有 %d 套，要 21 套", got)
	}
	for id := range layoutTable {
		if _, ok := ByID(id); !ok {
			t.Errorf("LAYOUTS 里的 %q 在主题表里不存在", id)
		}
	}

	for _, id := range []string{"eva01", "eva00", "minecraft"} {
		if _, _, ok := LayoutFor(id); ok {
			t.Errorf("%s 不该出现在 LAYOUTS 里", id)
		}
		if _, ok := themeOwnKB[id]; !ok {
			t.Errorf("%s 缺少 THEMES 自带的摆法", id)
		}
	}
	if _, _, ok := LayoutFor("no-such-theme"); ok {
		t.Error("LayoutFor 对不存在的 id 返回了 ok=true")
	}

	for _, th := range All() {
		if th.KBAnim == nil {
			t.Errorf("%s 没有 KBAnim", th.ID)
			continue
		}
		if !th.KBAnim(0).Any() {
			t.Errorf("%s 在 t=0 时整块键盘全黑", th.ID)
		}
		if th.DotAnim == nil {
			t.Errorf("%s 没有 DotAnim", th.ID)
		}
	}
}

// LAYOUTS 覆盖必须真的生效：主题拿到的是 LAYOUTS 的那份，不是 THEMES 里的旧值。
func TestLayoutOverrideApplied(t *testing.T) {
	want := layoutTable["pikachu"]
	got, _ := ByID("pikachu")
	if got.KBAnim == nil {
		t.Fatal("pikachu 没有 KBAnim")
	}
	if got.KBAnim(0) != want.anim(0) {
		t.Error("pikachu 的 KBAnim 不是 LAYOUTS 版本")
	}
	// 静态版本是 kbMask：只有闪电掩码上的格子亮，而且是电蓝色。
	static := want.kb()
	frame.EachCell(func(r, c, led int) {
		got := static[led]
		if maskAt(MBolt, r, c) {
			if got != (color.RGB{0, 120, 255}) {
				t.Errorf("pikachu 静态帧 (%d,%d) = %v，掩码内要电蓝 [0 120 255]", r, c, got)
			}
		} else if got != (color.RGB{255, 215, 0}) {
			t.Errorf("pikachu 静态帧 (%d,%d) = %v，掩码外要底色 [255 215 0]", r, c, got)
		}
	})
	// 掩码落在空位上的格子（比如第 5 行第 3 列）依然不点亮任何灯。
	if static.Get(5, 3).Sum() != 0 {
		t.Errorf("pikachu 静态帧在空位 (5,3) 点了灯：%v", static.Get(5, 3))
	}

	// eva01 走的是 THEMES 自带的那份（静态 + 动画都在 THEMES 里，没有 LAYOUTS 覆盖）。
	eva, _ := ByID("eva01")
	if eva.KBAnim(0) != themeOwnKB["eva01"].anim(0) {
		t.Error("eva01 的 KBAnim 不是 THEMES 自带版本")
	}
	// 这 3 套的 kbAnim 是活的：同一秒数同一帧、不同秒数不同帧。
	for _, id := range []string{"eva01", "eva00", "minecraft"} {
		th, _ := ByID(id)
		if th.KBAnim(0.25) != th.KBAnim(0.25) {
			t.Errorf("%s 的 KBAnim 不是纯函数", id)
		}
		if th.KBAnim(0.25) == th.KBAnim(0.9) {
			t.Errorf("%s 的 KBAnim 对时间不敏感", id)
		}
	}
}

// 掩码必须是 6×16，只含 'X' 和占位字符。
func TestMaskShape(t *testing.T) {
	for name, mask := range map[string][frame.KBRows]string{
		"MBolt": MBolt, "MCrack": MCrack, "MEyes": MEyes, "MTails": MTails, "ECG": ECG,
	} {
		if len(mask) != frame.KBRows {
			t.Errorf("%s: %d 行，要 %d", name, len(mask), frame.KBRows)
		}
		hasX := false
		for r, row := range mask {
			if len(row) != frame.KBCols {
				t.Errorf("%s: 第 %d 行有 %d 列，要 %d", name, r, len(row), frame.KBCols)
			}
			for c := 0; c < len(row); c++ {
				if row[c] == 'X' {
					hasX = true
				}
			}
		}
		if !hasX {
			t.Errorf("%s 一个 X 都没有", name)
		}
	}
}

func TestPatternCount(t *testing.T) {
	ps := Patterns()
	if len(ps) != 48 {
		t.Fatalf("图案数 = %d，要 48", len(ps))
	}
	seen := make(map[string]bool)
	for _, p := range ps {
		if seen[p.ID] {
			t.Errorf("重复图案 id: %q", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" {
			t.Errorf("%s 没有名字", p.ID)
		}
		if len(p.Art) != frame.DotRows {
			t.Errorf("%s: Art 有 %d 行，要 %d", p.ID, len(p.Art), frame.DotRows)
		}
		for r, row := range p.Art {
			if len(row) != frame.DotCols {
				t.Errorf("%s: Art 第 %d 行有 %d 个字符，要 %d", p.ID, r, len(row), frame.DotCols)
			}
		}
	}
	if len(statics) != 48 {
		t.Errorf("STATICS 有 %d 张，要 48", len(statics))
	}
}

func TestGroups(t *testing.T) {
	gs := Groups()
	if len(gs) != 6 {
		t.Fatalf("分组数 = %d，要 6", len(gs))
	}
	if gs[0].Name != "角色 / 符号" {
		t.Errorf("第一组名 = %q，要 角色 / 符号", gs[0].Name)
	}
	total := 0
	flat := make([]string, 0, 48)
	for _, g := range gs {
		if len(g.IDs) == 0 {
			t.Errorf("分组 %q 是空的", g.Name)
		}
		for _, id := range g.IDs {
			if _, ok := statics[id]; !ok {
				t.Errorf("分组 %q 引用了不存在的图案 %q", g.Name, id)
			}
			flat = append(flat, id)
		}
		total += len(g.IDs)
	}
	if total != 48 {
		t.Errorf("分组里共 %d 张，要 48", total)
	}
	// Patterns() 的顺序必须就是 GALLERY 摊平的顺序。
	ps := Patterns()
	for i, id := range flat {
		if ps[i].ID != id {
			t.Errorf("Patterns()[%d] = %q，GALLERY 顺序里是 %q", i, ps[i].ID, id)
		}
	}
}

func TestGroupsAreCopies(t *testing.T) {
	gs := Groups()
	gs[0].Name = "被改了"
	if Groups()[0].Name == "被改了" {
		t.Error("Groups() 返回了内部切片，外部改动会污染数据")
	}
	ps := Patterns()
	ps[0].Art[0] = "XXXXXX"
	if Patterns()[0].Art[0] == "XXXXXX" {
		t.Error("Patterns() 返回了内部数据")
	}
}

// ANIMS 的键顺序即 UI 里的动画列表顺序。
func TestAnimRegistry(t *testing.T) {
	if len(AnimNames) != 7 {
		t.Fatalf("动画名有 %d 个，要 7 个", len(AnimNames))
	}
	for _, n := range AnimNames {
		fn, ok := ANIMS[n]
		if !ok {
			t.Errorf("ANIMS 里没有 %q", n)
			continue
		}
		_ = fn(0.7) // 每个都能渲染一帧
	}
	if _, ok := ANIMS["no-such-anim"]; ok {
		t.Error("ANIMS 里出现了不存在的名字")
	}
}

// kbConst / kbArt 的桥：字符画直接固化成帧。
func TestKBConst(t *testing.T) {
	art := blankKBRows()
	setKBChar(&art, 0, 1, 'g')
	f := kbConst(art)
	l := f()
	if got, want := l[1], (color.RGB{25, 190, 45}); got != want {
		t.Errorf("kbConst 的 LED[1] = %v，要 %v", got, want)
	}
	// 静态帧不该随"时间"变 —— 它根本没有时间参数，重复调用结果一致。
	if f() != f() {
		t.Error("kbConst 每次返回不同结果")
	}
}

// 分区表必须覆盖全部 84 颗灯，且互不重叠（kbCaps 的兜底逻辑依赖这一点）。
func TestZonePartition(t *testing.T) {
	var seen [frame.LEDCount]int
	for _, i := range zoneAlphas {
		seen[i]++
	}
	for _, i := range zoneAccent {
		seen[i]++
	}
	for _, i := range zoneMods {
		seen[i]++
	}
	if len(zoneMods) != 43 {
		t.Errorf("修饰键 %d 个，要 43 个", len(zoneMods))
	}
	for i, n := range seen {
		if n != 1 {
			t.Errorf("LED %d 被分区覆盖 %d 次，要恰好 1 次", i, n)
		}
	}
}

// TestNoThemeRendersBlank 是防回归的护栏。
//
// 这里踩过一次：原版 24 套主题的键盘配色**全部**来自构图函数
// （`kbDiamond(...)` / `kbFlow(...)` 之类），没有一套是字符画，
// 所以 `Theme.KB` 字段对它们是空的。谁按"字符画"去读，
// 推出来的就是一块全黑的键盘 —— 而且不报错、不 panic，只是屏幕不亮。
func TestNoThemeRendersBlank(t *testing.T) {
	for _, th := range All() {
		kb := th.KBAt(0)
		if !kb.Any() {
			t.Errorf("%s 的键盘帧全黑 —— 大概率是读错了字段（KB 字符画是空的）", th.ID)
		}
		// 点阵查静态图：lava 的 DotAt(0) 是火焰动画的第一帧，按设计就是全黑（缓冲区初值零）
		dot := th.DotFrame()
		if !dot.Any() {
			t.Errorf("%s 的点阵静态图全黑", th.ID)
		}
	}
}
