package companion

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

/* ══════════ 映射表 ══════════

   这张表是"原生 hook 名 → 标准事件"的全部知识。
   错一个，那一类事件就永远不点亮对应状态 —— 而且不会报错。 */

func TestMapEvent(t *testing.T) {
	cases := []struct{ native, want string }{
		{"SessionStart", evSessionStarted},
		{"session_start", evSessionStarted},
		{"SessionEnd", evSessionEnded},
		{"UserPromptSubmit", evUserMessage},
		{"UserPromptSubmitted", evUserMessage},
		{"PreToolUse", evProcessingResumed},
		{"PostToolUse", evProcessingResumed},
		{"PostToolUseFailure", evProcessingResumed},
		{"PermissionRequest", evApprovalRequested},
		{"PermissionDenied", evProcessingResumed},
		{"PermissionResolved", evProcessingResumed},
		{"supplement_requested", evSupplementRequested},
		{"SupplementRequested", evSupplementRequested},
		{"Stop", evTurnCompleted},
		{"TurnCompleted", evTurnCompleted},
		{"StopFailure", evTurnCompletedFailed},
		{"SubagentStop", ""},
		{"PreCompact", ""},
		{"Notification", ""}, // Notification 要看 payload 里的类型，单独测
		{"", ""},
		{"随便什么", ""},
	}
	for _, c := range cases {
		if got := mapEvent(c.native); got != c.want {
			t.Errorf("mapEvent(%q) = %q，期望 %q", c.native, got, c.want)
		}
	}
}

func TestMapNotification(t *testing.T) {
	cases := []struct{ kind, want string }{
		{"permission_prompt", evApprovalRequested},
		{"permission_request", evApprovalRequested},
		{"approval_requested", evApprovalRequested},
		{"elicitation_dialog", evSupplementRequested},
		{"question", evSupplementRequested},
		{"ask_user_question", evSupplementRequested},
		{"idle_prompt", evTurnCompleted},
		{"completed", evTurnCompleted},
		{"完全不认识", ""},
	}
	for _, c := range cases {
		p := map[string]any{"notification_type": c.kind}
		if got := mapNotification(p); got != c.want {
			t.Errorf("notification %q → %q，期望 %q", c.kind, got, c.want)
		}
	}
}

/* ══════════ 端到端：发到真实的 HTTP 服务器 ══════════ */

// TestSupplementRequestedViaCLI 确认标准事件名可以直接从 --event 传进来
// （payload 里不带事件名的工具只能这么配）。
func TestSupplementRequestedViaCLI(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)
	cfg.HookEvent = "supplement_requested"
	run(t, cfg, map[string]any{"session_id": "s1", "turn_id": "t1"})
	body := receive(t, ch)
	if body["event"] != evSupplementRequested {
		t.Fatalf("event = %v，期望 %v", body["event"], evSupplementRequested)
	}
}

// recorder 起一个本机服务，把收到的 body 收进 channel。
func recorder(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		select {
		case ch <- m:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func run(t *testing.T, cfg Config, payload map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(payload)
	var out bytes.Buffer
	if err := RunIO(cfg, bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("RunIO: %v", err)
	}
	return payload
}

func baseCfg(t *testing.T, url string) Config {
	return Config{Agent: "claude", URL: url + "/events", StateDir: t.TempDir()}
}

func receive(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	default:
		t.Fatal("没有收到事件")
		return nil
	}
}

func TestSessionStartToEvent(t *testing.T) {
	srv, ch := recorder(t)
	run(t, baseCfg(t, srv.URL), map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      "abc-123",
		"cwd":             `D:\proj\demo`,
	})
	got := receive(t, ch)

	if got["event"] != evSessionStarted {
		t.Errorf("event = %v", got["event"])
	}
	if got["sessionId"] != "claude:abc-123" {
		t.Errorf("sessionId = %v，应为 claude:<原生 id>", got["sessionId"])
	}
	if got["agent"] != "claude" {
		// 适配器**不做厂商别名归一**：--agent 是什么就报什么。
		// claude → claude-code 的归一发生在服务端 internal/agent。
		t.Errorf("agent = %v，应当原样透传", got["agent"])
	}
	if got["schema"] != "yogo.companion.v1" {
		t.Errorf("schema = %v", got["schema"])
	}
	if _, ok := got["eventId"].(string); !ok {
		t.Error("缺 eventId（服务端靠它去重）")
	}
	if seq, ok := got["sequence"].(float64); !ok || seq < 1 {
		t.Errorf("sequence = %v，应当从 1 开始", got["sequence"])
	}

	data, _ := got["data"].(map[string]any)
	if data["project"] != "demo" {
		t.Errorf("project = %v，应当从 cwd 推出 demo", data["project"])
	}
}

func TestTurnIDGeneratedAndReused(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)

	run(t, cfg, map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": `D:\p\a`,
	})
	first := receive(t, ch)

	run(t, cfg, map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "s1", "cwd": `D:\p\a`,
		"tool_name": "Bash",
	})
	second := receive(t, ch)

	turn1, _ := first["turnId"].(string)
	turn2, _ := second["turnId"].(string)
	if turn1 == "" {
		t.Fatal("user_message 没有生成 turnId")
	}
	if turn1 != turn2 {
		t.Errorf("同一轮里 turnId 变了：%q → %q（服务端会当成两轮）", turn1, turn2)
	}

	seq1, _ := first["sequence"].(float64)
	seq2, _ := second["sequence"].(float64)
	if seq2 <= seq1 {
		t.Errorf("sequence 没有递增：%v → %v（乱序保护会失效）", seq1, seq2)
	}

	// 新的一轮必须换 turnId
	run(t, cfg, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s1"})
	third := receive(t, ch)
	if turn3, _ := third["turnId"].(string); turn3 == turn1 {
		t.Errorf("新的一轮沿用了旧 turnId %q", turn3)
	}
}

func TestApprovalThenResume(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)

	run(t, cfg, map[string]any{
		"hook_event_name": "Notification", "notification_type": "permission_prompt",
		"session_id": "s2", "tool_name": "Bash", "tool_use_id": "toolu_9",
	})
	approval := receive(t, ch)
	if approval["event"] != evApprovalRequested {
		t.Fatalf("event = %v", approval["event"])
	}
	inter, _ := approval["interaction"].(map[string]any)
	if inter["requestId"] != "toolu_9" {
		t.Errorf("requestId = %v", inter["requestId"])
	}

	// 恢复处理时不该带 requestId，适配器要自己把挂着的那个销掉，
	// 否则服务端会一直停在"等待授权"
	run(t, cfg, map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "s2", "tool_name": "Read",
	})
	resumed := receive(t, ch)
	inter2, _ := resumed["interaction"].(map[string]any)
	if inter2["requestId"] != "toolu_9" {
		t.Errorf("恢复时没销掉挂起的授权：%v", inter2["requestId"])
	}

	// 再恢复一次就不该再带了
	run(t, cfg, map[string]any{"hook_event_name": "PostToolUse", "session_id": "s2"})
	again := receive(t, ch)
	if _, ok := again["interaction"]; ok {
		t.Error("授权已经销过，不该再带 interaction")
	}
}

func TestStopFailure(t *testing.T) {
	srv, ch := recorder(t)
	run(t, baseCfg(t, srv.URL), map[string]any{
		"hook_event_name": "StopFailure", "session_id": "s3",
	})
	got := receive(t, ch)
	if got["event"] != evTurnCompleted {
		t.Errorf("event = %v，失败也走 turn_completed", got["event"])
	}
	if got["outcome"] != "failed" {
		t.Errorf("outcome = %v，应为 failed", got["outcome"])
	}
}

func TestSessionEndDeletesState(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)

	run(t, cfg, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s4"})
	<-ch
	path := statePath(stateDir(cfg.StateDir), "claude", "claude:s4")
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("状态文件没落下来：%v", err)
	}

	run(t, cfg, map[string]any{"hook_event_name": "SessionEnd", "session_id": "s4"})
	receive(t, ch)
	if _, err := os.ReadFile(path); err == nil {
		t.Error("会话结束后状态文件应当删掉")
	}
}

/* ══════════ 隐私与安全 ══════════ */

// TestPrivacyDoesNotLeakContent 是这个适配器最重要的测试。
//
// 它每一回合都会被调用，看到的东西包括源码、密钥、私人对话。
// 白名单之外的东西一个字节都不许带出去。
func TestPrivacyDoesNotLeakContent(t *testing.T) {
	srv, ch := recorder(t)
	run(t, baseCfg(t, srv.URL), map[string]any{
		"hook_event_name":        "UserPromptSubmit",
		"session_id":             "s5",
		"cwd":                    `D:\p\demo`,
		"prompt":                 "我的数据库密码是 hunter2",
		"tool_input":             `{"command":"curl -H 'Authorization: Bearer sk-secret' "}`,
		"tool_response":          "TOP SECRET RESPONSE",
		"last_assistant_message": "机密回答内容",
		"message":                "另一段机密",
		"additionalContext":      "系统提示词",
		"transcript_path":        `C:\Users\x\.claude\transcript.jsonl`,
	})
	raw, _ := json.Marshal(receive(t, ch))

	for _, secret := range []string{"hunter2", "sk-secret", "TOP SECRET", "机密回答", "另一段机密", "系统提示词", "transcript"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("事件里泄漏了 %q：\n%s", secret, raw)
		}
	}
}

func TestNonLoopbackURLIsRefused(t *testing.T) {
	cases := []string{
		"http://example.com/events",
		"http://192.168.1.5:25264/events",
		"https://127.0.0.1:25264/events", // https 也不收
		"ftp://127.0.0.1/events",
		"不是地址",
	}
	for _, u := range cases {
		if got := loopbackURL(u); got != "" {
			t.Errorf("loopbackURL(%q) = %q，应当拒绝", u, got)
		}
	}
	// 不传地址时必须自己推回环地址，绝不能推成外网；默认端口固定 25264。
	t.Setenv("YOGO_COMPANION_URL", "")
	t.Setenv("YOGO_PORT", "")
	if got, want := loopbackURL(""), "http://127.0.0.1:25264/events"; got != want {
		t.Errorf("默认地址 = %q，期望 %q", got, want)
	}
	for _, u := range []string{
		"http://127.0.0.1:25264/events",
		"http://localhost:25264/events",
		"http://[::1]:25264/events",
	} {
		if got := loopbackURL(u); got == "" {
			t.Errorf("loopbackURL(%q) 被误拒", u)
		}
	}
}

// TestUnreachableServerDoesNotFail：服务没起时 hook 必须安静地成功。
func TestUnreachableServerDoesNotFail(t *testing.T) {
	cfg := Config{Agent: "claude", URL: "http://127.0.0.1:1/events", StateDir: t.TempDir()}
	raw, _ := json.Marshal(map[string]any{"hook_event_name": "Stop", "session_id": "s9"})
	var out bytes.Buffer
	if err := RunIO(cfg, bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("服务不可达时不该报错：%v", err)
	}
}

func TestUnknownEventIsSilent(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)
	raw, _ := json.Marshal(map[string]any{"hook_event_name": "SubagentStop", "session_id": "s6"})
	var out bytes.Buffer
	if err := RunIO(cfg, bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("不认识的事件不该报错：%v", err)
	}
	select {
	case m := <-ch:
		t.Errorf("不该发送，却收到了 %v", m["event"])
	default:
	}
}

func TestGarbageStdinIsSilent(t *testing.T) {
	cfg := Config{Agent: "claude", StateDir: t.TempDir()}
	for _, in := range []string{"", "  ", "不是 JSON", "[1,2,3]", `{"hook_event_name":123}`} {
		var out bytes.Buffer
		if err := RunIO(cfg, strings.NewReader(in), &out); err != nil {
			t.Errorf("输入 %q 时不该报错：%v", in, err)
		}
	}
}

func TestDryRunPrintsWithoutSending(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)
	cfg.DryRun = true

	raw, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "s7"})
	var out bytes.Buffer
	if err := RunIO(cfg, bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	select {
	case m := <-ch:
		t.Errorf("dry-run 不该真的发送，却收到了 %v", m["event"])
	default:
	}
	if !strings.Contains(out.String(), evUserMessage) {
		t.Errorf("dry-run 应当打印标准事件，实际输出：%q", out.String())
	}
	// dry-run 也不该留下状态文件
	if _, err := os.ReadFile(statePath(stateDir(cfg.StateDir), "claude", "claude:s7")); err == nil {
		t.Error("dry-run 不该写状态文件")
	}
}

/* ══════════ 字段提取 ══════════ */

func TestSessionIDFallback(t *testing.T) {
	// 没有 session_id 时退回 cwd 的末级目录名 —— 至少同一项目会归到一个会话
	got := sessionIDOf(map[string]any{"cwd": `D:\work\myproj`}, "codex")
	if got != "codex:myproj" {
		t.Errorf("sessionIDOf = %q，期望 codex:myproj", got)
	}
	// 什么都没有也不能空
	if got := sessionIDOf(map[string]any{}, "claude"); got != "claude:default" {
		t.Errorf("兜底 = %q", got)
	}
	// 超长 id 要压成哈希，不能把 sessionId 撑爆
	long := strings.Repeat("x", 400)
	got = sessionIDOf(map[string]any{"session_id": long}, "claude")
	if len([]rune(got)) > maxSession {
		t.Errorf("压缩后仍超长：%d", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "claude:") {
		t.Errorf("压缩后丢了前缀：%q", got)
	}
}

func TestNativeTurnIDWins(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)
	run(t, cfg, map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": "s8", "turn_id": "native-turn-7",
	})
	got := receive(t, ch)
	if got["turnId"] != "native-turn-7" {
		t.Errorf("原生 turnId 被丢掉了：%v", got["turnId"])
	}
}

func TestToolNameAndRequestID(t *testing.T) {
	p := map[string]any{
		"toolName":   "Edit",
		"permission": map[string]any{"request_id": "req-42"},
		"workspace":  `D:\w\proj`,
		"thread_id":  "th-1",
		"turnId":     "t-1",
	}
	if got := toolOf(p); got != "Edit" {
		t.Errorf("toolOf = %q", got)
	}
	if got := requestIDOf(p); got != "req-42" {
		t.Errorf("requestIDOf = %q，应当能钻进 permission 对象里找", got)
	}
	if got := cwdOf(p); got != `D:\w\proj` {
		t.Errorf("cwdOf = %q", got)
	}
	if got := sessionIDOf(p, "trae"); got != "trae:th-1" {
		t.Errorf("sessionIDOf = %q", got)
	}
}

func TestClipCountsRunes(t *testing.T) {
	// 按字符截断，不是按字节 —— 否则中文消息会被切成半个字
	got := clip("一二三四五", 3)
	if got != "一二三" {
		t.Errorf("clip = %q", got)
	}
	if n := len([]rune(clip(strings.Repeat("字", 300), maxMessage))); n != maxMessage {
		t.Errorf("截断后长度 = %d，应为 %d", n, maxMessage)
	}
}

/* ══════════ 状态文件 ══════════ */

// TestStateSurvivesAcrossCalls 模拟真实用法：每次 hook 都是一个新进程，
// 编号必须靠磁盘上的状态延续下去。
func TestStateSurvivesAcrossCalls(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)
	dir := filepath.Join(cfg.StateDir, "sessions")

	for i := 0; i < 5; i++ {
		c := cfg
		c.StateDir = dir
		run(t, c, map[string]any{
			"hook_event_name": "PreToolUse", "session_id": "sA", "tool_name": "Read",
		})
		got := receive(t, ch)
		want := float64(i + 1)
		if got["sequence"] != want {
			t.Fatalf("第 %d 次调用 sequence = %v，期望 %v", i, got["sequence"], want)
		}
	}
}

func TestConcurrentHooksDoNotLoseState(t *testing.T) {
	srv, ch := recorder(t)
	cfg := baseCfg(t, srv.URL)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse", "session_id": "sB", "tool_name": "Bash",
			})
			var out bytes.Buffer
			_ = RunIO(cfg, bytes.NewReader(raw), &out)
		}()
	}
	wg.Wait()

	// 8 个并发调用，sequence 不该出现重复（锁的意义就在这儿）
	seen := map[float64]bool{}
	timeout := 0
	for timeout < 8 {
		select {
		case m := <-ch:
			seq, _ := m["sequence"].(float64)
			if seen[seq] {
				t.Errorf("sequence %v 重复了 —— 锁没起作用", seq)
			}
			seen[seq] = true
			timeout++
		default:
			timeout = 99
		}
	}
	if len(seen) < 8 {
		t.Errorf("只收到 %d 个事件，期望 8（部分调用失败了？）", len(seen))
	}
}

/* ══════════ 通用性：不绑定任何厂商 ══════════ */

// TestAnyAgentNameWorks 是这个适配器的立身之本：
// 没有厂商白名单，随便什么工具名都能接，只要它发的事件名在表里。
func TestAnyAgentNameWorks(t *testing.T) {
	for _, name := range []string{"cursor", "github-copilot", "Aider", "我的工具"} {
		srv, ch := recorder(t)
		cfg := Config{Agent: name, URL: srv.URL + "/events", StateDir: t.TempDir()}

		raw, _ := json.Marshal(map[string]any{"hook_event_name": "Stop", "session_id": "x"})
		var out bytes.Buffer
		if err := RunIO(cfg, bytes.NewReader(raw), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := receive(t, ch)

		if got["event"] != evTurnCompleted {
			t.Errorf("%s: event = %v", name, got["event"])
		}
		if want := normAgent(name) + ":x"; got["sessionId"] != want {
			t.Errorf("%s: sessionId = %v，期望 %v", name, got["sessionId"], want)
		}
	}
}

// TestEventNameVariants：各家写法不同，归一化之后是同一个事件。
func TestEventNameVariants(t *testing.T) {
	for _, spelling := range []string{
		"SessionStart", "session_start", "session-start", "SESSION_START",
		"sessionStart", "Session.Start", "sessionstart",
	} {
		if got := mapEvent(spelling); got != evSessionStarted {
			t.Errorf("mapEvent(%q) = %q，期望 %q", spelling, got, evSessionStarted)
		}
	}
}

// TestAgentFromPayload：没给 --agent 时用 payload 自报的名字。
func TestAgentFromPayload(t *testing.T) {
	cases := []struct {
		payload map[string]any
		want    string
	}{
		{map[string]any{"agent": "Cursor"}, "cursor"},
		{map[string]any{"source": "github-copilot"}, "github-copilot"},
		{map[string]any{"client": "Aider"}, "aider"},
		{map[string]any{}, "unknown"},
	}
	for _, c := range cases {
		if got := agentOf(c.payload, ""); got != c.want {
			t.Errorf("agentOf(%v) = %q，期望 %q", c.payload, got, c.want)
		}
	}
	// 命令行显式给的优先
	if got := agentOf(map[string]any{"agent": "cursor"}, "windsurf"); got != "windsurf" {
		t.Errorf("命令行的 --agent 应当优先，实际 %q", got)
	}
}

func TestKnownEventsTable(t *testing.T) {
	known := KnownEvents()
	if len(known) == 0 {
		t.Fatal("事件表是空的")
	}
	for _, must := range []string{"sessionstart", "sessionend", "userpromptsubmit",
		"pretooluse", "posttooluse", "stop", "permissionrequest"} {
		if _, ok := known[must]; !ok {
			t.Errorf("事件表里缺 %q（yogod hook events 打印的就是它）", must)
		}
	}
	// 表里的名字必须都已经归一化，否则永远匹配不上
	for name := range known {
		if normName(name) != name {
			t.Errorf("表项 %q 没归一化，查表会漏", name)
		}
	}
	// 每个标准事件都得有名字能映射过去
	for _, ev := range []string{evSessionStarted, evSessionEnded, evUserMessage,
		evProcessingResumed, evApprovalRequested, evSupplementRequested, evTurnCompleted} {
		found := false
		for _, target := range known {
			if target == ev {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("标准事件 %q 没有任何原生名能映射到它", ev)
		}
	}
}
