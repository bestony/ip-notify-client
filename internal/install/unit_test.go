package install

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	unit := RenderUnit(UnitOptions{
		BinaryPath: "/usr/local/bin/ip-notify",
		ConfigPath: "/etc/ip-notify/config.yaml",
		User:       "ip-notify",
		Group:      "ip-notify",
		StateDir:   "/var/lib/ip-notify",
	})

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

func TestDefaultOptionsUsesDefaultConfigPathAndExecutableErrors(t *testing.T) {
	restoreInstallSeams(t)
	osExecutable = func() (string, error) {
		return "/proc/self/exe", nil
	}
	options, err := DefaultOptions("")
	if err != nil {
		t.Fatalf("default options: %v", err)
	}
	if options.ConfigPath != filepath.Join(DefaultConfigDir, "config.yaml") {
		t.Fatalf("unexpected default config path: %q", options.ConfigPath)
	}
	if options.BinaryPath != "/proc/self/exe" {
		t.Fatalf("unexpected binary path: %q", options.BinaryPath)
	}

	restoreInstallSeams(t)
	osExecutable = func() (string, error) {
		return "", errors.New("executable failed")
	}
	if _, err := DefaultOptions(""); err == nil {
		t.Fatal("expected executable error")
	}
}

func TestOptionsNormalizeFillsDefaults(t *testing.T) {
	options := (Options{}).Normalize()
	if options.ConfigPath != filepath.Join(DefaultConfigDir, "config.yaml") {
		t.Fatalf("unexpected config path: %q", options.ConfigPath)
	}
	if options.InstallPath != DefaultInstallPath {
		t.Fatalf("unexpected install path: %q", options.InstallPath)
	}
	if options.ServicePath != DefaultServicePath {
		t.Fatalf("unexpected service path: %q", options.ServicePath)
	}
	if options.ConfigDir != DefaultConfigDir {
		t.Fatalf("unexpected config dir: %q", options.ConfigDir)
	}
	if options.StateDir != DefaultStateDir || options.User != DefaultUser || options.Group != DefaultGroup {
		t.Fatalf("unexpected normalized options: %#v", options)
	}
}

func TestPlanRequiresBinaryPath(t *testing.T) {
	if _, err := (Installer{}).Plan(Options{}); err == nil {
		t.Fatal("expected missing binary path error")
	}
}

func TestPlanUsesExecRunnerWhenRunnerMissing(t *testing.T) {
	restoreInstallSeams(t)
	operations, err := (Installer{}).Plan(Options{BinaryPath: "/tmp/ip-notify"})
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	if len(operations) == 0 {
		t.Fatal("expected operations")
	}
}

func TestInstallerPlanOperations(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source", "ip-notify")
	destination := filepath.Join(tempDir, "bin", "ip-notify")
	configDir := filepath.Join(tempDir, "etc", "ip-notify")
	configPath := filepath.Join(configDir, "config.yaml")
	stateDir := filepath.Join(tempDir, "state")
	servicePath := filepath.Join(tempDir, "ip-notify.service")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatalf("write source binary: %v", err)
	}
	runner := &recordingRunner{
		errors: map[string]error{
			"getent group ip-notify": errors.New("missing group"),
			"id -u ip-notify":        errors.New("missing user"),
		},
	}
	options := Options{
		ConfigPath:  configPath,
		BinaryPath:  source,
		InstallPath: destination,
		ServicePath: servicePath,
		ConfigDir:   configDir,
		StateDir:    stateDir,
		User:        "ip-notify",
		Group:       "ip-notify",
		Enable:      true,
		Start:       true,
	}

	operations, err := (Installer{Runner: runner}).Plan(options)
	if err != nil {
		t.Fatalf("plan install: %v", err)
	}
	for _, operation := range operations {
		if err := operation.run(context.Background()); err != nil {
			t.Fatalf("run operation %q: %v", operation.Description, err)
		}
	}

	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination binary: %v", err)
	}
	if string(content) != "binary" {
		t.Fatalf("unexpected copied binary: %q", string(content))
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("expected state dir: %v", err)
	}
	unit, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("read service unit: %v", err)
	}
	if !strings.Contains(string(unit), "ExecStart="+destination+" run --config "+configPath) {
		t.Fatalf("unexpected unit:\n%s", string(unit))
	}
	assertCommands(t, runner.commands, []string{
		"getent group ip-notify",
		"groupadd --system ip-notify",
		"id -u ip-notify",
		"useradd --system --home-dir " + stateDir + " --no-create-home --shell /usr/sbin/nologin --gid ip-notify ip-notify",
		"chown root:ip-notify " + configDir,
		"chown root:ip-notify " + configPath,
		"chown ip-notify:ip-notify " + stateDir,
		"systemctl daemon-reload",
		"systemctl enable ip-notify.service",
		"systemctl start ip-notify.service",
	})
}

func TestPlanGroupAndUserAlreadyExist(t *testing.T) {
	runner := &recordingRunner{}
	options := Options{
		ConfigPath:  filepath.Join(t.TempDir(), "config.yaml"),
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   "/etc/ip-notify",
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}
	if err := runPlanOperation(t, Installer{Runner: runner}, options, "ensure system group"); err != nil {
		t.Fatalf("group operation: %v", err)
	}
	if err := runPlanOperation(t, Installer{Runner: runner}, options, "ensure system user"); err != nil {
		t.Fatalf("user operation: %v", err)
	}
	assertCommands(t, runner.commands, []string{
		"getent group ip-notify",
		"id -u ip-notify",
	})
}

func TestWriteDefaultConfigErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	restoreInstallSeams(t)
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("create failed")
	}
	if _, err := writeDefaultConfigIfMissing(path, defaultConfigMode); err == nil {
		t.Fatal("expected create error")
	}

	restoreInstallSeams(t)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, defaultConfigMode)
	if err != nil {
		t.Fatalf("create config file: %v", err)
	}
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) {
		return file, nil
	}
	writeString = func(io.Writer, string) (int, error) {
		return 0, errors.New("write failed")
	}
	osRemove = func(string) error { return nil }
	if _, err := writeDefaultConfigIfMissing(path, defaultConfigMode); err == nil {
		t.Fatal("expected write error")
	}

	restoreInstallSeams(t)
	path = filepath.Join(t.TempDir(), "config.yaml")
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, defaultConfigMode)
	if err != nil {
		t.Fatalf("create config file: %v", err)
	}
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) { return file, nil }
	chmodFile = func(*os.File, os.FileMode) error { return errors.New("chmod failed") }
	osRemove = func(string) error { return nil }
	if _, err := writeDefaultConfigIfMissing(path, defaultConfigMode); err == nil {
		t.Fatal("expected chmod error")
	}

	restoreInstallSeams(t)
	path = filepath.Join(t.TempDir(), "config.yaml")
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, defaultConfigMode)
	if err != nil {
		t.Fatalf("create config file: %v", err)
	}
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) { return file, nil }
	closeFile = func(*os.File) error { return errors.New("close failed") }
	osRemove = func(string) error { return nil }
	if _, err := writeDefaultConfigIfMissing(path, defaultConfigMode); err == nil {
		t.Fatal("expected close error")
	}
}

func TestWriteDefaultConfigOwnershipFailureRemovesCreatedFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	removed := false
	restoreInstallSeams(t)
	osRemove = func(path string) error {
		removed = true
		return os.Remove(path)
	}
	runner := &recordingRunner{
		errors: map[string]error{
			"chown root:ip-notify " + configPath: errors.New("chown failed"),
		},
	}

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
	if err == nil {
		t.Fatal("expected chown error")
	}
	if !removed {
		t.Fatal("expected created config to be removed")
	}
}

func TestWriteDefaultConfigOperationReturnsCreateError(t *testing.T) {
	restoreInstallSeams(t)
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("create failed")
	}

	err := runPlanOperation(t, Installer{Runner: &recordingRunner{}}, Options{
		ConfigPath:  filepath.Join(t.TempDir(), "config.yaml"),
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigDir:   "/etc/ip-notify",
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, "write default config")
	if err == nil {
		t.Fatal("expected default config create error")
	}
}

func TestInstallErrors(t *testing.T) {
	var output strings.Builder
	err := (Installer{}).Install(context.Background(), Options{DryRun: true}, &output)
	if err == nil {
		t.Fatal("expected plan error")
	}

	err = (Installer{
		EUID: func() int { return 1000 },
	}).Install(context.Background(), Options{
		BinaryPath: "/tmp/ip-notify",
	}, &output)
	if err == nil {
		t.Fatal("expected root privilege error")
	}

	runner := &recordingRunner{
		errors: map[string]error{
			"getent group ip-notify":      errors.New("missing group"),
			"groupadd --system ip-notify": errors.New("groupadd failed"),
		},
	}
	err = (Installer{
		Runner: runner,
		EUID:   func() int { return 0 },
	}).Install(context.Background(), Options{
		BinaryPath:  "/tmp/ip-notify",
		InstallPath: "/usr/local/bin/ip-notify",
		ServicePath: "/etc/systemd/system/ip-notify.service",
		ConfigPath:  "/etc/ip-notify/config.yaml",
		ConfigDir:   "/etc/ip-notify",
		StateDir:    "/var/lib/ip-notify",
		User:        "ip-notify",
		Group:       "ip-notify",
	}, &output)
	if err == nil {
		t.Fatal("expected operation error")
	}

	source := filepath.Join(t.TempDir(), "ip-notify")
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	output.Reset()
	err = (Installer{
		Runner: &recordingRunner{},
		EUID:   func() int { return 0 },
	}).Install(context.Background(), Options{
		BinaryPath:  source,
		InstallPath: filepath.Join(t.TempDir(), "bin", "ip-notify"),
		ServicePath: filepath.Join(t.TempDir(), "ip-notify.service"),
		ConfigPath:  filepath.Join(t.TempDir(), "config.yaml"),
		ConfigDir:   t.TempDir(),
		StateDir:    t.TempDir(),
		User:        "ip-notify",
		Group:       "ip-notify",
	}, &output)
	if err != nil {
		t.Fatalf("expected successful install: %v", err)
	}
	if !strings.Contains(output.String(), "Running:") {
		t.Fatalf("expected running output, got %q", output.String())
	}
}

func TestExecRunner(t *testing.T) {
	if err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "exit 0"); err != nil {
		t.Fatalf("expected command success: %v", err)
	}
	if err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "echo nope; exit 1"); err == nil {
		t.Fatal("expected command failure")
	}
}

func TestCopyFile(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "bin", "ip-notify")
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyFile(source, destination, 0o755); err != nil {
		t.Fatalf("copy file: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(content) != "binary" {
		t.Fatalf("unexpected destination content: %q", string(content))
	}
}

func TestCopyFileErrors(t *testing.T) {
	if err := copyFile(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dest"), 0o755); err == nil {
		t.Fatal("expected open source error")
	}

	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "bin", "ip-notify")

	restoreInstallSeams(t)
	osMkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected mkdir error")
	}

	restoreInstallSeams(t)
	osCreateTemp = func(string, string) (*os.File, error) {
		return nil, errors.New("create temp failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected create temp error")
	}

	restoreInstallSeams(t)
	copyStream = func(io.Writer, io.Reader) (int64, error) {
		return 0, errors.New("copy failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected copy error")
	}

	restoreInstallSeams(t)
	chmodFile = func(*os.File, os.FileMode) error {
		return errors.New("chmod failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected chmod error")
	}

	restoreInstallSeams(t)
	closeFile = func(*os.File) error {
		return errors.New("close failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected close error")
	}

	restoreInstallSeams(t)
	osRename = func(string, string) error {
		return errors.New("rename failed")
	}
	if err := copyFile(source, destination, 0o755); err == nil {
		t.Fatal("expected rename error")
	}
}

type recordingRunner struct {
	commands []string
	errors   map[string]error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	command := name + " " + strings.Join(args, " ")
	r.commands = append(r.commands, command)
	if err := r.errors[command]; err != nil {
		return err
	}
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

func restoreInstallSeams(t *testing.T) {
	t.Helper()

	osExecutable = os.Executable
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile
	osOpen = os.Open
	osOpenFile = os.OpenFile
	osCreateTemp = os.CreateTemp
	osRemove = os.Remove
	osRename = os.Rename
	copyStream = io.Copy
	writeString = io.WriteString
	chmodFile = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	closeFile = func(file *os.File) error { return file.Close() }
	t.Cleanup(func() {
		osExecutable = os.Executable
		osMkdirAll = os.MkdirAll
		osWriteFile = os.WriteFile
		osOpen = os.Open
		osOpenFile = os.OpenFile
		osCreateTemp = os.CreateTemp
		osRemove = os.Remove
		osRename = os.Rename
		copyStream = io.Copy
		writeString = io.WriteString
		chmodFile = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
		closeFile = func(file *os.File) error { return file.Close() }
	})
}
