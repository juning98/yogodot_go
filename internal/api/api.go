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
	"errors"
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
	// Control 与托盘、自动场景切换共享命令锁。
	Control  sync.Locker
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
	// SetIdleAgent 由 main 提供：空闲时显示的工具形状（"" / "auto" = 跟随会话）。
	SetIdleAgent func(id string)
	// ApplyOutput 只发布全局参数，所有场景共用。
	ApplyOutput func()
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

	commands    sync.Mutex
	broadcastMu sync.Mutex
}

// New 创建一个 API 服务。
func New(d Deps) *Server {
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	s := &Server{
		d:   d,
		hub: newHub(),
		mux: http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler 返回 http.Handler。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			lock := s.d.Control
			if lock == nil {
				lock = &s.commands
			}
			lock.Lock()
			defer lock.Unlock()
		}
		s.mux.ServeHTTP(w, r)
	})
}

// SetSceneState 发布场景选择，状态读取统一来自 Settings 快照。
func (s *Server) SetSceneState(id string) {
	s.d.Settings.Update(func(c *store.Settings) { c.Mode = id })
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
	Output    engine.OutputStatus `json:"output"`
	Settings  store.Config        `json:"settings"`
	Scene     string              `json:"scene"`
	Theme     string              `json:"theme"`
	ThemeName string              `json:"themeName"`
	Dynamic   bool                `json:"dynamic"`
	Device    device.Status       `json:"device"`
	Pomodoro  engine.PomStatus    `json:"pomodoro"`
	Sessions  []SessionBrief      `json:"sessions"`
	Top       *SessionBrief       `json:"top,omitempty"`
	SceneID   string              `json:"sceneId"`
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
	cfg := s.d.Settings.Snapshot()
	scene, themeID, dyn := cfg.Mode, cfg.Theme, cfg.Dynamic

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
	st.Output = s.d.Engine.Status()
	st.Settings = cfg
	st.Pomodoro = s.d.Pomodoro.Status(time.Now())
	st.KBEffect = -1
	if cfg.KBEffect != nil {
		st.KBEffect = *cfg.KBEffect
	}
	st.Pattern = cfg.Pattern
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
	before := s.d.Settings.Snapshot().Pattern
	s.d.Settings.Update(func(c *store.Settings) { c.Pattern = req.ID })
	if err := s.d.Save(); err != nil {
		s.d.Settings.Update(func(c *store.Settings) { c.Pattern = before })
		http.Error(w, err.Error(), 500)
		return
	}
	if s.d.SetPattern != nil {
		if err := s.d.SetPattern(req.ID); err != nil {
			s.d.Settings.Update(func(c *store.Settings) { c.Pattern = before })
			err = errors.Join(err, s.d.Save())
			http.Error(w, err.Error(), 500)
			return
		}
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
	switch req.ID {
	case "theme", "focus", "companion", "pomodoro":
	default:
		http.Error(w, "未知场景", 400)
		return
	}
	id, dyn := s.curTheme(), s.curDynamic()
	if req.Theme != "" {
		id = req.Theme
	}
	if req.Dynamic != nil {
		dyn = *req.Dynamic
	}
	if id != s.curTheme() || dyn != s.curDynamic() {
		if err := s.applyTheme(id, dyn); err != nil {
			http.Error(w, err.Error(), 502)
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
	before := s.d.Settings.Snapshot()
	s.d.Settings.Update(func(c *store.Settings) {
		if c.Theme != id {
			c.Pattern = ""
		}
		c.Theme = id
		c.Dynamic = dynamic
		c.Mode = "theme"
	})
	if err := s.d.Save(); err != nil {
		s.d.Settings.Update(func(c *store.Settings) {
			c.Theme = before.Theme
			c.Dynamic = before.Dynamic
			c.Pattern = before.Pattern
			c.Mode = before.Mode
		})
		return err
	}
	if err := s.d.SwitchTheme(id, dynamic); err != nil {
		s.d.Settings.Update(func(c *store.Settings) {
			c.Theme = before.Theme
			c.Dynamic = before.Dynamic
			c.Pattern = before.Pattern
			c.Mode = before.Mode
		})
		return errors.Join(err, s.d.Save())
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
	before := s.d.Settings.Snapshot().Bright
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
		s.d.Settings.Update(func(c *store.Settings) { c.Bright = before })
		http.Error(w, "保存失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.d.ApplyOutput != nil {
		s.d.ApplyOutput()
	}
	s.Broadcast()
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
	switch req.Action {
	case "start", "pause", "resume", "skip", "reset":
	default:
		http.Error(w, "未知 action", 400)
		return
	}
	if p := req.Preset; p != nil && (p.Focus <= 0 || p.Break <= 0 || p.LongBreak <= 0 || p.Every <= 0) {
		http.Error(w, "番茄钟时长和循环次数必须大于零", 400)
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
		writeJSON(w, http.StatusOK, s.d.Settings.Snapshot())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 GET/POST", 405)
		return
	}
	var req struct {
		Calib       *store.Calibration `json:"calibration"`
		Idle        *store.Idle        `json:"idle"`
		TypingGuard *bool              `json:"typing_guard"`
		RateTier    *int               `json:"rate_tier"`
		IdleAgent   *string            `json:"idle_agent"`
		Outputs     *struct {
			Dot  *bool `json:"dot"`
			Keys *bool `json:"keys"`
		} `json:"outputs"`
	}
	if !decode(w, r, &req) {
		return
	}
	// 完整验证后一次发布，避免请求的后半段非法却已改动前半段。
	if req.RateTier != nil && (*req.RateTier < 0 || *req.RateTier > 2) {
		http.Error(w, "rate_tier 必须为 0..2", 400)
		return
	}
	if c := req.Calib; c != nil {
		if c.Corr < 0 || c.Corr > 1 || c.Sat < 0 || c.Sat > 3 || c.EQL < 0 || c.EQL > 255 {
			http.Error(w, "校准参数超出范围", 400)
			return
		}
		for _, g := range c.Gain {
			if g < 0 || g > 3 {
				http.Error(w, "增益必须为 0..3", 400)
				return
			}
		}
	}
	if i := req.Idle; i != nil {
		if i.BlankSeconds < 0 || i.KeysOffPercent < 0 || i.KeysOffPercent > 100 || i.AllOffPercent < 0 || i.AllOffPercent > 100 || (i.KeysOffPercent > 0 && i.AllOffPercent > i.KeysOffPercent) {
			http.Error(w, "熄屏阈值无效", 400)
			return
		}
	}
	if req.IdleAgent != nil {
		id := strings.TrimSpace(*req.IdleAgent)
		if id == "auto" {
			id = ""
		}
		if id != "" && id != "generic" {
			id = agent.NormalizeName(id)
			if _, ok := engine.BuiltinShapes()[id]; !ok {
				http.Error(w, "没有形状 "+id, 400)
				return
			}
		}
		*req.IdleAgent = id
	}
	before := s.d.Settings.Snapshot()
	s.d.Settings.Update(func(c *store.Settings) {
		if req.Calib != nil {
			c.Calib = *req.Calib
		}
		if req.Idle != nil {
			c.IdlePolicy = *req.Idle
		}
		if req.TypingGuard != nil {
			c.TypingGuard = *req.TypingGuard
		}
		if req.RateTier != nil {
			c.RateTier = *req.RateTier
		}
		if req.IdleAgent != nil {
			c.IdleAgent = *req.IdleAgent
		}
		if req.Outputs != nil {
			if req.Outputs.Dot != nil {
				c.Outputs.Dot = *req.Outputs.Dot
			}
			if req.Outputs.Keys != nil {
				c.Outputs.Keys = *req.Outputs.Keys
			}
		}
	})
	if err := s.d.Save(); err != nil {
		s.d.Settings.Update(func(c *store.Settings) { c.Config = before })
		http.Error(w, "保存失败："+err.Error(), 500)
		return
	}
	if s.d.ApplyOutput != nil {
		s.d.ApplyOutput()
	}
	if req.Idle != nil && s.d.SetIdlePolicy != nil {
		s.d.SetIdlePolicy(*req.Idle)
	}
	if req.TypingGuard != nil && s.d.SetTypingGuard != nil {
		s.d.SetTypingGuard(*req.TypingGuard)
	}
	if req.IdleAgent != nil && s.d.SetIdleAgent != nil {
		s.d.SetIdleAgent(*req.IdleAgent)
	}
	s.Broadcast()
	writeJSON(w, http.StatusOK, s.d.Settings.Snapshot())
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

func (s *Server) curTheme() string { return s.d.Settings.Snapshot().Theme }
func (s *Server) curDynamic() bool { return s.d.Settings.Snapshot().Dynamic }

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

type subscription struct {
	ready        chan struct{}
	state, frame []byte
}
type hub struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

func newHub() *hub { return &hub{subs: map[*subscription]struct{}{}} }
func (h *hub) add() *subscription {
	sub := &subscription{ready: make(chan struct{}, 1)}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}
func (h *hub) remove(sub *subscription) { h.mu.Lock(); delete(h.subs, sub); h.mu.Unlock() }
func (h *hub) send(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := strings.HasPrefix(string(msg), "event: state")
	for sub := range h.subs {
		if state {
			sub.state = msg
		} else {
			sub.frame = msg
		}
		select {
		case sub.ready <- struct{}{}:
		default:
		}
	}
}
func (h *hub) take(sub *subscription) (state, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, frame = sub.state, sub.frame
	sub.state, sub.frame = nil, nil
	return
}

// Broadcast 推送一次全量状态。
func (s *Server) Broadcast() {
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
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
	var lastOutput engine.OutputStatus
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			status := s.d.Engine.Status()
			if status != lastOutput {
				lastOutput = status
				s.Broadcast()
			}
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
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))

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
		case <-ch.ready:
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			state, frame := s.hub.take(ch)
			// 分别保留最新状态与最新画面，慢客户端不会被历史帧淹没。
			for _, msg := range [][]byte{state, frame} {
				if len(msg) > 0 {
					if _, err := w.Write(msg); err != nil {
						return
					}
				}
			}
			flusher.Flush()
		case <-ka.C:
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// 不要 WriteTimeout：SSE 是长连接
	}
	return srv.Serve(ln)
}
