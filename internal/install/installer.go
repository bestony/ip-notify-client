package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	DefaultInstallPath = "/usr/local/bin/ip-notify"
	DefaultServicePath = "/etc/systemd/system/ip-notify.service"
	DefaultConfigDir   = "/etc/ip-notify"
	DefaultStateDir    = "/var/lib/ip-notify"
	DefaultUser        = "ip-notify"
	DefaultGroup       = "ip-notify"
	defaultConfigMode  = 0o640
)

type Options struct {
	ConfigPath  string
	BinaryPath  string
	InstallPath string
	ServicePath string
	ConfigDir   string
	StateDir    string
	User        string
	Group       string
	Enable      bool
	Start       bool
	DryRun      bool
}

type Operation struct {
	Description string
	run         func(context.Context) error
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type Installer struct {
	Runner CommandRunner
	EUID   func() int
}

type ExecRunner struct{}

var (
	osExecutable = os.Executable
	osMkdirAll   = os.MkdirAll
	osWriteFile  = os.WriteFile
	osOpen       = os.Open
	osOpenFile   = os.OpenFile
	osCreateTemp = os.CreateTemp
	osRemove     = os.Remove
	osRename     = os.Rename
	copyStream   = io.Copy
	writeString  = io.WriteString
	chmodFile    = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	closeFile    = func(file *os.File) error { return file.Close() }
)

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func DefaultOptions(configPath string) (Options, error) {
	executable, err := osExecutable()
	if err != nil {
		return Options{}, fmt.Errorf("resolve current executable: %w", err)
	}
	if configPath == "" {
		configPath = filepath.Join(DefaultConfigDir, "config.yaml")
	}
	return Options{
		ConfigPath:  configPath,
		BinaryPath:  executable,
		InstallPath: DefaultInstallPath,
		ServicePath: DefaultServicePath,
		ConfigDir:   filepath.Dir(configPath),
		StateDir:    DefaultStateDir,
		User:        DefaultUser,
		Group:       DefaultGroup,
		Enable:      true,
	}, nil
}

func (o Options) Normalize() Options {
	if o.ConfigPath == "" {
		o.ConfigPath = filepath.Join(DefaultConfigDir, "config.yaml")
	}
	if o.InstallPath == "" {
		o.InstallPath = DefaultInstallPath
	}
	if o.ServicePath == "" {
		o.ServicePath = DefaultServicePath
	}
	if o.ConfigDir == "" {
		o.ConfigDir = filepath.Dir(o.ConfigPath)
	}
	if o.StateDir == "" {
		o.StateDir = DefaultStateDir
	}
	if o.User == "" {
		o.User = DefaultUser
	}
	if o.Group == "" {
		o.Group = DefaultGroup
	}
	return o
}

func (i Installer) Plan(options Options) ([]Operation, error) {
	options = options.Normalize()
	if options.BinaryPath == "" {
		return nil, errors.New("binary path is required")
	}

	unit := RenderUnit(UnitOptions{
		BinaryPath: options.InstallPath,
		ConfigPath: options.ConfigPath,
		User:       options.User,
		Group:      options.Group,
		StateDir:   options.StateDir,
	})

	runner := i.Runner
	if runner == nil {
		runner = ExecRunner{}
	}

	operations := []Operation{
		{
			Description: fmt.Sprintf("copy %s to %s", options.BinaryPath, options.InstallPath),
			run: func(context.Context) error {
				return copyFile(options.BinaryPath, options.InstallPath, 0o755)
			},
		},
		{
			Description: fmt.Sprintf("ensure system group %s exists", options.Group),
			run: func(ctx context.Context) error {
				if err := runner.Run(ctx, "getent", "group", options.Group); err == nil {
					return nil
				}
				return runner.Run(ctx, "groupadd", "--system", options.Group)
			},
		},
		{
			Description: fmt.Sprintf("ensure system user %s exists", options.User),
			run: func(ctx context.Context) error {
				if err := runner.Run(ctx, "id", "-u", options.User); err == nil {
					return nil
				}
				return runner.Run(ctx, "useradd",
					"--system",
					"--home-dir", options.StateDir,
					"--no-create-home",
					"--shell", "/usr/sbin/nologin",
					"--gid", options.Group,
					options.User,
				)
			},
		},
		{
			Description: fmt.Sprintf("create config directory %s", options.ConfigDir),
			run: func(context.Context) error {
				return osMkdirAll(options.ConfigDir, 0o750)
			},
		},
		{
			Description: fmt.Sprintf("set ownership root:%s on %s", options.Group, options.ConfigDir),
			run: func(ctx context.Context) error {
				return runner.Run(ctx, "chown", "root:"+options.Group, options.ConfigDir)
			},
		},
		{
			Description: fmt.Sprintf("write default config %s if missing; skip existing file", options.ConfigPath),
			run: func(ctx context.Context) error {
				created, err := writeDefaultConfigIfMissing(options.ConfigPath, defaultConfigMode)
				if err != nil {
					return err
				}
				if !created {
					return nil
				}
				if err := runner.Run(ctx, "chown", "root:"+options.Group, options.ConfigPath); err != nil {
					_ = osRemove(options.ConfigPath)
					return fmt.Errorf("set default config ownership: %w", err)
				}
				return nil
			},
		},
		{
			Description: fmt.Sprintf("create state directory %s", options.StateDir),
			run: func(context.Context) error {
				return osMkdirAll(options.StateDir, 0o750)
			},
		},
		{
			Description: fmt.Sprintf("set ownership %s:%s on %s", options.User, options.Group, options.StateDir),
			run: func(ctx context.Context) error {
				return runner.Run(ctx, "chown", options.User+":"+options.Group, options.StateDir)
			},
		},
		{
			Description: fmt.Sprintf("write systemd unit %s", options.ServicePath),
			run: func(context.Context) error {
				return osWriteFile(options.ServicePath, []byte(unit), 0o644)
			},
		},
		{
			Description: "run systemctl daemon-reload",
			run: func(ctx context.Context) error {
				return runner.Run(ctx, "systemctl", "daemon-reload")
			},
		},
	}

	if options.Enable {
		operations = append(operations, Operation{
			Description: "enable ip-notify.service",
			run: func(ctx context.Context) error {
				return runner.Run(ctx, "systemctl", "enable", "ip-notify.service")
			},
		})
	}
	if options.Start {
		operations = append(operations, Operation{
			Description: "start ip-notify.service",
			run: func(ctx context.Context) error {
				return runner.Run(ctx, "systemctl", "start", "ip-notify.service")
			},
		})
	}

	return operations, nil
}

func (i Installer) Install(ctx context.Context, options Options, writer io.Writer) error {
	options = options.Normalize()
	operations, err := i.Plan(options)
	if err != nil {
		return err
	}

	if options.DryRun {
		for _, operation := range operations {
			fmt.Fprintf(writer, "DRY-RUN: %s\n", operation.Description)
		}
		return nil
	}

	euid := os.Geteuid
	if i.EUID != nil {
		euid = i.EUID
	}
	if euid() != 0 {
		return errors.New("install-daemon requires root privileges; rerun with sudo or use --dry-run to inspect planned operations")
	}

	for _, operation := range operations {
		fmt.Fprintf(writer, "Running: %s\n", operation.Description)
		if err := operation.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", operation.Description, err)
		}
	}
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := osOpen(source)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer input.Close()

	if err := osMkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	temp, err := osCreateTemp(filepath.Dir(destination), ".ip-notify-*")
	if err != nil {
		return fmt.Errorf("create temp destination: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = osRemove(tempName)
	}()

	if _, err := copyStream(temp, input); err != nil {
		_ = closeFile(temp)
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := chmodFile(temp, mode); err != nil {
		_ = closeFile(temp)
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := closeFile(temp); err != nil {
		return fmt.Errorf("close copied binary: %w", err)
	}
	if err := osRename(tempName, destination); err != nil {
		return fmt.Errorf("replace destination binary: %w", err)
	}
	return nil
}
