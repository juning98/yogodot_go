package store

import (
	"testing"
	"time"
)

func TestSnapshotsDoNotWaitForDiskWriterOrReaderCallbacks(t *testing.T) {
	s := Defaults()
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() { s.View(func(*Settings) { close(entered); <-release }); close(done) }()
	<-entered
	defer func() { close(release); <-done }()
	read := make(chan Config, 1)
	go func() { read <- s.Snapshot() }()
	select {
	case <-read:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("snapshot waits for writer/reader IO")
	}
}
func TestSnapshotOwnsPointerFields(t *testing.T) {
	s := Defaults()
	id := 2
	s.KBEffect = &id
	snap := s.Snapshot()
	*snap.KBEffect = 9
	if *s.Snapshot().KBEffect != 2 {
		t.Fatal("snapshot mutated settings")
	}
}
