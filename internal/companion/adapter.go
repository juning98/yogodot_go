// Package companion 把 AI 编程工具的原生 hook 翻译成 YogoDot 标准事件。
//
// 这是"伴侣"功能真正的入口。internal/agent 只知道标准事件，
// 而各家工具发出来的是 SessionStart / PreToolUse / Stop 这种原生名，
// 两边之间必须有个人翻译 —— 这个包就是那个人。
//
// **它不认厂商，只认事件名。** 没有任何 per-vendor 分支：
// 事件名先归一化（SessionStart / session_start / session-start 都是
// `sessionstart`），再查一张固定的事件名表。表里有的就翻译，没有的就安静丢掉。
// 所以任何"支持 hook 且事件名在表里"的工具都能接，不需要为新工具改代码 ——
// 加一行表项就够。
//
// 三条硬规则：
//
//  1. **绝不阻塞工具。** 这是挂在别人每回合流程里的进程，
//     它慢一秒，用户的对话就慢一秒。所以：超时 350ms、失败重试一次、
//     任何错误都吞掉、永远 exit 0。
//
//  2. **只提取白名单元数据。** prompt、tool_input、tool_response、
//     last_assistant_message 这些正文一律不碰 —— 它们可能包含源码、
//     密钥、私人对话，而接收方只是一个桌面灯效程序。
//
//  3. **只连本机。** 地址必须是 127.0.0.1 / localhost / ::1。
//     hook 配置里的命令谁都能改，不能让这个口子变成外泄通道。
package companion

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// 上限，和原版一致。
const (
	maxInput   = 8 << 20 // stdin 最多读这么多，超了就当空
	maxMessage = 200
	maxTool    = 80
	maxCwd     = 300
	maxProject = 80
	maxSession = 120
	maxTurn    = 80
	maxRequest = 120
)

// Config 是一次调用的参数。
type Config struct {
	// Agent 是自由字符串：只用来给会话分组、给界面显示，不参与任何映射。
	// 空则从 payload 的 agent/source 字段里猜，再不行叫 unknown。
	Agent string
	// HookEvent 是命令行显式指定的事件名。
	// 用于那些 payload 里不带事件名的工具（事件名只能从配置里传）。
	HookEvent string
	// URL 是 --url；空则按 YOGO_COMPANION_URL / YOGO_PORT 推出来，
	// 最后退回固定端口 25264（与 cmd/yogod 的默认值保持一致）。
	URL string
	// StateDir 是 --state-dir；空则用 ~/.yogo/companion。
	StateDir string
	// DryRun 只打印标准事件，不发送、不落状态。
	DryRun bool
}

// 标准事件名，与 internal/agent 一致。
const (
	evSessionStarted      = "session_started"
	evSessionEnded        = "session_ended"
	evUserMessage         = "user_message_submitted"
	evProcessingResumed   = "processing_resumed"
	evApprovalRequested   = "approval_requested"
	evSupplementRequested = "supplement_requested"
	evTurnCompleted       = "turn_completed"
	// evTurnCompletedFailed 是内部标记，出门前会变成 turn_completed + outcome=failed。
	evTurnCompletedFailed = "turn_completed_failed"
)

// ── 入口 ──────────────────────────────────────────────────────────

// Run 读 os.Stdin 并发送。永远不会因为业务原因返回错误。
func Run(cfg Config) error {
	return RunIO(cfg, os.Stdin, os.Stdout)
}

// RunIO 是 Run 的可测试版本。
func RunIO(cfg Config, in io.Reader, out io.Writer) error {
	payload := readPayload(in)
	native, mapped := mapPayloadEvent(payload, cfg.HookEvent)
	if mapped == "" {
		return nil // 不认识的事件：安静退出，不是错误
	}

	agent := agentOf(payload, cfg.Agent)
	sid := sessionIDOf(payload, agent)
	dir := stateDir(cfg.StateDir)
	path := statePath(dir, agent, sid)

	var body map[string]any
	err := withLock(path, func() error {
		st := loadState(path)
		var del bool
		body, del = translate(agent, native, mapped, payload, st, sid)
		if cfg.DryRun {
			return nil
		}
		if u := loopbackURL(cfg.URL); u != "" {
			postEvent(u, body)
		}
		if del {
			_ = os.Remove(path)
		} else {
			saveState(path, st)
		}
		return nil
	})

	if cfg.DryRun && body != nil && out != nil {
		raw, _ := json.MarshalIndent(body, "", "  ")
		fmt.Fprintf(out, "%s\n", raw)
	}
	return err
}

// ── payload 读取 ──────────────────────────────────────────────────

func readPayload(in io.Reader) map[string]any {
	if in == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxInput+1))
	if err != nil || len(raw) == 0 || len(raw) > maxInput {
		return nil
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if text == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// clip 按**字符**截断（与 Python 的 str 切片一致，不是按字节）。
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// txt 对应原版的 _txt：字符串和数字能转，其余一律空。
// bool 被排除在外 —— true 不该变成 "true" 塞进消息里。
func txt(v any, n int) string {
	switch t := v.(type) {
	case string:
		return clip(t, n)
	case float64:
		// JSON 里所有数字都是 float64。整数按整数打，别输出 "42.000000"。
		if t == float64(int64(t)) {
			return clip(strconv.FormatInt(int64(t), 10), n)
		}
		return clip(strconv.FormatFloat(t, 'f', -1, 64), n)
	case int:
		return clip(strconv.Itoa(t), n)
	case int64:
		return clip(strconv.FormatInt(t, 10), n)
	}
	return ""
}

// first 返回第一个非空值。
func first(vals ...any) string {
	for _, v := range vals {
		if s := txt(v, 4096); s != "" {
			return s
		}
	}
	return ""
}

// normName 去掉所有非字母数字并转小写：把 "PreToolUse" 和 "pre_tool_use" 归一。
func normName(v any) string {
	s := strings.ToLower(clip(txt(v, 120), 120))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// agentOf 决定这次事件算哪个工具发来的。
//
// 顺序：命令行显式给的 > payload 里自报的 > "unknown"。
// **不做厂商别名归一** —— 那是服务端 internal/agent 的事。
// 这里只做"能当文件名和显示名"的清洗，保持通用。
func agentOf(p map[string]any, arg string) string {
	raw := first(arg, p["agent"], p["source"], p["tool"], p["client"], p["app"])
	return normAgent(raw)
}

// normAgent 把任意字符串收成安全的标识：小写，空白和标点换成 -
//
// 保留中文等非 ASCII 字母 —— 工具名会直接显示在控制台和托盘上，
// 把"我的工具"洗成 "unknown" 对用户没有任何好处。
func normAgent(v string) string {
	s := strings.ToLower(clip(v, 40))
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len([]rune(out)) > 32 {
		out = string([]rune(out)[:32])
	}
	if out == "" {
		return "unknown"
	}
	return out
}

// ── 字段提取 ──────────────────────────────────────────────────────

func cwdOf(p map[string]any) string {
	cwd := first(p["cwd"], p["workspace"], p["working_directory"], p["workingDirectory"])
	if cwd == "" {
		if roots, ok := p["workspace_roots"].([]any); ok && len(roots) > 0 {
			cwd = first(roots[0])
		}
	}
	return clip(cwd, maxCwd)
}

func projectOf(p map[string]any, cwd string) string {
	if roots, ok := p["workspace_roots"].([]any); ok {
		for _, root := range roots {
			if s := txt(root, maxCwd); s != "" {
				return clip(baseName(s), maxProject)
			}
		}
	}
	if cwd != "" {
		return clip(baseName(cwd), maxProject)
	}
	return ""
}

// baseName 取路径最后一段，两个分隔符都认（Windows 上会混着来）。
func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func sessionIDOf(p map[string]any, agent string) string {
	raw := first(p["session_id"], p["sessionId"], p["thread_id"], p["threadId"],
		p["conversation_id"], p["conversationId"], p["session"])
	if raw == "" {
		raw = baseName(cwdOf(p))
	}
	if raw == "" {
		raw = "default"
	}
	sid := agent + ":" + raw
	if len([]rune(sid)) > maxSession {
		sum := sha1.Sum([]byte(raw))
		sid = agent + ":" + hex.EncodeToString(sum[:])[:20]
	}
	return sid
}

func nativeTurnID(p map[string]any) string {
	return clip(first(p["turn_id"], p["turnId"], p["turnID"], p["thread_turn_id"]), maxTurn)
}

func toolOf(p map[string]any) string {
	return clip(first(p["tool_name"], p["toolName"], p["llm_tool_name"],
		p["llmToolName"], p["tool"], p["operation"]), maxTool)
}

func requestIDOf(p map[string]any) string {
	for _, k := range []string{"tool_use_id", "toolUseId", "toolUseID", "tool_useId",
		"permission_id", "permissionId", "approval_id", "approvalId",
		"question_id", "questionId", "request_id", "requestId",
		"call_id", "callId", "callID"} {
		if v := txt(p[k], maxRequest); v != "" {
			return v
		}
	}
	for _, k := range []string{"permission", "approval", "question", "request", "input", "info"} {
		obj := asMap(p[k])
		if obj == nil {
			continue
		}
		for _, sub := range []string{"requestId", "request_id", "permissionId", "permission_id",
			"approvalId", "approval_id", "questionId", "question_id",
			"toolUseId", "tool_use_id", "callId", "call_id", "id"} {
			if v := txt(obj[sub], maxRequest); v != "" {
				return v
			}
		}
	}
	return ""
}

func notificationType(p map[string]any) string {
	return first(p["notification_type"], p["notificationType"], p["notification"], p["type"])
}

// nativeEventName 挑出原生事件名。payload 里的 hook_event_name 最可信；
// 其次是模板里显式传的；最后才退回泛化的 event/type
// （Codex 的 type 不一定是 hook 名，所以排在最后）。
func nativeEventName(p map[string]any, arg string) string {
	return first(p["hook_event_name"], p["hookEventName"], arg, p["event"], p["type"])
}

// ── 映射 ──────────────────────────────────────────────────────────

// eventTable 是**唯一**的事件名知识库：归一化后的原生事件名 → 标准事件。
//
// 归一化做的事：转小写 + 去掉所有非字母数字。
// 于是 SessionStart / session_start / session-start / SESSION_START
// 全都落成 `sessionstart` 一个键。
//
// 想支持一个新工具？先看它的事件名归一化后是不是已经在表里 ——
// 绝大多数都在（各家其实用的是同一批词）。不在的话加一行，仅此而已，
// 代码里没有任何"如果是某家工具就……"的分支。
var eventTable = map[string]string{
	// 会话生命周期
	"sessionstart":      evSessionStarted,
	"sessionstarted":    evSessionStarted,
	"sessioncreated":    evSessionStarted,
	"sessionend":        evSessionEnded,
	"sessionended":      evSessionEnded,
	"sessionclosed":     evSessionEnded,
	"conversationstart": evSessionStarted,
	"conversationend":   evSessionEnded,
	"threadstart":       evSessionStarted,
	"threadend":         evSessionEnded,

	// 用户发话 = 新一轮开始
	"userpromptsubmit":     evUserMessage,
	"userpromptsubmitted":  evUserMessage,
	"usermessagesubmit":    evUserMessage,
	"usermessagesubmitted": evUserMessage,
	"promptsubmit":         evUserMessage,
	"promptsubmitted":      evUserMessage,
	"usermessage":          evUserMessage,

	// 需要授权
	"permissionrequest":        evApprovalRequested,
	"permissionrequested":      evApprovalRequested,
	"approvalrequest":          evApprovalRequested,
	"approvalrequested":        evApprovalRequested,
	"directoryapprovalrequest": evApprovalRequested,
	"workspaceapprovalrequest": evApprovalRequested,

	// 需要补充信息（契约的标准名，之前只有 Notification 分支能映射到）
	"supplementrequested": evSupplementRequested,

	// 授权结束 / 继续干活
	"permissionresolved": evProcessingResumed,
	"permissionapproved": evProcessingResumed,
	"approvalresolved":   evProcessingResumed,
	"approvalapproved":   evProcessingResumed,
	"permissiondenied":   evProcessingResumed,
	"approvaldenied":     evProcessingResumed,
	"processingresumed":  evProcessingResumed,
	"pretooluse":         evProcessingResumed,
	"toolusestarted":     evProcessingResumed,
	"toolstart":          evProcessingResumed,
	"posttooluse":        evProcessingResumed,
	"posttoolusefailure": evProcessingResumed,
	"toolusecompleted":   evProcessingResumed,
	"toolend":            evProcessingResumed,

	// 一轮结束
	"stop":             evTurnCompleted,
	"turncomplete":     evTurnCompleted,
	"turncompleted":    evTurnCompleted,
	"responsecomplete": evTurnCompleted,
	"agentend":         evTurnCompleted,
	"stopfailure":      evTurnCompletedFailed,
	"turnfailed":       evTurnCompletedFailed,
	"responsefailed":   evTurnCompletedFailed,
}

// 明确不点灯的事件：子代理、上下文压缩这类不改变"用户要等多久"。
// 单独列出来，是为了让 KnownEvents 能解释清楚"为什么不亮"。
var ignoredEvents = map[string]string{
	"subagentstart": "子代理开始，主任务还在跑",
	"subagentstop":  "子代理结束，主任务还在跑",
	"precompact":    "压缩上下文，用户没在等",
	"postcompact":   "压缩结束",
}

// KnownEvents 返回识别到的事件名（归一化后）及其对应的标准事件，
// 供 `yogod hook events` 打印 —— 配 hook 的时候照着填就行。
func KnownEvents() map[string]string {
	out := make(map[string]string, len(eventTable)+len(ignoredEvents))
	for k, v := range eventTable {
		out[k] = v
	}
	for k, why := range ignoredEvents {
		out[k] = "（忽略：" + why + "）"
	}
	return out
}

func mapNotification(p map[string]any) string {
	switch normName(notificationType(p)) {
	case "permissionprompt", "permissionrequest", "permissionrequested",
		"approvalrequest", "approvalrequested":
		return evApprovalRequested
	case "elicitationdialog", "question", "askuserquestion", "documentreview",
		"browserinteraction", "supplement", "supplementrequested":
		return evSupplementRequested
	case "idleprompt", "completed", "complete", "done":
		return evTurnCompleted
	}
	return ""
}

func mapEvent(native string) string {
	return eventTable[normName(native)]
}

func mapPayloadEvent(p map[string]any, arg string) (native, mapped string) {
	native = nativeEventName(p, arg)
	switch normName(native) {
	case "notification", "notify":
		// 这类事件光有名字不够，要看 payload 里的 notification_type
		return native, mapNotification(p)
	}
	return native, mapEvent(native)
}

func messageFor(mapped, native, tool string) string {
	n := normName(native)
	switch mapped {
	case evSessionStarted:
		return "已连接"
	case evSessionEnded:
		return ""
	case evUserMessage:
		return "开始处理"
	case evProcessingResumed:
		switch {
		case n == "permissiondenied" || n == "approvaldenied":
			return "授权被拒绝，继续处理"
		case n == "permissionresolved" || n == "permissionapproved" ||
			n == "approvalresolved" || n == "approvalapproved" || n == "processingresumed":
			return "授权已处理，继续执行"
		case n == "posttoolusefailure":
			if tool != "" {
				return "工具失败：" + tool
			}
			return "工具失败"
		case n == "posttooluse" || n == "toolusecompleted":
			if tool != "" {
				return "工具完成：" + tool
			}
			return "继续执行"
		}
		if tool != "" {
			return "执行：" + tool
		}
		return "执行中"
	case evApprovalRequested:
		if tool != "" {
			return "等待授权：" + tool
		}
		return "等待授权"
	case evSupplementRequested:
		return "等待补充"
	case evTurnCompleted, evTurnCompletedFailed:
		if mapped == evTurnCompletedFailed {
			return "失败"
		}
		return "完成"
	}
	return ""
}

// ── 翻译 ──────────────────────────────────────────────────────────

// translate 把一个原生事件变成标准事件。
//
// 难点不在翻译，在**编号**：服务端靠 (eventId 去重, turnId 判迟到,
// sequence 判乱序) 三道闸门挡住 hook 重试和乱序投递，而原生 hook
// 不提供 turnId 和递增序号 —— 只能由适配器在跨进程状态里替它维护。
func translate(agent, native, mapped string, p map[string]any, st *sessionState, sid string) (map[string]any, bool) {
	tool := toolOf(p)
	cwd := cwdOf(p)
	project := projectOf(p, cwd)
	nativeTurn := nativeTurnID(p)

	if st.SessionID == "" {
		st.SessionID = sid
	}
	st.Seq++
	seq := st.Seq

	del := false
	var turnID string

	switch mapped {
	case evSessionStarted:
		turnID = nativeTurn
		if turnID == "" {
			turnID = st.TurnID
		}
		st.PendingApproval = ""
		st.PendingSupplement = ""
	case evSessionEnded:
		turnID = nativeTurn
		if turnID == "" {
			turnID = st.TurnID
		}
		del = true
	case evUserMessage:
		// 用户发话 = 新一轮。原生给了 turnId 就用，否则自己编号。
		if nativeTurn != "" {
			turnID = nativeTurn
		} else {
			st.Counter++
			turnID = newTurnID(sid, st.Counter)
		}
		st.TurnID = turnID
		st.PendingApproval = ""
		st.PendingSupplement = ""
	default:
		turnID = nativeTurn
		if turnID == "" {
			turnID = st.TurnID
		}
		if turnID == "" {
			st.Counter++
			turnID = newTurnID(sid, st.Counter)
			st.TurnID = turnID
		}
	}

	event := mapped
	outcome := ""
	if mapped == evTurnCompletedFailed {
		event = evTurnCompleted
		outcome = "failed"
	}

	body := map[string]any{
		"schema":     "yogo.companion.v1",
		"event":      event,
		"sessionId":  sid,
		"turnId":     turnID,
		"eventId":    randomID(),
		"agent":      agent,
		"occurredAt": time.Now().UnixMilli(),
		"sequence":   seq,
	}

	data := map[string]any{}
	if msg := messageFor(mapped, native, tool); msg != "" {
		data["message"] = clip(msg, maxMessage)
	}
	if tool != "" {
		data["tool"] = tool
	}
	if cwd != "" {
		data["cwd"] = cwd
	}
	if project != "" {
		data["project"] = project
		data["name"] = project
	}
	if host := clip(os.Getenv("YOGO_COMPANION_HOST"), 80); host != "" {
		data["host"] = host
	}
	if len(data) > 0 {
		body["data"] = data
	}
	if outcome != "" {
		body["outcome"] = outcome
	}

	rid := requestIDOf(p)
	switch mapped {
	case evApprovalRequested:
		st.PendingApproval = rid
		body["interaction"] = map[string]any{"kind": "authorization", "requestId": rid, "tool": tool}
	case evSupplementRequested:
		st.PendingSupplement = rid
		body["interaction"] = map[string]any{"kind": "supplement", "requestId": rid, "tool": tool}
	case evProcessingResumed:
		// 恢复处理时把挂着的授权销掉，否则服务端会一直停在"等待授权"。
		use := ""
		switch {
		case rid != "":
			use = rid
			if rid == st.PendingApproval {
				st.PendingApproval = ""
			} else if rid == st.PendingSupplement {
				st.PendingSupplement = ""
			}
		case st.PendingApproval != "":
			use = st.PendingApproval
			st.PendingApproval = ""
		case st.PendingSupplement != "":
			use = st.PendingSupplement
			st.PendingSupplement = ""
		}
		if use != "" {
			body["interaction"] = map[string]any{"requestId": use, "tool": tool}
		}
	case evTurnCompleted, evTurnCompletedFailed, evSessionEnded:
		st.PendingApproval = ""
		st.PendingSupplement = ""
	}

	_ = outcome
	return body, del
}

func newTurnID(sid string, counter int) string {
	s := sid
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40])
	}
	return clip(fmt.Sprintf("%s:%d", s, counter), maxTurn)
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机数不可用不该让 hook 失败，退化成时间戳
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// ── 跨进程状态 ────────────────────────────────────────────────────

// sessionState 是每个会话一个的小账本。
//
// 字段名沿用原版，换了适配器也不会把正在跑的会话打断。
type sessionState struct {
	SessionID         string `json:"sessionId"`
	TurnID            string `json:"turnId"`
	Counter           int    `json:"counter"`
	Seq               int    `json:"seq"`
	PendingApproval   string `json:"pendingApproval"`
	PendingSupplement string `json:"pendingSupplement"`
	Version           int    `json:"version"`
	Updated           int64  `json:"updated"`
}

func stateDir(arg string) string {
	d := clip(arg, 400)
	if d == "" {
		d = clip(os.Getenv("YOGO_STATE_DIR"), 400)
	}
	if d == "" {
		// 固定放用户目录：Codex 的受限 hook 环境偶尔没有 LOCALAPPDATA，
		// 放那儿会让同一个会话在两个目录之间分裂。
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = os.Getenv("USERPROFILE")
		}
		if home == "" {
			home = os.TempDir()
		}
		d = filepath.Join(home, ".yogo", "companion")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

func statePath(dir, agent, sid string) string {
	sum := sha1.Sum([]byte(agent + "\x00" + sid))
	return filepath.Join(dir, hex.EncodeToString(sum[:])[:24]+".json")
}

func loadState(path string) *sessionState {
	st := &sessionState{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, st)
	return st
}

func saveState(path string, st *sessionState) {
	st.Version = 1
	st.Updated = time.Now().Unix()

	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// withLock 是跨进程的小锁：两个 hook 同时到（并行工具调用）时别互相覆盖。
//
// 拿不到锁也照常执行 —— 宁可丢一次编号，也不能把工具卡住。
func withLock(path string, fn func() error) error {
	lock := path + ".lock"
	var fd *os.File
	for i := 0; i < 50; i++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fd = f
			break
		}
		// 陈旧的锁文件（进程被杀了没清）超过 5 秒就抢过来
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 5*time.Second {
			_ = os.Remove(lock)
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := fn()

	if fd != nil {
		_ = fd.Close()
		_ = os.Remove(lock)
	}
	return out
}

// ── 发送 ──────────────────────────────────────────────────────────

// loopbackURL 只接受回环地址。
//
// 这不是洁癖：hook 的 command 是明文配置，能被任何脚本改。
// 只收 127.0.0.1 就断了"把它改成外网地址偷数据"这条路。
func loopbackURL(arg string) string {
	raw := clip(arg, 400)
	if raw == "" {
		raw = clip(os.Getenv("YOGO_COMPANION_URL"), 400)
	}
	if raw == "" {
		port := clip(os.Getenv("YOGO_PORT"), 12)
		if port == "" {
			port = "25264"
		}
		raw = "http://127.0.0.1:" + port + "/events"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return ""
	}
	return raw
}

var postClient = &http.Client{Timeout: 350 * time.Millisecond}

// postEvent 把事件送出去。失败就算了 —— 键盘没开、服务没起，
// 都不该让用户的工具等下去。
func postEvent(u string, body map[string]any) bool {
	raw, err := json.Marshal(body)
	if err != nil {
		return false
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(raw))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := postClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			return resp.StatusCode < 300
		}
		if attempt == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return false
}
