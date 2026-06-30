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
	currentState, err := r.Store.Load()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	currentState.Normalize()

	snapshot, err := r.Detector.Detect(ctx, ipdetect.Options{
		PublicSources:      r.Config.Check.PublicSources,
		IncludePrivate:     r.Config.Check.IncludePrivate,
		InterfaceAllowlist: r.Config.Check.InterfaceAllowlist,
	})
	if err != nil {
		return err
	}

	hash, err := snapshot.Hash()
	if err != nil {
		return err
	}

	hadSnapshot := currentState.CurrentHash != ""
	previousHash := currentState.CurrentHash
	changed := currentState.RecordSnapshot(snapshot, hash, r.Now())
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
		return r.Store.Save(currentState)
	}
	if currentState.InitialSkippedHash == hash {
		logger.Debug("snapshot notification skipped because initial notification is disabled", "hash", hash)
		return r.Store.Save(currentState)
	}

	message := notify.Message{
		Title: "IP address changed",
		Body:  snapshot.Body(),
	}
	for _, notifier := range r.Notifiers {
		name := notifier.Name()
		if !currentState.NeedsNotification(name, hash) {
			logger.Debug("notifier already handled current snapshot", "notifier", name, "hash", hash)
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
				continue
			}
			logger.Warn("provider delivery failed; will retry on next interval",
				"notifier", name,
				"hash", hash,
				"error", err,
			)
			continue
		}

		currentState.MarkNotifierSuccess(name, hash)
		logger.Info("notification delivered", "notifier", name, "hash", hash)
	}

	if err := r.Store.Save(currentState); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func (r Runner) runCheck(ctx context.Context) {
	if err := r.ProcessOnce(ctx); err != nil {
		loggerOrDiscard(r.Logger).Warn("IP check failed", "error", err)
	}
}
