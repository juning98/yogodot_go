package agent

import (
	"testing"
	"time"
)

func TestSnapshotIsReadOnlyAndDoesNotAcquireWriterLock(t *testing.T) {
	s := NewStore()
	s.Apply(evWith(EvSessionStarted, "s1", "", "", 1000))
	s.Apply(evWith(EvUserMessage, "s1", "t1", "", 2000))
	s.mu.Lock()
	s.sessions["s1"].At = time.Now().Add(-3 * time.Hour).Unix()
	done := make(chan Snapshot, 1)
	go func() { done <- s.Snapshot() }()
	select {
	case snap := <-done:
		if len(snap.Sessions) != 1 {
			s.mu.Unlock()
			t.Fatal("read cleaned session")
		}
	case <-time.After(300 * time.Millisecond):
		s.mu.Unlock()
		t.Fatal("read waits on writer")
	}
	s.mu.Unlock()
	if !s.Prune(time.Now()) || len(s.Snapshot().Sessions) != 0 {
		t.Fatal("background prune failed")
	}
}
