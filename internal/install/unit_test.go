package install

import (
	"context"
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
