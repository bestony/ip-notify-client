package state

import (
	"path/filepath"
	"testing"
	"time"

	"bestony.com/ip-notify-client/internal/ipdetect"
)

func TestStorePersistsState(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	current := NewState()
	snapshot := ipdetect.Snapshot{PublicIP: "203.0.113.9"}
	hash, err := snapshot.Hash()
	if err != nil {
		t.Fatal(err)
	}
	current.RecordSnapshot(snapshot, hash, time.Unix(100, 0).UTC())
	current.MarkNotifierSuccess("bark", hash)

	if err := store.Save(current); err != nil {
		t.Fatalf("save state: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.CurrentHash != hash {
		t.Fatalf("expected hash %s, got %s", hash, loaded.CurrentHash)
	}
	if !loaded.NeedsNotification("pushover", hash) {
		t.Fatal("expected pushover to need notification")
	}
	if loaded.NeedsNotification("bark", hash) {
		t.Fatal("expected bark to be marked successful")
	}
}

func TestStateTracksTerminalFailures(t *testing.T) {
	state := NewState()
	state.MarkNotifierTerminalFailure("pushover", "hash-1")
	if state.NeedsNotification("pushover", "hash-1") {
		t.Fatal("expected terminal failure to suppress retry for same hash")
	}
	if !state.NeedsNotification("pushover", "hash-2") {
		t.Fatal("expected new hash to require notification")
	}
	state.MarkNotifierSuccess("pushover", "hash-2")
	if state.NotifierTerminalFailures["pushover"] != "" {
		t.Fatal("expected success to clear terminal failure")
	}
}
