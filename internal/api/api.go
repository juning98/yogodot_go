// Package api 是对外的唯一入口：控制台页面、SSE 推送、Coding 伴侣事件、
// 以及给旧页面/脚本用的 /kb/* 兼容层。
//
// 这一层**不参与出帧**。它只做三件事：读状态、改状态、把状态变化推给订阅者。
// 出帧在 internal/engine 里，页面关掉也不影响键盘 ——
// 这正是旧架构（页面当动画引擎）最根本的错误。
package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
	"github.com/wednesque/yogod/internal/store"
	"github.com/wednesque/yogod/internal/theme"
)

// Deps 是 API 需要的全部依赖，由 main 装配。
type Deps struct {
	Engine   *engine.Engine
	Device   *device.Controller
	Settings *store.Settings
	Agent    *agent.Store
	Pomodoro *engine.Pomodoro
	// Console 是控制台页面（go:embed 进来的）。
	Console fs.FS
	// SwitchScene 由 main 提供：切场景要 engine.SetScene + ctx。
	SwitchScene func(id string) error
	// SwitchTheme 由 main 提供：切主题（内部会重建 ThemeScene）。
	SwitchTheme func(id string, dynamic bool) error
	// SetPattern 由 main 提供：图案库单选（"" 还原主题默认点阵）。
	SetPattern func(id string) error
	// SetKBEffect 由 main 提供：切原厂键盘灯效（-1 = 用主题配色）。
	SetKBEffect func(id int) error
	// SetTypingGuard 由 main 提供：打字保护开关的实时生效钩子
	// （Settings 落盘由 Save 负责，这里只更新引擎侧的判定）。
	// 为 nil 时配置照常保存，只是运行中的实例不热更新。
	SetTypingGuard func(on bool)
	// SetRateTier 由 main 提供：推帧档位的实时生效钩子（0 默认 / 1 流畅 / 2 极速）
	SetRateTier func(tier int)
	// SetIdleAgent 由 main 提供：空闲时显示的工具形状（"" / "auto" = 跟随会话）。
	SetIdleAgent func(id string)
	// RebuildTheme 由 main 提供：亮度等参数变化后的被动重建——只换素材，
	// 不打断正在看的场景（从伴侣里拖亮度滑杆不该把场景踢回主题）。
	RebuildTheme func() error
	// SetCalib 由 main 提供：校准参数热更新，运行中的场景立刻按新参数出帧，
	// 不用重启服务。
	SetCalib func(c store.Calibration) error
	// SetIdlePolicy 由 main 提供：熄屏策略的热更新钩子（省电监视器阈值）。
	SetIdlePolicy func(idle store.Idle)
	// ExePath 是 yogod 可执行文件的绝对路径，控制台生成接入指令时给 Agent 用。
	ExePath string
	// Version 是发布版本号（/api/state 带给控制台页脚）。
	Version string
	// Save 由 main 提供：配置改了就落盘。
	Save func() error
	Logf func(format string, args ...any)
}

// Server 是 HTTP 服务。
type Server struct {
	d   Deps
	hub *hub
	mux *http.ServeMux

	mu      sync.Mutex
	scene   string
	themeID string
	dynamic bool
}

// New 创建一个 API 服务。
func New(d Deps) *Server {
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	s := &Server{
		d:       d,
		hub:     newHub(),
		mux:     http.NewServeMux(),
		scene:   d.Settings.Mode,
		themeID: d.Settings.Theme,
		dynamic: d.Settings.Dynamic,
	}
	s.routes()
	return s
}

// Handler 返回 http.Handler。
func (s *Server) Handler() http.Handler { return s.mux }

// SetSceneState 让 main 在切场景后同步一下（用于 /api/state）。
func (s *Server) SetSceneState(id string) {
	s.mu.Lock()
	s.scene = id
	s.mu.Unlock()
	s.Broadcast()
}

func (s *Server) routes() {
	// 控制台
	s.mux.HandleFunc("/", s.handleIndex)

	// 新 API
	s.mux.HandleFunc("/api/ping", s.handlePing)
	s.mux.HandleFunc("/api/state", s.handleState)
	s.mux.HandleFunc("/api/themes", s.handleThemes)
	s.mux.HandleFunc("/api/patterns", s.handlePatterns)
	s.mux.HandleFunc("/api/pattern", s.handlePattern)
	s.mux.HandleFunc("/api/agents", s.handleAgents)
	s.mux.HandleFunc("/api/scene", s.handleScene)
	s.mux.HandleFunc("/api/theme", s.handleTheme)
	s.mux.HandleFunc("/api/brightness", s.handleBrightness)
	s.mux.HandleFunc("/api/keyboard", s.handleKeyboard)
	s.mux.HandleFunc("/api/pomodoro", s.handlePomodoro)
	s.mux.HandleFunc("/api/sessions", s.handleSessions)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/settings", s.handleSettings)

	// 兼容层
	s.registerCompat()
}

// ── 状态 ──────────────────────────────────────────────────────────

// SessionBrief 是推给页面的会话摘要。
type SessionBrief struct {
	Key     string      `json:"key"`
	Name    string      `json:"name"`
	Agent   string      `json:"agent"`
	State   agent.State `json:"state"`
	Tool    string      `json:"tool"`
	Project string      `json:"project"`
	Msg     string      `json:"msg"`
	Pending int         `json:"pending"`
	At      int64       `json:"at"`
}

// State 是一次全量查询的结果。
type State struct {
	Scene     string           `json:"scene"`
	Theme     string           `json:"theme"`
	ThemeName string           `json:"themeName"`
	Dynamic   bool             `json:"dynamic"`
	Device    device.Status    `json:"device"`
	Pomodoro  engine.PomStatus `json:"pomodoro"`
	Sessions  []SessionBrief   `json:"sessions"`
	Top       *SessionBrief    `json:"top,omitempty"`
	SceneID   string           `json:"sceneId"`
	// KBEffect 是当前生效的原厂键盘灯效 id；-1 表示走主题逐键配色。
	KBEffect int `json:"kbEffect"`
	// Pattern 是当前图案库单选图案 id；空串 = 点阵走主题默认。
	Pattern string `json:"pattern"`
	// ExePath 是 yogod 可执行文件路径（控制台拼接入指令用）。
	ExePath string `json:"exePath"`
	// Version 是发布版本号（控制台页脚显示）。
	Version string `json:"version"`
}

func (s *Server) state() State {
	s.mu.Lock()
	scene, themeID, dyn := s.scene, s.themeID, s.dynamic
	s.mu.Unlock()

	// Sessions 先给成空切片而不是 nil：nil 会序列化成 null，
	// 调用方写 sessions.map(...) 就得先判空。
	st := State{
		Scene: scene, SceneID: scene, Theme: themeID, Dynamic: dyn,
		Sessions: []SessionBrief{},
	}
	if t, ok := theme.ByID(themeID); ok {
		st.ThemeName = t.Name
	}
	st.Device = s.d.Device.Status()
	st.Pomodoro = s.d.Pomodoro.Status(time.Now())
	st.KBEffect = s.kbEffect()
	s.d.Settings.View(func(cfg *store.Settings) { st.Pattern = cfg.Pattern })
	st.ExePath = s.d.ExePath
	st.Version = s.d.Version

	snap := s.d.Agent.Snapshot()
	for _, r := range snap.Sessions {
		st.Sessions = append(st.Sessions, SessionBrief{
			Key: r.Key, Name: r.Name, Agent: r.Agent, State: r.State,
			Tool: r.Tool, Project: r.Project, Msg: r.Msg, Pending: r.Pending, At: r.At,
		})
	}
	if snap.Top != nil {
		top := snap.Top
		st.Top = &SessionBrief{
			Key: top.Key, Name: top.Name, Agent: top.Agent, State: top.State,
			Tool: top.Tool, Project: top.Project, Msg: top.Msg,
			Pending: top.Pending, At: top.At,
		}
	}
	return st
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.state())
}

// ThemeBrief 是主题列表项。
type ThemeBrief struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Light   string    `json:"light"` // #RRGGBB
	Dynamic bool      `json:"dynamic"`
	Dot     [6]string `json:"dot"`
}

func (s *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	all := theme.All()
	out := make([]ThemeBrief, 0, len(all))
	for _, t := range all {
		out = append(out, ThemeBrief{
			ID:      t.ID,
			Name:    t.Name,
			Light:   fmt.Sprintf("#%02X%02X%02X", t.Light[0], t.Light[1], t.Light[2]),
			Dynamic: t.Dynamic(),
			Dot:     t.Dot,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": out})
}

func (s *Server) handlePatterns(w http.ResponseWriter, r *http.Request) {
	groups := theme.Groups()
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		items := make([]map[string]any, 0, len(g.IDs))
		for _, id := range g.IDs {
			p, ok := theme.PatternByID(id)
			if !ok {
				continue
			}
			items = append(items, map[string]any{"id": p.ID, "name": p.Name, "art": p.Art})
		}
		out = append(out, map[string]any{"name": g.Name, "items": items})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// ── 控制 ──────────────────────────────────────────────────────────

// handlePattern 处理图案库单选：点阵屏换显示这张图案，背光不动。
// 再点一次已选中的（或传空 id）还原主题默认点阵。
func (s *Server) handlePattern(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ID != "" {
		if _, ok := theme.PatternByID(req.ID); !ok {
			http.Error(w, fmt.Sprintf("没有图案 %q", req.ID), http.StatusBadRequest)
			return
		}
	}
	// 先改配置再调钩子：钩子里重建场景时会读这个新值。
	s.d.Settings.Update(func(st *store.Settings) { st.Pattern = req.ID })
	if s.d.SetPattern != nil {
		if err := s.d.SetPattern(req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := s.d.Save(); err != nil {
		s.d.Logf("保存配置失败：%v", err)
	}
	s.Broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pattern": req.ID})
}

// ── 工具形状（伴侣） ──────────────────────────────────────────────

// handleAgents 列出内置工具形状（只读）。不认得的工具在伴侣场景里
// 统一走宝石兜底形状，六种状态动效自动套用，不需要用户自己画。
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}
	type brief struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Art     []string `json:"art"`
		Builtin bool     `json:"builtin"`
	}
	out := []brief{}
	// 固定顺序，页面稳定
	for _, id := range []string{"claude-code", "codex", "opencode"} {
		art := engine.BuiltinShapes()[id]
		out = append(out, brief{
			ID: id, Name: engine.BuiltinShapeNames[id],
			Art: artRows(art), Builtin: true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// artRows 把 [6]string 转成 JSON 友好的 []string。
func artRows(art [6]string) []string {
	out := make([]string, len(art))
	copy(out, art[:])
	return out
}

func (s *Server) handleScene(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Dynamic *bool  `json:"dynamic"`
		Theme   string `json:"theme"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		http.Error(w, "缺少 id", http.StatusBadRequest)
		return
	}
	if req.Theme != "" && req.Theme != s.curTheme() {
		if err := s.applyTheme(req.Theme, s.curDynamic()); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	if req.Dynamic != nil && *req.Dynamic != s.curDynamic() {
		if err := s.applyTheme(s.curTheme(), *req.Dynamic); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	if err := s.d.SwitchScene(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.SetSceneState(req.ID)
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Dynamic *bool  `json:"dynamic"`
	}
	if !decode(w, r, &req) {
		return
	}
	dyn := s.curDynamic()
	if req.Dynamic != nil {
		dyn = *req.Dynamic
	}
	if err := s.applyTheme(req.ID, dyn); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, s.state())
}

// applyTheme 换主题并落盘。切主题顺带把场景切到 theme —— 否则用户点了没反应。
func (s *Server) applyTheme(id string, dynamic bool) error {
	if _, ok := theme.ByID(id); !ok {
		return fmt.Errorf("没有主题 %q", id)
	}
	// 换成另一套主题 = 还原那套主题的完整画面：图案库单选只对当前主题生效。
	// 必须在 SwitchTheme 之前清 —— 钩子重建场景时读的就是 Settings.Pattern。
	// 切换失败把旧图案还回来，切换失败不该顺手丢设置。
	// 同主题重推（含动/静态切换）保留图案。
	var oldPattern string
	s.d.Settings.View(func(st *store.Settings) { oldPattern = st.Pattern })
	if id != s.curTheme() {
		s.d.Settings.Update(func(st *store.Settings) { st.Pattern = "" })
	}
	if err := s.d.SwitchTheme(id, dynamic); err != nil {
		if id != s.curTheme() {
			s.d.Settings.Update(func(st *store.Settings) { st.Pattern = oldPattern })
		}
		return err
	}
	s.mu.Lock()
	s.themeID = id
	s.dynamic = dynamic
	s.scene = "theme"
	s.mu.Unlock()

	s.d.Settings.Update(func(st *store.Settings) {
		st.Theme = id
		st.Dynamic = dynamic
		st.Mode = "theme"
	})
	if err := s.d.Save(); err != nil {
		s.d.Logf("保存配置失败：%v", err)
	}
	s.Broadcast()
	return nil
}

func (s *Server) handleBrightness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Dot  *float64 `json:"dot"`
		Keys *float64 `json:"keys"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Dot != nil || req.Keys != nil {
		s.d.Settings.Update(func(st *store.Settings) {
			if req.Dot != nil {
				st.Bright.Dot = clampF(*req.Dot, 0, 100)
			}
			if req.Keys != nil {
				st.Bright.Keys = clampF(*req.Keys, 0, 100)
			}
		})
	}
	if err := s.d.Save(); err != nil {
		s.d.Logf("保存配置失败：%v", err)
	}
	// 亮度变化要重建场景才生效。
	// 走 RebuildTheme（被动重建）：亮度滑杆是调参不是点主题,
	// 用户在伴侣场景里拖滑杆,场景不该被踢回主题。
	if s.d.RebuildTheme != nil {
		if err := s.d.RebuildTheme(); err != nil {
			s.d.Logf("按新亮度重建场景失败：%v", err)
		}
	} else if s.d.SwitchTheme != nil {
		if err := s.d.SwitchTheme(s.curTheme(), s.curDynamic()); err != nil {
			s.d.Logf("按新亮度重建场景失败：%v", err)
		}
	}
	writeJSON(w, http.StatusOK, s.state())
}

// kbEffect 返回当前生效的原厂键盘灯效；-1 表示走主题逐键配色。
func (s *Server) kbEffect() int {
	id := -1
	s.d.Settings.View(func(st *store.Settings) {
		if st.KBEffect != nil {
			id = *st.KBEffect
		}
	})
	return id
}

// handleKeyboard 读写原厂内置键盘灯效。
//
// 这是"键盘区域单独一套灯效"那条路：选了固件效果之后，
// 我们只推点阵，背光归键盘自己的芯片跑，一帧都不占 2.4G。
func (s *Server) handleKeyboard(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"effect":  s.kbEffect(),
			"effects": device.FirmwareEffects,
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 GET/POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID *int `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	id := -1
	if req.ID != nil {
		id = *req.ID
	}
	if id >= 0 {
		if _, ok := device.EffectByID(id); !ok {
			http.Error(w, fmt.Sprintf("没有灯效 %d", id), http.StatusBadRequest)
			return
		}
	}
	if s.d.SetKBEffect == nil {
		http.Error(w, "这个服务没接设备控制", http.StatusNotImplemented)
		return
	}
	if err := s.d.SetKBEffect(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// 伴侣等场景下换灯效不会触发场景广播（enter=false 的内部重建），
	// 页面全靠 SSE 更新 —— 这里必须自己推一次，否则页面停在旧选择。
	s.Broadcast()
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handlePomodoro(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.d.Pomodoro.Status(time.Now()))
		return
	}
	var req struct {
		Action string `json:"action"`
		Preset *struct {
			Name      string `json:"name"`
			Focus     int    `json:"focus"`
			Break     int    `json:"break"`
			LongBreak int    `json:"longBreak"`
			Every     int    `json:"every"`
		} `json:"preset"`
	}
	if !decode(w, r, &req) {
		return
	}
	now := time.Now()
	if req.Preset != nil {
		s.d.Pomodoro.SetPreset(engine.Preset{
			Name: req.Preset.Name, Focus: req.Preset.Focus, Break: req.Preset.Break,
			LongBreak: req.Preset.LongBreak, Every: req.Preset.Every,
		})
	}
	switch req.Action {
	case "start":
		s.d.Pomodoro.Start(now)
	case "pause", "resume":
		s.d.Pomodoro.Pause(now)
	case "skip":
		s.d.Pomodoro.Skip(now)
	case "reset":
		s.d.Pomodoro.Reset()
	default:
		http.Error(w, "未知 action（start/pause/skip/reset）", http.StatusBadRequest)
		return
	}
	if req.Action != "reset" {
		// reset 是"归零",不是"我要看番茄钟" —— 不该把场景切过去。
		// 切场景失败要如实报错，静默吞掉就是"点了没反应"。
		if err := s.d.SwitchScene("pomodoro"); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		s.SetSceneState("pomodoro")
	}
	writeJSON(w, http.StatusOK, s.d.Pomodoro.Status(now))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// 整个结构在锁内序列化：写方（同 handler 的 POST）并发改字段。
		s.d.Settings.View(func(st *store.Settings) { writeJSON(w, http.StatusOK, st) })
		return
	}
	var req struct {
		Calib       *store.Calibration `json:"calibration"`
		Idle        *store.Idle        `json:"idle"`
		TypingGuard *bool              `json:"typing_guard"`
		RateTier    *int               `json:"rate_tier"`
		IdleAgent   *string            `json:"idle_agent"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Calib != nil {
		s.d.Settings.Update(func(st *store.Settings) { st.Calib = *req.Calib })
		// 热更新：直接改运行中的校准,场景按新参数重建。
		// 没有这条的话滑杆"已保存"但键盘纹丝不动 —— 要重启才生效。
		if s.d.SetCalib != nil {
			if err := s.d.SetCalib(*req.Calib); err != nil {
				s.d.Logf("校准热更新失败：%v", err)
			}
		}
	}
	if req.Idle != nil {
		s.d.Settings.Update(func(st *store.Settings) { st.IdlePolicy = *req.Idle })
		// 热更新省电阈值 —— 同上,不能让"熄屏策略"页变成假保存。
		if s.d.SetIdlePolicy != nil {
			s.d.SetIdlePolicy(*req.Idle)
		}
	}
	if req.TypingGuard != nil {
		s.d.Settings.Update(func(st *store.Settings) { st.TypingGuard = *req.TypingGuard })
		if s.d.SetTypingGuard != nil {
			s.d.SetTypingGuard(*req.TypingGuard)
		}
	}
	if req.RateTier != nil {
		t := *req.RateTier
		if t < 0 || t > 2 {
			http.Error(w, "rate_tier 只能是 0（默认）/ 1（流畅）/ 2（极速）", http.StatusBadRequest)
			return
		}
		s.d.Settings.Update(func(st *store.Settings) { st.RateTier = t })
		if s.d.SetRateTier != nil {
			s.d.SetRateTier(t)
		}
	}
	if req.IdleAgent != nil {
		// 空串 / "auto" = 跟随最近会话。注意不能先归一化：
		// NormalizeName("") 会默认成 "claude-code"，把"清空"变成"选定"。
		id := strings.TrimSpace(*req.IdleAgent)
		if id == "auto" {
			id = ""
		}
		if id != "" && id != "generic" {
			id = agent.NormalizeName(id)
			// "generic" 是页面上的"通用宝石"瓦片：不是内置形状,
			// 但伴侣 resolve 会把任何不认得的 id 落成它,所以是合法值。
			if _, ok := engine.BuiltinShapes()[id]; !ok {
				http.Error(w, fmt.Sprintf("没有形状 %q，空闲展示只能选已收录的工具", id), http.StatusBadRequest)
				return
			}
		}
		s.d.Settings.Update(func(st *store.Settings) { st.IdleAgent = id })
		if s.d.SetIdleAgent != nil {
			s.d.SetIdleAgent(id)
		}
	}
	if err := s.d.Save(); err != nil {
		s.d.Logf("保存配置失败：%v", err)
	}
	writeJSON(w, http.StatusOK, s.d.Settings)
}

// handlePing 是后端健康检查端点：返回 yogod 签名。
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"yogod": true})
}

// ── 事件 ──────────────────────────────────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.serveSSE(w, r)
		return
	}
	var raw map[string]any
	if !decode(w, r, &raw) {
		return
	}
	ev, err := agent.ParseEvent(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "why": err.Error()})
		return
	}
	out := s.d.Agent.Apply(ev)
	writeJSON(w, http.StatusOK, out)
	s.Broadcast()
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.d.Agent.Snapshot())
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
		All       bool   `json:"all"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.SessionID == "" && !req.All {
		http.Error(w, "需要 sessionId 或 all", http.StatusBadRequest)
		return
	}
	n := s.d.Agent.Clear(req.SessionID, req.All)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": n})
	s.Broadcast()
}

// ── 页面 ──────────────────────────────────────────────────────────

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	if s.d.Console == nil {
		http.Error(w, "控制台页面没有打包进来", http.StatusNotFound)
		return
	}
	raw, err := fs.ReadFile(s.d.Console, "console.html")
	if err != nil {
		http.Error(w, "控制台页面缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面绝不能缓存：这是长期挂着的窗口，缓存住旧版本排查起来极其费劲
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write(raw)
}

// ── 工具 ──────────────────────────────────────────────────────────

func (s *Server) curTheme() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.themeID
}

func (s *Server) curDynamic() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dynamic
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		http.Error(w, "缺少请求体", http.StatusBadRequest)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		http.Error(w, "JSON 解析失败："+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── SSE ───────────────────────────────────────────────────────────

type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) add() chan []byte {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) remove(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *hub) send(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // 订阅者跟不上就丢这一条，绝不阻塞出帧
		}
	}
}

// Broadcast 推送一次全量状态。
func (s *Server) Broadcast() {
	st := s.state()
	if b, err := json.Marshal(st); err == nil {
		s.hub.send(append([]byte("event: state\ndata: "), append(b, '\n', '\n')...))
	}
}

// currentFrame 读引擎当前帧并 hex 编码；ok=false 表示引擎还没出过任何帧。
func (s *Server) currentFrame() (ms, ks string, ok bool) {
	m, k, hm, hk := s.d.Engine.LastFrame()
	if hm {
		ms = hex.EncodeToString(m[:])
	}
	if hk {
		ks = hex.EncodeToString(k[:])
	}
	return ms, ks, hm || hk
}

// RunPreview 定期把当前帧推给订阅者（控制台因此不需要懂任何渲染）。
//
// 只在帧真的变了才推，静态画面几乎没有流量。
func (s *Server) RunPreview(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 100 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()

	var lastM, lastK string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ms, ks, ok := s.currentFrame()
			if !ok || (ms == lastM && ks == lastK) {
				continue
			}
			lastM, lastK = ms, ks
			payload := fmt.Sprintf(`{"m":"%s","k":"%s"}`, ms, ks)
			s.hub.send([]byte("event: frame\ndata: " + payload + "\n\n"))
		}
	}
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式响应", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ch := s.hub.add()
	defer s.hub.remove(ch)

	// 先把当前状态全给一遍，页面不用再单独拉
	first, _ := json.Marshal(s.state())
	fmt.Fprintf(w, "event: state\ndata: %s\n\n", first)
	// 帧快照也要给：RunPreview 只在画面变化时推帧，静态场景下
	// 新订阅者等不到下一条 frame 事件，预览会一直黑着。
	if ms, ks, ok := s.currentFrame(); ok {
		fmt.Fprintf(w, "event: frame\ndata: {\"m\":\"%s\",\"k\":\"%s\"}\n\n", ms, ks)
	}
	flusher.Flush()

	ka := time.NewTicker(15 * time.Second)
	defer ka.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ka.C:
			if _, err := fmt.Fprint(w, ": ka\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ListenAndServe 在自己绑地址后起服务，直到出错。
//
// 想在启动阶段就发现"端口被占"的调用方（比如托盘程序）应当自己
// net.Listen 再调 Serve —— 否则错误要到后台 goroutine 里才浮出来。
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve 在一个已经绑好的监听器上提供服务，直到该监听器出错。
func (s *Server) Serve(ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		// 不要 WriteTimeout：SSE 是长连接
	}
	return srv.Serve(ln)
}
