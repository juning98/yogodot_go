package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/api"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/store"
)

/* ══════════ 测试脚手架 ══════════

   API 层是唯一一个"什么都碰"的模块：HTTP、JSON 契约、SSE、兼容层。
   它也是最容易悄悄坏掉的地方 —— 字段名拼错、状态码写错，
   编译期全看不出来，只有打开页面才发现。

   所以这里不装真键盘：device.Controller 的零值就是"没连接"，
   引擎用 NewWithSink 接一个假设备。 */

// nullSink 接受一切写入，什么也不做。
type nullSink struct {
	mu    sync.Mutex
	keys  int
	matrx int
}

func (s *nullSink) SendMatrix(context.Context, frame.Matrix) (device.Result, error) {
	s.mu.Lock()
	s.matrx++
	s.mu.Unlock()
	return device.Result{Sent: true}, nil
}

func (s *nullSink) SendKeys(context.Context, frame.Keys) (device.Result, error) {
	s.mu.Lock()
	s.keys++
	s.mu.Unlock()
	return device.Result{Sent: true}, nil
}

func (s *nullSink) FlushPending(context.Context) {}

// gridScene 每帧把时钟刻进帧里，保证内容一直在变。
type gridScene struct{}

func (gridScene) Name() string                                    { return "grid" }
func (gridScene) Enter(context.Context, *device.Controller) error { return nil }
func (gridScene) Exit(context.Context, *device.Controller)        {}

func (gridScene) Frame(t time.Duration) engine.Render {
	var m frame.Matrix
	var k frame.Keys
	v := byte(t / time.Millisecond)
	for i := range m {
		m[i] = v + byte(i)
	}
	for i := range k {
		k[i] = v ^ byte(i)
	}
	return engine.Render{Matrix: &m, Keys: &k}
}

type harness struct {
	t   *testing.T
	srv *api.Server
	set *store.Settings
	eng *engine.Engine
	ag  *agent.Store
	pom *engine.Pomodoro
	dir string

	mu     sync.Mutex
	scenes []string
	themes []string
	saved  int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	set, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	h := &harness{
		t:   t,
		set: set,
		dir: dir,
		eng: engine.NewWithSink(&nullSink{}, engine.Options{Tick: 5 * time.Millisecond}),
		ag:  agent.NewStore(),
		pom: engine.NewPomodoro(engine.DefaultPresets[0]),
	}
	h.srv = api.New(api.Deps{
		Engine:   h.eng,
		Device:   &device.Controller{}, // 零值 = 没连键盘
		Settings: set,
		Agent:    h.ag,
		Pomodoro: h.pom,
		Console:  api.ConsoleFS(),

		SwitchScene: func(id string) error {
			h.mu.Lock()
			h.scenes = append(h.scenes, id)
			h.mu.Unlock()
			return nil
		},
		SwitchTheme: func(id string, dyn bool) error {
			h.mu.Lock()
			h.themes = append(h.themes, fmt.Sprintf("%s/%v", id, dyn))
			h.mu.Unlock()
			return nil
		},
		Save: func() error {
			h.mu.Lock()
			h.saved++
			h.mu.Unlock()
			return set.Save()
		},
	})
	return h
}

func (h *harness) calls() (scenes, themes []string, saves int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.scenes...), append([]string(nil), h.themes...), h.saved
}

// do 发一个请求，body 为 nil 表示不带请求体。
func (h *harness) do(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// state 拉一次 /api/state 并解成 map，方便断言真实 JSON 契约。
func (h *harness) state() map[string]any {
	h.t.Helper()
	rec := h.do(http.MethodGet, "/api/state", nil)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("GET /api/state = %d，body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		h.t.Fatalf("state 不是合法 JSON: %v", err)
	}
	return m
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("响应不是合法 JSON（%d）：%v\n%s", rec.Code, err, rec.Body.String())
	}
}

/* ══════════ 状态与列表 ══════════ */

func TestStateShape(t *testing.T) {
	h := newHarness(t)
	m := h.state()

	if m["theme"] != "pikachu" {
		t.Errorf("theme = %v，默认应为 pikachu", m["theme"])
	}
	if m["themeName"] == "" {
		t.Error("themeName 为空：页面靠它显示中文名")
	}
	if m["scene"] != m["sceneId"] {
		t.Errorf("scene(%v) 与 sceneId(%v) 不一致", m["scene"], m["sceneId"])
	}
	for _, k := range []string{"scene", "theme", "themeName", "dynamic", "device", "pomodoro", "sessions"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少字段 %q", k)
		}
	}
	// sessions 必须是 []，不能是 null —— 页面对 null 和空数组处理不一样
	if _, ok := m["sessions"].([]any); !ok {
		t.Errorf("sessions 应为数组，实际 %T", m["sessions"])
	}
}

// TestDeviceJSONIsLowerCamel 是这次发现的 bug 的回归测试。
//
// device.Status 原来没有 json tag，序列化成 PascalCase，
// 而 console.html 读的是 s.device.connected —— 于是页面永远显示"键盘没应答"。
// 这个测试盯的是线上契约，不是 Go 类型。
func TestDeviceJSONIsLowerCamel(t *testing.T) {
	h := newHarness(t)
	m := h.state()

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device 不是对象：%T", m["device"])
	}
	for _, k := range []string{"connected", "awake", "product", "wired", "power", "error", "dropped"} {
		if _, ok := dev[k]; !ok {
			t.Errorf("device 缺字段 %q（现有：%v）", k, keysOf(dev))
		}
	}
	// PascalCase 一个都不许出现，出现了就说明又有人加了没 tag 的字段
	for k := range dev {
		if k != strings.ToLower(k[:1])+k[1:] {
			t.Errorf("device 字段 %q 不是小驼峰", k)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestConsoleFieldContract 把页面和 JSON 绑在一起：
// 页面读了 s.device.X，服务端就必须发 X。
func TestConsoleFieldContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("console.html"))
	if err != nil {
		t.Fatalf("读 console.html: %v", err)
	}
	page := string(raw)
	for _, expr := range []string{
		"s.device.connected", "s.device.awake", "s.device.product",
		"s.device.wired", "s.device.power", "s.pattern",
	} {
		if !strings.Contains(page, expr) {
			t.Errorf("页面不再使用 %s；若改了小驼峰约定，请同步本测试", expr)
		}
	}
	// 电量字段写成大写就是 bug（曾经真的这么写）
	for _, bad := range []string{"p.Percent", "p.Charging"} {
		if strings.Contains(page, bad) {
			t.Errorf("页面用了 %s，服务端发的是小驼峰", bad)
		}
	}
}

func TestThemesList(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/themes", nil)

	var out struct {
		Themes []api.ThemeBrief `json:"themes"`
	}
	decodeInto(t, rec, &out)

	if len(out.Themes) != 24 {
		t.Fatalf("主题数 = %d，期望 24", len(out.Themes))
	}
	seen := map[string]bool{}
	for _, th := range out.Themes {
		if th.ID == "" || th.Name == "" {
			t.Errorf("主题字段不全：%+v", th)
		}
		if seen[th.ID] {
			t.Errorf("主题 id 重复：%s", th.ID)
		}
		seen[th.ID] = true
		if !strings.HasPrefix(th.Light, "#") || len(th.Light) != 7 {
			t.Errorf("%s 的 light = %q，应为 #RRGGBB", th.ID, th.Light)
		}
		if strings.ToUpper(th.Light) != th.Light {
			t.Errorf("%s 的 light 不是大写十六进制：%q", th.ID, th.Light)
		}
		for r, row := range th.Dot {
			if len(row) != frame.DotCols {
				t.Errorf("%s 点阵第 %d 行长度 %d，应为 %d", th.ID, r, len(row), frame.DotCols)
			}
		}
	}
	if !seen["pikachu"] {
		t.Error("主题列表里没有 pikachu")
	}
}

func TestPatternsList(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/patterns", nil)

	var out struct {
		Groups []struct {
			Name  string `json:"name"`
			Items []struct {
				ID   string    `json:"id"`
				Name string    `json:"name"`
				Art  [6]string `json:"art"`
			} `json:"items"`
		} `json:"groups"`
	}
	decodeInto(t, rec, &out)

	if len(out.Groups) == 0 {
		t.Fatal("没有任何图案分组")
	}
	total := 0
	for _, g := range out.Groups {
		if g.Name == "" {
			t.Error("分组没有名字")
		}
		if len(g.Items) == 0 {
			t.Errorf("分组 %q 是空的", g.Name)
		}
		for _, it := range g.Items {
			total++
			if it.ID == "" {
				t.Errorf("分组 %q 里有图案没有 id", g.Name)
			}
			for r, row := range it.Art {
				if len(row) != frame.DotCols {
					t.Errorf("图案 %s 第 %d 行长度 %d", it.ID, r, len(row))
				}
			}
		}
	}
	if total != 48 {
		t.Errorf("图案总数 = %d，期望 48", total)
	}
}

/* ══════════ 控制 ══════════ */

func TestThemeSwitchPersists(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/theme", map[string]any{"id": "panda", "dynamic": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/theme = %d：%s", rec.Code, rec.Body.String())
	}

	m := h.state()
	if m["theme"] != "panda" {
		t.Errorf("切完 theme = %v", m["theme"])
	}
	if m["dynamic"] != false {
		t.Errorf("dynamic = %v，应为 false", m["dynamic"])
	}
	// 切主题必须顺手把场景切到 theme，否则用户点了没反应
	if m["scene"] != "theme" {
		t.Errorf("scene = %v，切主题后应为 theme", m["scene"])
	}

	_, themes, saves := h.calls()
	if len(themes) != 1 || themes[0] != "panda/false" {
		t.Errorf("SwitchTheme 调用 = %v", themes)
	}
	if saves == 0 {
		t.Error("没有落盘")
	}

	// 真的写到磁盘了吗
	raw, err := os.ReadFile(filepath.Join(h.dir, store.SettingsFile))
	if err != nil {
		t.Fatalf("读 settings.json: %v", err)
	}
	if !strings.Contains(string(raw), `"theme": "panda"`) {
		t.Errorf("settings.json 里没写进 panda：\n%s", raw)
	}
}

func TestThemeSwitchRejectsUnknown(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/theme", map[string]any{"id": "没有这个主题"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("未知主题应 502，实际 %d：%s", rec.Code, rec.Body.String())
	}
	// 失败不能改动任何状态
	if m := h.state(); m["theme"] != "pikachu" {
		t.Errorf("失败后 theme 被改成了 %v", m["theme"])
	}
	_, themes, _ := h.calls()
	if len(themes) != 0 {
		t.Errorf("失败后仍然调了 SwitchTheme：%v", themes)
	}
}

func TestMethodGuards(t *testing.T) {
	cases := []struct{ path string }{
		{"/api/theme"}, {"/api/scene"}, {"/api/brightness"},
	}
	for _, c := range cases {
		h := newHarness(t)
		rec := h.do(http.MethodGet, c.path, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d，应为 405", c.path, rec.Code)
		}
	}
}

func TestSceneSwitch(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/scene", map[string]any{"id": "pomodoro"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/scene = %d：%s", rec.Code, rec.Body.String())
	}
	if m := h.state(); m["scene"] != "pomodoro" {
		t.Errorf("scene = %v", m["scene"])
	}
	scenes, _, _ := h.calls()
	if len(scenes) != 1 || scenes[0] != "pomodoro" {
		t.Errorf("SwitchScene 调用 = %v", scenes)
	}
}

func TestSceneRequiresID(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodPost, "/api/scene", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 id 应 400，实际 %d", rec.Code)
	}
}

func TestBadJSONIsRejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/theme", "{不是 JSON")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "JSON") {
		t.Errorf("错误信息没说清是 JSON 的问题：%s", rec.Body.String())
	}
}

func TestBrightnessClamped(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/brightness", map[string]any{"dot": 500, "keys": -20})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/brightness = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.Bright.Dot != 100 {
		t.Errorf("dot 应被夹到 100，实际 %v", h.set.Bright.Dot)
	}
	if h.set.Bright.Keys != 0 {
		t.Errorf("keys 应被夹到 0，实际 %v", h.set.Bright.Keys)
	}
	// 亮度改完要重建场景才生效
	_, themes, _ := h.calls()
	if len(themes) != 1 {
		t.Errorf("亮度变更后应重建场景一次，实际 %v", themes)
	}
}

/* ══════════ 会话与事件 ══════════ */

func TestEventIngestAndClear(t *testing.T) {
	h := newHarness(t)
	ev := map[string]any{
		"event":     "session_started",
		"sessionId": "s-1",
		"agent":     "claude",
		"cwd":       `D:\proj\demo`,
	}
	rec := h.do(http.MethodPost, "/api/events", ev)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/events = %d：%s", rec.Code, rec.Body.String())
	}
	var out agent.Outcome
	decodeInto(t, rec, &out)
	if !out.OK {
		t.Errorf("事件没被接收：%+v", out)
	}

	// 出现在 /api/sessions
	rec = h.do(http.MethodGet, "/api/sessions", nil)
	var snap agent.Snapshot
	decodeInto(t, rec, &snap)
	if len(snap.Sessions) != 1 {
		t.Fatalf("会话数 = %d，期望 1", len(snap.Sessions))
	}
	if snap.Sessions[0].SessionID != "s-1" {
		t.Errorf("sessionId = %q", snap.Sessions[0].SessionID)
	}

	// 也出现在 /api/state
	m := h.state()
	if n := len(m["sessions"].([]any)); n != 1 {
		t.Errorf("state.sessions = %d，期望 1", n)
	}

	// 清空
	if rec := h.do(http.MethodPost, "/api/sessions", map[string]any{"all": true}); rec.Code != http.StatusOK {
		t.Fatalf("清空会话 = %d", rec.Code)
	}
	rec = h.do(http.MethodGet, "/api/sessions", nil)
	decodeInto(t, rec, &snap)
	if len(snap.Sessions) != 0 {
		t.Errorf("清空后还剩 %d 个会话", len(snap.Sessions))
	}
}

func TestEventValidation(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"未知事件", map[string]any{"event": "hi", "sessionId": "s"}},
		{"没有 sessionId", map[string]any{"event": "session_started"}},
		{"schema 不支持", map[string]any{"event": "session_started", "sessionId": "s", "schema": "v9"}},
	}
	for _, c := range cases {
		rec := h.do(http.MethodPost, "/api/events", c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s：状态码 %d，应为 400（%s）", c.name, rec.Code, rec.Body.String())
		}
	}
}

func TestSessionsClearNeedsTarget(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodPost, "/api/sessions", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("既没 sessionId 也没 all，应 400，实际 %d", rec.Code)
	}
}

func TestPomodoroActions(t *testing.T) {
	h := newHarness(t)

	rec := h.do(http.MethodPost, "/api/pomodoro", map[string]any{"action": "start"})
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d：%s", rec.Code, rec.Body.String())
	}
	var st engine.PomStatus
	decodeInto(t, rec, &st)
	if st.Phase != engine.PhaseFocus {
		t.Errorf("start 后 phase = %q，应为 focus", st.Phase)
	}
	if st.Remaining <= 0 || st.Total <= 0 {
		t.Errorf("start 后剩余/总时长异常：%v/%v", st.Remaining, st.Total)
	}

	// 番茄钟会把场景切过去
	if m := h.state(); m["scene"] != "pomodoro" {
		t.Errorf("scene = %v，应为 pomodoro", m["scene"])
	}

	if rec := h.do(http.MethodPost, "/api/pomodoro", map[string]any{"action": "乱来"}); rec.Code != http.StatusBadRequest {
		t.Errorf("未知 action 应 400，实际 %d", rec.Code)
	}

	// GET 直接读状态
	if rec := h.do(http.MethodGet, "/api/pomodoro", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /api/pomodoro = %d", rec.Code)
	}
}

/* ══════════ 设置 ══════════ */

func TestSettingsRoundTrip(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/settings", map[string]any{
		"idle": map[string]any{"blank_seconds": 600, "keys_off_percent": 15, "all_off_percent": 5},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.IdlePolicy.BlankSeconds != 600 {
		t.Errorf("BlankSeconds = %d", h.set.IdlePolicy.BlankSeconds)
	}

	// 刷新后还在（走磁盘，不是内存）
	again, err := store.Open(h.dir)
	if err != nil {
		t.Fatalf("重读配置: %v", err)
	}
	if again.IdlePolicy.BlankSeconds != 600 {
		t.Errorf("落盘后重读 = %d，应为 600", again.IdlePolicy.BlankSeconds)
	}
}

// 打字保护开关：POST 既要落盘，也要触发实时生效钩子。
// 只落盘不触发钩子的话，运行中的引擎要到重启才按新配置走。
func TestSettingsTypingGuard(t *testing.T) {
	h := newHarness(t)
	var got *bool
	h.srv = api.New(api.Deps{
		Engine: h.eng, Device: &device.Controller{}, Settings: h.set,
		Agent: h.ag, Pomodoro: h.pom, Console: api.ConsoleFS(),
		Save:           func() error { return h.set.Save() },
		SetTypingGuard: func(on bool) { got = &on },
	})
	// 默认关，所以测「打开」这条有意义的跃迁
	rec := h.do(http.MethodPost, "/api/settings", map[string]any{"typing_guard": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings = %d：%s", rec.Code, rec.Body.String())
	}
	if !h.set.TypingGuard {
		t.Error("TypingGuard 应为 true")
	}
	if got == nil || *got != true {
		t.Error("SetTypingGuard 钩子没被调用或值不对")
	}
	again, err := store.Open(h.dir)
	if err != nil {
		t.Fatalf("重读配置: %v", err)
	}
	if !again.TypingGuard {
		t.Error("落盘后重读 TypingGuard 应为 true")
	}
}

// 推帧档位：POST 生效 + 落盘 + 越界拒绝。
// 图案库单选：POST 生效 + 状态暴露 + 非法 id 拒绝 + 空 id 还原。
func TestPatternSelect(t *testing.T) {
	h := newHarness(t)
	var got string
	var called bool
	h.srv = api.New(api.Deps{
		Engine: h.eng, Device: &device.Controller{}, Settings: h.set,
		Agent: h.ag, Pomodoro: h.pom, Console: api.ConsoleFS(),
		Save:       func() error { return h.set.Save() },
		SetPattern: func(id string) error { got, called = id, true; return nil },
	})

	rec := h.do(http.MethodPost, "/api/pattern", map[string]any{"id": "ghost"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/pattern = %d：%s", rec.Code, rec.Body.String())
	}
	if !called || got != "ghost" {
		t.Errorf("SetPattern 钩子应收到 ghost，实际 %q", got)
	}
	if h.set.Pattern != "ghost" {
		t.Errorf("Settings.Pattern 应为 ghost，实际 %q", h.set.Pattern)
	}

	st := h.do(http.MethodGet, "/api/state", nil)
	var sm map[string]any
	decodeInto(t, st, &sm)
	if sm["pattern"] != "ghost" {
		t.Errorf("state.pattern 应为 ghost，实际 %v", sm["pattern"])
	}

	bad := h.do(http.MethodPost, "/api/pattern", map[string]any{"id": "不存在"})
	if bad.Code != http.StatusBadRequest {
		t.Errorf("非法图案 id 应返回 400，实际 %d", bad.Code)
	}

	clr := h.do(http.MethodPost, "/api/pattern", map[string]any{"id": ""})
	if clr.Code != http.StatusOK || h.set.Pattern != "" {
		t.Errorf("空 id 应清空图案，实际 code=%d pattern=%q", clr.Code, h.set.Pattern)
	}
}

// 换成另一套主题要还原那套主题的完整画面：图案库单选随之清除
// （否则点阵被旧图案卡住、键盘灯却已经变了 —— 用户实际踩到的 bug）。
// 同主题重推（动/静态切换走的就是这条）必须保留图案。
func TestThemeSwitchClearsPattern(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/pattern", map[string]any{"id": "ghost"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/pattern = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.Pattern != "ghost" {
		t.Fatalf("前置条件：Pattern 应为 ghost，实际 %q", h.set.Pattern)
	}

	// 同主题重推：保留
	rec = h.do(http.MethodPost, "/api/theme", map[string]any{"id": "pikachu", "dynamic": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/theme(同主题) = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.Pattern != "ghost" {
		t.Errorf("同主题重推不应清图案，实际 %q", h.set.Pattern)
	}

	// 换主题：清除，且状态同步暴露
	rec = h.do(http.MethodPost, "/api/theme", map[string]any{"id": "panda"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/theme(换主题) = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.Pattern != "" {
		t.Errorf("换主题应清空图案，实际 %q", h.set.Pattern)
	}
	if m := h.state(); m["pattern"] != "" {
		t.Errorf("state.pattern 应为空，实际 %v", m["pattern"])
	}
}

// 工具形状库只读：固定三家内置，不支持增删（不认得的工具走宝石兜底）。
func TestAgentsListBuiltin(t *testing.T) {
	h := newHarness(t)
	h.srv = api.New(api.Deps{
		Engine: h.eng, Device: &device.Controller{}, Settings: h.set,
		Agent: h.ag, Pomodoro: h.pom, Console: api.ConsoleFS(),
		Save: func() error { return h.set.Save() },
	})

	rec := h.do(http.MethodGet, "/api/agents", nil)
	var list struct {
		Agents []struct {
			ID      string `json:"id"`
			Builtin bool   `json:"builtin"`
		} `json:"agents"`
	}
	decodeInto(t, rec, &list)
	if len(list.Agents) != 3 || !list.Agents[0].Builtin {
		t.Fatalf("应固定 3 个内置：%+v", list.Agents)
	}
	if list.Agents[0].ID != "claude-code" || list.Agents[1].ID != "codex" || list.Agents[2].ID != "opencode" {
		t.Errorf("内置顺序应固定：%+v", list.Agents)
	}

	// 只读：POST / DELETE 一律 405
	if rec := h.do(http.MethodPost, "/api/agents", map[string]any{"name": "x"}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 应 405，实际 %d", rec.Code)
	}
	if rec := h.do(http.MethodDelete, "/api/agents", map[string]any{"id": "x"}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE 应 405，实际 %d", rec.Code)
	}
}

// 空闲展示：内置形状可选，未知形状拒绝，auto 归一成空串。
func TestSettingsIdleAgent(t *testing.T) {
	h := newHarness(t)
	got := "∅"
	h.srv = api.New(api.Deps{
		Engine: h.eng, Device: &device.Controller{}, Settings: h.set,
		Agent: h.ag, Pomodoro: h.pom, Console: api.ConsoleFS(),
		Save:         func() error { return h.set.Save() },
		SetIdleAgent: func(id string) { got = id },
	})

	rec := h.do(http.MethodPost, "/api/settings", map[string]any{"idle_agent": "codex"})
	if rec.Code != http.StatusOK || h.set.IdleAgent != "codex" || got != "codex" {
		t.Fatalf("内置形状应可选：code=%d saved=%q hook=%q", rec.Code, h.set.IdleAgent, got)
	}
	if rec := h.do(http.MethodPost, "/api/settings", map[string]any{"idle_agent": "zcode"}); rec.Code != http.StatusBadRequest {
		t.Errorf("不存在的形状应 400，实际 %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, "/api/settings", map[string]any{"idle_agent": "不存在的工具"}); rec.Code != http.StatusBadRequest {
		t.Errorf("未知形状应 400，实际 %d", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/settings", map[string]any{"idle_agent": "auto"})
	if rec.Code != http.StatusOK || h.set.IdleAgent != "" || got != "" {
		t.Errorf("auto 应归一成空串：code=%d saved=%q hook=%q", rec.Code, h.set.IdleAgent, got)
	}
	// 空串 = 清空回跟随会话，绝不能被 NormalizeName 默认成 claude-code
	h.set.IdleAgent = "codex"
	rec = h.do(http.MethodPost, "/api/settings", map[string]any{"idle_agent": ""})
	if rec.Code != http.StatusOK || h.set.IdleAgent != "" {
		t.Errorf("空串应清空：code=%d saved=%q", rec.Code, h.set.IdleAgent)
	}
}

func TestSettingsRateTier(t *testing.T) {
	h := newHarness(t)
	var got = -1
	h.srv = api.New(api.Deps{
		Engine: h.eng, Device: &device.Controller{}, Settings: h.set,
		Agent: h.ag, Pomodoro: h.pom, Console: api.ConsoleFS(),
		Save:        func() error { return h.set.Save() },
		SetRateTier: func(tier int) { got = tier },
	})
	rec := h.do(http.MethodPost, "/api/settings", map[string]any{"rate_tier": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings = %d：%s", rec.Code, rec.Body.String())
	}
	if h.set.RateTier != 2 {
		t.Errorf("RateTier 应为 2，实际 %d", h.set.RateTier)
	}
	if got != 2 {
		t.Errorf("SetRateTier 钩子应收到 2，实际 %d", got)
	}
	again, err := store.Open(h.dir)
	if err != nil {
		t.Fatalf("重读配置: %v", err)
	}
	if again.RateTier != 2 {
		t.Error("落盘后重读 RateTier 应为 2")
	}

	bad := h.do(http.MethodPost, "/api/settings", map[string]any{"rate_tier": 3})
	if bad.Code != http.StatusBadRequest {
		t.Errorf("rate_tier=3 应返回 400，实际 %d", bad.Code)
	}
	if h.set.RateTier != 2 {
		t.Error("越界值不应改动已保存的档位")
	}
}

// TestSettingsJSONKeys 覆盖控制台从 /api/settings 读的那几个字段
// （亮度滑杆靠 s.brightness.dot / .keys）。
func TestSettingsJSONKeys(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings = %d", rec.Code)
	}
	var m map[string]any
	decodeInto(t, rec, &m)

	bright, ok := m["brightness"].(map[string]any)
	if !ok {
		t.Fatalf("settings 缺 brightness 或类型不对：%T", m["brightness"])
	}
	for _, k := range []string{"dot", "keys"} {
		if _, ok := bright[k]; !ok {
			t.Errorf("brightness 缺 %q（现有：%v）", k, keysOf(bright))
		}
	}
	for _, k := range []string{"calibration", "idle", "theme", "mode"} {
		if _, ok := m[k]; !ok {
			t.Errorf("settings 缺 %q", k)
		}
	}
}

/* ══════════ 页面 ══════════ */

func TestIndexServesConsoleNoStore(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	// 长期挂着的窗口，缓存住旧版本排查起来极费劲
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q，应含 no-store", cc)
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Error("返回的不是 HTML")
	}
}

func TestIndexRejectsOtherPaths(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodGet, "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d，应为 404", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/index.html", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /index.html = %d，应为 200", rec.Code)
	}
}

/* ══════════ 兼容层 ══════════ */

func TestKBStatus(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/kb/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /kb/status = %d", rec.Code)
	}
	var out map[string]any
	decodeInto(t, rec, &out)
	if out["ok"] != true {
		t.Errorf("ok = %v", out["ok"])
	}
	// 没接键盘时 connected 必须是 false，不能假装连上了
	if out["connected"] != false {
		t.Errorf("没接键盘却报 connected=%v", out["connected"])
	}
	for _, k := range []string{"awake", "product", "wired", "dropped"} {
		if _, ok := out[k]; !ok {
			t.Errorf("/kb/status 缺字段 %q", k)
		}
	}
}

func TestKBUnimplementedIsHonest(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/kb/lightbuf", "/kb/anim", "/kb/anim/stop"} {
		rec := h.do(http.MethodPost, p, map[string]any{})
		if rec.Code != http.StatusOK {
			t.Errorf("POST %s = %d", p, rec.Code)
			continue
		}
		var out map[string]any
		decodeInto(t, rec, &out)
		// 没实现就明确说没实现，不能回 ok:true 骗调用方
		if out["ok"] != false {
			t.Errorf("%s 回了 ok=%v，应当如实说没实现", p, out["ok"])
		}
		if out["why"] == nil {
			t.Errorf("%s 没说原因", p)
		}
	}
}

func TestKBMatrixLengthChecked(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/kb/matrix", map[string]any{"rgb": []int{1, 2, 3}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("短 rgb 应 400，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "108") {
		t.Errorf("错误信息没说清要几个数：%s", rec.Body.String())
	}
}

func TestKBCompatAliases(t *testing.T) {
	h := newHarness(t)
	// 老路径 /events 必须和新路径等价
	rec := h.do(http.MethodPost, "/events", map[string]any{
		"event": "session_started", "sessionId": "old-1", "agent": "codex",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /events = %d：%s", rec.Code, rec.Body.String())
	}
	rec = h.do(http.MethodGet, "/snapshot", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /snapshot = %d", rec.Code)
	}
	var snap agent.Snapshot
	decodeInto(t, rec, &snap)
	if len(snap.Sessions) != 1 {
		t.Errorf("/snapshot 里会话数 = %d", len(snap.Sessions))
	}
}

/* ══════════ SSE ══════════ */

// sseReader 从一条 SSE 连接上按事件读。
type sseReader struct {
	t    *testing.T
	br   *bufio.Reader
	resp *http.Response
}

func (s *sseReader) next() (event string, data string) {
	s.t.Helper()
	var ev, buf string
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			s.t.Fatalf("读 SSE 失败（event=%q）：%v", ev, err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			ev = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			buf = strings.TrimPrefix(line, "data: ")
		case line == "":
			if ev != "" {
				return ev, buf
			}
		}
	}
}

func (s *sseReader) close() { s.resp.Body.Close() }

func dialSSE(t *testing.T, srv *api.Server, path string) *sseReader {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("建请求: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("连接 SSE: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("Content-Type = %q，应为 text/event-stream", ct)
	}
	return &sseReader{t: t, br: bufio.NewReader(resp.Body), resp: resp}
}

func TestSSEInitialStateThenBroadcast(t *testing.T) {
	h := newHarness(t)
	r := dialSSE(t, h.srv, "/api/events")
	defer r.close()

	// 连上就先给一次全量状态，页面不必再单独拉
	ev, data := r.next()
	if ev != "state" {
		t.Fatalf("第一个事件是 %q，应为 state", ev)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(data), &st); err != nil {
		t.Fatalf("state 载荷不是 JSON：%v", err)
	}
	if st["theme"] != "pikachu" {
		t.Errorf("初始 theme = %v", st["theme"])
	}

	// 改主题后必须推一条新的 state
	if rec := h.do(http.MethodPost, "/api/theme", map[string]any{"id": "doraemon"}); rec.Code != http.StatusOK {
		t.Fatalf("切主题失败：%d", rec.Code)
	}
	ev, data = r.next()
	if ev != "state" {
		t.Fatalf("切主题后收到 %q，应为 state", ev)
	}
	if !strings.Contains(data, "doraemon") {
		t.Errorf("推送的状态里没有新主题：%s", data)
	}
}

// TestPreviewPushesFrames 是这次重写最核心的那条断言的 HTTP 侧版本：
// 页面只是订阅者。帧由引擎产出，服务端只负责把它塞进 SSE。
func TestPreviewPushesFrames(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := h.eng.SetScene(ctx, gridScene{}); err != nil {
		t.Fatalf("SetScene: %v", err)
	}
	go func() { _ = h.eng.Pump(ctx) }()
	go func() { _ = h.eng.Drain(ctx) }()
	go h.srv.RunPreview(ctx, 10*time.Millisecond)

	r := dialSSE(t, h.srv, "/api/events")
	defer r.close()

	// 先吃掉首帧状态，再等 frame
	if ev, _ := r.next(); ev != "state" {
		t.Fatalf("第一个事件是 %q", ev)
	}
	ev, data := r.next()
	if ev != "frame" {
		t.Fatalf("事件是 %q，应为 frame", ev)
	}

	var fr struct {
		M string `json:"m"`
		K string `json:"k"`
	}
	if err := json.Unmarshal([]byte(data), &fr); err != nil {
		t.Fatalf("frame 载荷不是 JSON：%v（%s）", err, data)
	}
	if len(fr.M) != frame.MatrixLen*2 {
		t.Errorf("点阵十六进制长度 = %d，应为 %d", len(fr.M), frame.MatrixLen*2)
	}
	if len(fr.K) != frame.KeysLen*2 {
		t.Errorf("键盘十六进制长度 = %d，应为 %d", len(fr.K), frame.KeysLen*2)
	}
	if _, err := hex.DecodeString(fr.M); err != nil {
		t.Errorf("点阵不是合法十六进制：%v", err)
	}
	if _, err := hex.DecodeString(fr.K); err != nil {
		t.Errorf("键盘不是合法十六进制：%v", err)
	}

	// 帧确实在变（引擎在推进时钟）
	_, second := r.next()
	var fr2 struct {
		M string `json:"m"`
	}
	if err := json.Unmarshal([]byte(second), &fr2); err != nil {
		t.Fatalf("第二帧不是 JSON：%v", err)
	}
	if fr2.M == fr.M {
		t.Error("两帧内容完全一样，帧泵可能没在推进")
	}
}

// TestSSESlowSubscriberDoesNotBlock 确认订阅者跟不上时帧推送不会被拖住。
// 这是"跑久了卡死"的另一个入口：旧版页面就是一个慢消费者。
func TestSSESlowSubscriberDoesNotBlock(t *testing.T) {
	h := newHarness(t)
	r := dialSSE(t, h.srv, "/api/events")
	defer r.close()

	// 占满订阅者缓冲区（hub 里是 16），再多推 50 次
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			h.srv.Broadcast()
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("订阅者不读，Broadcast 被阻塞了")
	}
}

// 后端健康检查端点：返回签名。
func TestPingSignature(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/ping", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/ping = %d", rec.Code)
	}
	var m map[string]any
	decodeInto(t, rec, &m)
	if m["yogod"] != true {
		t.Errorf("签名应为 yogod:true，实际 %v", m["yogod"])
	}
}
