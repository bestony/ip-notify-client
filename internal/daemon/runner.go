package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"bestony.com/ip-notify-client/internal/config"
	"bestony.com/ip-notify-client/internal/ipdetect"
	"bestony.com/ip-notify-client/internal/notify"
	"bestony.com/ip-notify-client/internal/state"
)

type Detector interface {
	Detect(ctx context.Context, options ipdetect.Options) (ipdetect.Snapshot, error)
}

type StateStore interface {
	Load() (state.State, error)
	Save(state state.State) error
}

type Runner struct {
	Config    config.Config
	Detector  Detector
	Store     StateStore
	Notifiers []notify.Notifier
	Logger    *slog.Logger
	Now       func() time.Time
}

type NotificationStatus string

const (
	NotificationStatusDelivered        NotificationStatus = "delivered"
	NotificationStatusFailed           NotificationStatus = "failed"
	NotificationStatusPermanentFailure NotificationStatus = "permanent_failure"
	NotificationStatusSkipped          NotificationStatus = "skipped"
)

type NotificationResult struct {
	Notifier  string             `json:"notifier"`
	Status    NotificationStatus `json:"status"`
	Reason    string             `json:"reason,omitempty"`
	Permanent bool               `json:"permanent,omitempty"`
}

type ProcessResult struct {
	Snapshot      ipdetect.Snapshot    `json:"snapshot"`
	Hash          string               `json:"hash"`
	Changed       bool                 `json:"changed"`
	Notified      bool                 `json:"notified"`
	Notifications []NotificationResult `json:"notifications,omitempty"`
}

func (r Runner) Run(ctx context.Context) error {
	if r.Detector == nil {
		return fmt.Errorf("detector is required")
	}
	if r.Store == nil {
		return fmt.Errorf("state store is required")
	}
	if r.Now == nil {
		r.Now = time.Now
	}

	logger := loggerOrDiscard(r.Logger)
	logger.Info("service loop starting",
		"interval", r.Config.Check.Interval.String(),
		"timeout", r.Config.Check.Timeout.String(),
		"notifiers", r.Config.EnabledNotifierNames(),
	)

	if err := ctx.Err(); err != nil {
		logger.Info("service loop stopped before first check", "reason", err)
		return nil
	}
	r.runCheck(ctx)

	ticker := time.NewTicker(r.Config.Check.Interval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("service loop stopped", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			r.runCheck(ctx)
		}
	}
}

func (r Runner) ProcessOnce(ctx context.Context) error {
	_, err := r.ProcessOnceResult(ctx)
	return err
}

func (r Runner) ProcessOnceResult(ctx context.Context) (ProcessResult, error) {
	if r.Detector == nil {
		return ProcessResult{}, fmt.Errorf("detector is required")
	}
	if r.Store == nil {
		return ProcessResult{}, fmt.Errorf("state store is required")
	}
	if r.Now == nil {
		r.Now = time.Now
	}

	logger := loggerOrDiscard(r.Logger)
	currentState, err := r.Store.Load()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("load state: %w", err)
	}
	currentState.Normalize()

	snapshot, err := r.Detector.Detect(ctx, ipdetect.Options{
		PublicSources:      r.Config.Check.PublicSources,
		IncludePrivate:     r.Config.Check.IncludePrivate,
		InterfaceAllowlist: r.Config.Check.InterfaceAllowlist,
	})
	if err != nil {
		return ProcessResult{}, err
	}

	hash, err := snapshot.Hash()
	if err != nil {
		return ProcessResult{}, err
	}

	hadSnapshot := currentState.CurrentHash != ""
	previousHash := currentState.CurrentHash
	changed := currentState.RecordSnapshot(snapshot, hash, r.Now())
	result := ProcessResult{
		Snapshot: snapshot.Normalize(),
		Hash:     hash,
		Changed:  changed,
	}
	if changed {
		logger.Info("IP snapshot changed",
			"previous_hash", previousHash,
			"current_hash", hash,
			"public_ip", snapshot.PublicIP,
			"interface_ip_count", len(snapshot.InterfaceIPs),
		)
	} else {
		logger.Debug("IP snapshot unchanged", "hash", hash)
	}

	if !hadSnapshot && !r.Config.Check.NotifyInitial {
		currentState.MarkInitialSkipped(hash)
		logger.Info("initial IP snapshot recorded without notification", "hash", hash)
		result.Notifications = skippedNotifications(r.Notifiers, "initial_notification_disabled")
		return result, saveState(r.Store, currentState)
	}
	if currentState.InitialSkippedHash == hash {
		logger.Debug("snapshot notification skipped because initial notification is disabled", "hash", hash)
		result.Notifications = skippedNotifications(r.Notifiers, "initial_notification_disabled")
		return result, saveState(r.Store, currentState)
	}

	message := notify.Message{
		Title: "IP address changed",
		Body:  snapshot.Body(),
	}
	for _, notifier := range r.Notifiers {
		name := notifier.Name()
		if !currentState.NeedsNotification(name, hash) {
			logger.Debug("notifier already handled current snapshot", "notifier", name, "hash", hash)
			result.Notifications = append(result.Notifications, NotificationResult{
				Notifier: name,
				Status:   NotificationStatusSkipped,
				Reason:   "already_handled",
			})
			continue
		}

		logger.Debug("notifying provider", "notifier", name, "hash", hash)
		if err := notifier.Notify(ctx, message); err != nil {
			if notify.IsPermanent(err) {
				currentState.MarkNotifierTerminalFailure(name, hash)
				logger.Warn("provider rejected notification permanently; automatic retry suppressed",
					"notifier", name,
					"hash", hash,
					"error", err,
				)
				result.Notifications = append(result.Notifications, NotificationResult{
					Notifier:  name,
					Status:    NotificationStatusPermanentFailure,
					Permanent: true,
				})
				continue
			}
			logger.Warn("provider delivery failed; will retry on next interval",
				"notifier", name,
				"hash", hash,
				"error", err,
			)
			result.Notifications = append(result.Notifications, NotificationResult{
				Notifier: name,
				Status:   NotificationStatusFailed,
			})
			continue
		}

		currentState.MarkNotifierSuccess(name, hash)
		result.Notified = true
		result.Notifications = append(result.Notifications, NotificationResult{
			Notifier: name,
			Status:   NotificationStatusDelivered,
		})
		logger.Info("notification delivered", "notifier", name, "hash", hash)
	}

	return result, saveState(r.Store, currentState)
}

func (r Runner) runCheck(ctx context.Context) {
	if err := r.ProcessOnce(ctx); err != nil {
		loggerOrDiscard(r.Logger).Warn("IP check failed", "error", err)
	}
}

func skippedNotifications(notifiers []notify.Notifier, reason string) []NotificationResult {
	results := make([]NotificationResult, 0, len(notifiers))
	for _, notifier := range notifiers {
		results = append(results, NotificationResult{
			Notifier: notifier.Name(),
			Status:   NotificationStatusSkipped,
			Reason:   reason,
		})
	}
	return results
}

func saveState(store StateStore, state state.State) error {
	if err := store.Save(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
