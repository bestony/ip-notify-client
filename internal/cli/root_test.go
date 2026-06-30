package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bestony.com/ip-notify-client/internal/ipdetect"
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
