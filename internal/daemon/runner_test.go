package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"bestony.com/ip-notify-client/internal/config"
	"bestony.com/ip-notify-client/internal/ipdetect"
	"bestony.com/ip-notify-client/internal/notify"
	"bestony.com/ip-notify-client/internal/state"
)

type fakeDetector struct {
	snapshot ipdetect.Snapshot
	err      error
}

func (f fakeDetector) Detect(context.Context, ipdetect.Options) (ipdetect.Snapshot, error) {
	return f.snapshot, f.err
}

type memoryStore struct {
	state state.State
}

func (s *memoryStore) Load() (state.State, error) {
	s.state.Normalize()
	return s.state, nil
}

func (s *memoryStore) Save(next state.State) error {
	next.Normalize()
	s.state = next
	return nil
}

type fakeNotifier struct {
	name  string
	err   error
	calls int
}

func (n *fakeNotifier) Name() string {
	return n.name
}

func (n *fakeNotifier) Notify(context.Context, notify.Message) error {
	n.calls++
	return n.err
}

func TestRunnerRetriesOnlyFailedProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Check.NotifyInitial = true
	store := &memoryStore{state: state.NewState()}
	bark := &fakeNotifier{name: "bark"}
	pushover := &fakeNotifier{name: "pushover", err: errors.New("network")}
	runner := Runner{
		Config: cfg,
		Detector: fakeDetector{
			snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.4"},
		},
		Store:     store,
		Notifiers: []notify.Notifier{bark, pushover},
		Now:       func() time.Time { return time.Unix(100, 0).UTC() },
	}

	if err := runner.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("first process: %v", err)
	}
	if bark.calls != 1 || pushover.calls != 1 {
		t.Fatalf("expected both notifiers called once, got bark=%d pushover=%d", bark.calls, pushover.calls)
	}

	if err := runner.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if bark.calls != 1 {
		t.Fatalf("expected successful provider not to be retried, got %d calls", bark.calls)
	}
	if pushover.calls != 2 {
		t.Fatalf("expected failed provider to retry, got %d calls", pushover.calls)
	}
}

func TestRunnerSuppressesPermanentProviderFailure(t *testing.T) {
	cfg := config.Default()
	store := &memoryStore{state: state.NewState()}
	pushover := &fakeNotifier{
		name: "pushover",
		err: &notify.DeliveryError{
			Provider:  "pushover",
			Permanent: true,
			Err:       errors.New("bad token"),
		},
	}
	runner := Runner{
		Config: cfg,
		Detector: fakeDetector{
			snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.4"},
		},
		Store:     store,
		Notifiers: []notify.Notifier{pushover},
		Now:       func() time.Time { return time.Unix(100, 0).UTC() },
	}

	if err := runner.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("first process: %v", err)
	}
	if err := runner.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if pushover.calls != 1 {
		t.Fatalf("expected permanent error to suppress retry, got %d calls", pushover.calls)
	}
}

func TestRunnerHonorsNotifyInitialFalse(t *testing.T) {
	cfg := config.Default()
	cfg.Check.NotifyInitial = false
	notifier := &fakeNotifier{name: "bark"}
	runner := Runner{
		Config: cfg,
		Detector: fakeDetector{
			snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.4"},
		},
		Store:     &memoryStore{state: state.NewState()},
		Notifiers: []notify.Notifier{notifier},
		Now:       func() time.Time { return time.Unix(100, 0).UTC() },
	}

	if err := runner.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("expected initial notification to be skipped, got %d calls", notifier.calls)
	}
}

func TestRunnerDoesNotCheckWhenContextAlreadyCancelled(t *testing.T) {
	cfg := config.Default()
	detector := fakeDetector{
		err: errors.New("should not be called"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Runner{
		Config:   cfg,
		Detector: detector,
		Store:    &memoryStore{state: state.NewState()},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
	}.Run(ctx)
	if err != nil {
		t.Fatalf("run with cancelled context: %v", err)
	}
}
