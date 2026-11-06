package engine

import (
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/theme"
)

var crab = compCellsByName["claude-code"]

// compScene 造一个处于指定状态的伴侣场景：往真 Store 里打事件，
// 和线上走同一条路径。
func compScene(t *testing.T, evs ...agent.Event) *CompanionScene {
	t.Helper()
	st := agent.NewStore()
	base := time.Now().UnixMilli()
	for i, ev := range evs {
		if ev.SessionID == "" {
			ev.SessionID = "s1"
		}
		if ev.Agent == "" {
			ev.Agent = "claude-code"
		}
		if ev.OccurredAt == 0 {
			// 同毫秒会被 Store 的乱序守卫拒掉，逐条 +1ms
			ev.OccurredAt = base + int64(i)
		}
		if ev.EventID == "" {
			// 空 eventId 会被去重窗口当成同一条，逐条给唯一 id
			ev.EventID = "test-" + ev.SessionID + "-" + string(rune('a'+i))
		}
		if out := st.Apply(ev); !out.OK {
			t.Fatalf("事件 %s 应用失败: %+v", ev.Name, out)
		}
	}
	return &CompanionScene{Store: st}
}

// 状态对应的事件序列。
var compStateEvents = map[string][]agent.Event{
	"idle":  {},
	"busy":  {{Name: agent.EvUserMessage}},
	"wait":  {{Name: agent.EvApprovalRequested}},
	"ask":   {{Name: agent.EvSupplementRequested}},
	"done":  {{Name: agent.EvUserMessage}, {Name: agent.EvTurnCompleted}},
	"error": {{Name: agent.EvUserMessage}, {Name: agent.EvTurnCompleted, Outcome: "failed"}},
}

// 硬契约：伴侣场景只接管点阵，键盘帧要么透传主题、要么为空 ——
// 绝不自己编一套。推流主题：键帧透传；固件灯效：键帧为 nil（固件自己跑）。
func TestCompanionKeysArePassthroughOnly(t *testing.T) {
	th := NewThemeScene(theme.Default(), true, color.DefaultCalibration())
	sc := compScene(t, compStateEvents["busy"]...)
	sc.SetKeys(th)
	r := sc.Frame(0)
	if r.Keys == nil {
		t.Fatal("挂着推流主题时键帧应透传，得到 nil —— 键盘灯效就是这么丢的")
	}
	if r.Matrix == nil {
		t.Fatal("必须出点阵帧")
	}

	eff := device.FirmwareEffect{ID: 3, Name: "单色呼吸", Colorful: true}
	th.KBFirmware = &eff
	if r := sc.Frame(0); r.Keys != nil {
		t.Error("固件灯效时一帧键帧都不许推（固件自己跑）")
	}

	// 不挂主题（兜底）：键帧 nil，绝不无中生有
	sc.SetKeys(nil)
	if r := sc.Frame(0); r.Keys != nil {
		t.Error("没有主题可透传时，伴侣不许自己造键帧")
	}
}

// resolve 必须把 Store 的头部会话解析成对应状态。
func TestCompanionResolveStates(t *testing.T) {
	want := map[string]agent.State{
		"idle": agent.StateIdle, "busy": agent.StateBusy, "wait": agent.StateWait,
		"ask": agent.StateAsk, "done": agent.StateDone, "error": agent.StateError,
	}
	for name, evs := range compStateEvents {
		sc := compScene(t, evs...)
		_, st := sc.resolve()
		if st != want[name] {
			t.Errorf("%s: resolve = %s，期望 %s", name, st, want[name])
		}
	}
}

// 颜色认状态：每个状态渲染出自己的状态色（色相，不看亮度）。
// 注意眼睛/细节格在任何状态都可能是白色，所以断言取一个身体格。
func TestCompanionStateColors(t *testing.T) {
	for name, st := range map[string]agent.State{
		"idle": agent.StateIdle, "busy": agent.StateBusy, "wait": agent.StateWait,
		"ask": agent.StateAsk, "done": agent.StateDone, "error": agent.StateError,
	} {
		g := compFrame("claude-code", crab, st, compColor[st].Float(), 0, 0)
		if c := g[2][0]; !sameHue(c, compColor[st].Float()) {
			t.Errorf("%s: 色相不对，得到 %v，期望 %v 的缩放", name, c, compColor[st].Float())
		}
	}
}

// 眼睛不特殊化：任何状态都跟身体一起走状态色（无白色特权格）。
func TestCompanionEyesFollowStateColor(t *testing.T) {
	for name, st := range map[string]agent.State{
		"busy": agent.StateBusy, "wait": agent.StateWait,
		"ask": agent.StateAsk, "error": agent.StateError,
	} {
		g := compFrame("claude-code", crab, st, compColor[st].Float(), 0.06, 0.06)
		if c := g[0][1]; !sameHue(c, compColor[st].Float()) {
			t.Errorf("%s: 眼睛应是状态色 %v 的缩放，得到 %v", name, compColor[st].Float(), c)
		}
	}
}

// 执行中：流光在动 —— 三种扫法，不同时刻帧都必须不同。
func TestCompanionBusyWaveFlows(t *testing.T) {
	col := compColor[agent.StateBusy].Float()
	for name, cells := range map[string]compCells{
		"claude-code": crab, "codex": compCellsByName["codex"], "opencode": compCellsByName["opencode"],
	} {
		g1 := compFrame(name, cells, agent.StateBusy, col, 0, 0)
		g2 := compFrame(name, cells, agent.StateBusy, col, 0.5, 0)
		if g1 == g2 {
			t.Errorf("%s: 流光停了（500ms 两帧完全一致）", name)
		}
	}
	// OpenCode 行扫描：高亮行确实在往下滚
	door := compCellsByName["opencode"]
	litRow := func(g [6][6]color.RGBFloat) int {
		best, bestLum := -1, -1.0
		for r := 0; r < 6; r++ {
			if l := lum(g[r][0]); l > bestLum {
				best, bestLum = r, l
			}
		}
		return best
	}
	if litRow(compFrame("opencode", door, agent.StateBusy, col, 0, 0)) ==
		litRow(compFrame("opencode", door, agent.StateBusy, col, 0.5, 0)) {
		t.Error("行扫描 500ms 后高亮行没动")
	}
}

// 等你授权：双跳心跳 —— 两个峰、谷底拉得开，眼睛在拍点上亮白。
func TestCompanionWaitHeartbeat(t *testing.T) {
	col := compColor[agent.StateWait].Float()
	peak1 := compFrame("claude-code", crab, agent.StateWait, col, 0.065, 0)
	rest := compFrame("claude-code", crab, agent.StateWait, col, 0.60, 0)
	peak2 := compFrame("claude-code", crab, agent.StateWait, col, 0.25, 0)
	if lum(peak1[2][2]) <= lum(rest[2][2])*1.8 {
		t.Errorf("第一跳峰谷拉不开：峰 %v 谷 %v", peak1[2][2], rest[2][2])
	}
	if lum(peak2[2][2]) <= lum(rest[2][2])*1.3 {
		t.Errorf("第二跳不存在：%v vs %v", peak2[2][2], rest[2][2])
	}
}

// 等你补充：三连轻叩，一波弱过一波；形状原地不动（不晃）。
func TestCompanionAskTripleKnock(t *testing.T) {
	col := compColor[agent.StateAsk].Float()
	k1 := compFrame("claude-code", crab, agent.StateAsk, col, 0.07, 0)
	gap := compFrame("claude-code", crab, agent.StateAsk, col, 0.60, 0)
	k3 := compFrame("claude-code", crab, agent.StateAsk, col, 0.41, 0)
	if lum(k1[2][2]) < 50 || lum(gap[2][2]) > lum(k1[2][2])*0.4 {
		t.Errorf("轻叩峰谷不对：%v / %v", k1[2][2], gap[2][2])
	}
	if lum(k3[2][2]) <= lum(gap[2][2]) || lum(k3[2][2]) >= lum(k1[2][2]) {
		t.Errorf("第三叩应弱于第一叩、强于间歇：%v / %v / %v", k1[2][2], k3[2][2], gap[2][2])
	}
	// 不晃：两次叩点亮的是同一组格子
	for r := 0; r < 6; r++ {
		for c := 0; c < 6; c++ {
			if (k1[r][c] != (color.RGBFloat{})) != (k3[r][c] != (color.RGBFloat{})) {
				t.Fatalf("格子 (%d,%d) 一闪一灭 —— 形状在晃", r, c)
			}
		}
	}
}

// 完成：亮一下（指数衰减），光晕从中心荡过形状后稳定；6 秒停留结束退回空闲。
func TestCompanionDoneBloomsThenSettles(t *testing.T) {
	col := compColor[agent.StateDone].Float()
	fresh := compFrame("claude-code", crab, agent.StateDone, col, 0, 0.05)
	settled := compFrame("claude-code", crab, agent.StateDone, col, 0, 3.0)
	if lum(fresh[2][2]) <= lum(settled[2][2])*1.4 {
		t.Errorf("开头的绽放衰减没了：%v vs %v", fresh[2][2], settled[2][2])
	}
	// 涟漪荡到 (2,4)：dt≈0.35 时它应比稳定后亮
	rip := compFrame("claude-code", crab, agent.StateDone, col, 0, 0.35)
	if lum(rip[2][4]) <= lum(settled[2][4]) {
		t.Errorf("光晕没荡到边缘格：%v vs %v", rip[2][4], settled[2][4])
	}
	// 不摆字形：绽放前后亮的是同一组格子（螃蟹自己）
	for r := 0; r < 6; r++ {
		for c := 0; c < 6; c++ {
			if (fresh[r][c] != (color.RGBFloat{})) != (settled[r][c] != (color.RGBFloat{})) {
				t.Fatalf("格子 (%d,%d) 一亮一灭 —— 形状换了", r, c)
			}
		}
	}

	sc := compScene(t, compStateEvents["done"]...)
	if _, st := sc.resolve(); st != agent.StateDone {
		t.Fatalf("刚完成应为 done，实际 %s", st)
	}
	sc.holdUntil = time.Now().Add(-time.Second) // 模拟停留结束
	if _, st := sc.resolve(); st != agent.StateIdle {
		t.Errorf("停留结束应退回 idle，实际 %s", st)
	}
}

// 出错：警示双闪 —— 急两声、长一息；不摆字形。
func TestCompanionErrorDoubleBlink(t *testing.T) {
	col := compColor[agent.StateError].Float()
	on1 := compFrame("claude-code", crab, agent.StateError, col, 0, 0.06)
	gap := compFrame("claude-code", crab, agent.StateError, col, 0, 0.155)
	on2 := compFrame("claude-code", crab, agent.StateError, col, 0, 0.25)
	rest := compFrame("claude-code", crab, agent.StateError, col, 0, 0.8)
	if lum(on1[2][2]) < 50 || lum(gap[2][2]) > lum(on1[2][2])*0.35 {
		t.Errorf("双闪峰谷不对：%v / %v", on1[2][2], gap[2][2])
	}
	if lum(on2[2][2]) <= lum(rest[2][2]) {
		t.Errorf("第二闪不存在：%v vs %v", on2[2][2], rest[2][2])
	}
	// 不摆字形：亮格集合始终是螃蟹
	for r := 0; r < 6; r++ {
		for c := 0; c < 6; c++ {
			if (on1[r][c] != (color.RGBFloat{})) != (crabAt(r, c)) {
				t.Fatalf("格子 (%d,%d) 不属于螃蟹形状 —— 出现字形", r, c)
			}
		}
	}
}

func crabAt(r, c int) bool {
	row := compShape["claude-code"][r]
	return c < len(row) && (row[c] == 'b' || row[c] == 'e' || row[c] == 'p')
}

// 空闲：Claude 会眨眼 —— 眨眼窗口内眼睛灭、身体亮。
func TestCompanionIdleBlink(t *testing.T) {
	col := compColor[agent.StateIdle].Float()
	open := compFrame("claude-code", crab, agent.StateIdle, col, 3.0, 0)
	shut := compFrame("claude-code", crab, agent.StateIdle, col, 0.1, 0) // mod 6.4 = 0.1 < 0.28 → 眨眼
	eye := [2]int{0, 1}                                                  // 'e' 格
	if open[eye[0]][eye[1]] == (color.RGBFloat{}) {
		t.Fatal("不眨眼时眼睛应亮着")
	}
	if shut[eye[0]][eye[1]] != (color.RGBFloat{}) {
		t.Error("眨眼窗口内眼睛应熄灭")
	}
	if shut[2][2] == (color.RGBFloat{}) {
		t.Error("眨眼只灭眼睛，身体不能跟着灭")
	}
}

// 空闲形状可选：指定了就用指定的；跟随会话则没有会话时退兜底。
func TestCompanionIdleAgentChoice(t *testing.T) {
	sc := compScene(t) // 无会话 → 空闲
	sc.SetIdleAgent("codex")
	if name, st := sc.resolve(); name != "codex" || st != agent.StateIdle {
		t.Errorf("指定空闲形状应为 codex/idle，实际 %s/%s", name, st)
	}
	sc.SetIdleAgent("")
	if name, _ := sc.resolve(); name != "generic" {
		t.Errorf("跟随会话且无会话应为 generic，实际 %s", name)
	}
	// 有会话时，跟随会话 = 用会话的形状
	sc = compScene(t, compStateEvents["done"]...)
	sc.SetIdleAgent("auto")
	sc.resolve()                                // 第一次：登记签名、开始停留
	sc.holdUntil = time.Now().Add(-time.Second) // 演完回空闲
	if name, st := sc.resolve(); name != "claude-code" || st != agent.StateIdle {
		t.Errorf("跟随会话空闲应显示会话工具 claude-code，实际 %s/%s", name, st)
	}
}

// firstLit 找帧里第一个亮格。
func firstLit(g [6][6]color.RGBFloat) (color.RGBFloat, bool) {
	for r := 0; r < 6; r++ {
		for c := 0; c < 6; c++ {
			if g[r][c] != (color.RGBFloat{}) {
				return g[r][c], true
			}
		}
	}
	return color.RGBFloat{}, false
}

// sameHue 判断 a 是否就是 b 的整体变亮/变暗（色相一致）：
// b 的零通道 a 必须也是零，非零通道共享同一个缩放（容差取整误差）。
func sameHue(a, b color.RGBFloat) bool {
	scale := -1.0
	for i := 0; i < 3; i++ {
		if b[i] == 0 {
			if a[i] != 0 {
				return false
			}
			continue
		}
		s := float64(a[i]) / float64(b[i])
		if scale < 0 {
			scale = s
			continue
		}
		if s < scale*0.88 || s > scale*1.14 {
			return false
		}
	}
	return scale > 0
}

func lum(c color.RGBFloat) float64 {
	return 0.3*float64(c[0]) + 0.6*float64(c[1]) + 0.1*float64(c[2])
}

// 兜底宝石：不认识的工具一律走 gem —— 12 格轮廓 + 4 格暗核，
// 跑圈的 ring 是一条闭合回路（相邻格都是 8 邻接，不会隔着洞跳）。
func TestCompanionGemFallback(t *testing.T) {
	gem := compCellsByName["generic"]
	if len(gem.body) != 12 || len(gem.detail) != 4 {
		t.Fatalf("宝石应为 12 轮廓 + 4 暗核，实际 %d/%d", len(gem.body), len(gem.detail))
	}

	sc := compScene(t, agent.Event{Name: agent.EvUserMessage, Agent: "zcode"})
	if name, _ := sc.resolve(); name != "generic" {
		t.Errorf("未知工具应回落 generic，实际 %s", name)
	}
	if got := sc.cellsFor("zcode"); len(got.body) != 12 {
		t.Errorf("未知工具应用宝石点位，实际 %d 格", len(got.body))
	}
}

// TestCompanionIdleBreathesToWhite 钉住空闲呼吸的吸气顶必须到纯白：
// 白色是唯一"压亮度即变灰"的颜色，波峰不碰 1.0 的话预览永远灰蒙蒙。
func TestCompanionIdleBreathesToWhite(t *testing.T) {
	gem := compCellsByName["generic"]
	white := func(g frame.Grid) int {
		n := 0
		for _, row := range g {
			for _, c := range row {
				if c[0] > 240 && c[1] > 240 && c[2] > 240 {
					n++
				}
			}
		}
		return n
	}
	// t=1.75 是 7 秒周期的 sin 波峰（br=1.0），波谷在 5.25。
	if white(compFrame("generic", gem, agent.StateIdle, compColor[agent.StateIdle].Float(), 1.75, 0)) == 0 {
		t.Fatal("空闲呼吸的吸气顶应到纯白")
	}
	if white(compFrame("generic", gem, agent.StateIdle, compColor[agent.StateIdle].Float(), 5.25, 0)) > 0 {
		t.Fatal("空闲呼吸的呼气不该顶到纯白（呼吸幅度不能丢）")
	}
}
