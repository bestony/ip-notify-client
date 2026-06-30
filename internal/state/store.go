package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"bestony.com/ip-notify-client/internal/ipdetect"
)

type State struct {
	CurrentHash              string             `json:"current_hash"`
	CurrentSnapshot          ipdetect.Snapshot  `json:"current_snapshot"`
	PreviousSnapshot         *ipdetect.Snapshot `json:"previous_snapshot,omitempty"`
	NotifierHashes           map[string]string  `json:"notifier_hashes"`
	NotifierTerminalFailures map[string]string  `json:"notifier_terminal_failures,omitempty"`
	InitialSkippedHash       string             `json:"initial_skipped_hash,omitempty"`
	UpdatedAt                time.Time          `json:"updated_at"`
}

type Store struct {
	Path string
}

func New(path string) Store {
	return Store{Path: path}
}

func NewState() State {
	return State{
		NotifierHashes:           map[string]string{},
		NotifierTerminalFailures: map[string]string{},
	}
}

func (s *State) Normalize() {
	if s.NotifierHashes == nil {
		s.NotifierHashes = map[string]string{}
	}
	if s.NotifierTerminalFailures == nil {
		s.NotifierTerminalFailures = map[string]string{}
	}
	s.CurrentSnapshot = s.CurrentSnapshot.Normalize()
	if s.PreviousSnapshot != nil {
		previous := s.PreviousSnapshot.Normalize()
		s.PreviousSnapshot = &previous
	}
}

func (s *State) RecordSnapshot(snapshot ipdetect.Snapshot, hash string, now time.Time) bool {
	s.Normalize()
	if s.CurrentHash == hash {
		s.CurrentSnapshot = snapshot.Normalize()
		s.UpdatedAt = now
		return false
	}

	if s.CurrentHash != "" {
		previous := s.CurrentSnapshot.Normalize()
		s.PreviousSnapshot = &previous
	}
	s.CurrentHash = hash
	s.CurrentSnapshot = snapshot.Normalize()
	s.UpdatedAt = now
	return true
}

func (s *State) MarkNotifierSuccess(name, hash string) {
	s.Normalize()
	s.NotifierHashes[name] = hash
	delete(s.NotifierTerminalFailures, name)
}

func (s *State) MarkNotifierTerminalFailure(name, hash string) {
	s.Normalize()
	s.NotifierTerminalFailures[name] = hash
}

func (s *State) MarkInitialSkipped(hash string) {
	s.InitialSkippedHash = hash
}

func (s *State) NeedsNotification(name, hash string) bool {
	s.Normalize()
	return s.NotifierHashes[name] != hash && s.NotifierTerminalFailures[name] != hash
}

func (s Store) Load() (State, error) {
	if s.Path == "" {
		return State{}, errors.New("state path is required")
	}

	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state %q: %w", s.Path, err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode state %q: %w", s.Path, err)
	}
	state.Normalize()
	return state, nil
}

func (s Store) Save(state State) error {
	if s.Path == "" {
		return errors.New("state path is required")
	}
	state.Normalize()

	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create state directory %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}

	if err := os.Rename(tempName, s.Path); err != nil {
		return fmt.Errorf("replace state file %q: %w", s.Path, err)
	}
	return nil
}
