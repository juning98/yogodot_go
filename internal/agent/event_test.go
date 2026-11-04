package agent

import "testing"

// 契约客户端把 outcome 发成对象：{"status":"failed"}。
func TestParseOutcomeObject(t *testing.T) {
	ev, err := ParseEvent(map[string]any{
		"event":     "turn_completed",
		"sessionId": "s1",
		"outcome":   map[string]any{"status": "failed"},
	})
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Outcome != "failed" {
		t.Fatalf("outcome = %q，期望 failed", ev.Outcome)
	}
}

// 旧的字符串写法继续可用；canceled / cancelled 统一成一种拼写。
func TestParseOutcomeString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"failed", "failed"},
		{"cancelled", "cancelled"},
		{"canceled", "cancelled"},
	}
	for _, c := range cases {
		ev, err := ParseEvent(map[string]any{
			"event":     "turn_completed",
			"sessionId": "s1",
			"outcome":   c.in,
		})
		if err != nil {
			t.Fatalf("ParseEvent(%q): %v", c.in, err)
		}
		if ev.Outcome != c.want {
			t.Errorf("outcome %q = %q，期望 %q", c.in, ev.Outcome, c.want)
		}
	}
}

// 契约用 source，旧版用 agent；两者都认。
func TestParseSourceAsAgent(t *testing.T) {
	ev, err := ParseEvent(map[string]any{
		"event":     "session_started",
		"sessionId": "s1",
		"source":    "my-tool",
	})
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Agent != "my-tool" {
		t.Fatalf("agent = %q，期望 my-tool", ev.Agent)
	}
}
