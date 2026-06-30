package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "/etc/ip-notify/config.yaml"

var defaultPublicSources = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!int" {
			var seconds int64
			if err := value.Decode(&seconds); err != nil {
				return err
			}
			d.Duration = time.Duration(seconds) * time.Second
			return nil
		}

		var raw string
		if err := value.Decode(&raw); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", raw, err)
		}
		d.Duration = parsed
		return nil
	default:
		return fmt.Errorf("duration must be a scalar value")
	}
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

type Config struct {
	Log       LogConfig       `yaml:"log"`
	Check     CheckConfig     `yaml:"check"`
	State     StateConfig     `yaml:"state"`
	Notifiers NotifiersConfig `yaml:"notifiers"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type CheckConfig struct {
	Interval           Duration `yaml:"interval"`
	Timeout            Duration `yaml:"timeout"`
	NotifyInitial      bool     `yaml:"notify_initial"`
	PublicSources      []string `yaml:"public_sources"`
	IncludePrivate     bool     `yaml:"include_private"`
	InterfaceAllowlist []string `yaml:"interface_allowlist"`
}

type StateConfig struct {
	Path string `yaml:"path"`
}

type NotifiersConfig struct {
	Bark     BarkConfig     `yaml:"bark"`
	Pushover PushoverConfig `yaml:"pushover"`
}

type BarkConfig struct {
	Enabled    bool     `yaml:"enabled"`
	ServerURL  string   `yaml:"server_url"`
	DeviceKey  string   `yaml:"device_key,omitempty"`
	DeviceKeys []string `yaml:"device_keys"`
	Group      string   `yaml:"group"`
}

type PushoverConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
	User    string `yaml:"user"`
	Device  string `yaml:"device"`
}

func Default() Config {
	return Config{
		Log: LogConfig{
			Level: "info",
		},
		Check: CheckConfig{
			Interval:           Duration{Duration: 10 * time.Minute},
			Timeout:            Duration{Duration: 5 * time.Second},
			NotifyInitial:      true,
			PublicSources:      append([]string(nil), defaultPublicSources...),
			IncludePrivate:     true,
			InterfaceAllowlist: []string{},
		},
		State: StateConfig{
			Path: "/var/lib/ip-notify/state.json",
		},
		Notifiers: NotifiersConfig{
			Bark: BarkConfig{
				ServerURL:  "https://api.day.app",
				DeviceKeys: []string{},
				Group:      "ip-notify",
			},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	cfg.Normalize()
	return cfg, nil
}

func (c *Config) Normalize() {
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}

	c.State.Path = strings.TrimSpace(c.State.Path)
	c.Notifiers.Bark.ServerURL = strings.TrimRight(strings.TrimSpace(c.Notifiers.Bark.ServerURL), "/")
	c.Notifiers.Bark.DeviceKey = strings.TrimSpace(c.Notifiers.Bark.DeviceKey)
	c.Notifiers.Bark.Group = strings.TrimSpace(c.Notifiers.Bark.Group)
	c.Notifiers.Pushover.Token = strings.TrimSpace(c.Notifiers.Pushover.Token)
	c.Notifiers.Pushover.User = strings.TrimSpace(c.Notifiers.Pushover.User)
	c.Notifiers.Pushover.Device = strings.TrimSpace(c.Notifiers.Pushover.Device)

	c.Check.PublicSources = cleanStringSlice(c.Check.PublicSources)
	c.Check.InterfaceAllowlist = cleanStringSlice(c.Check.InterfaceAllowlist)
	c.Notifiers.Bark.DeviceKeys = cleanStringSlice(c.Notifiers.Bark.DeviceKeys)
}

func (c Config) Validate() error {
	var problems []string

	if !isValidLogLevel(c.Log.Level) {
		problems = append(problems, "log.level must be one of debug, info, warn, error")
	}
	if c.Check.Interval.Duration <= 0 {
		problems = append(problems, "check.interval must be greater than 0")
	}
	if c.Check.Timeout.Duration <= 0 {
		problems = append(problems, "check.timeout must be greater than 0")
	}
	if len(c.Check.PublicSources) == 0 {
		problems = append(problems, "check.public_sources must contain at least one HTTP source")
	}
	for _, source := range c.Check.PublicSources {
		if err := validateHTTPURL(source); err != nil {
			problems = append(problems, fmt.Sprintf("check.public_sources contains invalid URL %q: %v", source, err))
		}
	}
	if c.State.Path == "" {
		problems = append(problems, "state.path is required")
	}

	if !c.Notifiers.Bark.Enabled && !c.Notifiers.Pushover.Enabled {
		problems = append(problems, "at least one notifier must be enabled")
	}
	if c.Notifiers.Bark.Enabled {
		if err := validateHTTPURL(c.Notifiers.Bark.ServerURL); err != nil {
			problems = append(problems, fmt.Sprintf("notifiers.bark.server_url is invalid: %v", err))
		}
		if c.Notifiers.Bark.DeviceKey == "" && len(c.Notifiers.Bark.DeviceKeys) == 0 {
			problems = append(problems, "notifiers.bark.device_keys must contain at least one key when Bark is enabled")
		}
	}
	if c.Notifiers.Pushover.Enabled {
		if c.Notifiers.Pushover.Token == "" {
			problems = append(problems, "notifiers.pushover.token is required when Pushover is enabled")
		}
		if c.Notifiers.Pushover.User == "" {
			problems = append(problems, "notifiers.pushover.user is required when Pushover is enabled")
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) EnabledNotifierNames() []string {
	names := make([]string, 0, 2)
	if c.Notifiers.Bark.Enabled {
		names = append(names, "bark")
	}
	if c.Notifiers.Pushover.Enabled {
		names = append(names, "pushover")
	}
	return names
}

func (c Config) Redacted() Config {
	redacted := c
	if redacted.Notifiers.Bark.DeviceKey != "" {
		redacted.Notifiers.Bark.DeviceKey = "[REDACTED]"
	}
	for i := range redacted.Notifiers.Bark.DeviceKeys {
		if redacted.Notifiers.Bark.DeviceKeys[i] != "" {
			redacted.Notifiers.Bark.DeviceKeys[i] = "[REDACTED]"
		}
	}
	if redacted.Notifiers.Pushover.Token != "" {
		redacted.Notifiers.Pushover.Token = "[REDACTED]"
	}
	if redacted.Notifiers.Pushover.User != "" {
		redacted.Notifiers.Pushover.User = "[REDACTED]"
	}
	return redacted
}

func cleanStringSlice(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func isValidLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validateHTTPURL(raw string) error {
	if raw == "" {
		return errors.New("URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("host is required")
	}
	return nil
}
