// Package agent 是 Coding 伴侣的状态机：把四家 AI 工具的原生 hook
// 归一成 7 个标准事件，维护一份"当前有哪些会话、各自什么状态"的表。
//
// 移植自旧版 server.py 的 apply_event 一整套语义 —— 那部分做得不错，
// 尤其是对乱序、重复、迟到事件的处理。这里逐条保留，并补了单元测试。
package agent

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// State 是会话状态。
type State string

// 六种状态。wait 和 ask 同级（都是"等人"），其余按数值比优先级。
const (
	StateIdle  State = "idle"
	StateBusy  State = "busy"
	StateWait  State = "wait"
	StateAsk   State = "ask"
	StateDone  State = "done"
	StateError State = "error"
)

// priority 决定"当前最该提醒你的是哪个会话"。
var priority = map[State]int{
	StateWait:  5,
	StateAsk:   5,
	StateError: 4,
	StateBusy:  3,
	StateDone:  2,
	StateIdle:  1,
}

// Priority 返回状态的优先级数值。
func Priority(s State) int {
	if p, ok := priority[s]; ok {
		return p
	}
	return 0
}

// EventName 是标准事件名。
type EventName string

// 7 个标准事件。适配器负责把各家的原生 hook 翻译成这些。
const (
	EvSessionStarted      EventName = "session_started"
	EvSessionEnded        EventName = "session_ended"
	EvUserMessage         EventName = "user_message_submitted"
	EvProcessingResumed   EventName = "processing_resumed"
	EvApprovalRequested   EventName = "approval_requested"
	EvSupplementRequested EventName = "supplement_requested"
	EvTurnCompleted       EventName = "turn_completed"
)

// eventState 是事件直接映射到的状态；空表示要按上下文决定。
var eventState = map[EventName]State{
	EvUserMessage:         StateBusy,
	EvProcessingResumed:   StateBusy,
	EvApprovalRequested:   StateWait,
	EvSupplementRequested: StateAsk,
}

// 别名表：不同工具/旧版本用的名字。
var eventAliases = map[string]EventName{
	"session_start":       EvSessionStarted,
	"session_end":         EvSessionEnded,
	"user_message":        EvUserMessage,
	"prompt_submitted":    EvUserMessage,
	"processing_started":  EvProcessingResumed,
	"approval_required":   EvApprovalRequested,
	"supplement_required": EvSupplementRequested,
	"turn_complete":       EvTurnCompleted,
}

// 厂商名归一化。
var agentAliases = map[string]string{
	"claude":       "claude-code",
	"claude_code":  "claude-code",
	"claudecode":   "claude-code",
	"claude-code":  "claude-code",
	"claude code":  "claude-code",
	"codex":        "codex",
	"openai-codex": "codex",
	"openai_codex": "codex",
	"opencode":     "opencode",
	"open-code":    "opencode",
	"open_code":    "opencode",
}

// Event 是一条已解析、已校验的标准事件。
type Event struct {
	Schema     string
	Name       EventName
	SessionID  string
	TurnID     string
	EventID    string
	Agent      string
	OccurredAt int64 // 毫秒
	Sequence   int64
	RequestID  string
	Outcome    string
	Tool       string
	Message    string
	Cwd        string
	Project    string
	NameHint   string
	Host       string
	Index      string
}

// 上限，防止长时间运行被撑爆。
const (
	seenMax     = 1024         // eventId 去重窗口
	graceMS     = int64(15000) // 15 秒内的乱序仍然接受
	turnsMax    = 8            // 每个会话记住最近几轮已完成
	pendingMax  = 8            // 每个会话最多挂几个待处理授权
	requestsMax = 2048         // 全局待处理账本
	endedMax    = 512          // 已结束会话的记忆

	// IdleAfter 是多久没动静就把会话判成空闲。
	IdleAfter = 15 * time.Minute
	// DropAfter 是多久没动静就从列表里移除。
	DropAfter = 2 * time.Hour
)

// 解析错误。都对应 HTTP 400。
var (
	ErrNotObject    = errors.New("body 必须是 JSON 对象")
	ErrUnknownEvent = errors.New("未知事件")
	ErrNoSession    = errors.New("sessionId 必填")
	ErrBadSchema    = errors.New("不支持的 schema")
)

// ParseEvent 把原始 JSON 对象解析成标准事件。
//
// 字段别名很多是历史包袱：四家工具的 hook 各有各的写法。
// 这里全部收口，上层只认 Event。
func ParseEvent(d map[string]any) (Event, error) {
	if d == nil {
		return Event{}, ErrNotObject
	}
	if raw, ok := d["_raw"]; ok && len(d) == 1 {
		_ = raw
		return Event{}, errors.New("JSON 体无效")
	}

	var ev Event
	ev.Schema = text(d["schema"], 40)
	if ev.Schema != "" && !validSchema(ev.Schema) {
		return Event{}, fmt.Errorf("%w: %s", ErrBadSchema, ev.Schema)
	}

	rawName := d["event"]
	if rawName == nil {
		rawName = firstNonNil(d["eventType"], d["event_type"], d["type"])
	}
	s, ok := rawName.(string)
	if !ok {
		return Event{}, errors.New("event 必须是字符串")
	}
	name := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
	if alias, ok := eventAliases[name]; ok {
		name = string(alias)
	}
	ev.Name = EventName(name)
	if !knownEvent(ev.Name) {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownEvent, s)
	}

	data := asMap(d["data"])
	inter := asMap(d["interaction"])

	ev.SessionID = text(firstNonNil(d["sessionId"], d["session_id"], d["session"],
		data["sessionId"], data["session_id"]), 120)
	if ev.SessionID == "" {
		return Event{}, ErrNoSession
	}
	ev.TurnID = text(firstNonNil(d["turnId"], d["turn_id"], d["turn"],
		data["turnId"], data["turn_id"]), 80)
	ev.EventID = text(firstNonNil(d["eventId"], d["event_id"], d["id"], data["eventId"]), 120)
	ev.Agent = normalizeAgent(firstNonNil(d["agent"], d["source"],
		data["agent"], data["source"]))
	ev.OccurredAt = toMillis(firstNonNil(d["occurredAt"], d["occurred_at"],
		d["timestamp"], d["ts"]))
	ev.Sequence = toInt(d["sequence"])
	ev.RequestID = text(firstNonNil(d["requestId"], d["request_id"],
		inter["requestId"], inter["request_id"], data["requestId"]), 120)
	ev.Outcome = outcomeStatus(d)
	ev.Tool = text(firstNonNil(inter["tool"], d["toolName"], data["tool"]), 80)
	ev.Message = text(firstNonNil(data["message"], d["message"]), 200)
	ev.Cwd = text(firstNonNil(data["cwd"], d["cwd"]), 260)
	ev.Project = text(firstNonNil(data["project"], d["project"], data["projectName"]), 120)
	ev.NameHint = text(firstNonNil(data["sessionName"], data["name"], d["name"]), 120)
	ev.Host = text(firstNonNil(data["host"], d["host"]), 80)
	ev.Index = text(firstNonNil(data["index"], data["idx"], d["idx"]), 20)

	if ev.EventID == "" {
		ev.EventID = autoEventID(ev)
	}
	return ev, nil
}

// outcomeStatus 读"这一轮的结果"。
//
// 契约版是对象 {"status":"failed",...}，旧版是字符串 "failed"，两种都收；
// 拼写统一成 cancelled，状态机只认这一种。
func outcomeStatus(d map[string]any) string {
	var raw any = d["outcome"]
	if m := asMap(raw); m != nil {
		raw = firstNonNil(m["status"], m["outcome"], m["result"])
	}
	s := strings.ToLower(text(firstNonNil(raw, d["outcomeStatus"], d["outcome_status"]), 40))
	if s == "canceled" {
		return "cancelled"
	}
	return s
}

// autoEventID 给没带 eventId 的调用方生成一个稳定 id。
//
// 必须稳定：适配器重试同一条事件时，两次生成的 id 要一样，
// 否则去重窗口形同虚设。
func autoEventID(ev Event) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%s|%s|%d|%d|%s|%s", ev.SessionID, ev.TurnID,
		string(ev.Name), ev.Sequence, ev.OccurredAt, ev.RequestID, ev.Message)
	return "auto:" + hex.EncodeToString(h.Sum(nil))[:16]
}

func knownEvent(n EventName) bool {
	switch n {
	case EvSessionStarted, EvSessionEnded, EvUserMessage, EvProcessingResumed,
		EvApprovalRequested, EvSupplementRequested, EvTurnCompleted:
		return true
	}
	return false
}

func validSchema(s string) bool {
	s = strings.TrimSuffix(s, " ")
	if !strings.HasPrefix(s, "v1") &&
		!strings.HasPrefix(s, "companion.v1") &&
		!strings.HasPrefix(s, "yogo.companion.v1") {
		return false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(
		s, "yogo.companion.v1"), "companion.v1"), "v1")
	return rest == "" || strings.HasPrefix(rest, ".")
}

// NormalizeName 把任意工具名归一化成事件里 agent 字段的形态。
// 自定义工具外形按它生成 id —— 用户配 hook 时 --agent 填这个 id 就能对上。
func NormalizeName(s string) string { return normalizeAgent(s) }

func normalizeAgent(v any) string {
	s := strings.ToLower(text(v, 40))
	if s == "" {
		return "claude-code"
	}
	if a, ok := agentAliases[s]; ok {
		return a
	}
	// 保留字母数字与 . _ -，其余压成 -
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		return v
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func text(v any, max int) string {
	var s string
	switch t := v.(type) {
	case string:
		s = strings.TrimSpace(t)
	case float64:
		s = strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		s = strconv.Itoa(t)
	case int64:
		s = strconv.FormatInt(t, 10)
	case bool:
		return ""
	default:
		return ""
	}
	if len(s) > max {
		// 按 rune 截断，别把汉字切一半
		r := []rune(s)
		if len(r) > max {
			r = r[:max]
		}
		s = string(r)
	}
	return s
}

func toInt(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int:
		return int64(t)
	case int64:
		return t
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	}
	return 0
}

// toMillis 接受毫秒、秒、数字字符串或 ISO 8601。
func toMillis(v any) int64 {
	switch t := v.(type) {
	case bool:
		return 0
	case float64:
		if t <= 0 {
			return 0
		}
		if t < 1e11 {
			return int64(t * 1000)
		}
		return int64(t)
	case int64:
		if t <= 0 {
			return 0
		}
		if t < 1e11 {
			return t * 1000
		}
		return t
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return toMillis(f)
		}
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05",
		} {
			if tm, err := time.Parse(layout, s); err == nil {
				return tm.UnixMilli()
			}
		}
		return 0
	}
	return 0
}
