package theme

import (
	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/frame"
)

// DotAnim 是一帧点阵动画：给定场景本地时钟（秒），返回这一帧。
// 必须是纯函数 —— 引擎会在任意时刻调用它，不允许有跨帧的隐式状态
// （旧版只有 fire 一个动画有内部缓冲，这里改成自己带状态的结构体）。
type DotAnim func(t float64) frame.Grid

// KBAnim 是一帧键盘动画：给定场景本地时钟（秒），返回这一帧。
type KBAnim func(t float64) frame.LEDs

// Theme 是一套主题：点阵静态图 + 点阵动画 + 键盘静态配色 + 键盘动画。
//
// 静态模式：两边都定格；动态模式：两边一起动。
type Theme struct {
	ID   string
	Name string
	// Light 是这套主题的主色，给固件内置效果和胶囊取色用。
	Light color.RGB
	// Dot 是 6×6 字符画，行 0 在上、列 0 在左。
	Dot [frame.DotRows]string
	// KB 是 6×16 字符画，空格位用 '.' 或任意无效字符表示不点亮。
	KB [frame.KBRows]string
	// DotAnim / KBAnim 为 nil 表示这套主题没有动态版本。
	DotAnim DotAnim
	KBAnim  KBAnim
}

// DotFrame 渲染主题的静态点阵图。
//
// 如果字符画是空的但配了动画，退回动画的第 0 帧 ——
// 原版 24 套主题的键盘配色全部来自构图函数，字符画字段是空的，
// 直接读字符画会得到一块全黑的键盘。
func (t Theme) DotFrame() frame.Grid {
	if !artIsEmpty(t.Dot) {
		return DotFromArt(t.Dot)
	}
	if t.DotAnim != nil {
		return t.DotAnim(0)
	}
	return frame.Grid{}
}

// KBFrames 渲染主题的静态键盘配色。空字符画时退回构图函数的第一帧。
//
// 这里踩过一次：24 套主题的 `kb` 在原版里全是构图函数调用（`kbDiamond(...)` 之类），
// 没有一套是字符画，所以 `KB` 字段对它们全是空的。
// 谁要是按"字符画"去读，推出来的就是一块全黑的键盘 —— 而且不会报错。
func (t Theme) KBFrames() frame.LEDs {
	if !artIsEmpty(t.KB) {
		return KBFromArt(t.KB)
	}
	if t.KBAnim != nil {
		return t.KBAnim(0)
	}
	return frame.LEDs{}
}

func artIsEmpty(art [frame.KBRows]string) bool {
	for _, row := range art {
		if row != "" && row != "......" && row != "................" {
			return false
		}
	}
	return true
}

// DotAt 返回 t 时刻的点阵帧：有动画就用动画，否则用静态图。
func (t Theme) DotAt(sec float64) frame.Grid {
	if t.DotAnim != nil {
		return t.DotAnim(sec)
	}
	return t.DotFrame()
}

// KBAt 返回 t 时刻的键盘帧：有动画就用动画，否则用静态配色。
func (t Theme) KBAt(sec float64) frame.LEDs {
	if t.KBAnim != nil {
		return t.KBAnim(sec)
	}
	return t.KBFrames()
}

// Dynamic 报告这套主题是否至少有一半会动。
func (t Theme) Dynamic() bool { return t.DotAnim != nil || t.KBAnim != nil }

// DotFromArt 把 6×6 字符画渲染成点阵帧。
//
// 行不足 6 行按熄灭补，行内不足 6 列也按熄灭补 —— 数据写错不至于让程序崩。
func DotFromArt(art [frame.DotRows]string) frame.Grid {
	var g frame.Grid
	for r := 0; r < frame.DotRows; r++ {
		row := art[r]
		for c := 0; c < frame.DotCols; c++ {
			if c < len(row) {
				g[r][c] = DotColor(row[c])
			}
		}
	}
	return g
}

// KBFromArt 把 6×16 字符画渲染成键盘帧。
//
// 注意：字符画的列是"物理格子"，不是 LED 索引。
// 空格位（KBMap 里为 255）即使画了颜色也会被跳过。
func KBFromArt(art [frame.KBRows]string) frame.LEDs {
	var l frame.LEDs
	for r := 0; r < frame.KBRows; r++ {
		row := art[r]
		for c := 0; c < frame.KBCols; c++ {
			if !frame.HasLED(r, c) {
				continue
			}
			if c >= len(row) {
				continue
			}
			l.Set(r, c, KBColor(row[c]))
		}
	}
	return l
}
