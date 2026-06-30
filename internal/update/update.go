package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultRepository     = "bestony/ip-notify-client"
	defaultAPIBaseURL     = "https://api.github.com"
	defaultReleaseBaseURL = "https://github.com/bestony/ip-notify-client/releases/download"
	DefaultServiceName    = "ip-notify.service"
	binaryName            = "ip-notify"
	binaryMode            = 0o755
)

var ErrCommandUnavailable = errors.New("command unavailable")

type Options struct {
	Version        string
	InstallPath    string
	DryRun         bool
	NoRestart      bool
	OS             string
	Arch           string
	APIBaseURL     string
	ReleaseBaseURL string
	ServiceName    string
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

type Updater struct {
	Client     *http.Client
	Runner     CommandRunner
	Logger     *slog.Logger
	Executable func() (string, error)
}

var (
	osMkdirTemp  = os.MkdirTemp
	osRemoveAll  = os.RemoveAll
	osCreate     = os.Create
	osReadFile   = os.ReadFile
	osOpen       = os.Open
	osMkdirAll   = os.MkdirAll
	osCreateTemp = os.CreateTemp
	osRemove     = os.Remove
	osRename     = os.Rename
	copyStream   = io.Copy
	readAll      = io.ReadAll
	closeFile    = func(file *os.File) error { return file.Close() }
	chmodFile    = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	syncFile     = func(file *os.File) error { return file.Sync() }
)

type releaseAsset struct {
	ArchiveName  string
	ArchiveURL   string
	ChecksumsURL string
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrCommandUnavailable, name)
	}
	return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
}

func (u Updater) Update(ctx context.Context, options Options, writer io.Writer) error {
	if writer == nil {
		writer = io.Discard
	}

	options, err := u.normalizeOptions(options)
	if err != nil {
		return err
	}

	arch, err := supportedPlatform(options.OS, options.Arch)
	if err != nil {
		return err
	}

	logger := u.logger()
	logger.Debug("selected update platform", "os", options.OS, "arch", arch)

	tag := strings.TrimSpace(options.Version)
	if tag == "" {
		if options.DryRun {
			tag = "<latest>"
			fmt.Fprintln(writer, "DRY-RUN: would resolve latest release from GitHub")
		} else {
			fmt.Fprintln(writer, "Resolving latest release from GitHub")
			tag, err = u.resolveLatestVersion(ctx, options.APIBaseURL)
			if err != nil {
				return err
			}
		}
	}

	asset := assetFor(tag, arch, options.ReleaseBaseURL)
	logger.Debug("selected release asset", "archive", asset.ArchiveName, "archive_url", asset.ArchiveURL, "checksums_url", asset.ChecksumsURL)

	fmt.Fprintf(writer, "Version: %s\n", tag)
	fmt.Fprintf(writer, "Architecture: linux/%s\n", arch)
	fmt.Fprintf(writer, "Install path: %s\n", options.InstallPath)

	if options.DryRun {
		fmt.Fprintf(writer, "DRY-RUN: would download %s\n", asset.ArchiveURL)
		fmt.Fprintf(writer, "DRY-RUN: would download %s\n", asset.ChecksumsURL)
		fmt.Fprintf(writer, "DRY-RUN: would verify %s with SHA256SUMS\n", asset.ArchiveName)
		fmt.Fprintln(writer, "DRY-RUN: would extract ip-notify binary")
		fmt.Fprintf(writer, "DRY-RUN: would replace %s atomically\n", options.InstallPath)
		if options.NoRestart {
			fmt.Fprintf(writer, "DRY-RUN: service restart disabled for %s\n", options.ServiceName)
		} else {
			fmt.Fprintf(writer, "DRY-RUN: would restart %s only if it is active\n", options.ServiceName)
		}
		return nil
	}

	tempDir, err := osMkdirTemp("", "ip-notify-update-*")
	if err != nil {
		return fmt.Errorf("create update temp directory: %w", err)
	}
	defer osRemoveAll(tempDir)

	archivePath := filepath.Join(tempDir, asset.ArchiveName)
	checksumsPath := filepath.Join(tempDir, "SHA256SUMS")

	fmt.Fprintf(writer, "Downloading %s\n", asset.ArchiveName)
	if err := u.downloadFile(ctx, asset.ArchiveURL, archivePath); err != nil {
		return err
	}
	fmt.Fprintln(writer, "Downloading SHA256SUMS")
	if err := u.downloadFile(ctx, asset.ChecksumsURL, checksumsPath); err != nil {
		return err
	}

	fmt.Fprintln(writer, "Verifying SHA256 checksum")
	if err := verifyArchiveChecksum(archivePath, checksumsPath, asset.ArchiveName); err != nil {
		return err
	}
	logger.Info("verified release archive checksum", "archive", asset.ArchiveName)

	fmt.Fprintln(writer, "Extracting ip-notify binary")
	binary, err := extractBinaryFromArchive(archivePath)
	if err != nil {
		return err
	}
	logger.Debug("extracted update binary", "bytes", len(binary))

	fmt.Fprintf(writer, "Replacing %s\n", options.InstallPath)
	if err := replaceBinary(options.InstallPath, binary); err != nil {
		return err
	}
	logger.Info("replaced installed binary", "install_path", options.InstallPath)

	if options.NoRestart {
		fmt.Fprintf(writer, "Skipping service restart because --no-restart was set for %s\n", options.ServiceName)
		logger.Info("skipped service restart by option", "service", options.ServiceName)
		fmt.Fprintln(writer, "Update complete")
		return nil
	}

	if err := u.restartActiveService(ctx, options.ServiceName, writer); err != nil {
		return err
	}
	fmt.Fprintln(writer, "Update complete")
	return nil
}

func (u Updater) normalizeOptions(options Options) (Options, error) {
	if options.OS == "" {
		options.OS = runtime.GOOS
	}
	if options.Arch == "" {
		options.Arch = runtime.GOARCH
	}
	if options.APIBaseURL == "" {
		options.APIBaseURL = defaultAPIBaseURL
	}
	if options.ReleaseBaseURL == "" {
		options.ReleaseBaseURL = defaultReleaseBaseURL
	}
	if options.ServiceName == "" {
		options.ServiceName = DefaultServiceName
	}
	if options.InstallPath == "" {
		executable := os.Executable
		if u.Executable != nil {
			executable = u.Executable
		}
		path, err := executable()
		if err != nil {
			return Options{}, fmt.Errorf("resolve current executable: %w", err)
		}
		options.InstallPath = path
	}
	return options, nil
}

func (u Updater) httpClient() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

func (u Updater) commandRunner() CommandRunner {
	if u.Runner != nil {
		return u.Runner
	}
	return ExecRunner{}
}

func (u Updater) logger() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (u Updater) resolveLatestVersion(ctx context.Context, apiBaseURL string) (string, error) {
	url := strings.TrimRight(apiBaseURL, "/") + "/repos/" + defaultRepository + "/releases/latest"
	u.logger().Debug("resolving latest release", "url", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create latest release request: %w", err)
	}
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("resolve latest release: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode latest release response: %w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", errors.New("latest release response did not include tag_name")
	}
	return tag, nil
}

func (u Updater) downloadFile(ctx context.Context, url, destination string) error {
	u.logger().Debug("downloading update asset", "url", url, "destination", destination)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request for %s: %w", url, err)
	}
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download %s: unexpected status %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	output, err := osCreate(destination)
	if err != nil {
		return fmt.Errorf("create download file %s: %w", destination, err)
	}
	defer closeFile(output)

	if _, err := copyStream(output, resp.Body); err != nil {
		return fmt.Errorf("write download file %s: %w", destination, err)
	}
	if err := closeFile(output); err != nil {
		return fmt.Errorf("close download file %s: %w", destination, err)
	}
	return nil
}

func (u Updater) restartActiveService(ctx context.Context, serviceName string, writer io.Writer) error {
	runner := u.commandRunner()
	logger := u.logger()

	err := runner.Run(ctx, "systemctl", "is-active", "--quiet", serviceName)
	if errors.Is(err, ErrCommandUnavailable) {
		fmt.Fprintf(writer, "systemctl is unavailable; skipping restart of %s\n", serviceName)
		logger.Info("skipped service restart because systemctl is unavailable", "service", serviceName)
		return nil
	}
	if err != nil {
		fmt.Fprintf(writer, "%s is not active; skipping restart\n", serviceName)
		logger.Info("skipped service restart because service is not active", "service", serviceName, "error", err)
		return nil
	}

	fmt.Fprintf(writer, "Restarting %s\n", serviceName)
	logger.Info("restarting active service", "service", serviceName)
	if err := runner.Run(ctx, "systemctl", "restart", serviceName); err != nil {
		return fmt.Errorf("restart %s: %w", serviceName, err)
	}
	return nil
}

func supportedPlatform(goos, goarch string) (string, error) {
	if goos != "linux" {
		return "", fmt.Errorf("unsupported operating system %q; ip-notify update supports linux only", goos)
	}
	switch goarch {
	case "amd64", "arm64":
		return goarch, nil
	default:
		return "", fmt.Errorf("unsupported architecture %q; supported architectures: amd64, arm64", goarch)
	}
}

func assetFor(tag, arch, baseURL string) releaseAsset {
	archiveName := fmt.Sprintf("ip-notify_%s_linux_%s.tar.gz", tag, arch)
	base := strings.TrimRight(baseURL, "/")
	return releaseAsset{
		ArchiveName:  archiveName,
		ArchiveURL:   fmt.Sprintf("%s/%s/%s", base, tag, archiveName),
		ChecksumsURL: fmt.Sprintf("%s/%s/SHA256SUMS", base, tag),
	}
}

func verifyArchiveChecksum(archivePath, checksumsPath, archiveName string) error {
	checksums, err := osReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("read SHA256SUMS: %w", err)
	}
	expected, err := checksumForArchive(checksums, archiveName)
	if err != nil {
		return err
	}

	file, err := osOpen(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := copyStream(hash, file); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", archiveName, expected, actual)
	}
	return nil
}

func checksumForArchive(checksums []byte, archiveName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] != archiveName {
			continue
		}
		hash := strings.ToLower(fields[0])
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid checksum for %s in SHA256SUMS", archiveName)
		}
		return hash, nil
	}
	return "", fmt.Errorf("SHA256SUMS does not contain an entry for %s", archiveName)
}

func extractBinaryFromArchive(archivePath string) ([]byte, error) {
	file, err := osOpen(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	var binary []byte
	found := false

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar archive: %w", err)
		}

		cleanName, err := safeTarName(header.Name)
		if err != nil {
			return nil, err
		}
		if cleanName != binaryName {
			continue
		}
		if found {
			return nil, fmt.Errorf("release archive contains multiple %s entries", binaryName)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("release archive %s entry is not a regular file", binaryName)
		}
		if os.FileMode(header.Mode).Perm()&0o111 == 0 {
			return nil, fmt.Errorf("release archive %s entry is not executable", binaryName)
		}

		data, err := readAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("extract %s from archive: %w", binaryName, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("release archive %s entry is empty", binaryName)
		}
		binary = data
		found = true
	}

	if !found {
		return nil, fmt.Errorf("release archive does not contain an executable %s binary", binaryName)
	}
	return binary, nil
}

func safeTarName(name string) (string, error) {
	if name == "" {
		return "", errors.New("release archive contains an empty path")
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("release archive contains unsafe path %q", name)
	}
	if path.IsAbs(name) {
		return "", fmt.Errorf("release archive contains unsafe absolute path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("release archive contains unsafe path %q", name)
		}
	}

	cleanName := path.Clean(name)
	return cleanName, nil
}

func replaceBinary(destination string, binary []byte) error {
	if destination == "" {
		return errors.New("install path is required")
	}
	if err := osMkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create install directory: %w", err)
	}

	temp, err := osCreateTemp(filepath.Dir(destination), ".ip-notify-update-*")
	if err != nil {
		return fmt.Errorf("create temp binary in install directory: %w", err)
	}
	tempName := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = osRemove(tempName)
		}
	}()

	if _, err := temp.Write(binary); err != nil {
		_ = closeFile(temp)
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := chmodFile(temp, binaryMode); err != nil {
		_ = closeFile(temp)
		return fmt.Errorf("chmod temp binary: %w", err)
	}
	if err := syncFile(temp); err != nil {
		_ = closeFile(temp)
		return fmt.Errorf("sync temp binary: %w", err)
	}
	if err := closeFile(temp); err != nil {
		return fmt.Errorf("close temp binary: %w", err)
	}
	if err := osRename(tempName, destination); err != nil {
		return fmt.Errorf("replace installed binary: %w", err)
	}
	removeTemp = false

	if dir, err := osOpen(filepath.Dir(destination)); err == nil {
		_ = syncFile(dir)
		_ = closeFile(dir)
	}
	return nil
}
