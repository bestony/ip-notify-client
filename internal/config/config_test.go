package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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

func TestDurationYAML(t *testing.T) {
	var parsed struct {
		IntValue    Duration `yaml:"int_value"`
		StringValue Duration `yaml:"string_value"`
	}
	if err := yaml.Unmarshal([]byte("int_value: 15\nstring_value: 250ms\n"), &parsed); err != nil {
		t.Fatalf("unmarshal durations: %v", err)
	}
	if parsed.IntValue.Duration != 15*time.Second {
		t.Fatalf("expected 15s, got %s", parsed.IntValue)
	}
	if parsed.StringValue.Duration != 250*time.Millisecond {
		t.Fatalf("expected 250ms, got %s", parsed.StringValue)
	}

	marshaled, err := yaml.Marshal(struct {
		Timeout Duration `yaml:"timeout"`
	}{Timeout: Duration{Duration: 3 * time.Second}})
	if err != nil {
		t.Fatalf("marshal duration: %v", err)
	}
	if !strings.Contains(string(marshaled), "3s") {
		t.Fatalf("expected marshaled duration string, got %s", string(marshaled))
	}
}

func TestDurationRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "invalid string", data: "value: nope\n"},
		{name: "non scalar", data: "value:\n  - 1s\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parsed struct {
				Value Duration `yaml:"value"`
			}
			if err := yaml.Unmarshal([]byte(test.data), &parsed); err == nil {
				t.Fatal("expected duration error")
			}
		})
	}

	var duration Duration
	err := duration.UnmarshalYAML(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: "not-int",
	})
	if err == nil {
		t.Fatal("expected integer decode error")
	}
	err = duration.UnmarshalYAML(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!seq",
		Value: "[]",
	})
	if err == nil {
		t.Fatal("expected string decode error")
	}
}

func TestLoadRejectsMissingAndInvalidConfig(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected missing config error")
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field decode error")
	}
}

func TestNormalizeCleansValues(t *testing.T) {
	cfg := Default()
	cfg.Log.Level = "  "
	cfg.State.Path = " /tmp/state.json "
	cfg.Notifiers.Bark.ServerURL = " https://api.day.app/// "
	cfg.Notifiers.Bark.DeviceKey = " primary "
	cfg.Notifiers.Bark.DeviceKeys = []string{" one ", "", " two "}
	cfg.Notifiers.Bark.Group = " group "
	cfg.Notifiers.Pushover.Token = " token "
	cfg.Notifiers.Pushover.User = " user "
	cfg.Notifiers.Pushover.Device = " device "
	cfg.Check.PublicSources = []string{" http://127.0.0.1/ip ", " "}
	cfg.Check.InterfaceAllowlist = []string{" eth0 ", ""}

	cfg.Normalize()
	if cfg.Log.Level != "info" {
		t.Fatalf("expected empty log level to normalize to info, got %q", cfg.Log.Level)
	}
	if cfg.State.Path != "/tmp/state.json" {
		t.Fatalf("unexpected state path: %q", cfg.State.Path)
	}
	if cfg.Notifiers.Bark.ServerURL != "https://api.day.app" {
		t.Fatalf("unexpected server URL: %q", cfg.Notifiers.Bark.ServerURL)
	}
	if !reflect.DeepEqual(cfg.Notifiers.Bark.DeviceKeys, []string{"one", "two"}) {
		t.Fatalf("unexpected device keys: %#v", cfg.Notifiers.Bark.DeviceKeys)
	}
	if !reflect.DeepEqual(cfg.Check.PublicSources, []string{"http://127.0.0.1/ip"}) {
		t.Fatalf("unexpected public sources: %#v", cfg.Check.PublicSources)
	}
	if !reflect.DeepEqual(cfg.Check.InterfaceAllowlist, []string{"eth0"}) {
		t.Fatalf("unexpected allowlist: %#v", cfg.Check.InterfaceAllowlist)
	}
	if cfg.Notifiers.Pushover.Device != "device" {
		t.Fatalf("unexpected pushover device: %q", cfg.Notifiers.Pushover.Device)
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

func TestValidateReportsAllFailures(t *testing.T) {
	cfg := Config{
		Log: LogConfig{Level: "trace"},
		Check: CheckConfig{
			Interval:      Duration{Duration: 0},
			Timeout:       Duration{Duration: -time.Second},
			PublicSources: []string{"", "ftp://example.com/ip", "http://"},
		},
		Notifiers: NotifiersConfig{
			Bark: BarkConfig{
				Enabled:   true,
				ServerURL: "ftp://example.com",
			},
			Pushover: PushoverConfig{
				Enabled: true,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, fragment := range []string{
		"log.level must be one of debug, info, warn, error",
		"check.interval must be greater than 0",
		"check.timeout must be greater than 0",
		"check.public_sources contains invalid URL \"\": URL is required",
		"scheme must be http or https",
		"host is required",
		"state.path is required",
		"notifiers.bark.server_url is invalid",
		"notifiers.bark.device_keys must contain at least one key",
		"notifiers.pushover.token is required",
		"notifiers.pushover.user is required",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("validation error missing %q:\n%s", fragment, err.Error())
		}
	}
}

func TestValidateRequiresPublicSources(t *testing.T) {
	cfg := Default()
	cfg.Check.PublicSources = nil
	cfg.State.Path = "/tmp/state.json"
	cfg.Notifiers.Bark.Enabled = true
	cfg.Notifiers.Bark.DeviceKeys = []string{"key"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected public source validation error")
	}
	if !strings.Contains(err.Error(), "check.public_sources must contain at least one HTTP source") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateAcceptsPushoverOnlyConfig(t *testing.T) {
	cfg := Default()
	cfg.Notifiers.Pushover.Enabled = true
	cfg.Notifiers.Pushover.Token = "token"
	cfg.Notifiers.Pushover.User = "user"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate pushover-only config: %v", err)
	}
}

func TestEnabledNotifierNames(t *testing.T) {
	cfg := Default()
	if names := cfg.EnabledNotifierNames(); len(names) != 0 {
		t.Fatalf("expected no enabled notifiers, got %#v", names)
	}

	cfg.Notifiers.Bark.Enabled = true
	cfg.Notifiers.Pushover.Enabled = true
	names := cfg.EnabledNotifierNames()
	want := []string{"bark", "pushover"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("expected %#v, got %#v", want, names)
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

func TestValidateHTTPURLRejectsParseError(t *testing.T) {
	err := validateHTTPURL("http://[::1")
	if err == nil {
		t.Fatal("expected URL parse error")
	}
	if !strings.Contains(fmt.Sprint(err), "missing") && !strings.Contains(fmt.Sprint(err), "invalid") {
		t.Fatalf("unexpected parse error: %v", err)
	}
}
