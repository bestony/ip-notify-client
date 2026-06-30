package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()

	if cfg.Log.Level != "info" {
		t.Fatalf("expected default log level info, got %q", cfg.Log.Level)
	}
	if cfg.Check.Interval.Duration != 10*time.Minute {
		t.Fatalf("expected default interval 10m, got %s", cfg.Check.Interval)
	}
	if cfg.Check.Timeout.Duration != 5*time.Second {
		t.Fatalf("expected default timeout 5s, got %s", cfg.Check.Timeout)
	}
	if !cfg.Check.NotifyInitial {
		t.Fatal("expected notify_initial to default true")
	}
	if len(cfg.Check.PublicSources) != 3 {
		t.Fatalf("expected three public sources, got %d", len(cfg.Check.PublicSources))
	}
}

func TestLoadYAMLAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
log:
  level: debug
check:
  interval: 1m
  timeout: 2s
  public_sources:
    - http://127.0.0.1/ip
  include_private: false
state:
  path: /tmp/ip-notify-state.json
notifiers:
  bark:
    enabled: true
    server_url: http://127.0.0.1:8080
    device_keys:
      - key-1
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if cfg.Check.Interval.Duration != time.Minute {
		t.Fatalf("expected interval 1m, got %s", cfg.Check.Interval)
	}
	if cfg.Notifiers.Bark.ServerURL != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected Bark server URL: %q", cfg.Notifiers.Bark.ServerURL)
	}
}

func TestValidateRequiresNotifier(t *testing.T) {
	cfg := Default()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "at least one notifier") {
		t.Fatalf("expected notifier validation error, got %v", err)
	}
}

func TestRedactedHidesSecrets(t *testing.T) {
	cfg := Default()
	cfg.Notifiers.Bark.DeviceKey = "bark-secret"
	cfg.Notifiers.Bark.DeviceKeys = []string{"bark-secret-2"}
	cfg.Notifiers.Pushover.Token = "token-secret"
	cfg.Notifiers.Pushover.User = "user-secret"

	redacted := cfg.Redacted()
	if redacted.Notifiers.Bark.DeviceKey == "bark-secret" {
		t.Fatal("Bark device key was not redacted")
	}
	if redacted.Notifiers.Bark.DeviceKeys[0] == "bark-secret-2" {
		t.Fatal("Bark device_keys was not redacted")
	}
	if redacted.Notifiers.Pushover.Token == "token-secret" {
		t.Fatal("Pushover token was not redacted")
	}
	if redacted.Notifiers.Pushover.User == "user-secret" {
		t.Fatal("Pushover user was not redacted")
	}
}
