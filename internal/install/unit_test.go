package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	unit, err := RenderUnit(UnitOptions{
		BinaryPath: "/usr/local/bin/ip-notify",
		ConfigPath: "/etc/ip-notify/config.yaml",
		User:       "ip-notify",
		Group:      "ip-notify",
		StateDir:   "/var/lib/ip-notify",
	})
	if err != nil {
		t.Fatalf("render unit: %v", err)
	}

	for _, fragment := range []string{
		"ExecStart=/usr/local/bin/ip-notify run --config /etc/ip-notify/config.yaml",
		"After=network-online.target",
		"Wants=network-online.target",
		"User=ip-notify",
		"Group=ip-notify",
		"Restart=on-failure",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"PrivateTmp=true",
		"ReadWritePaths=/var/lib/ip-notify",
	} {
		if !strings.Contains(unit, fragment) {
			t.Fatalf("unit missing fragment %q:\n%s", fragment, unit)
		}
	}
}

func TestDefaultConfigMatchesExample(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "configs", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	if string(example) != defaultConfigYAML {
		t.Fatalf("default config must match configs/config.example.yaml")
	}
}

func TestInstallerPlanDryRunDoesNotRunCommands(t *testing.T) {
	runner := &recordingRunner{}
	installer := Installer{
		Runner: runner,
		EUID:   func() int { return 1000 },
	}

	var output strings.Builder
	err := installer.Install(context.Background(), Options{
		ConfigPath:  "/etc/ip-notify/config.yaml",
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   "/etc/ip-notify",
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
		Enable:      true,
		DryRun:      true,
	}, &output)
	if err != nil {
		t.Fatalf("dry-run install: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry-run should not run commands, got %#v", runner.commands)
	}
	for _, fragment := range []string{
		"copy /tmp/ip-notify to /usr/local/bin/ip-notify",
		"ensure system group ip-notify exists",
		"ensure system user ip-notify exists",
		"create config directory /etc/ip-notify",
		"write default config /etc/ip-notify/config.yaml if missing; skip existing file",
		"create state directory /var/lib/ip-notify",
		"write systemd unit /etc/systemd/system/ip-notify.service",
		"run systemctl daemon-reload",
		"enable ip-notify.service",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("dry-run output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestInstallerPlanCreatesDefaultConfigIfMissing(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "etc", "ip-notify", "config.yaml")
	runner := &recordingRunner{}

	err := runPlanOperation(t, Installer{Runner: runner}, Options{
		ConfigPath:  configPath,
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "create config directory")
	if err != nil {
		t.Fatalf("create config directory operation: %v", err)
	}

	err = runPlanOperation(t, Installer{Runner: runner}, Options{
		ConfigPath:  configPath,
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "write default config")
	if err != nil {
		t.Fatalf("write default config operation: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read created config: %v", err)
	}
	if string(content) != defaultConfigYAML {
		t.Fatalf("created config content mismatch:\n%s", string(content))
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat created config: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected config mode 0640, got %04o", info.Mode().Perm())
	}

	assertCommands(t, runner.commands, []string{"chown root:ip-notify " + configPath})
}

func TestInstallerPlanKeepsExistingConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	original := []byte("notifiers:\n  bark:\n    enabled: true\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}
	runner := &recordingRunner{}

	err := runPlanOperation(t, Installer{Runner: runner}, Options{
		ConfigPath:  configPath,
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "write default config")
	if err != nil {
		t.Fatalf("write default config operation: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if string(content) != string(original) {
		t.Fatalf("existing config was overwritten:\n%s", string(content))
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat existing config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected existing mode to remain 0600, got %04o", info.Mode().Perm())
	}

	assertCommands(t, runner.commands, nil)
}

func TestInstallerPlanCreatesDefaultConfigAtCustomPath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "custom", "path", "config.yaml")
	runner := &recordingRunner{}

	err := runPlanOperation(t, Installer{Runner: runner}, Options{
		ConfigPath:  configPath,
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "create config directory")
	if err != nil {
		t.Fatalf("create config directory operation: %v", err)
	}

	err = runPlanOperation(t, Installer{Runner: runner}, Options{
		ConfigPath:  configPath,
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "write default config")
	if err != nil {
		t.Fatalf("write default config operation: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read custom config: %v", err)
	}
	if string(content) != defaultConfigYAML {
		t.Fatalf("custom config content mismatch:\n%s", string(content))
	}

	info, err := os.Stat(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("stat custom config directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected custom config directory to be a directory")
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("expected custom config directory mode 0750, got %04o", info.Mode().Perm())
	}
}

func TestDefaultOptionsDerivesConfigDirFromConfigPath(t *testing.T) {
	options, err := DefaultOptions("/custom/ip-notify/config.yaml")
	if err != nil {
		t.Fatalf("default options: %v", err)
	}
	if options.ConfigDir != "/custom/ip-notify" {
		t.Fatalf("expected config dir from config path, got %q", options.ConfigDir)
	}
}

type recordingRunner struct {
	commands []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	return nil
}

func runPlanOperation(t *testing.T, installer Installer, options Options, description string) error {
	t.Helper()

	operations, err := installer.Plan(options)
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	for _, operation := range operations {
		if strings.Contains(operation.Description, description) {
			return operation.run(context.Background())
		}
	}
	t.Fatalf("operation %q not found", description)
	return nil
}

func assertCommands(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("commands mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("command %d mismatch: got %q, want %q", index, got[index], want[index])
		}
	}
}
