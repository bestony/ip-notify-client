package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bestony.com/ip-notify-client/internal/config"
	installpkg "bestony.com/ip-notify-client/internal/install"
	"bestony.com/ip-notify-client/internal/ipdetect"
	updatecmd "bestony.com/ip-notify-client/internal/update"
	"github.com/spf13/cobra"
)

func TestOnceCommandPrintsHumanResult(t *testing.T) {
	configPath, _ := writeOnceTestConfig(t, "203.0.113.4")

	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"once", "--config", configPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute once: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"Public IP: 203.0.113.4",
		"Interface IPs: none",
		"Snapshot Hash:",
		"Changed: true",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
}

func TestOnceCommandPrintsJSONResult(t *testing.T) {
	configPath, _ := writeOnceTestConfig(t, "203.0.113.5")

	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"once", "--json", "--config", configPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute once json: %v", err)
	}

	var result struct {
		Snapshot ipdetect.Snapshot `json:"snapshot"`
		Hash     string            `json:"hash"`
		Changed  bool              `json:"changed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode json output %q: %v", stdout.String(), err)
	}
	if result.Snapshot.PublicIP != "203.0.113.5" {
		t.Fatalf("expected public IP in JSON, got %#v", result.Snapshot)
	}
	if result.Hash == "" {
		t.Fatalf("expected hash in JSON")
	}
	if !result.Changed {
		t.Fatalf("expected changed=true in JSON")
	}
}

func TestOnceCommandFailsWhenConfigMissing(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"once", "--config", filepath.Join(t.TempDir(), "missing.yaml")})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected missing config error")
	}
	if !strings.Contains(err.Error(), "open config") {
		t.Fatalf("expected open config error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", stdout.String())
	}
}

func TestOnceCommandFailsWhenNoNotifierEnabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "state.json")
	data := []byte(`log:
  level: error
check:
  interval: 10m
  timeout: 5s
  notify_initial: true
  public_sources:
    - http://127.0.0.1/ip
  include_private: false
state:
  path: ` + statePath + `
notifiers:
  bark:
    enabled: false
    server_url: http://127.0.0.1
    device_keys: []
    group: ip-notify
  pushover:
    enabled: false
    token: ""
    user: ""
    device: ""
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"once", "--config", configPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "at least one notifier must be enabled") {
		t.Fatalf("expected notifier validation error, got %v", err)
	}
}

func TestRunCommandStopsWhenContextCancelled(t *testing.T) {
	configPath, _ := writeOnceTestConfig(t, "203.0.113.6")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := NewRootCommand()
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "--config", configPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute run with cancelled context: %v", err)
	}
}

func TestRunCommandReturnsConfigError(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "--config", filepath.Join(t.TempDir(), "missing.yaml")})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected run config error")
	}
	if !strings.Contains(err.Error(), "open config") {
		t.Fatalf("unexpected run error: %v", err)
	}
}

func TestOnceCommandReturnsDetectorError(t *testing.T) {
	configPath, _ := writeOnceTestConfig(t, "not-an-ip")

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"once", "--config", configPath})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected detector error")
	}
	if !strings.Contains(err.Error(), "resolve public IP") {
		t.Fatalf("unexpected once error: %v", err)
	}
}

func TestInstallDaemonCommandDryRun(t *testing.T) {
	stubDefaultInstallOptions(t, func(configPath string) (installpkg.Options, error) {
		return installpkg.Options{
			ConfigPath:  configPath,
			BinaryPath:  "/tmp/ip-notify",
			InstallPath: "/usr/local/bin/ip-notify",
			ServicePath: "/etc/systemd/system/ip-notify.service",
			ConfigDir:   "/etc/ip-notify",
			StateDir:    "/var/lib/ip-notify",
			User:        "ip-notify",
			Group:       "ip-notify",
			Enable:      true,
		}, nil
	})

	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install-daemon", "--config", "/tmp/config.yaml", "--install-path", "/tmp/bin/ip-notify", "--service-path", "/tmp/ip-notify.service", "--start", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute install-daemon dry-run: %v", err)
	}
	for _, fragment := range []string{
		"DRY-RUN: copy /tmp/ip-notify to /tmp/bin/ip-notify",
		"DRY-RUN: start ip-notify.service",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("dry-run output missing %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestInstallDaemonCommandDefaultOptionsError(t *testing.T) {
	stubDefaultInstallOptions(t, func(string) (installpkg.Options, error) {
		return installpkg.Options{}, errors.New("default options failed")
	})

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install-daemon"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected default options error")
	}
	if !strings.Contains(err.Error(), "default options failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	if !strings.Contains(stdout.String(), "ip-notify") {
		t.Fatalf("expected version output, got %q", stdout.String())
	}
}

func TestBuildNotifiersIncludesPushover(t *testing.T) {
	cfg := minimalPushoverConfig()
	notifiers := buildNotifiers(cfg, http.DefaultClient, nil)
	if len(notifiers) != 1 || notifiers[0].Name() != "pushover" {
		t.Fatalf("expected one Pushover notifier, got %#v", notifiers)
	}
}

func TestLoggerOrDefault(t *testing.T) {
	logger := loggerOrDefault(nil)
	if logger == nil {
		t.Fatal("expected default logger")
	}
	if got := loggerOrDefault(logger); got != logger {
		t.Fatal("expected existing logger")
	}
}

func TestExecuteReturnsErrorForDefaultMissingConfig(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"ip-notify", "once", "--config", filepath.Join(t.TempDir(), "missing.yaml")}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	err := Execute()
	if err == nil {
		t.Fatal("expected Execute error")
	}
	if !strings.Contains(err.Error(), "open config") {
		t.Fatalf("unexpected Execute error: %v", err)
	}
}

func TestExitOnError(t *testing.T) {
	var stderr bytes.Buffer
	exited := false
	previousExitProcess := exitProcess
	previousStderrWriter := stderrWriter
	exitProcess = func(code int) {
		exited = true
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	}
	stderrWriter = &stderr
	t.Cleanup(func() {
		exitProcess = previousExitProcess
		stderrWriter = previousStderrWriter
	})

	ExitOnError(nil)
	if exited {
		t.Fatal("nil error should not exit")
	}

	ExitOnError(errors.New("boom"))
	if !exited {
		t.Fatal("expected process exit")
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("expected stderr to contain error, got %q", stderr.String())
	}
}

func TestRunUpdateDefaultFunction(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runUpdate(cmd, updatecmd.Options{
		Version:     "v1.2.3",
		InstallPath: filepath.Join(t.TempDir(), "ip-notify"),
		OS:          "darwin",
		Arch:        "amd64",
	})
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestUpdateCommandDryRunWiresFlagsAndPrintsPlannedLatestUpdate(t *testing.T) {
	stubRunUpdate(t, func(cmd *cobra.Command, options updatecmd.Options) error {
		if options.Version != "" {
			t.Fatalf("expected latest version default, got %q", options.Version)
		}
		if options.InstallPath != "" {
			t.Fatalf("expected install path default, got %q", options.InstallPath)
		}
		if !options.DryRun {
			t.Fatalf("expected dry-run option")
		}
		if options.NoRestart {
			t.Fatalf("did not expect no-restart option")
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "DRY-RUN: would resolve latest release from GitHub\nVersion: <latest>")
		return err
	})

	var stdout bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute update dry-run: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"DRY-RUN: would resolve latest release from GitHub",
		"Version: <latest>",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
}

func TestUpdateCommandPassesExplicitVersionAndInstallPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ip-notify")
	stubRunUpdate(t, func(_ *cobra.Command, options updatecmd.Options) error {
		if options.Version != "v9.8.7" {
			t.Fatalf("expected version v9.8.7, got %q", options.Version)
		}
		if options.InstallPath != target {
			t.Fatalf("expected install path %q, got %q", target, options.InstallPath)
		}
		if options.DryRun {
			t.Fatalf("did not expect dry-run option")
		}
		return nil
	})

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update", "--version", "v9.8.7", "--install-path", target})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute update explicit version: %v", err)
	}
}

func TestUpdateCommandNoRestartDisablesServiceChecks(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ip-notify")
	stubRunUpdate(t, func(_ *cobra.Command, options updatecmd.Options) error {
		if !options.NoRestart {
			t.Fatalf("expected no-restart option")
		}
		if options.InstallPath != target {
			t.Fatalf("expected install path %q, got %q", target, options.InstallPath)
		}
		return nil
	})

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"update", "--version", "v1.2.3", "--install-path", target, "--no-restart"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute update no-restart: %v", err)
	}
}

func writeOnceTestConfig(t *testing.T, publicIP string) (string, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ip":
			_, _ = writer.Write([]byte(publicIP))
		case "/push":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"code":200,"message":"success"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "state.json")
	data := []byte(`log:
  level: error
check:
  interval: 10m
  timeout: 5s
  notify_initial: true
  public_sources:
    - ` + server.URL + `/ip
  include_private: false
state:
  path: ` + statePath + `
notifiers:
  bark:
    enabled: true
    server_url: ` + server.URL + `
    device_keys:
      - test-device
    group: ip-notify
  pushover:
    enabled: false
    token: ""
    user: ""
    device: ""
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, server
}

func stubRunUpdate(t *testing.T, run func(*cobra.Command, updatecmd.Options) error) {
	t.Helper()
	previous := runUpdate
	runUpdate = run
	t.Cleanup(func() {
		runUpdate = previous
	})
}

func stubDefaultInstallOptions(t *testing.T, resolve func(string) (installpkg.Options, error)) {
	t.Helper()
	previous := defaultInstallOptions
	defaultInstallOptions = resolve
	t.Cleanup(func() {
		defaultInstallOptions = previous
	})
}

func minimalPushoverConfig() config.Config {
	cfg := config.Default()
	cfg.Notifiers.Pushover.Enabled = true
	cfg.Notifiers.Pushover.Token = "token"
	cfg.Notifiers.Pushover.User = "user"
	cfg.Notifiers.Pushover.Device = "phone"
	return cfg
}
