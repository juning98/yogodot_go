package agent

import (
	"container/list"
	"sort"
	"sync"
	"time"
)

// Session 是一个被跟踪的会话。
type Session struct {
	Key       string `json:"key"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	State     State  `json:"state"`
	Prev      State  `json:"prev"`
	Tool      string `json:"tool"`
	Cwd       string `json:"cwd"`
	Project   string `json:"project"`
	Host      string `json:"host"`
	Msg       string `json:"msg"`
	Index     string `json:"idx"`
	TurnID    string `json:"turnId"`
	RequestID string `json:"requestId"`
	Pending   int    `json:"pending"`
	Sequence  int64  `json:"seq"`
	EventAt   int64  `json:"eventAt"`
	At        int64  `json:"at"` // unix 秒

	completed map[string]struct{} // 最近完成的 turn，挡迟到事件
	pending   map[string]pendingItem
}

type pendingItem struct {
	event  EventName
	turnID string
	atMS   int64
}

// Snapshot 是一次查询的结果。
type Snapshot struct {
	Sessions []Session `json:"sessions"`
	Top      *Session  `json:"top"`
}

// Outcome 是一条事件的处理结果，直接映射成 HTTP 响应。
type Outcome struct {
	OK        bool   `json:"ok"`
	Event     string `json:"event,omitempty"`
	Session   string `json:"sessionId,omitempty"`
	State     State  `json:"state,omitempty"`
	Pending   int    `json:"pending,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Stale     bool   `json:"stale,omitempty"`
	Ended     bool   `json:"ended,omitempty"`
	Removed   int    `json:"removed,omitempty"`
}

// Store 是会话表 + 事件治理。
//
// 所有方法并发安全。设计上刻意把所有"防脏"逻辑关在一个 mutex 里：
// 去重窗口、乱序宽限、待处理账本、迟到事件 —— 它们互相耦合，
// 拆开各自加锁反而更难推。
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	seen     *lru              // eventId 去重
	requests map[string]string // requestId -> session key
	ended    map[string]int64  // sessionId -> 结束时刻（毫秒）
	endedLRU *list.List
	// endedIdx 给每个 key 定位它在 LRU 里的唯一节点：
	// 重复 end 或结束后复活再 end 不能叠加节点,否则驱逐逻辑会
	// 顺着陈旧节点提前删掉该会话较新的 ended 记录。
	endedIdx map[string]*list.Element
}

// NewStore 创建一个空的会话表。
func NewStore() *Store {
	return &Store{
		sessions: map[string]*Session{},
		seen:     newLRU(seenMax),
		requests: map[string]string{},
		ended:    map[string]int64{},
		endedLRU: list.New(),
		endedIdx: map[string]*list.Element{},
	}
}

func nowMS() int64 { return time.Now().UnixMilli() }

// Apply 处理一条标准事件。
func (s *Store) Apply(ev Event) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	out := Outcome{OK: true, Event: string(ev.Name), Session: ev.SessionID}

	// ── 去重：同一 eventId 只算一次 ──
	if s.seen.has(ev.EventID) {
		out.Duplicate = true
		if cur := s.sessions[ev.SessionID]; cur != nil {
			out.State = cur.State
			out.Pending = cur.Pending
		}
		return out
	}
	s.seen.add(ev.EventID)

	atMS := ev.OccurredAt
	if atMS == 0 {
		atMS = now.UnixMilli()
	}

	// ── 已结束的会话：之后到达的事件一律当过期 ──
	if ev.Name != EvSessionStarted && ev.Name != EvUserMessage {
		if endedAt, ok := s.ended[ev.SessionID]; ok && atMS <= endedAt+graceMS {
			out.Stale = true
			out.Ended = true
			out.State = StateIdle
			return out
		}
	}

	cur := s.sessions[ev.SessionID]
	if cur == nil {
		cur = s.newSession(ev, now)
		s.sessions[ev.SessionID] = cur
	} else if ev.Name == EvSessionStarted {
		// 重开：只有"比现有更新"或"同一毫秒且现态已收尾"才算
		if atMS < cur.EventAt || (atMS == cur.EventAt && !isSettled(cur.State)) {
			out.Stale = true
			out.State = cur.State
			out.Pending = cur.Pending
			return out
		}
	}

	// ── 迟到 / 乱序判定 ──
	if isStaleLocked(cur, ev, atMS) {
		out.Stale = true
		out.State = cur.State
		out.Pending = cur.Pending
		return out
	}

	// ── 应用 ──
	switch ev.Name {
	case EvSessionStarted:
		cur.Prev = cur.State
		cur.State = StateIdle
		cur.completed = map[string]struct{}{}
		cur.pending = map[string]pendingItem{}
		delete(s.ended, ev.SessionID)
		if e, ok := s.endedIdx[ev.SessionID]; ok {
			s.endedLRU.Remove(e)
			delete(s.endedIdx, ev.SessionID)
		}
	case EvSessionEnded:
		s.dropLocked(ev.SessionID)
		s.markEndedLocked(ev.SessionID, atMS)
		out.Ended = true
		out.Removed = 1
		out.State = StateIdle
		return out
	case EvTurnCompleted:
		next := StateDone
		switch ev.Outcome {
		case "failed":
			next = StateError
		case "cancelled":
			// 用户主动取消：不亮 ✓ 也不亮 ✕，回到空闲
			next = StateIdle
		}
		markCompleted(cur, ev.TurnID)
		// 这一轮结束了，它挂的"等人"账目一并清掉
		s.dropAttentionLocked(cur, ev.RequestID)
		cur.Prev = cur.State
		cur.State = next
	case EvProcessingResumed:
		if s.resolveAttentionLocked(cur, ev.RequestID) {
			cur.Prev = cur.State
			cur.State = StateBusy
		}
		// 没解除对应授权就维持 wait/ask —— 别把"等人"抹掉
	default:
		if st, ok := eventState[ev.Name]; ok {
			cur.Prev = cur.State
			cur.State = st
			if ev.Name == EvApprovalRequested || ev.Name == EvSupplementRequested {
				s.addAttentionLocked(cur, ev, atMS)
			}
		}
	}

	// ── 更新元数据 ──
	if ev.Agent != "" {
		cur.Agent = ev.Agent
	}
	cur.At = now.Unix()
	cur.EventAt = atMS
	if ev.TurnID != "" {
		cur.TurnID = ev.TurnID
	}
	if ev.Sequence > 0 {
		cur.Sequence = ev.Sequence
	}
	setIf(&cur.Tool, ev.Tool)
	setIf(&cur.Cwd, ev.Cwd)
	setIf(&cur.Project, ev.Project)
	setIf(&cur.Host, ev.Host)
	setIf(&cur.Index, ev.Index)
	setIf(&cur.Msg, ev.Message)
	if ev.NameHint != "" {
		cur.Name = ev.NameHint
	}

	out.State = cur.State
	out.Pending = cur.Pending
	return out
}

func (s *Store) newSession(ev Event, now time.Time) *Session {
	name := ev.NameHint
	if name == "" {
		name = ev.SessionID
	}
	if len(name) > 60 {
		name = name[:60]
	}
	return &Session{
		Key:       ev.SessionID,
		SessionID: ev.SessionID,
		Name:      name,
		Agent:     ev.Agent,
		State:     StateIdle,
		Prev:      StateIdle,
		At:        now.Unix(),
		EventAt:   ev.OccurredAt,
		completed: map[string]struct{}{},
		pending:   map[string]pendingItem{},
	}
}

func isSettled(st State) bool {
	return st == StateIdle || st == StateDone || st == StateError
}

func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// isStaleLocked 判断一条事件是否已经过期（迟到、乱序、来回头的旧轮次）。
func isStaleLocked(cur *Session, ev Event, atMS int64) bool {
	// 这一轮已经结束了，后面还会有工具事件迟到
	if ev.TurnID != "" {
		if _, done := cur.completed[ev.TurnID]; done {
			return true
		}
		// 同一轮里序号倒退
		if ev.TurnID == cur.TurnID && ev.Sequence > 0 &&
			cur.Sequence > 0 && ev.Sequence <= cur.Sequence {
			return true
		}
	}
	// 时间上明显更旧
	if cur.EventAt > 0 && atMS+graceMS < cur.EventAt {
		return true
	}
	return false
}

func markCompleted(cur *Session, turnID string) {
	if turnID == "" {
		return
	}
	if cur.completed == nil {
		cur.completed = map[string]struct{}{}
	}
	if _, ok := cur.completed[turnID]; !ok {
		cur.completed[turnID] = struct{}{}
		// 超上限就忘掉最早的（map 无序，够用）
		for len(cur.completed) > turnsMax {
			for k := range cur.completed {
				if k != turnID {
					delete(cur.completed, k)
					break
				}
			}
		}
	}
}

func (s *Store) addAttentionLocked(cur *Session, ev Event, atMS int64) {
	if cur.pending == nil {
		cur.pending = map[string]pendingItem{}
	}
	rid := ev.RequestID
	cur.pending[rid] = pendingItem{event: ev.Name, turnID: ev.TurnID, atMS: atMS}
	if rid != "" {
		s.requests[rid] = cur.Key
		for len(s.requests) > requestsMax {
			for k := range s.requests {
				delete(s.requests, k)
				break
			}
		}
	}
	for len(cur.pending) > pendingMax {
		for k := range cur.pending {
			if k != rid {
				delete(cur.pending, k)
				delete(s.requests, k)
				break
			}
		}
	}
	cur.Pending = len(cur.pending)
	cur.RequestID = rid
}

// dropAttentionLocked 清掉待处理授权；rid 为空表示全清。
func (s *Store) dropAttentionLocked(cur *Session, rid string) int {
	if len(cur.pending) == 0 {
		return 0
	}
	n := 0
	if rid == "" {
		for k := range cur.pending {
			delete(s.requests, k)
			delete(cur.pending, k)
			n++
		}
	} else if _, ok := cur.pending[rid]; ok {
		delete(s.requests, rid)
		delete(cur.pending, rid)
		n = 1
	}
	cur.Pending = len(cur.pending)
	if cur.Pending == 0 {
		cur.RequestID = ""
	}
	return n
}

// resolveAttentionLocked 判断 processing_resumed 能不能解除等待态。
func (s *Store) resolveAttentionLocked(cur *Session, rid string) bool {
	if len(cur.pending) == 0 {
		return true
	}
	if rid != "" {
		if _, ok := cur.pending[rid]; ok {
			s.dropAttentionLocked(cur, rid)
			return true
		}
		return false // 它等的是别人，维持原状
	}
	// 没带 requestId：当作解除最先挂的那条
	for k := range cur.pending {
		s.dropAttentionLocked(cur, k)
		return true
	}
	return false
}

func (s *Store) dropLocked(key string) {
	if cur, ok := s.sessions[key]; ok {
		for rid := range cur.pending {
			delete(s.requests, rid)
		}
		delete(s.sessions, key)
	}
}

func (s *Store) markEndedLocked(key string, atMS int64) {
	if atMS == 0 {
		atMS = nowMS()
	}
	if old, ok := s.ended[key]; !ok || atMS > old {
		s.ended[key] = atMS
	}
	// 一个 key 一个节点：重复 end 时挪到队尾而不是再压一个。
	if e, ok := s.endedIdx[key]; ok {
		s.endedLRU.MoveToBack(e)
	} else {
		s.endedIdx[key] = s.endedLRU.PushBack(key)
	}
	for len(s.ended) > endedMax {
		front := s.endedLRU.Front()
		if front == nil {
			break
		}
		s.endedLRU.Remove(front)
		if k, ok := front.Value.(string); ok {
			delete(s.endedIdx, k)
			if k != key {
				delete(s.ended, k)
			}
		}
	}
}

// Snapshot 返回当前所有会话，外加"最该提醒你的那一个"。
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(time.Now())
}

func (s *Store) snapshotLocked(now time.Time) Snapshot {
	var rows []Session
	var dead []string

	for key, cur := range s.sessions {
		age := now.Unix() - cur.At
		if age > int64(DropAfter/time.Second) {
			dead = append(dead, key)
			continue
		}
		// 15 分钟没动静：不管之前是什么状态，都判空闲
		if cur.State != StateIdle && age > int64(IdleAfter/time.Second) {
			cur.Prev = cur.State
			cur.State = StateIdle
			s.dropAttentionLocked(cur, "")
		}
		rows = append(rows, *cur)
	}
	for _, k := range dead {
		s.dropLocked(k)
	}

	sort.Slice(rows, func(i, j int) bool {
		pi, pj := Priority(rows[i].State), Priority(rows[j].State)
		if pi != pj {
			return pi > pj
		}
		if rows[i].At != rows[j].At {
			return rows[i].At > rows[j].At
		}
		return rows[i].EventAt > rows[j].EventAt
	})

	snap := Snapshot{Sessions: rows}
	if len(rows) > 0 {
		top := rows[0]
		snap.Top = &top
	}
	return snap
}

// Clear 主动清理会话。all 为 true 时清空全部。
func (s *Store) Clear(sessionID string, all bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var keys []string
	if all {
		for k := range s.sessions {
			keys = append(keys, k)
		}
	} else if _, ok := s.sessions[sessionID]; ok {
		keys = append(keys, sessionID)
	}
	for _, k := range keys {
		s.dropLocked(k)
	}
	return len(keys)
}

// ── 极简 LRU ──────────────────────────────────────────────────────

type lru struct {
	cap   int
	order *list.List
	index map[string]*list.Element
}

func newLRU(capacity int) *lru {
	return &lru{cap: capacity, order: list.New(), index: map[string]*list.Element{}}
}

func (l *lru) has(k string) bool {
	_, ok := l.index[k]
	return ok
}

func (l *lru) add(k string) {
	if e, ok := l.index[k]; ok {
		l.order.MoveToBack(e)
		return
	}
	l.index[k] = l.order.PushBack(k)
	for len(l.index) > l.cap {
		front := l.order.Front()
		if front == nil {
			break
		}
		l.order.Remove(front)
		delete(l.index, front.Value.(string))
	}
}
