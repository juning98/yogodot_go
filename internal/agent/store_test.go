package agent

import (
	"testing"
	"time"
)

func mkEvent(name EventName, sid, turn string) Event {
	return Event{
		Name:      name,
		SessionID: sid,
		TurnID:    turn,
		EventID: autoEventID(Event{Name: name, SessionID: sid, TurnID: turn,
			Sequence: 0, OccurredAt: 0, RequestID: "", Message: ""}),
		Agent: "claude-code",
	}
}

func evWith(name EventName, sid, turn, rid string, ms int64) Event {
	e := Event{
		Name: name, SessionID: sid, TurnID: turn, RequestID: rid,
		OccurredAt: ms, Agent: "claude-code",
	}
	e.EventID = autoEventID(e)
	return e
}

// TestBasicLifecycle 走一遍最典型的流程：
// 开会话 → 提交任务 → 请求授权 → 恢复 → 完成。
func TestBasicLifecycle(t *testing.T) {
	s := NewStore()

	if out := s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000)); out.State != StateIdle {
		t.Fatalf("新会话应为 idle，实际 %v", out.State)
	}
	if out := s.Apply(evWith(EvUserMessage, "s1", "t1", "", 2000)); out.State != StateBusy {
		t.Fatalf("提交任务后应为 busy，实际 %v", out.State)
	}
	out := s.Apply(evWith(EvApprovalRequested, "s1", "t1", "r1", 3000))
	if out.State != StateWait || out.Pending != 1 {
		t.Fatalf("请求授权后应为 wait/pending=1，实际 %v/%d", out.State, out.Pending)
	}
	if out := s.Apply(evWith(EvProcessingResumed, "s1", "t1", "r1", 4000)); out.State != StateBusy {
		t.Fatalf("解除授权后应回到 busy，实际 %v", out.State)
	}
	if out := s.Apply(evWith(EvTurnCompleted, "s1", "t1", "", 5000)); out.State != StateDone {
		t.Fatalf("完成后应为 done，实际 %v", out.State)
	}
}

// TestPendingNotClearedByUnrelatedResume 是这条状态机最微妙的地方：
// 另一个 requestId 的 resumed 不该把"等你授权"抹掉。
func TestPendingNotClearedByUnrelatedResume(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(evWith(EvApprovalRequested, "s1", "t1", "r1", 2000))

	// 另一个授权回来了 —— 不该动 r1
	out := s.Apply(evWith(EvProcessingResumed, "s1", "t1", "r-other", 3000))
	if out.State != StateWait {
		t.Fatalf("不相关的 resumed 不该解除等待，实际 %v", out.State)
	}
	if out.Pending != 1 {
		t.Fatalf("待处理数应仍为 1，实际 %d", out.Pending)
	}

	// 真正的那条回来了
	if out := s.Apply(evWith(EvProcessingResumed, "s1", "t1", "r1", 4000)); out.State != StateBusy {
		t.Fatalf("对应 requestId 回来才该解除，实际 %v", out.State)
	}
}

// TestDuplicateIsIgnored 确认同一 eventId 只算一次（hook 会重试）。
func TestDuplicateIsIgnored(t *testing.T) {
	s := NewStore()
	ev := evWith(EvApprovalRequested, "s1", "t1", "r1", 2000)

	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(ev)

	out := s.Apply(ev) // 重放
	if !out.Duplicate {
		t.Fatal("同一 eventId 应被判定为重复")
	}
	if out.Pending != 1 {
		t.Fatalf("重复事件不该再加一条待处理，实际 %d", out.Pending)
	}
}

// TestLateEventAfterTurnCompleted 确认一轮结束后，属于那轮的迟到事件被丢掉。
//
// 场景：工具事件比 turn_completed 到得还晚（网络/进程调度都可能）。
// 不挡的话，界面会在"完成"之后又跳回"执行中"。
func TestLateEventAfterTurnCompleted(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(evWith(EvUserMessage, "s1", "t1", "", 2000))
	s.Apply(evWith(EvTurnCompleted, "s1", "t1", "", 3000))

	out := s.Apply(evWith(EvProcessingResumed, "s1", "t1", "", 3500))
	if !out.Stale {
		t.Fatalf("已完成的轮次里的事件应被判定过期，实际 %+v", out)
	}
	if out.State != StateDone {
		t.Fatalf("过期事件不该改变状态，实际 %v", out.State)
	}
}

// TestSequenceOutOfOrder 确认同一轮里序号倒退的事件被丢掉。
func TestSequenceOutOfOrder(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))

	first := evWith(EvUserMessage, "s1", "t1", "", 2000)
	first.Sequence = 5
	s.Apply(first)

	old := evWith(EvApprovalRequested, "s1", "t1", "r1", 2100)
	old.Sequence = 3
	out := s.Apply(old)
	if !out.Stale {
		t.Fatalf("序号倒退的事件应被判定过期，实际 %+v", out)
	}
}

// TestExpiredEventBeforeSessionStart 确认早于会话的大量旧事件不生效。
func TestExpiredEventBeforeSessionStart(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 100000))
	out := s.Apply(evWith(EvUserMessage, "s1", "", "", 100000-60000))
	if !out.Stale {
		t.Fatalf("一分钟前的事件应被判过期，实际 %+v", out)
	}
}

// TestSessionEndedBlocksLateEvents 确认 session_ended 之后的迟到事件被挡。
func TestSessionEndedBlocksLateEvents(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	out := s.Apply(evWith(EvSessionEnded, "s1", "", "", 2000))
	if !out.Ended {
		t.Fatal("session_ended 应报告 ended")
	}

	late := s.Apply(evWith(EvApprovalRequested, "s1", "", "r1", 3000))
	if !late.Stale || !late.Ended {
		t.Fatalf("会话结束后的迟到事件应被丢弃，实际 %+v", late)
	}
	if len(s.Snapshot().Sessions) != 0 {
		t.Fatal("会话应该已经不在列表里")
	}
}

// TestPriorityOrdering 确认快照按优先级排序，wait/ask 同级。
func TestPriorityOrdering(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "a", "", "", 1000))
	s.Apply(evWith(EvSessionStarted, "b", "", "", 1100))
	s.Apply(evWith(EvSessionStarted, "c", "", "", 1200))

	s.Apply(evWith(EvUserMessage, "a", "t", "", 2000))        // busy
	s.Apply(evWith(EvApprovalRequested, "b", "t", "r", 2100)) // wait
	s.Apply(evWith(EvTurnCompleted, "c", "t", "", 2200))      // done

	snap := s.Snapshot()
	if snap.Top == nil || snap.Top.SessionID != "b" {
		t.Fatalf("最该提醒的应是 wait 的 b，实际 %v", snap.Top)
	}
	got := []string{}
	for _, r := range snap.Sessions {
		got = append(got, r.SessionID)
	}
	want := []string{"b", "a", "c"} // wait > busy > done
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序应为 %v，实际 %v", want, got)
		}
	}
}

// TestIdleAfterInactivity 确认 15 分钟没动静就判空闲。
func TestIdleAfterInactivity(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(evWith(EvUserMessage, "s1", "t1", "", 2000))

	// 手动把 at 推到 16 分钟前
	s.mu.Lock()
	s.sessions["s1"].At = time.Now().Add(-16 * time.Minute).Unix()
	s.mu.Unlock()

	snap := s.Snapshot()
	if len(snap.Sessions) != 1 {
		t.Fatalf("会话不该被移除，实际 %d 条", len(snap.Sessions))
	}
	if snap.Sessions[0].State != StateIdle {
		t.Fatalf("16 分钟没动静应判空闲，实际 %v", snap.Sessions[0].State)
	}
}

// TestDropAfterLongInactivity 确认 2 小时没动静就从列表移除。
func TestDropAfterLongInactivity(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))

	s.mu.Lock()
	s.sessions["s1"].At = time.Now().Add(-3 * time.Hour).Unix()
	s.mu.Unlock()

	if n := len(s.Snapshot().Sessions); n != 0 {
		t.Fatalf("3 小时没动静应移除，实际还剩 %d 条", n)
	}
}

// TestTurnCompletedFailed 确认 outcome=failed 映射到 error。
func TestTurnCompletedFailed(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	e := evWith(EvTurnCompleted, "s1", "t1", "", 2000)
	e.Outcome = "failed"
	if out := s.Apply(e); out.State != StateError {
		t.Fatalf("失败的一轮应为 error，实际 %v", out.State)
	}
}

// TestTurnCompletedCancelled 确认主动取消回到 idle：不亮 ✓ 也不亮 ✕。
func TestTurnCompletedCancelled(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	e := evWith(EvTurnCompleted, "s1", "t1", "", 2000)
	e.Outcome = "cancelled"
	if out := s.Apply(e); out.State != StateIdle {
		t.Fatalf("取消的一轮应为 idle，实际 %v", out.State)
	}
}

// TestResumeWithoutRequestIDClearsOne 确认不带 requestId 的 resumed 能解除一条。
func TestResumeWithoutRequestIDClearsOne(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(evWith(EvApprovalRequested, "s1", "t1", "r1", 2000))

	if out := s.Apply(evWith(EvProcessingResumed, "s1", "t1", "", 3000)); out.State != StateBusy {
		t.Fatalf("不带 requestId 的 resumed 应解除等待，实际 %v", out.State)
	}
}

// TestClear 确认主动清理。
func TestClear(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "a", "", "", 1000))
	s.Apply(evWith(EvSessionStarted, "b", "", "", 1100))

	if n := s.Clear("a", false); n != 1 {
		t.Fatalf("清一个应返回 1，实际 %d", n)
	}
	if n := len(s.Snapshot().Sessions); n != 1 {
		t.Fatalf("应剩 1 条，实际 %d", n)
	}
	if n := s.Clear("", true); n != 1 {
		t.Fatalf("全清应返回 1，实际 %d", n)
	}
	if n := len(s.Snapshot().Sessions); n != 0 {
		t.Fatal("全清后应为空")
	}
}
