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
	calls    int
	onDetect func()
}

func (f *fakeDetector) Detect(context.Context, ipdetect.Options) (ipdetect.Snapshot, error) {
	f.calls++
	if f.onDetect != nil {
		f.onDetect()
	}
	return f.snapshot, f.err
}

type memoryStore struct {
	state   state.State
	loadErr error
	saveErr error
	saves   int
}

func (s *memoryStore) Load() (state.State, error) {
	if s.loadErr != nil {
		return state.State{}, s.loadErr
	}
	s.state.Normalize()
	return s.state, nil
}

func (s *memoryStore) Save(next state.State) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	next.Normalize()
	s.state = next
	s.saves++
	return nil
}

type fakeNotifier struct {
	name     string
	err      error
	calls    int
	messages []notify.Message
}

func (n *fakeNotifier) Name() string {
	return n.name
}

func (n *fakeNotifier) Notify(_ context.Context, message notify.Message) error {
	n.calls++
	n.messages = append(n.messages, message)
	return n.err
}

func TestNotificationTitleIncludesHostname(t *testing.T) {
	originalHostname := systemHostname
	systemHostname = func() (string, error) {
		return "test-host", nil
	}
	t.Cleanup(func() {
		systemHostname = originalHostname
	})

	if got := notificationTitle(); got != "test-host IP address changed" {
		t.Fatalf("expected hostname title, got %q", got)
	}
}

func TestNotificationTitleFallsBackForEmptyHostname(t *testing.T) {
	originalHostname := systemHostname
	systemHostname = func() (string, error) {
		return " \t\n ", nil
	}
	t.Cleanup(func() {
		systemHostname = originalHostname
	})

	if got := notificationTitle(); got != "IP address changed" {
		t.Fatalf("expected fallback title, got %q", got)
	}
}

func TestNotificationTitleFallsBackForHostnameError(t *testing.T) {
	originalHostname := systemHostname
	systemHostname = func() (string, error) {
		return "", errors.New("hostname unavailable")
	}
	t.Cleanup(func() {
		systemHostname = originalHostname
	})

	if got := notificationTitle(); got != "IP address changed" {
		t.Fatalf("expected fallback title, got %q", got)
	}
}

func TestRunnerRetriesOnlyFailedProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Check.NotifyInitial = true
	store := &memoryStore{state: state.NewState()}
	bark := &fakeNotifier{name: "bark"}
	pushover := &fakeNotifier{name: "pushover", err: errors.New("network")}
	runner := Runner{
		Config: cfg,
		Detector: &fakeDetector{
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
		Detector: &fakeDetector{
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
		Detector: &fakeDetector{
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

func TestRunnerProcessOnceResultReportsSnapshotHashAndChanged(t *testing.T) {
	originalHostname := systemHostname
	systemHostname = func() (string, error) {
		return "daemon-host", nil
	}
	t.Cleanup(func() {
		systemHostname = originalHostname
	})
	cfg := config.Default()
	cfg.Check.NotifyInitial = true
	snapshot := ipdetect.Snapshot{
		PublicIP: "203.0.113.4",
		InterfaceIPs: []ipdetect.InterfaceIP{
			{Interface: "eth0", IP: "192.0.2.10"},
		},
	}
	notifier := &fakeNotifier{name: "bark"}
	runner := Runner{
		Config: cfg,
		Detector: &fakeDetector{
			snapshot: snapshot,
		},
		Store:     &memoryStore{state: state.NewState()},
		Notifiers: []notify.Notifier{notifier},
		Now:       func() time.Time { return time.Unix(100, 0).UTC() },
	}

	result, err := runner.ProcessOnceResult(context.Background())
	if err != nil {
		t.Fatalf("process result: %v", err)
	}
	expectedHash := snapshot.Hash()
	if result.Hash != expectedHash {
		t.Fatalf("expected hash %q, got %q", expectedHash, result.Hash)
	}
	if !result.Changed {
		t.Fatalf("expected changed=true")
	}
	if result.Snapshot.PublicIP != "203.0.113.4" {
		t.Fatalf("expected snapshot public IP, got %#v", result.Snapshot)
	}
	if !result.Notified {
		t.Fatalf("expected notified=true")
	}
	if len(result.Notifications) != 1 || result.Notifications[0].Status != NotificationStatusDelivered {
		t.Fatalf("expected delivered notification, got %#v", result.Notifications)
	}
	if len(notifier.messages) != 1 || notifier.messages[0].Title != "daemon-host IP address changed" {
		t.Fatalf("expected hostname title, got %#v", notifier.messages)
	}
}

func TestRunnerProcessOnceResultReportsUnchangedSnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Check.NotifyInitial = true
	snapshot := ipdetect.Snapshot{PublicIP: "203.0.113.4"}
	hash := snapshot.Hash()
	notifier := &fakeNotifier{name: "bark"}
	runner := Runner{
		Config: cfg,
		Detector: &fakeDetector{
			snapshot: snapshot,
		},
		Store: &memoryStore{state: state.State{
			CurrentHash:     hash,
			CurrentSnapshot: snapshot,
			NotifierHashes: map[string]string{
				"bark": hash,
			},
		}},
		Notifiers: []notify.Notifier{notifier},
		Now:       func() time.Time { return time.Unix(100, 0).UTC() },
	}

	result, err := runner.ProcessOnceResult(context.Background())
	if err != nil {
		t.Fatalf("process result: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected changed=false")
	}
	if result.Notified {
		t.Fatalf("expected notified=false")
	}
	if notifier.calls != 0 {
		t.Fatalf("expected notifier not to be called, got %d", notifier.calls)
	}
	if len(result.Notifications) != 1 || result.Notifications[0].Status != NotificationStatusSkipped {
		t.Fatalf("expected skipped notification, got %#v", result.Notifications)
	}
}

func TestRunnerDoesNotCheckWhenContextAlreadyCancelled(t *testing.T) {
	cfg := config.Default()
	detector := &fakeDetector{
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

func TestRunnerRunValidationErrors(t *testing.T) {
	cfg := config.Default()
	if err := (Runner{Config: cfg, Store: &memoryStore{}}).Run(context.Background()); err == nil {
		t.Fatal("expected missing detector error")
	}
	if err := (Runner{Config: cfg, Detector: &fakeDetector{}}).Run(context.Background()); err == nil {
		t.Fatal("expected missing store error")
	}
}

func TestRunnerRunLoopChecksUntilCancelled(t *testing.T) {
	cfg := config.Default()
	cfg.Check.Interval = config.Duration{Duration: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	detector := &fakeDetector{
		snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.9"},
		onDetect: func() {
			cancel()
		},
	}

	err := Runner{
		Config:   cfg,
		Detector: detector,
		Store:    &memoryStore{state: state.NewState()},
	}.Run(ctx)
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if detector.calls == 0 {
		t.Fatal("expected at least one check")
	}
}

func TestRunnerRunLoopHandlesTickerCheck(t *testing.T) {
	cfg := config.Default()
	cfg.Check.Interval = config.Duration{Duration: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	detector := &fakeDetector{
		snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.10"},
	}
	detector.onDetect = func() {
		if detector.calls >= 2 {
			cancel()
		}
	}

	err := Runner{
		Config:   cfg,
		Detector: detector,
		Store:    &memoryStore{state: state.NewState()},
	}.Run(ctx)
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if detector.calls < 2 {
		t.Fatalf("expected ticker check, got %d calls", detector.calls)
	}
}

func TestRunnerProcessOnceValidationAndDependencyErrors(t *testing.T) {
	cfg := config.Default()
	if _, err := (Runner{Config: cfg, Store: &memoryStore{}}).ProcessOnceResult(context.Background()); err == nil {
		t.Fatal("expected missing detector error")
	}
	if _, err := (Runner{Config: cfg, Detector: &fakeDetector{}}).ProcessOnceResult(context.Background()); err == nil {
		t.Fatal("expected missing store error")
	}
	if _, err := (Runner{
		Config:   cfg,
		Detector: &fakeDetector{},
		Store:    &memoryStore{loadErr: errors.New("load failed")},
	}).ProcessOnceResult(context.Background()); err == nil {
		t.Fatal("expected load error")
	}
	if _, err := (Runner{
		Config:   cfg,
		Detector: &fakeDetector{err: errors.New("detect failed")},
		Store:    &memoryStore{state: state.NewState()},
	}).ProcessOnceResult(context.Background()); err == nil {
		t.Fatal("expected detect error")
	}
}

func TestRunnerKeepsSkippingInitialHash(t *testing.T) {
	cfg := config.Default()
	cfg.Check.NotifyInitial = false
	snapshot := ipdetect.Snapshot{PublicIP: "203.0.113.4"}
	hash := snapshot.Hash()
	store := &memoryStore{state: state.State{
		CurrentHash:        hash,
		CurrentSnapshot:    snapshot,
		InitialSkippedHash: hash,
	}}
	notifier := &fakeNotifier{name: "bark"}
	result, err := (Runner{
		Config:    cfg,
		Detector:  &fakeDetector{snapshot: snapshot},
		Store:     store,
		Notifiers: []notify.Notifier{notifier},
	}).ProcessOnceResult(context.Background())
	if err != nil {
		t.Fatalf("process skipped initial hash: %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("expected notifier skipped, got %d calls", notifier.calls)
	}
	if len(result.Notifications) != 1 || result.Notifications[0].Reason != "initial_notification_disabled" {
		t.Fatalf("unexpected notifications: %#v", result.Notifications)
	}
}

func TestRunnerSaveStateError(t *testing.T) {
	cfg := config.Default()
	_, err := (Runner{
		Config:   cfg,
		Detector: &fakeDetector{snapshot: ipdetect.Snapshot{PublicIP: "203.0.113.4"}},
		Store:    &memoryStore{state: state.NewState(), saveErr: errors.New("save failed")},
	}).ProcessOnceResult(context.Background())
	if err == nil {
		t.Fatal("expected save error")
	}
	if !errors.Is(err, errors.New("save failed")) && err.Error() != "save state: save failed" {
		t.Fatalf("unexpected save error: %v", err)
	}
}

func TestRunCheckLogsProcessError(t *testing.T) {
	Runner{
		Detector: &fakeDetector{err: errors.New("detect failed")},
		Store:    &memoryStore{state: state.NewState()},
	}.runCheck(context.Background())
}

func TestLoggerOrDiscard(t *testing.T) {
	logger := loggerOrDiscard(nil)
	if logger == nil {
		t.Fatal("expected fallback logger")
	}
	if got := loggerOrDiscard(logger); got != logger {
		t.Fatal("expected existing logger")
	}
}
