package engine

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// 状态色。
//
// 点阵灯珠的蓝通道效率远高于绿/红（约 14:4:1），低饱和色会被蓝压成青或紫。
// 所以状态色按"目标色相 + 非蓝状态尽量不给蓝"来配：
// 橙/绿/红把 B 压到 0，紫用高红 + 一点点蓝，才不会看成"执行中的蓝"。
var compColor = map[agent.State]color.RGB{
	agent.StateIdle:  {255, 255, 255},
	agent.StateBusy:  {0, 90, 255},
	agent.StateWait:  {255, 60, 0},
	agent.StateAsk:   {230, 0, 35},
	agent.StateDone:  {0, 220, 0},
	agent.StateError: {255, 0, 0},
}

// 形状认工具。'b' = 身体，'p' = 暗一档细节，'e' = 眼睛（空闲时会眨）。
var compShape = map[string][frame.DotRows]string{
	// Claude Code：方块螃蟹
	"claude-code": {
		".e..e.", ".b..b.", "bbbbbb", "bbbbbb", ".b..b.", "b....b",
	},
	// Codex：圆角云 + >_
	"codex": {
		".bbbb.", "bbbbbb", "bbpbbb", "bbbpbb", "bbpbpb", ".bbbb.",
	},
	// OpenCode：竖门
	"opencode": {
		"bbbbbb", "b....b", "bp...b", "b.p..b", "bp.p.b", "bbbbbb",
	},
	// 兜底（一切不认识的工具）：一颗宝石 —— 菱形切面轮廓 + 暗核。
	// 轮廓格跑圈是一整圈闭合路径，涟漪从暗核位置荡开，六种状态动效直接套用。
	"generic": {
		"..bb..", ".b..b.", "b.pp.b", "b.pp.b", ".b..b.", "..bb..",
	},
}

// BuiltinShapeNames 是内置形状的显示名。
var BuiltinShapeNames = map[string]string{
	"claude-code": "Claude Code",
	"codex":       "Codex",
	"opencode":    "OpenCode",
}

// BuiltinShapes 返回内置工具形状（id → 6×6 字符画），供页面展示与校验。
func BuiltinShapes() map[string][frame.DotRows]string {
	out := make(map[string][frame.DotRows]string, len(compShape))
	for id, art := range compShape {
		out[id] = art
	}
	return out
}

// CompanionHold 是"完成/出错"演多久之后退回空闲。
const CompanionHold = 6 * time.Second

// compCells 是一个形状的三种点位，按行主序排好。
type compCells struct {
	body   [][2]int // 'b'
	detail [][2]int // 'p'
	eyes   [][2]int // 'e'
}

func parseCompCells(art [frame.DotRows]string) compCells {
	var c compCells
	for r := 0; r < frame.DotRows; r++ {
		for col := 0; col < frame.DotCols && col < len(art[r]); col++ {
			switch art[r][col] {
			case 'b':
				c.body = append(c.body, [2]int{r, col})
			case 'p':
				c.detail = append(c.detail, [2]int{r, col})
			case 'e':
				c.eyes = append(c.eyes, [2]int{r, col})
			}
		}
	}
	return c
}

var compCellsByName = func() map[string]compCells {
	m := make(map[string]compCells, len(compShape))
	for name, art := range compShape {
		m[name] = parseCompCells(art)
	}
	return m
}()

// CompanionScene 把 AI 工具的任务状态画到 6×6 小屏上：
// **形状认工具、颜色认状态、节奏认进展**。
//
// 6×6 加低帧率，余光里最靠得住的是"动的节奏"；所有起伏都是正弦沿，
// 不摆字形、不打方波 —— 精致是这套灯语的底线：
//
//	空闲   慢呼吸（Claude 偶尔眨眼，Codex 光标闪）
//	执行中 一道流光扫过形状（波光 / 行扫描 / 对角亮带，各家扫法不同）
//	等你授权 双跳心跳，一拍强一拍弱
//	等你补充 三连轻叩，一波弱过一波
//	完成   亮一下，光晕从中心荡过形状后停 6 秒
//	出错   警示双闪：急两声、长一息，停 6 秒
//
// 键盘背光不归这里管：Keys 挂着当前主题场景，背光帧原样透传 ——
// 切进伴侣不会让键盘灯效丢掉（动态主题继续动，固件灯效继续跑）。
type CompanionScene struct {
	Store *agent.Store

	// Keys 是当前主题场景：点阵被伴侣接管，背光帧从它透传。
	// 写方必须走 SetKeys —— 帧泵 goroutine 在 Frame 里并发读。
	keysMu sync.RWMutex
	keys   *ThemeScene

	// holdUntil 记录完成/出错这种"演一下就走"的截止时刻；
	// stateSince 是这次完成/出错的开始时刻（一次性动画的相位零点）。
	holdUntil  time.Time
	stateSince time.Time
	lastSig    string

	// idleAgent 是空闲时显示的工具形状；"" / "auto" = 跟随最近会话。
	idleAgent atomic.Value // string
}

// SetKeys 换背光透传源（主题场景被重建时调用）。
func (c *CompanionScene) SetKeys(k *ThemeScene) {
	c.keysMu.Lock()
	c.keys = k
	c.keysMu.Unlock()
}

// keysSource 读透传源（锁内）。
func (c *CompanionScene) keysSource() *ThemeScene {
	c.keysMu.RLock()
	defer c.keysMu.RUnlock()
	return c.keys
}

// SetIdleAgent 设置空闲时显示的工具形状；"" / "auto" = 跟随最近会话。
func (c *CompanionScene) SetIdleAgent(id string) { c.idleAgent.Store(id) }

// Name 实现 Scene。
func (c *CompanionScene) Name() string { return "companion" }

// resolve 算出"现在该显示谁、什么状态"，含完成/出错的 6 秒停留回退
// 和空闲时的用户指定形状。
func (c *CompanionScene) resolve() (name string, st agent.State) {
	snap := c.Store.Snapshot()
	st = agent.StateIdle
	name = "generic"

	if snap.Top != nil {
		top := *snap.Top
		st = top.State
		name = top.Agent
		// 完成/出错演 6 秒后退回空闲：不让一次成功永远占着屏幕
		if st == agent.StateDone || st == agent.StateError {
			sig := top.SessionID + string(st) + itoa(top.EventAt)
			if sig != c.lastSig {
				c.lastSig = sig
				c.stateSince = time.Now()
				c.holdUntil = time.Now().Add(CompanionHold)
			} else if time.Now().After(c.holdUntil) {
				st = agent.StateIdle
			}
		}
	}

	// 空闲：用户指定了形状就用指定的（认不认识都先试试，不认识回 generic）；
	// 没指定就跟随最近会话的形状。
	if st == agent.StateIdle {
		if ia, _ := c.idleAgent.Load().(string); ia != "" && ia != "auto" {
			name = ia
		}
	}
	if _, ok := compCellsByName[name]; !ok {
		name = "generic"
	}
	return name, st
}

// cellsFor 取某个工具要画的点位：内置形状，不认识一律兜底宝石。
func (c *CompanionScene) cellsFor(name string) compCells {
	if cells, ok := compCellsByName[name]; ok {
		return cells
	}
	return compCellsByName["generic"]
}

// Frame 实现 Scene。点阵是伴侣的；键盘背光帧从主题场景透传（固件灯效时
// 主题帧本来就是 nil，固件自己跑，一帧都不推）。
func (c *CompanionScene) Frame(t time.Duration) Render {
	name, st := c.resolve()

	col, ok := compColor[st]
	if !ok {
		col = compColor[agent.StateIdle]
	}

	// dt 只在完成/出错里用：一次性动画需要"进入这个状态多久了"。
	dt := time.Since(c.stateSince).Seconds()
	g := compFrame(name, c.cellsFor(name), st, col, t.Seconds(), dt)
	m := g.Encode(color.DefaultCalibration(), 50, false)
	// 预览帧满亮度：设备端压一半的点阵，页面上看原始颜色。
	pm := g.Encode(color.DefaultCalibration(), 100, false)

	r := Render{Matrix: &m, PrevMatrix: &pm}
	if k := c.keysSource(); k != nil {
		kr := k.Frame(t)
		r.Keys = kr.Keys
		r.PrevKeys = kr.PrevKeys
	}
	return r
}

// compFrame 按状态分发到各自的节奏。
func compFrame(name string, cells compCells, st agent.State, col color.RGB, t, dt float64) frame.Grid {
	switch st {
	case agent.StateBusy:
		return compBusy(name, cells, col, t)
	case agent.StateWait:
		return compWait(cells, col, t)
	case agent.StateAsk:
		return compAsk(cells, col, t)
	case agent.StateDone:
		return compDone(cells, col, dt)
	case agent.StateError:
		return compError(cells, col, dt)
	default:
		return compIdle(name, cells, col, t)
	}
}

var white = color.RGB{255, 255, 255}

// paint 按三档亮度铺一个形状。
func paint(c compCells, col color.RGB, bodyBr, detailBr, eyeBr float64) frame.Grid {
	var g frame.Grid
	for _, p := range c.body {
		g[p[0]][p[1]] = color.Scale(col, bodyBr)
	}
	for _, p := range c.detail {
		g[p[0]][p[1]] = color.Scale(col, detailBr)
	}
	for _, p := range c.eyes {
		g[p[0]][p[1]] = color.Scale(col, eyeBr)
	}
	return g
}

// thump 是一段平滑的单击：width 内一个完整正弦包（0→1→0），其余为 0。
// 心跳、轻叩、警示闪全由它拼 —— 平滑是精致的底线，这里不许出现方波。
func thump(ph, off, width float64) float64 {
	p := ph - off
	if p <= 0 || p >= width {
		return 0
	}
	return math.Sin(math.Pi * p / width)
}

// mixRGB 把 a 往 b 混 k 比例（完成态的涟漪洗白用）。
func mixRGB(a, b color.RGB, k float64) color.RGB {
	if k <= 0 {
		return a
	}
	if k > 1 {
		k = 1
	}
	return color.RGB{
		uint8(float64(a[0]) + (float64(b[0])-float64(a[0]))*k),
		uint8(float64(a[1]) + (float64(b[1])-float64(a[1]))*k),
		uint8(float64(a[2]) + (float64(b[2])-float64(a[2]))*k),
	}
}

// compIdle 空闲：7 秒一周期的慢呼吸，证明"我还活着"，但不抢注意力。
// Claude 每 6.4 秒眨一次眼；Codex 的 >_ 像终端光标一样闪。
func compIdle(name string, c compCells, col color.RGB, t float64) frame.Grid {
	// 吸气必须顶到纯白:白色是唯一"压亮度即变灰"的颜色,
	// 波峰不碰 1.0 的话页面和屏幕上永远只能看到一层灰。
	br := 0.60 + 0.40*sin(t*2*math.Pi/7)
	eyeBr := br
	detailBr := br * 0.5
	switch name {
	case "claude-code":
		if math.Mod(t, 6.4) < 0.28 {
			eyeBr = 0 // 眨眼：眼睛熄一下
		}
	case "codex":
		if math.Mod(t, 1.25) < 0.42 {
			detailBr = 0 // 光标闪
		}
	}
	return paint(c, col, br, detailBr, eyeBr)
}

// compBusy 执行中：一道流光扫过形状，每家工具扫法不一样。
func compBusy(name string, c compCells, col color.RGB, t float64) frame.Grid {
	g := paint(c, col, 0, 0, 0)
	switch name {
	case "claude-code":
		// 细波光：低振幅的行间流动，螃蟹是活的但不张扬
		for _, p := range c.body {
			br := 0.52 + 0.20*sin(t*2.2+float64(p[0])*0.9+float64(p[1])*0.35)
			g[p[0]][p[1]] = color.Scale(col, br)
		}
		for _, p := range c.eyes {
			br := 0.52 + 0.20*sin(t*2.2+float64(p[0])*0.9+float64(p[1])*0.35)
			g[p[0]][p[1]] = color.Scale(col, br)
		}
	case "opencode":
		// 行扫描：边框逐行点亮滚下去，门在过数据
		s := int(math.Floor(t*2.2)) % 6
		for _, p := range c.body {
			br := 0.40
			if p[0] == s {
				br = 1.0
			}
			g[p[0]][p[1]] = color.Scale(col, br)
		}
		for _, p := range c.detail {
			br := 0.12
			if p[0] == s {
				br = 0.55
			}
			g[p[0]][p[1]] = color.Scale(col, br)
		}
	default:
		// 对角流光：cos⁴ 压窄的亮带从左下扫到右上，扫过才是一"道"光
		for _, p := range c.body {
			w := math.Pow(math.Max(0, math.Cos((float64(p[0])*0.95+float64(p[1])*0.65-t*2.2)*math.Pi)), 4)
			g[p[0]][p[1]] = color.Scale(col, 0.50+0.44*w)
		}
		for _, p := range c.detail {
			w := math.Pow(math.Max(0, math.Cos((float64(p[0])*0.95+float64(p[1])*0.65-t*2.2)*math.Pi)), 4)
			g[p[0]][p[1]] = color.Scale(col, 0.35+0.40*w)
		}
		for _, p := range c.eyes {
			w := math.Pow(math.Max(0, math.Cos((float64(p[0])*0.95+float64(p[1])*0.65-t*2.2)*math.Pi)), 4)
			g[p[0]][p[1]] = color.Scale(col, 0.50+0.44*w)
		}
	}
	return g
}

// compWait 等你授权：双跳心跳，形状随两跳起伏。
func compWait(c compCells, col color.RGB, t float64) frame.Grid {
	ph := math.Mod(t, 0.9)
	pulse := thump(ph, 0, 0.13) + 0.72*thump(ph, 0.19, 0.12)
	if pulse > 1 {
		pulse = 1
	}
	br := 0.26 + 0.68*pulse
	return paint(c, col, br, br*0.7, br)
}

// compAsk 等你补充：三连轻叩，一波弱过一波 —— 像敲门：在吗、在吗、在吗。
func compAsk(c compCells, col color.RGB, t float64) frame.Grid {
	ph := math.Mod(t, 1.15)
	pulse := thump(ph, 0, 0.14) +
		0.72*thump(ph, 0.17, 0.14) +
		0.50*thump(ph, 0.34, 0.14)
	if pulse > 1 {
		pulse = 1
	}
	br := 0.22 + 0.76*pulse
	return paint(c, col, br, br*0.7, br)
}

// compDone 完成：亮一下，一道光晕从中心荡过形状（经过的地方洗白），
// 余韵是一点很轻的闪烁 —— "成了"用绽放说，不用符号。
func compDone(c compCells, col color.RGB, dt float64) frame.Grid {
	base := 0.55 + 0.45*math.Exp(-dt*3.5) + 0.05*sin(dt*3)
	var g frame.Grid
	put := func(p [2]int, k float64) {
		d := math.Hypot((float64(p[0])-2.5)/2.6, (float64(p[1])-2.5)/2.6)
		rip := math.Pow(math.Max(0, math.Cos((d*1.5-dt*2.6)*math.Pi)), 8) * math.Exp(-dt*0.8)
		g[p[0]][p[1]] = color.Scale(mixRGB(col, white, 0.22*rip), (base+0.45*rip)*k)
	}
	for _, p := range c.body {
		put(p, 1)
	}
	for _, p := range c.detail {
		put(p, 0.7)
	}
	for _, p := range c.eyes {
		put(p, 1)
	}
	return g
}

// compError 出错：警示双闪 —— 急两声、长一息，正弦沿不刺眼。
// 不摆字形：红色加这个节奏本身就是"砸了"。
func compError(c compCells, col color.RGB, dt float64) frame.Grid {
	ph := math.Mod(dt, 1.4)
	pulse := thump(ph, 0, 0.12) + 0.85*thump(ph, 0.19, 0.12)
	if pulse > 1 {
		pulse = 1
	}
	br := 0.12 + 0.84*pulse
	return paint(c, col, br, br*0.7, br)
}

// Enter 实现 Scene：点阵切自定义模式；背光配置交给主题场景的 Enter ——
// 固件灯效写效果号、推流主题写 254，进伴侣前后键盘灯效不变。
func (c *CompanionScene) Enter(ctx context.Context, ctrl *device.Controller) error {
	if k := c.keysSource(); k != nil {
		return k.Enter(ctx, ctrl)
	}
	_, err := ctrl.PatchConfig(ctx, proto.DotModePatch(
		proto.DotModeCustom, u8(90), u8(3), nil, nil, nil))
	return err
}

// Exit 实现 Scene。透传源的 Exit 必须对称调用 —— 里面有 ForgetKeys：
// 专注场景会把背光模式切成 0，回来时键灯帧若和缓存相同会被去重永久挡掉，
// 不 ForgetKeys 键盘灯就滞留在旧画面。
func (c *CompanionScene) Exit(ctx context.Context, ctrl *device.Controller) {
	if k := c.keysSource(); k != nil {
		k.Exit(ctx, ctrl)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
