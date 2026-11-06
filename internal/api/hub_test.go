package api

import (
	"fmt"
	"testing"
)

func TestSlowSubscriberRetainsLatestOfBothEventTypes(t *testing.T) {
	h := newHub()
	sub := h.add()
	defer h.remove(sub)
	h.send([]byte("event: state\ndata: latest-state\n\n"))
	for i := 0; i < 100; i++ {
		h.send([]byte(fmt.Sprintf("event: frame\ndata: %d\n\n", i)))
	}
	<-sub.ready
	state, frame := h.take(sub)
	if string(state) != "event: state\ndata: latest-state\n\n" || string(frame) != "event: frame\ndata: 99\n\n" {
		t.Fatalf("lost latest values: %q %q", state, frame)
	}
	h.send([]byte("event: state\ndata: newer\n\n"))
	h.send([]byte("event: state\ndata: newest\n\n"))
	<-sub.ready
	state, frame = h.take(sub)
	if string(state) != "event: state\ndata: newest\n\n" || frame != nil {
		t.Fatalf("replayed stale values: %q %q", state, frame)
	}
}
