package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"bestony.com/ip-notify-client/internal/config"
	"bestony.com/ip-notify-client/internal/daemon"
	"bestony.com/ip-notify-client/internal/install"
	"bestony.com/ip-notify-client/internal/ipdetect"
	"bestony.com/ip-notify-client/internal/logging"
	"bestony.com/ip-notify-client/internal/notify"
	"bestony.com/ip-notify-client/internal/state"
	"bestony.com/ip-notify-client/internal/version"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "ip-notify",
		Short:         "Notify when public or local interface IP addresses change",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCommand())
	root.AddCommand(newOnceCommand())
	root.AddCommand(newInstallDaemonCommand())
	root.AddCommand(newVersionCommand())

	return root
}

func newRunCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the IP notification daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, cfg, err := newRunner(configPath)
			if err != nil {
				return err
			}
			loggerOrDefault(runner.Logger).Info("starting ip-notify", "config_path", configPath, "notifiers", cfg.EnabledNotifierNames())
			return runner.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "path to YAML config file")
	return cmd
}

func newOnceCommand() *cobra.Command {
	var configPath string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "once",
		Short: "Run one IP check and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, cfg, err := newRunner(configPath)
			if err != nil {
				return err
			}
			loggerOrDefault(runner.Logger).Info("running one ip-notify check", "config_path", configPath, "notifiers", cfg.EnabledNotifierNames())

			result, err := runner.ProcessOnceResult(cmd.Context())
			if err != nil {
				return err
			}
			return writeProcessResult(cmd.OutOrStdout(), result, jsonOutput)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "path to YAML config file")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the result as JSON")
	return cmd
}

func newInstallDaemonCommand() *cobra.Command {
	var configPath string
	var installPath string
	var servicePath string
	var start bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "install-daemon",
		Short: "Install ip-notify as a systemd service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options, err := install.DefaultOptions(configPath)
			if err != nil {
				return err
			}
			options.InstallPath = installPath
			options.ServicePath = servicePath
			options.Start = start
			options.DryRun = dryRun

			installer := install.Installer{}
			return installer.Install(cmd.Context(), options, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultPath, "service config path")
	cmd.Flags().StringVar(&installPath, "install-path", install.DefaultInstallPath, "destination path for the ip-notify binary")
	cmd.Flags().StringVar(&servicePath, "service-path", install.DefaultServicePath, "systemd service unit path")
	cmd.Flags().BoolVar(&start, "start", false, "start the service after installing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print planned operations without changing the system")
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}

func buildNotifiers(cfg config.Config, client *http.Client, logger *slog.Logger) []notify.Notifier {
	notifiers := make([]notify.Notifier, 0, 2)
	if cfg.Notifiers.Bark.Enabled {
		notifiers = append(notifiers, notify.NewBark(notify.BarkOptions{
			ServerURL:  cfg.Notifiers.Bark.ServerURL,
			DeviceKey:  cfg.Notifiers.Bark.DeviceKey,
			DeviceKeys: cfg.Notifiers.Bark.DeviceKeys,
			Group:      cfg.Notifiers.Bark.Group,
		}, client, logger))
	}
	if cfg.Notifiers.Pushover.Enabled {
		notifiers = append(notifiers, notify.NewPushover(notify.PushoverOptions{
			Token:  cfg.Notifiers.Pushover.Token,
			User:   cfg.Notifiers.Pushover.User,
			Device: cfg.Notifiers.Pushover.Device,
		}, client, logger))
	}
	return notifiers
}

func newRunner(configPath string) (daemon.Runner, config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return daemon.Runner{}, config.Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return daemon.Runner{}, config.Config{}, fmt.Errorf("invalid config: %w", err)
	}

	logger, err := logging.New(cfg.Log.Level)
	if err != nil {
		return daemon.Runner{}, config.Config{}, err
	}

	client := &http.Client{Timeout: cfg.Check.Timeout.Duration}
	detector := ipdetect.Detector{
		Public: ipdetect.PublicResolver{
			Client: client,
			Logger: logger,
		},
		Interface: ipdetect.InterfaceCollector{
			Logger: logger,
		},
		Logger: logger,
	}

	return daemon.Runner{
		Config:    cfg,
		Detector:  detector,
		Store:     state.New(cfg.State.Path),
		Notifiers: buildNotifiers(cfg, client, logger),
		Logger:    logger,
	}, cfg, nil
}

func writeProcessResult(writer io.Writer, result daemon.ProcessResult, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	_, err := fmt.Fprintf(writer, "%s\nSnapshot Hash: %s\nChanged: %t\n", result.Snapshot.Body(), result.Hash, result.Changed)
	return err
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

func Execute() error {
	return NewRootCommand().Execute()
}

func ExitOnError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
