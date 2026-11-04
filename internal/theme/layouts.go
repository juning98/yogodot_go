package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════════ 每个主题的键盘构图 ══════════
   两条硬规则：
   ① 摆法必须说得出寓意 —— 说不出就是瞎摆
   ② 同一主题里两个色的色相差 ≥ 90°，绝不用「同色相不同明度」区分（看不见） */

// layout 是一套摆法的两种形态：静态帧 + 动画。
type layout struct {
	kb   func() frame.LEDs
	anim KBAnim
}

// layoutTable 就是 JS 的 LAYOUTS：21 套主题各一套摆法，由 All() 覆盖到主题上。
//
// 每条的 kb 是"定格"版本、anim 是"动起来"版本，两者参数各自独立 ——
// 原版就是分别构造的，不是同一个函数换个参数。
//
// TODO(port): emoji / weather / veggie / fruit / animal 这 5 套的 JS 静态写法
// 少写了一对括号（例如 `kb:()=>kbField(MOODS[0].col,{})` 返回的是函数、不是帧），
// 原版在"静态模式"下会把一个函数当帧推给设备。这里按意图取它的定格帧，
// 即下面各自的 Static(...)。
var layoutTable = map[string]layout{
	// 寓意：闪电。黄场 + 电蓝色的闪电形，互补色最大对比（52° vs 210°）
	"pikachu": {
		kb:   Static(kbMask(color.RGB{255, 215, 0}, MBolt, color.RGB{0, 120, 255})),
		anim: kbMaskFlow(color.RGB{255, 215, 0}, MBolt, color.RGB{0, 120, 255}, color.RGB{0, 255, 255}, 6),
	},

	// 寓意：橙色章鱼浮在深蓝海水里 —— 扇形是从中心射出的触手
	"claude": {
		kb: Static(kbSector([]color.RGB{
			{255, 110, 0}, {0, 70, 220}, {255, 110, 0}, {0, 70, 220},
			{255, 110, 0}, {0, 70, 220}, {255, 110, 0}, {0, 70, 220},
		}, 0)),
		anim: kbFlowRadial([]color.RGB{{255, 110, 0}, {255, 175, 0}, {0, 70, 220}, {0, 150, 255}},
			FlowRadialOpts{Speed: 0.34, Scale: 0.8}),
	},

	// 寓意：分界线 = 已被吃掉的进度，会一路往右推
	"pacman": {
		kb:   Static(kbSplit(color.RGB{40, 0, 255}, color.RGB{255, 210, 0}, 7.5, 2.0, 0)),
		anim: kbSplit(color.RGB{40, 0, 255}, color.RGB{255, 210, 0}, -4, 2.0, 1.6),
	},

	// 寓意：脖子上那条红围巾 + 铃铛，横贯整块
	"doraemon": {
		kb: Static(kbRect(color.RGB{0, 110, 255},
			[]Rect{{C0: 0, C1: 15, R0: 3, R1: 3, Col: color.RGB{255, 0, 40}}})),
		anim: kbFlow([]color.RGB{{0, 110, 255}, {0, 180, 255}, {255, 0, 40}, {0, 110, 255}},
			FlowOpts{DX: Num(0), DY: Num(1), Speed: Num(0.18)}),
	},

	// 寓意：精灵球本体 —— 上半红、下半白、正中一颗按钮
	"pokeball": {
		kb: Static(kbRect(color.RGB{255, 20, 35}, []Rect{
			{C0: 0, C1: 15, R0: 3, R1: 5, Col: color.RGB{0, 225, 255}},
			{C0: 7, C1: 8, R0: 2, R1: 3, Col: color.RGB{255, 240, 0}},
		})),
		anim: kbFlow([]color.RGB{{255, 20, 35}, {255, 20, 35}, {255, 240, 0}, {0, 225, 255}, {0, 225, 255}},
			FlowOpts{DX: Num(0), DY: Num(1), Speed: Num(0.16)}),
	},

	// 寓意：卷起来的尾巴。橙场 + 深蓝螺旋臂
	"shiba": {
		kb:   Static(kbSpiral(color.RGB{255, 90, 0}, color.RGB{0, 90, 255}, 3.2, 0)),
		anim: kbFlow([]color.RGB{{255, 90, 0}, {255, 200, 0}, {255, 90, 0}, {0, 90, 255}}, FlowOpts{DX: Num(1), DY: Num(0.55), Speed: Num(0.2), Wobble: 0.06}),
	},

	// 寓意：两块黑眼圈。浅青场 + 两块深紫方斑，这是熊猫的全部识别特征
	"panda": {
		kb:   Static(kbMask(color.RGB{0, 235, 255}, MEyes, color.RGB{120, 0, 255})),
		anim: kbFlow([]color.RGB{{0, 235, 255}, {120, 0, 255}, {0, 235, 255}, {150, 60, 255}}, FlowOpts{DX: Num(1), DY: Num(0.8), Speed: Num(0.17)}),
	},

	// 寓意：帝企鹅的橙色胸腹 —— 深蓝身子中间一条橙带
	"penguin": {
		kb:   Static(kbBand(color.RGB{255, 140, 0}, color.RGB{0, 60, 235}, 6, 9)),
		anim: kbFlow([]color.RGB{{0, 60, 235}, {0, 150, 255}, {255, 140, 0}, {0, 60, 235}}, FlowOpts{DX: Num(1), DY: Num(0), Speed: Num(0.19)}),
	},

	// 寓意：双马尾。两侧各一条到底的葱色宽带，中间是深色的衣服
	"miku": {
		kb:   Static(kbMask(color.RGB{90, 0, 200}, MTails, color.RGB{0, 255, 215})),
		anim: kbFlow([]color.RGB{{0, 255, 215}, {0, 140, 255}, {255, 0, 150}, {0, 255, 215}}, FlowOpts{DX: Num(1), DY: Num(0.25), Speed: Num(0.24)}),
	},

	// 寓意：心电图波形
	"heartbeat": {
		kb:   Static(kbShape(color.RGB{255, 0, 90}, ECG, color.RGB{200, 255, 0}, ShapeOpts{})),
		anim: kbShape(color.RGB{255, 0, 90}, ECG, color.RGB{200, 255, 0}, ShapeOpts{Sweep: 0.55}),
	},

	// 寓意：字符从上往下掉
	"matrix": {
		kb:   Static(kbFall(color.RGB{0, 0, 0}, color.RGB{0, 255, 90}, 0, 3.6)),
		anim: kbFall(color.RGB{0, 0, 0}, color.RGB{0, 255, 90}, 1.9, 3.6),
	},

	// 寓意：黑色岩壳 + 底下透出来的发光裂缝。熄灭的底 = 最大对比
	"lava": {
		kb:   Static(kbMask(color.RGB{0, 0, 0}, MCrack, color.RGB{255, 120, 0})),
		anim: kbMaskFlow(color.RGB{0, 0, 0}, MCrack, color.RGB{255, 40, 0}, color.RGB{255, 220, 0}, 3.5),
	},

	// 寓意：极光就是一条横着铺开的色带
	"aurora": {
		kb:   Static(kbGrad(auroraStops)),
		anim: kbGradMove(auroraStops, 0.07),
	},

	// 寓意：天空的层次，从头顶的紫到地平线的金
	"sunset": {
		kb:   Static(kbRows(sunsetRows)),
		anim: kbRowsMove(sunsetRows, 0.06),
	},

	// 寓意：夜空里零星的星点
	"starry": {
		kb:   Static(kbScatter(color.RGB{25, 0, 255}, color.RGB{255, 230, 0}, 0.88, 0)),
		anim: kbScatter(color.RGB{25, 0, 255}, color.RGB{255, 230, 0}, 0.88, 1.1),
	},

	// 寓意：垂下来的触手，条纹本身是弯的。品红 vs 青，差 125°
	"jelly": {
		kb:   Static(kbWave([]color.RGB{{255, 0, 180}, {0, 220, 255}}, 2.5, 2, 0)),
		anim: kbFlowRadial([]color.RGB{{255, 0, 180}, {150, 0, 255}, {0, 220, 255}, {150, 0, 255}}, FlowRadialOpts{Speed: 0.26, Scale: 0.7}),
	},

	// 寓意：情绪淹没整张桌子 —— 不做花纹，整块就是当下那个情绪的颜色
	"emoji": {
		kb: Static(kbField(moods[0].Col, nil)),
		anim: func(t float64) frame.LEDs {
			f := t / moodT
			i := int(math.Floor(f)) % len(moods)
			k := math.Min(1, (f-math.Floor(f))/0.25)
			col := color.Mix(moods[idx(i-1, len(moods))].Col, moods[i].Col, k)
			return kbFlow([]color.RGB{col, hueShiftExact(col, 30), col, hueShiftExact(col, -30)},
				FlowOpts{DX: Num(1), DY: Num(0.3), Speed: Num(0.16)})(t)
		},
	},

	// 寓意：摆法本身跟着天气走 —— 晴=全场、雨=竖向下落、雷=整块闪、雪=稀疏飘落
	"weather": {
		kb: Static(kbField(weathers[0].Col, nil)),
		anim: func(t float64) frame.LEDs {
			i := int(math.Floor(t/3.4)) % len(weathers)
			switch i {
			case 0:
				return kbRadial(color.RGB{255, 230, 0}, color.RGB{255, 90, 0}, RadialOpts{Power: 1.1})(t)
			case 1:
				return kbFall(color.RGB{0, 0, 60}, color.RGB{0, 180, 255}, 2.2, 3.0)(t)
			case 2:
				c := color.RGB{70, 0, 200}
				if math.Mod(t, 1.4) < 0.18 {
					c = color.RGB{255, 240, 120}
				}
				return kbField(c, nil)(0)
			default:
				return kbScatter(color.RGB{0, 60, 180}, color.RGB{200, 255, 255}, 0.9, 0.7)(t)
			}
		},
	},

	// 寓意：菜地的垄。一垄菜色、一垄叶绿，隔条换
	"veggie": {
		kb: Static(kbStripes([]color.RGB{veggies[0].Col, veggies[0].Hi}, 2, 0)),
		anim: func(t float64) frame.LEDs {
			f := t / 3.2
			i := int(math.Floor(f)) % len(veggies)
			k := math.Min(1, (f-math.Floor(f))/0.3)
			p := veggies[idx(i-1, len(veggies))]
			cur := veggies[i]
			a := color.Mix(p.Col, cur.Col, k)
			b := color.Mix(p.Hi, cur.Hi, k)
			return kbFlow([]color.RGB{a, b, a, hueShiftExact(a, 25)},
				FlowOpts{DX: Num(1), DY: Num(0.3), Speed: Num(0.2)})(t)
		},
	},

	// 寓意：果子结在树上 —— 绿叶底 + 散落的果色
	"fruit": {
		kb: Static(kbScatter(color.RGB{30, 200, 40}, fruits[0].Col, 0.55, 0)),
		anim: func(t float64) frame.LEDs {
			f := t / 3.2
			i := int(math.Floor(f)) % len(fruits)
			k := math.Min(1, (f-math.Floor(f))/0.3)
			c := color.Mix(fruits[idx(i-1, len(fruits))].Col, fruits[i].Col, k)
			return kbFlow([]color.RGB{{30, 200, 40}, c, {30, 200, 40}, hueShiftExact(c, 20)},
				FlowOpts{DX: Num(1), DY: Num(0.45), Speed: Num(0.18)})(t)
		},
	},

	// 寓意：毛皮上的斑块 —— 马赛克就是不规则的花纹
	"animal": {
		kb: Static(kbMosaic([]color.RGB{animals[0].Col, hueShiftExact(animals[0].Col, 170)}, 2, 0)),
		anim: func(t float64) frame.LEDs {
			f := t / 3.2
			i := int(math.Floor(f)) % len(animals)
			k := math.Min(1, (f-math.Floor(f))/0.3)
			c := color.Mix(animals[idx(i-1, len(animals))].Col, animals[i].Col, k)
			return kbFlow([]color.RGB{c, hueShiftExact(c, 170), c, hueShiftExact(c, 90)},
				FlowOpts{DX: Num(1), DY: Num(0.6), Speed: Num(0.19), Wobble: 0.05})(t)
		},
	},
}

// auroraStops / sunsetRows 被静态与动画两个版本共用，抽出来避免抄两遍抄错。
var auroraStops = []Stop{
	{0, color.RGB{0, 255, 220}},
	{0.35, color.RGB{60, 255, 0}},
	{0.7, color.RGB{120, 0, 255}},
	{1, color.RGB{255, 0, 180}},
}

var sunsetRows = []color.RGB{
	{120, 0, 255}, {255, 0, 150}, {255, 0, 40}, {255, 90, 0}, {255, 180, 0}, {255, 240, 0},
}

// themeOwnKB 是 THEMES 里直接写死的键盘摆法 —— 这 3 套没有被 LAYOUTS 覆盖。
//
// 它们在 JS 里也是构造函数调用（不是字符画），所以 Theme.KB 留空，
// 真正的摆法在这里：kb 是静态写法，anim 是 kbAnim。
// 注意这 3 套的 kbAnim 是活的 —— 覆盖只发生在 LAYOUTS 有的那 21 套上。
var themeOwnKB = map[string]layout{
	// 寓意：A.T. 立场 —— 紫色机体上展开的黄绿色菱形障壁。
	// 紫 275° vs 黄绿 90°，差 185°。speed=0：障壁不转 × 流动的橙绿电流。
	"eva01": {
		kb: Static(kbDiamond(color.RGB{150, 40, 255}, color.RGB{60, 255, 0}, 4, 0)),
		anim: kbFlow([]color.RGB{{150, 40, 255}, {60, 255, 0}, {150, 40, 255}, {90, 0, 255}},
			FlowOpts{DX: Num(1), DY: Num(0.7), Speed: Num(0.21), Wobble: 0.05}),
	},

	// 寓意：独眼扫描 —— 橙黄机体上一道青色的目光来回扫。
	// 橙 30° vs 青 190°，差 160°。speed 传 0，但 JS 里是 `speed||0.5`，所以按 0.5 动。
	"eva00": {
		kb:   Static(kbScanCol(color.RGB{255, 130, 0}, color.RGB{0, 220, 255}, 0, 1.8)),
		anim: kbSweepSoft(color.RGB{255, 130, 0}, color.RGB{0, 220, 255}, SweepSoftOpts{Speed: 0.4, Width: 4.5}),
	},

	// 寓意：草方块的分层 —— 上面是草，下面是泥土，纵向流动。
	// 静态是 speed:0 的定格；动画随方块轮播换色，苦力怕爆炸时整块闪。
	"minecraft": {
		kb: Static(kbFlow([]color.RGB{mcblocks[0].Col, mcblocks[0].Hi, mcblocks[0].Col},
			FlowOpts{DX: Num(0), DY: Num(1), Speed: Num(0)})),
		anim: func(t float64) frame.LEDs {
			f := t / 3.4
			i := int(math.Floor(f)) % len(mcblocks)
			k := math.Min(1, (f-math.Floor(f))/0.3)
			p := mcblocks[idx(i-1, len(mcblocks))]
			cur := mcblocks[i]
			a := color.Mix(p.Col, cur.Col, k)
			b := color.Mix(p.Hi, cur.Hi, k)
			if cur.Name == "苦力怕" && math.Mod(t, 3.4) > 3.07 { // 爆炸时整块键盘跟着闪
				return kbFlow([]color.RGB{{255, 240, 180}, {255, 120, 0}, {255, 240, 180}},
					FlowOpts{DX: Num(1), DY: Num(0.4), Speed: Num(2.4)})(t)
			}
			return kbFlow([]color.RGB{a, b, a},
				FlowOpts{DX: Num(0), DY: Num(1), Speed: Num(0.17)})(t)
		},
	},
}

// LayoutFor 返回某个主题的键盘摆法。
//
// ok=false 表示这套主题没有覆盖布局，使用主题自带的字符画 ——
// 实际只有 eva01/eva00/minecraft 三套没有 LAYOUTS，它们的摆法见 themeOwnKB。
func LayoutFor(id string) (static func() frame.LEDs, anim KBAnim, ok bool) {
	l, ok := layoutTable[id]
	if !ok {
		return nil, nil, false
	}
	return l.kb, l.anim, true
}
