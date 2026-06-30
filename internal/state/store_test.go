package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bestony.com/ip-notify-client/internal/ipdetect"
)

func TestStorePersistsState(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	current := NewState()
	snapshot := ipdetect.Snapshot{PublicIP: "203.0.113.9"}
	hash := snapshot.Hash()
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

func TestStoreLoadMissingFileReturnsEmptyState(t *testing.T) {
	loaded, err := New(filepath.Join(t.TempDir(), "missing.json")).Load()
	if err != nil {
		t.Fatalf("load missing state: %v", err)
	}
	if loaded.NotifierHashes == nil || loaded.NotifierTerminalFailures == nil {
		t.Fatalf("expected normalized empty state, got %#v", loaded)
	}
}

func TestStoreLoadErrors(t *testing.T) {
	if _, err := New("").Load(); err == nil {
		t.Fatal("expected missing path error")
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write invalid state: %v", err)
	}
	if _, err := New(path).Load(); err == nil {
		t.Fatal("expected decode error")
	}

	restoreStateSeams(t)
	stateReadFile = func(string) ([]byte, error) {
		return nil, errors.New("read failed")
	}
	if _, err := New(path).Load(); err == nil {
		t.Fatal("expected read error")
	}
}

func TestStateNormalizeAndRecordSnapshot(t *testing.T) {
	previous := ipdetect.Snapshot{
		PublicIP: " 203.0.113.1 ",
		InterfaceIPs: []ipdetect.InterfaceIP{
			{Interface: " eth0 ", IP: "192.0.2.1"},
		},
	}
	current := ipdetect.Snapshot{PublicIP: "203.0.113.2"}
	state := State{
		CurrentHash:      "hash-1",
		CurrentSnapshot:  previous,
		PreviousSnapshot: &previous,
	}
	state.Normalize()
	if state.NotifierHashes == nil || state.NotifierTerminalFailures == nil {
		t.Fatalf("expected maps to be initialized: %#v", state)
	}
	if state.PreviousSnapshot.PublicIP != "203.0.113.1" {
		t.Fatalf("expected previous snapshot to normalize, got %#v", state.PreviousSnapshot)
	}

	changed := state.RecordSnapshot(current, "hash-2", time.Unix(200, 0).UTC())
	if !changed {
		t.Fatal("expected new hash to mark state changed")
	}
	if state.PreviousSnapshot == nil || state.PreviousSnapshot.PublicIP != "203.0.113.1" {
		t.Fatalf("expected previous snapshot to be retained, got %#v", state.PreviousSnapshot)
	}

	changed = state.RecordSnapshot(ipdetect.Snapshot{PublicIP: "203.0.113.2"}, "hash-2", time.Unix(300, 0).UTC())
	if changed {
		t.Fatal("expected same hash to be unchanged")
	}
	if !state.UpdatedAt.Equal(time.Unix(300, 0).UTC()) {
		t.Fatalf("expected updated timestamp, got %s", state.UpdatedAt)
	}

	state.MarkInitialSkipped("hash-2")
	if state.InitialSkippedHash != "hash-2" {
		t.Fatalf("expected initial skipped hash, got %q", state.InitialSkippedHash)
	}
}

func TestStoreSaveErrors(t *testing.T) {
	if err := New("").Save(NewState()); err == nil {
		t.Fatal("expected missing path error")
	}

	path := filepath.Join(t.TempDir(), "state.json")
	restoreStateSeams(t)
	stateMkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected mkdir error")
	}

	restoreStateSeams(t)
	stateCreateTemp = func(string, string) (tempFile, error) {
		return nil, errors.New("create temp failed")
	}
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected create temp error")
	}

	restoreStateSeams(t)
	stateCreateTemp = func(string, string) (tempFile, error) {
		return &fakeTempFile{name: "state.tmp", writeErr: errors.New("write failed")}, nil
	}
	stateRemove = func(string) error { return nil }
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected write error")
	}

	restoreStateSeams(t)
	stateCreateTemp = func(string, string) (tempFile, error) {
		return &fakeTempFile{name: "state.tmp", chmodErr: errors.New("chmod failed")}, nil
	}
	stateRemove = func(string) error { return nil }
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected chmod error")
	}

	restoreStateSeams(t)
	stateCreateTemp = func(string, string) (tempFile, error) {
		return &fakeTempFile{name: "state.tmp", closeErr: errors.New("close failed")}, nil
	}
	stateRemove = func(string) error { return nil }
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected close error")
	}

	restoreStateSeams(t)
	stateCreateTemp = func(string, string) (tempFile, error) {
		return &fakeTempFile{name: "state.tmp"}, nil
	}
	stateRemove = func(string) error { return nil }
	stateRename = func(string, string) error { return errors.New("rename failed") }
	if err := New(path).Save(NewState()); err == nil {
		t.Fatal("expected rename error")
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

type fakeTempFile struct {
	name     string
	writeErr error
	chmodErr error
	closeErr error
}

func (f *fakeTempFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *fakeTempFile) Chmod(os.FileMode) error {
	return f.chmodErr
}

func (f *fakeTempFile) Close() error {
	return f.closeErr
}

func (f *fakeTempFile) Name() string {
	return f.name
}

func restoreStateSeams(t *testing.T) {
	t.Helper()

	stateReadFile = os.ReadFile
	stateMkdirAll = os.MkdirAll
	stateCreateTemp = func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) }
	stateRemove = os.Remove
	stateRename = os.Rename
	t.Cleanup(func() {
		stateReadFile = os.ReadFile
		stateMkdirAll = os.MkdirAll
		stateCreateTemp = func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) }
		stateRemove = os.Remove
		stateRename = os.Rename
	})
}
