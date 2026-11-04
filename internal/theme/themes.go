package theme

import (
	"math"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

/* ══════ 主题库 ══════
   每个主题 = 点阵静态图 + 点阵动画 + 键盘逐键静态 + 键盘逐键动画
   静态模式：两边都定格；动态模式：两边一起动。                       */

// eachDot 造一帧点阵：对 36 个格子逐个问颜色。
func eachDot(fn func(r, c int) color.RGB) frame.Grid {
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			g[r][c] = fn(r, c)
		}
	}
	return g
}

// scaled 把整帧按同一个系数缩一遍 —— 轮播素材的呼吸用。
func scaled(g frame.Grid, k float64) frame.Grid {
	for r := 0; r < frame.DotRows; r++ {
		for c := 0; c < frame.DotCols; c++ {
			g[r][c] = color.RGB{
				color.Clamp(float64(g[r][c][0]) * k),
				color.Clamp(float64(g[r][c][1]) * k),
				color.Clamp(float64(g[r][c][2]) * k),
			}
		}
	}
	return g
}

// moodT 是一个表情停留的秒数。
const moodT = 2.6

// Mood 是"表情包"轮播的一帧。
// Face 里 X = 主色，o = 半亮，其余熄灭。
type Mood struct {
	Name string
	Col  color.RGB
	Face [frame.DotRows]string
}

var moods = []Mood{
	{"笑", color.RGB{255, 205, 20}, [frame.DotRows]string{"......", ".X..X.", "......", "X....X", ".XXXX.", "......"}},
	{"眨眼", color.RGB{255, 155, 30}, [frame.DotRows]string{"......", ".X....", "....XX", "X....X", ".XXXX.", "......"}},
	{"惊讶", color.RGB{0, 190, 230}, [frame.DotRows]string{"......", ".X..X.", "......", "..XX..", "..XX..", "......"}},
	{"心动", color.RGB{255, 85, 155}, [frame.DotRows]string{"......", "XX..XX", ".X..X.", "......", ".XXXX.", "......"}},
	{"生气", color.RGB{255, 55, 40}, [frame.DotRows]string{"XX..XX", ".X..X.", "......", ".XXXX.", "X....X", "......"}},
	{"睡觉", color.RGB{110, 90, 235}, [frame.DotRows]string{"......", "XX..XX", "......", "....XX", "...XX.", "..XXX."}},
	{"哭", color.RGB{60, 125, 240}, [frame.DotRows]string{"......", ".X..X.", ".o..o.", "......", ".XXXX.", "X....X"}},
	{"无语", color.RGB{125, 145, 125}, [frame.DotRows]string{"......", ".X..X.", "......", "......", ".XXXX.", "......"}},
}

// rotator 是轮播素材：名字 + 字符画 + 键盘主色 / 点缀色。
// mod 区自动取主色的 22% 明度，不用一格一格配。
type rotator struct {
	Name string
	Art  [frame.DotRows]string
	Col  color.RGB
	Hi   color.RGB
}

var veggies = []rotator{
	{"胡萝卜", statics["carrot"], color.RGB{255, 110, 0}, color.RGB{40, 220, 60}},
	{"西兰花", statics["broccoli"], color.RGB{35, 200, 55}, color.RGB{130, 245, 140}},
	{"茄子", statics["eggplant"], color.RGB{150, 55, 240}, color.RGB{40, 220, 60}},
	{"玉米", statics["corn"], color.RGB{255, 215, 0}, color.RGB{40, 220, 60}},
	{"辣椒", statics["chili"], color.RGB{255, 35, 25}, color.RGB{40, 220, 60}},
	{"南瓜", statics["pumpkin"], color.RGB{255, 125, 0}, color.RGB{40, 200, 60}},
	{"豌豆", statics["pea"], color.RGB{60, 215, 70}, color.RGB{160, 255, 150}},
}

var fruits = []rotator{
	{"苹果", statics["apple"], color.RGB{255, 40, 35}, color.RGB{40, 220, 60}},
	{"香蕉", statics["banana"], color.RGB{255, 220, 0}, color.RGB{120, 90, 20}},
	{"草莓", statics["berry"], color.RGB{255, 45, 70}, color.RGB{40, 220, 60}},
	{"葡萄", statics["grape"], color.RGB{150, 60, 245}, color.RGB{40, 220, 60}},
	{"橙子", statics["orange"], color.RGB{255, 125, 0}, color.RGB{40, 220, 60}},
	{"西瓜", statics["melon"], color.RGB{255, 50, 60}, color.RGB{40, 215, 60}},
	{"菠萝", statics["pineapp"], color.RGB{255, 205, 0}, color.RGB{40, 215, 60}},
	{"樱桃", statics["cherry"], color.RGB{235, 25, 55}, color.RGB{40, 220, 60}},
	{"柠檬", statics["lemon"], color.RGB{255, 235, 0}, color.RGB{190, 255, 90}},
}

var animals = []rotator{
	{"狐狸", statics["fox"], color.RGB{255, 120, 10}, color.RGB{255, 190, 60}},
	{"兔子", statics["rabbit"], color.RGB{255, 95, 150}, color.RGB{255, 180, 205}},
	{"青蛙", statics["frog"], color.RGB{45, 215, 60}, color.RGB{150, 255, 150}},
	{"小鸡", statics["chick"], color.RGB{255, 220, 0}, color.RGB{255, 130, 0}},
	{"猪", statics["pig"], color.RGB{255, 110, 160}, color.RGB{255, 50, 60}},
	{"鲸鱼", statics["whale"], color.RGB{30, 110, 255}, color.RGB{0, 215, 255}},
	{"蜜蜂", statics["bee"], color.RGB{255, 200, 0}, color.RGB{70, 70, 80}},
}

// mcblocks 是"我的世界"方块轮播：col = 键盘主色，hi = 第二色
// （草/泥、矿石/岩石这种搭配）。
var mcblocks = []rotator{
	{"草方块", statics["grass"], color.RGB{60, 220, 40}, color.RGB{165, 72, 0}},
	{"苦力怕", statics["creeper"], color.RGB{60, 230, 50}, color.RGB{20, 60, 15}},
	{"钻石", statics["diamond"], color.RGB{0, 225, 255}, color.RGB{0, 110, 255}},
	{"TNT", statics["tnt"], color.RGB{255, 35, 25}, color.RGB{255, 225, 60}},
	{"末影人", statics["enderman"], color.RGB{70, 70, 90}, color.RGB{170, 0, 255}},
	{"火把", statics["torch"], color.RGB{255, 200, 0}, color.RGB{165, 72, 0}},
	{"红石", statics["redstone"], color.RGB{255, 20, 20}, color.RGB{110, 0, 0}},
	{"南瓜灯", statics["jackol"], color.RGB{255, 120, 0}, color.RGB{60, 220, 40}},
	{"绿宝石", statics["emerald"], color.RGB{60, 255, 60}, color.RGB{0, 130, 40}},
}

// weatherFrame 是天气轮播的一帧：名字、键盘色温、点阵画法。
type weatherFrame struct {
	Name string
	Col  color.RGB
	Draw DotAnim
}

// weathers 是晴 / 雨 / 雷 / 雪 循环，键盘取当下天气的色温。
var weathers = []weatherFrame{
	{"晴", color.RGB{255, 190, 40}, func(t float64) frame.Grid {
		g := frame.BlankGrid()
		R := 1.6 + 0.25*math.Sin(t*3)
		for r := 0; r < frame.DotRows; r++ {
			for c := 0; c < frame.DotCols; c++ {
				d := math.Hypot(float64(c)-2.5, float64(r)-2.5)
				if d < R {
					g[r][c] = color.RGB{255, 205, 40}
				} else if d < R+1.15 && (math.Abs(float64(c)-2.5) < 0.6 || math.Abs(float64(r)-2.5) < 0.6) {
					g[r][c] = color.RGB{255, 160, 20}
				}
			}
		}
		return g
	}},
	{"雨", color.RGB{70, 120, 190}, func(t float64) frame.Grid {
		g := frame.BlankGrid()
		for c := 0; c < frame.DotCols; c++ {
			y := math.Mod(t*5+float64(c)*1.9, 7)
			for k := 0; k < 2; k++ {
				r := int(math.Floor(y)) - k
				if r >= 0 && r < frame.DotRows {
					g[r][c] = color.RGB{
						40,
						color.Clamp(150 * (1 - float64(k)/2)),
						color.Clamp(240 * (1 - float64(k)/2)),
					}
				}
			}
		}
		return g
	}},
	{"雷", color.RGB{150, 120, 235}, func(t float64) frame.Grid {
		g := frame.BlankGrid()
		for c := 0; c < frame.DotCols; c++ {
			for r := 0; r < 2; r++ {
				g[r][c] = color.RGB{70, 70, 110}
			}
		}
		if math.Mod(t, 1.4) < 0.18 {
			bolts := [5]string{"...XX.", "..XX..", ".XXXX.", "..XX..", ".XX..."}
			for r := 0; r < len(bolts); r++ {
				for c := 0; c < frame.DotCols && c < len(bolts[r]); c++ {
					if bolts[r][c] == 'X' && r+1 < frame.DotRows {
						g[r+1][c] = color.RGB{255, 245, 180}
					}
				}
			}
		}
		return g
	}},
	{"雪", color.RGB{130, 180, 225}, func(t float64) frame.Grid {
		g := frame.BlankGrid()
		for c := 0; c < frame.DotCols; c++ {
			r := int(math.Floor(math.Mod(t*1.8+float64(c)*2.3, 8)))
			if r >= 0 && r < frame.DotRows {
				g[r][c] = color.RGB{170, 210, 245}
			}
		}
		return g
	}},
}

// blankKB 是键盘字符画的"空"：6 行、每行 16 个 '.'。
//
// JS 的 THEMES 里 kb 全是构造函数调用，没有一套是字面量字符串数组，
// 所以 Theme.KB 一律留空 —— 真正的摆法在 KBAnim 里（见 LayoutFor）。
var blankKB = [frame.KBRows]string{
	"................",
	"................",
	"................",
	"................",
	"................",
	"................",
}

// registry 是套用 LAYOUTS 覆盖之后的最终主题表，包初始化时算一次。
var registry = buildAll()

func buildAll() []Theme {
	themes := []Theme{
		{
			ID: "pikachu", Name: "皮卡丘", Light: color.RGB{255, 200, 0},
			Dot: [frame.DotRows]string{".y..y.", "yyyyyy", "y.yy.y", "ryyyyr", "yy..yy", ".yyyy."},
			// 皮卡丘自己动：耳朵抖 -> 眨眼 -> 腮红放电。一个 4.2 秒的循环，
			// 键盘不再画闪电（压在脸上很丑），只做同步的静电微闪。
			DotAnim: func(t float64) frame.Grid {
				const T = 4.2
				ph := math.Mod(t, T)
				twitch := ph > 1.05 && ph < 1.38
				blink := ph > 2.30 && ph < 2.54
				spark := ph > 3.15 && ph < 3.78
				Y := color.RGB{255, 205, 0}
				R := color.RGB{255, 58, 58}
				W := color.RGB{255, 246, 186}
				g := frame.BlankGrid()
				eL, eR := 1, 4
				if twitch { // 耳朵抖一下
					eL, eR = 0, 5
				}
				g[0][eL] = Y
				g[0][eR] = Y
				for c := 0; c < 6; c++ {
					g[1][c] = Y
					g[2][c] = Y
					g[3][c] = Y
				}
				if !blink { // 睁眼时挖出眼珠
					g[2][1] = color.RGB{}
					g[2][4] = color.RGB{}
				}
				g[3][0] = R // 腮红
				g[3][5] = R
				g[4][0] = Y
				g[4][1] = Y
				g[4][4] = Y
				g[4][5] = Y
				for c := 1; c < 5; c++ {
					g[5][c] = Y
				}
				if spark { // 腮红噼啪放电
					k := 0.35 + 0.65*math.Abs(math.Sin(t*38))
					g[3][0] = color.Mix(R, W, k)
					g[3][5] = color.Mix(R, W, k)
					g[2][0] = color.Mix(Y, W, k*0.8)
					g[2][5] = color.Mix(Y, W, k*0.8)
				}
				return g
			},
		},
		{
			ID: "claude", Name: "Claude 章鱼", Light: color.RGB{255, 100, 0},
			Dot: [frame.DotRows]string{"..oo..", ".oooo.", "o.oo.o", "oooooo", ".o.o.o", "o.o.o."},
			// 触手摆动 + 头部呼吸。
			//
			// 头 / 身 / 触手用同一橙色的三个明度，不掺白 ——
			// 之前头部两行用了奶白 [242,228,210]，在灯上就是一顶白帽子。
			DotAnim: func(t float64) frame.Grid {
				b := 0.75 + 0.25*math.Sin(t*1.8)
				HEAD := color.RGB{255, 158, 45}
				BODY := color.RGB{255, 100, 0}
				TENT := color.RGB{196, 58, 0}
				g := frame.BlankGrid()
				rows := [4]string{"..oo..", ".oooo.", "o.oo.o", "oooooo"}
				for r := 0; r < 4; r++ {
					for c := 0; c < 6; c++ {
						if rows[r][c] != 'o' {
							continue
						}
						col := BODY
						if r < 2 {
							col = HEAD
						}
						g[r][c] = color.Mix(color.RGB{}, col, b)
					}
				}
				for c := 0; c < 6; c++ { // 两行触手错相摆动
					s := math.Sin(t*3.4 + float64(c)*1.1)
					if s > 0 {
						g[4][c] = color.Mix(color.RGB{}, TENT, 0.5+0.5*s)
					} else {
						g[5][c] = color.Mix(color.RGB{}, TENT, 0.5-0.5*s)
					}
				}
				return g
			},
		},
		{
			ID: "pacman", Name: "吃豆人", Light: color.RGB{255, 210, 0},
			Dot: [frame.DotRows]string{".yyyy.", "yyyyyy", "yyy...", "yyy...", "yyyyyy", ".yyyy."},
			// 张嘴闭嘴
			DotAnim: func(t float64) frame.Grid {
				open := (math.Sin(t*7) + 1) / 2
				return eachDot(func(r, c int) color.RGB {
					dx := float64(c) - 2.5
					dy := float64(r) - 2.5
					if math.Hypot(dx, dy) > 3.1 {
						return color.RGB{}
					}
					if math.Abs(math.Atan2(dy, dx)) < open*0.75 && dx > 0 {
						return color.RGB{}
					}
					return color.RGB{255, 210, 0}
				})
			},
		},
		{
			ID: "doraemon", Name: "哆啦A梦", Light: color.RGB{32, 112, 232},
			Dot: [frame.DotRows]string{".bbbb.", "bllllb", "l.ll.l", "llrrll", "llllll", ".llll."},
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{".bbbb.", "bllllb", "l.ll.l", "llrrll", "llllll", ".llll."})
				if math.Mod(t, 3.4) > 3.1 { // 眨眼
					g[2][1] = DotPalette['l']
					g[2][4] = DotPalette['l']
				}
				b := 0.7 + 0.3*math.Sin(t*2.4)
				g[3][2] = color.Mix(color.RGB{}, color.RGB{255, 45, 40}, b)
				g[3][3] = color.Mix(color.RGB{}, color.RGB{255, 45, 40}, b)
				return g
			},
		},
		{
			ID: "pokeball", Name: "精灵球", Light: color.RGB{232, 45, 36},
			Dot: [frame.DotRows]string{".rrrr.", "rrrrrr", "rrrrrr", "ssSSss", "ssssss", ".ssss."},
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{".rrrr.", "rrrrrr", "rrrrrr", "ssSSss", "ssssss", ".ssss."})
				b := 0.25 + 0.75*math.Pow(math.Abs(math.Sin(t*2.6)), 3)
				g[3][2] = color.Mix(color.RGB{40, 40, 50}, color.RGB{255, 240, 200}, b)
				g[3][3] = color.Mix(color.RGB{40, 40, 50}, color.RGB{255, 240, 200}, b)
				return g
			},
		},
		{
			ID: "lava", Name: "岩浆", Light: color.RGB{255, 95, 10},
			Dot: [frame.DotRows]string{"...m..", "..mm..", ".myym.", "myyyym", "myrrym", ".mrrm."},
			// 火是唯一有状态的动画：这一份闭包带着自己的缓冲区。
			DotAnim: NewFire(),
		},
		{
			ID: "aurora", Name: "极光", Light: color.RGB{40, 210, 180},
			Dot: [frame.DotRows]string{"..aa..", ".aGa..", "aGGaa.", ".aGGa.", "..aGa.", "...a.."},
			DotAnim: func(t float64) frame.Grid {
				return eachDot(func(r, c int) color.RGB {
					v := math.Sin(float64(c)*0.8+t*1.6) + math.Sin(float64(r)*1.1-t*1.1)
					return color.HSV(150+v*38, 0.85, math.Max(0, 0.35+0.65*math.Sin(v*1.4+t)))
				})
			},
		},
		{
			ID: "sunset", Name: "落日", Light: color.RGB{255, 120, 60},
			Dot: [frame.DotRows]string{"......", "..mm..", ".mmmm.", "mmmmmm", "rrrrrr", "nnnnnn"},
			DotAnim: func(t float64) frame.Grid {
				g := frame.BlankGrid()
				y := 2.2 + 1.1*math.Sin(t*0.7) // 太阳缓慢起落
				for r := 0; r < 6; r++ {
					for c := 0; c < 6; c++ {
						d := math.Hypot((float64(c)-2.5)*0.9, float64(r)-y)
						if d < 2.2 {
							g[r][c] = color.Mix(color.RGB{255, 205, 80}, color.RGB{255, 90, 30}, d/2.2)
						} else {
							g[r][c] = color.Mix(color.RGB{70, 20, 60}, color.RGB{15, 8, 25},
								math.Min(1, float64(r)/5))
						}
					}
				}
				return g
			},
		},
		{
			ID: "matrix", Name: "数字雨", Light: color.RGB{30, 230, 80},
			Dot: [frame.DotRows]string{"g.....", ".g...g", "..g.g.", "g..g..", ".g...g", "...g.."},
			DotAnim: func(t float64) frame.Grid {
				g := frame.BlankGrid()
				for c := 0; c < 6; c++ {
					off := math.Mod(float64(c)*2.7, 6)
					y := math.Mod(t*4+off, 8)
					for k := 0; k < 3; k++ {
						r := int(math.Floor(y)) - k
						if r >= 0 && r < 6 {
							g[r][c] = color.RGB{
								0,
								color.Clamp(255 * (1 - float64(k)/3)),
								color.Clamp(90 * (1 - float64(k)/3)),
							}
						}
					}
				}
				return g
			},
		},
		{
			// 循环型主题：点阵屏轮播内容，键盘配色跟着内容走 ——
			// 这个结构比在键盘上画角色好用得多。
			ID: "emoji", Name: "表情包", Light: color.RGB{255, 205, 20},
			Dot: [frame.DotRows]string{"......", ".y..y.", "......", "y....y", ".yyyy.", "......"},
			DotAnim: func(t float64) frame.Grid {
				m := moods[idx(int(math.Floor(t/moodT)), len(moods))]
				g := frame.BlankGrid()
				for r := 0; r < 6; r++ {
					for c := 0; c < 6; c++ {
						switch m.Face[r][c] {
						case 'X':
							g[r][c] = m.Col
						case 'o':
							g[r][c] = color.Mix(color.RGB{}, m.Col, 0.35)
						}
					}
				}
				return g
			},
		},
		{
			ID: "weather", Name: "天气", Light: color.RGB{110, 180, 240},
			Dot: [frame.DotRows]string{"..yy..", ".yyyy.", "yyyyyy", ".yyyy.", "..yy..", "......"},
			DotAnim: func(t float64) frame.Grid {
				return weathers[idx(int(math.Floor(t/3.4)), len(weathers))].Draw(t)
			},
		},
		{
			// 可爱动物：脸放点阵屏，键盘只取它的毛色。
			ID: "shiba", Name: "柴犬", Light: color.RGB{226, 140, 58},
			Dot: [frame.DotRows]string{"o....o", "oooooo", "o.oo.o", "oOOOOo", "oO..Oo", ".OOOO."},
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{"o....o", "oooooo", "o.oo.o", "oOOOOo", "oO..Oo", ".OOOO."})
				if math.Mod(t, 3.6) > 3.3 { // 眨眼
					g[2][1] = DotPalette['o']
					g[2][4] = DotPalette['o']
				}
				if math.Mod(t, 5) > 3.9 { // 吐舌
					g[5][2] = color.RGB{255, 90, 120}
					g[5][3] = color.RGB{255, 90, 120}
				}
				return g
			},
		},
		{
			ID: "panda", Name: "熊猫", Light: color.RGB{120, 140, 165},
			Dot: [frame.DotRows]string{"s....s", "llllll", "lsllsl", "lsllsl", "llssll", ".llll."},
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{"s....s", "llllll", "lsllsl", "lsllsl", "llssll", ".llll."})
				chew := math.Sin(t*4) > 0 // 嚼竹子
				chewed := color.RGB{60, 62, 74}
				if chew {
					g[4][2] = chewed
					g[4][3] = chewed
				} else {
					g[4][2] = DotPalette['l']
					g[4][3] = DotPalette['l']
				}
				return g
			},
		},
		{
			ID: "penguin", Name: "企鹅", Light: color.RGB{45, 70, 110},
			Dot: [frame.DotRows]string{"..ss..", ".ssss.", "s.ss.s", ".smms.", "slllls", ".m..m."},
			DotAnim: func(t float64) frame.Grid {
				last := ".m..m."
				if math.Sin(t*2.2) > 0 { // 左右摇摆
					last = "m...m."
				}
				return DotFromArt([frame.DotRows]string{"..ss..", ".ssss.", "s.ss.s", ".smms.", "slllls", last})
			},
		},
		{
			ID: "jelly", Name: "水母", Light: color.RGB{210, 90, 225},
			Dot: [frame.DotRows]string{"..pp..", ".pppp.", "pppppp", "p.pp.p", ".v.v.v", "v.v.v."},
			DotAnim: func(t float64) frame.Grid {
				g := frame.BlankGrid()
				b := 0.7 + 0.3*math.Sin(t*1.9)
				rows := [4]string{"..pp..", ".pppp.", "pppppp", "p.pp.p"}
				for r := 0; r < 4; r++ {
					for c := 0; c < 6; c++ {
						if rows[r][c] == 'p' {
							g[r][c] = color.Mix(color.RGB{}, color.RGB{235, 105, 215}, b)
						}
					}
				}
				for c := 0; c < 6; c++ { // 触手飘
					sw := math.Sin(t*3.1 + float64(c)*1.05)
					if sw > 0 {
						g[4][c] = color.Mix(color.RGB{}, color.RGB{150, 70, 240}, 0.45+0.55*sw)
					} else {
						g[5][c] = color.Mix(color.RGB{}, color.RGB{150, 70, 240}, 0.45-0.55*sw)
					}
				}
				return g
			},
		},
		{
			ID: "starry", Name: "星空", Light: color.RGB{40, 60, 150},
			Dot: [frame.DotRows]string{".....y", "..y...", "y....y", "...y..", ".y...y", "....y."},
			// 星星各自闪
			DotAnim: func(t float64) frame.Grid {
				return eachDot(func(r, c int) color.RGB {
					ph := math.Sin(t*1.7 + float64(r*6+c)*2.399)
					if ph > 0.72 {
						return color.Mix(color.RGB{2, 4, 18}, color.RGB{255, 245, 210}, (ph-0.72)/0.28)
					}
					return color.RGB{2, 4, 18}
				})
			},
		},
		{
			ID: "miku", Name: "初音", Light: color.RGB{0, 235, 205},
			Dot: [frame.DotRows]string{"....tt", "...t.t", "...t..", "...t..", ".ttt..", ".ttt.."},
			// 音符上下轻跳 + 尾巴摆
			DotAnim: func(t float64) frame.Grid {
				g := frame.BlankGrid()
				up := 1
				if math.Sin(t*3.2) > 0 {
					up = 0
				}
				art := [6]string{"....tt", "...t.t", "...t..", "...t..", ".ttt..", ".ttt.."}
				for r := 0; r < 6; r++ {
					for c := 0; c < 6; c++ {
						rr := r - up
						if art[r][c] == 't' && rr >= 0 {
							g[rr][c] = color.RGB{0, 235, 205}
						}
					}
				}
				for c := 0; c < 6; c++ { // 底部一条起伏的声波
					h := math.Abs(math.Sin(t*4 + float64(c)*0.9))
					if h > 0.55 {
						g[5][c] = color.RGB{0, color.Clamp(200 * h), color.Clamp(180 * h)}
					}
				}
				return g
			},
		},
		{
			ID: "veggie", Name: "蔬菜", Light: color.RGB{255, 110, 0},
			Dot: [frame.DotRows]string{".g.g..", "..g.g.", ".oooo.", ".oooo.", "..oo..", "..o..."},
			DotAnim: func(t float64) frame.Grid {
				it := veggies[idx(int(math.Floor(t/3.2)), len(veggies))]
				g := DotFromArt(it.Art)
				return scaled(g, 0.86+0.14*math.Sin(t*2.4))
			},
		},
		{
			ID: "fruit", Name: "水果", Light: color.RGB{255, 45, 40},
			Dot: [frame.DotRows]string{"..g...", ".rrrr.", "rrrrrr", "rrrrrr", "rrrrrr", ".rrrr."},
			DotAnim: func(t float64) frame.Grid {
				it := fruits[idx(int(math.Floor(t/3.2)), len(fruits))]
				g := DotFromArt(it.Art)
				return scaled(g, 0.86+0.14*math.Sin(t*2.4))
			},
		},
		{
			ID: "animal", Name: "动物", Light: color.RGB{255, 120, 10},
			Dot: [frame.DotRows]string{"o....o", ".oooo.", "o.oo.o", "oooooo", ".OrrO.", "..OO.."},
			DotAnim: func(t float64) frame.Grid {
				it := animals[idx(int(math.Floor(t/3.2)), len(animals))]
				g := DotFromArt(it.Art)
				return scaled(g, 0.86+0.14*math.Sin(t*2.4))
			},
		},
		{
			ID: "eva01", Name: "EVA 初号机", Light: color.RGB{150, 40, 255},
			Dot: [frame.DotRows]string{"..LL..", ".vvvv.", "vvvvvv", "vLvvLv", "vvvvvv", ".vvvv."},
			// 双眼发光 -> 暴走时整张脸泛绿
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{"..LL..", ".vvvv.", "vvvvvv", "vLvvLv", "vvvvvv", ".vvvv."})
				rage := math.Mod(t, 6.5) > 5.3
				b := 0.55 + 0.45*math.Abs(math.Sin(t*3.2))
				dark := color.RGB{40, 10, 80}
				green := color.RGB{60, 255, 0}
				g[3][1] = color.Mix(dark, green, b)
				g[3][4] = color.Mix(dark, green, b)
				g[0][2] = color.Mix(dark, green, b)
				g[0][3] = color.Mix(dark, green, b)
				if rage {
					for r := 0; r < 6; r++ {
						for c := 0; c < 6; c++ {
							if g[r][c].Sum() > 20 {
								g[r][c] = color.Mix(g[r][c], green, 0.55)
							}
						}
					}
				}
				return g
			},
		},
		{
			ID: "eva00", Name: "EVA 零号机", Light: color.RGB{255, 130, 0},
			Dot: [frame.DotRows]string{"..oo..", ".oooo.", "oooooo", "o.aa.o", "oooooo", ".oooo."},
			// 独眼左右扫视
			DotAnim: func(t float64) frame.Grid {
				g := DotFromArt([frame.DotRows]string{"..oo..", ".oooo.", "oooooo", "oooooo", "oooooo", ".oooo."})
				ph := math.Mod(t*0.9, 2)
				x := ph
				if ph >= 1 {
					x = 2 - ph
				}
				xi := int(math.Round(x*3)) + 1
				cyan := color.RGB{0, 220, 255}
				g[3][xi] = cyan
				if xi+1 < 6 {
					g[3][xi+1] = cyan
				}
				return g
			},
		},
		{
			ID: "minecraft", Name: "我的世界", Light: color.RGB{60, 220, 40},
			Dot: [frame.DotRows]string{"gggggg", "gLggLg", "BBBBBB", "BnBBnB", "BBBnBB", "BBBBBB"},
			// 方块轮播；轮到苦力怕就嘶嘶闪白后炸开
			DotAnim: func(t float64) frame.Grid {
				b := mcblocks[idx(int(math.Floor(t/3.4)), len(mcblocks))]
				ph := math.Mod(t, 3.4)
				if b.Name == "苦力怕" && ph > 2.35 {
					k := (ph - 2.35) / 1.05
					if k < 0.72 { // 充能：整块越闪越快
						f := math.Abs(math.Sin(k * k * 70))
						g := DotFromArt(b.Art)
						for r := 0; r < 6; r++ {
							for c := 0; c < 6; c++ {
								g[r][c] = color.Mix(g[r][c], color.RGB{255, 255, 210}, f*0.85)
							}
						}
						return g
					}
					e := (k - 0.72) / 0.28 // 爆炸：亮环向外扩
					return eachDot(func(r, c int) color.RGB {
						d := math.Hypot(float64(c)-2.5, float64(r)-2.5)
						if math.Abs(d-e*3.6) < 1.1 {
							return color.Mix(color.RGB{255, 220, 60}, color.RGB{255, 80, 0}, e)
						}
						return color.RGB{}
					})
				}
				return DotFromArt(b.Art)
			},
		},
		{
			ID: "heartbeat", Name: "心跳", Light: color.RGB{235, 40, 60},
			Dot: [frame.DotRows]string{"rr..rr", "rrrrrr", "rrrrrr", ".rrrr.", "..rr..", "......"},
			DotAnim: func(t float64) frame.Grid {
				ph := math.Mod(t*1.9, 1)
				var b float64
				switch {
				case ph < 0.12:
					b = 0.4 + 0.6*(ph/0.12)
				case ph < 0.26:
					b = 1 - 0.5*((ph-0.12)/0.14)
				case ph < 0.38:
					b = 0.5 + 0.5*((ph-0.26)/0.12)
				default:
					b = 0.4 + 0.6*math.Max(0, 1-(ph-0.38)/0.4)
				}
				g := frame.BlankGrid()
				art := [6]string{"rr..rr", "rrrrrr", "rrrrrr", ".rrrr.", "..rr..", "......"}
				for r := 0; r < 6; r++ {
					for c := 0; c < 6; c++ {
						if art[r][c] == 'r' {
							g[r][c] = color.RGB{
								color.Clamp(240 * b),
								color.Clamp(40 * b),
								color.Clamp(52 * b),
							}
						}
					}
				}
				return g
			},
		},
	}

	// 关键一步：等价于 JS 的
	//   Object.keys(LAYOUTS).forEach(k => Object.assign(THEMES[k], LAYOUTS[k]))
	// 这里要做在取数据之前，调用方拿到的必须是覆盖后的最终值。
	for i := range themes {
		// JS 里 24 套的 kb 全是函数，没有一套是字面量字符画，
		// 所以 KB 一律填成"6 行 × 16 列全灭"。真正的摆法在 KBAnim 里。
		themes[i].KB = blankKB

		if _, anim, ok := LayoutFor(themes[i].ID); ok {
			themes[i].KBAnim = anim
			continue
		}
		// LAYOUTS 没覆盖的那 3 套：摆法直接写在 THEMES 里（是构造函数不是字符画）。
		if own, ok := themeOwnKB[themes[i].ID]; ok {
			themes[i].KBAnim = own.anim
		}
	}
	return themes
}

// All 按固定顺序返回 24 套主题（顺序即 UI 里的展示顺序，必须与 JS 对象字面量一致）。
func All() []Theme {
	out := make([]Theme, len(registry))
	copy(out, registry)
	return out
}

// ByID 按 id 查主题。
func ByID(id string) (Theme, bool) {
	for _, t := range registry {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// Default 返回默认主题（pikachu）。
func Default() Theme {
	t, _ := ByID("pikachu")
	return t
}
