package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateResolvesLatestVersion(t *testing.T) {
	archive := buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Body: "latest-binary"})
	paths := requestPaths(t, "v1.2.3", "amd64")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/bestony/ip-notify-client/releases/latest":
			_, _ = writer.Write([]byte(`{"tag_name":"v1.2.3"}`))
		case paths.archive:
			_, _ = writer.Write(archive)
		case paths.sums:
			_, _ = fmt.Fprintf(writer, "%s  %s\n", sha256Hex(archive), paths.archiveName)
		default:
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	target := filepath.Join(t.TempDir(), "ip-notify")
	runner := &recordingRunner{}
	var output bytes.Buffer

	err := Updater{
		Client: server.Client(),
		Runner: runner,
	}.Update(context.Background(), Options{
		InstallPath:    target,
		OS:             "linux",
		Arch:           "amd64",
		APIBaseURL:     server.URL,
		ReleaseBaseURL: server.URL + "/download",
		ServiceName:    "ip-notify.service",
	}, &output)
	if err != nil {
		t.Fatalf("update latest: %v", err)
	}

	assertFileContent(t, target, "latest-binary")
	if !strings.Contains(output.String(), "Version: v1.2.3") {
		t.Fatalf("expected latest version in output:\n%s", output.String())
	}
	assertCommands(t, runner.commands, []string{
		"systemctl is-active --quiet ip-notify.service",
		"systemctl restart ip-notify.service",
	})
}

func TestUpdateExplicitVersionSkipsLatestLookup(t *testing.T) {
	archive := buildArchive(t, tarEntry{Name: "./ip-notify", Mode: 0o755, Body: "explicit-binary"})
	paths := requestPaths(t, "v9.8.7", "amd64")
	latestCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/bestony/ip-notify-client/releases/latest":
			latestCalled = true
			http.Error(writer, "latest should not be called", http.StatusInternalServerError)
		case paths.archive:
			_, _ = writer.Write(archive)
		case paths.sums:
			_, _ = fmt.Fprintf(writer, "%s  %s\n", sha256Hex(archive), paths.archiveName)
		default:
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	target := filepath.Join(t.TempDir(), "ip-notify")
	var output bytes.Buffer

	err := Updater{
		Client: server.Client(),
		Runner: &recordingRunner{},
	}.Update(context.Background(), Options{
		Version:        "v9.8.7",
		InstallPath:    target,
		OS:             "linux",
		Arch:           "amd64",
		APIBaseURL:     server.URL,
		ReleaseBaseURL: server.URL + "/download",
	}, &output)
	if err != nil {
		t.Fatalf("update explicit version: %v", err)
	}
	if latestCalled {
		t.Fatalf("explicit version should skip latest API call")
	}
	assertFileContent(t, target, "explicit-binary")
}

func TestAssetForLinuxArchitectures(t *testing.T) {
	for _, tc := range []struct {
		arch        string
		archiveName string
	}{
		{arch: "amd64", archiveName: "ip-notify_v1.0.0_linux_amd64.tar.gz"},
		{arch: "arm64", archiveName: "ip-notify_v1.0.0_linux_arm64.tar.gz"},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			arch, err := supportedPlatform("linux", tc.arch)
			if err != nil {
				t.Fatalf("supported platform: %v", err)
			}
			asset := assetFor("v1.0.0", arch, "https://example.test/releases/download/")
			if asset.ArchiveName != tc.archiveName {
				t.Fatalf("archive name mismatch: got %q want %q", asset.ArchiveName, tc.archiveName)
			}
			if asset.ArchiveURL != "https://example.test/releases/download/v1.0.0/"+tc.archiveName {
				t.Fatalf("archive URL mismatch: %q", asset.ArchiveURL)
			}
			if asset.ChecksumsURL != "https://example.test/releases/download/v1.0.0/SHA256SUMS" {
				t.Fatalf("checksums URL mismatch: %q", asset.ChecksumsURL)
			}
		})
	}
}

func TestVerifyArchiveChecksum(t *testing.T) {
	dir := t.TempDir()
	archiveName := "ip-notify_v1.0.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	checksumsPath := filepath.Join(dir, "SHA256SUMS")
	archive := []byte("archive bytes")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(checksumsPath, []byte(fmt.Sprintf("%s  %s\n", sha256Hex(archive), archiveName)), 0o600); err != nil {
		t.Fatalf("write sums: %v", err)
	}

	if err := verifyArchiveChecksum(archivePath, checksumsPath, archiveName); err != nil {
		t.Fatalf("verify checksum: %v", err)
	}
}

func TestVerifyArchiveChecksumMissingEntry(t *testing.T) {
	dir := t.TempDir()
	archiveName := "ip-notify_v1.0.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	checksumsPath := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(checksumsPath, []byte("abc  other.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("write sums: %v", err)
	}

	err := verifyArchiveChecksum(archivePath, checksumsPath, archiveName)
	if err == nil || !strings.Contains(err.Error(), "does not contain an entry") {
		t.Fatalf("expected missing checksum error, got %v", err)
	}
}

func TestVerifyArchiveChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	archiveName := "ip-notify_v1.0.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	checksumsPath := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(checksumsPath, []byte(strings.Repeat("0", 64)+"  "+archiveName+"\n"), 0o600); err != nil {
		t.Fatalf("write sums: %v", err)
	}

	err := verifyArchiveChecksum(archivePath, checksumsPath, archiveName)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
}

func TestExtractBinaryFromArchiveAcceptsRootBinary(t *testing.T) {
	archivePath := writeArchive(t, buildArchive(t, tarEntry{Name: "./README.md", Mode: 0o644, Body: "readme"}, tarEntry{Name: "./ip-notify", Mode: 0o755, Body: "binary"}))

	binary, err := extractBinaryFromArchive(archivePath)
	if err != nil {
		t.Fatalf("extract binary: %v", err)
	}
	if string(binary) != "binary" {
		t.Fatalf("unexpected binary content: %q", string(binary))
	}
}

func TestExtractBinaryFromArchiveRejectsMissingBinary(t *testing.T) {
	archivePath := writeArchive(t, buildArchive(t, tarEntry{Name: "README.md", Mode: 0o644, Body: "readme"}))

	_, err := extractBinaryFromArchive(archivePath)
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("expected missing binary error, got %v", err)
	}
}

func TestExtractBinaryFromArchiveRejectsNonExecutableBinary(t *testing.T) {
	archivePath := writeArchive(t, buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o644, Body: "binary"}))

	_, err := extractBinaryFromArchive(archivePath)
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("expected executable error, got %v", err)
	}
}

func TestExtractBinaryFromArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, unsafeName := range []string{"../ip-notify", "/ip-notify", "dir\\ip-notify"} {
		t.Run(unsafeName, func(t *testing.T) {
			archivePath := writeArchive(t, buildArchive(t, tarEntry{Name: unsafeName, Mode: 0o755, Body: "binary"}))

			_, err := extractBinaryFromArchive(archivePath)
			if err == nil || !strings.Contains(err.Error(), "unsafe") {
				t.Fatalf("expected unsafe path error, got %v", err)
			}
		})
	}
}

func TestDryRunPerformsNoDownloadReplaceOrRestart(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	runner := &recordingRunner{}
	target := filepath.Join(t.TempDir(), "ip-notify")
	var output bytes.Buffer

	err := Updater{
		Client: server.Client(),
		Runner: runner,
	}.Update(context.Background(), Options{
		Version:        "v1.2.3",
		InstallPath:    target,
		DryRun:         true,
		OS:             "linux",
		Arch:           "amd64",
		APIBaseURL:     server.URL,
		ReleaseBaseURL: server.URL + "/download",
	}, &output)
	if err != nil {
		t.Fatalf("dry-run update: %v", err)
	}
	if called {
		t.Fatalf("dry-run should not call network")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry-run should not run commands, got %#v", runner.commands)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run should not create target, stat err=%v", err)
	}
	for _, fragment := range []string{
		"Version: v1.2.3",
		"DRY-RUN: would download",
		"DRY-RUN: would replace",
		"DRY-RUN: would restart ip-notify.service only if it is active",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("dry-run output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestReplaceBinaryWritesContentAndMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), "bin", "ip-notify")

	if err := replaceBinary(target, []byte("new binary")); err != nil {
		t.Fatalf("replace binary: %v", err)
	}

	assertFileContent(t, target, "new binary")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected mode 0755, got %04o", info.Mode().Perm())
	}
}

func TestActiveServiceTriggersRestart(t *testing.T) {
	runner := &recordingRunner{}
	var output bytes.Buffer

	err := Updater{Runner: runner}.restartActiveService(context.Background(), "ip-notify.service", &output)
	if err != nil {
		t.Fatalf("restart active service: %v", err)
	}

	assertCommands(t, runner.commands, []string{
		"systemctl is-active --quiet ip-notify.service",
		"systemctl restart ip-notify.service",
	})
	if !strings.Contains(output.String(), "Restarting ip-notify.service") {
		t.Fatalf("expected restart output, got:\n%s", output.String())
	}
}

func TestInactiveServiceDoesNotRestart(t *testing.T) {
	runner := &recordingRunner{
		errors: map[string]error{
			"systemctl is-active --quiet ip-notify.service": errors.New("inactive"),
		},
	}
	var output bytes.Buffer

	err := Updater{Runner: runner}.restartActiveService(context.Background(), "ip-notify.service", &output)
	if err != nil {
		t.Fatalf("inactive service should not fail: %v", err)
	}

	assertCommands(t, runner.commands, []string{
		"systemctl is-active --quiet ip-notify.service",
	})
	if !strings.Contains(output.String(), "is not active; skipping restart") {
		t.Fatalf("expected inactive output, got:\n%s", output.String())
	}
}

func TestSystemctlUnavailableSkipsRestart(t *testing.T) {
	runner := &recordingRunner{
		errors: map[string]error{
			"systemctl is-active --quiet ip-notify.service": fmt.Errorf("%w: systemctl", ErrCommandUnavailable),
		},
	}
	var output bytes.Buffer

	err := Updater{Runner: runner}.restartActiveService(context.Background(), "ip-notify.service", &output)
	if err != nil {
		t.Fatalf("unavailable systemctl should not fail: %v", err)
	}

	assertCommands(t, runner.commands, []string{
		"systemctl is-active --quiet ip-notify.service",
	})
	if !strings.Contains(output.String(), "systemctl is unavailable; skipping restart") {
		t.Fatalf("expected unavailable output, got:\n%s", output.String())
	}
}

func TestUnsupportedPlatformFailsBeforeNetworkWork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	err := Updater{Client: server.Client()}.Update(context.Background(), Options{
		Version:        "v1.2.3",
		InstallPath:    filepath.Join(t.TempDir(), "ip-notify"),
		OS:             "darwin",
		Arch:           "amd64",
		APIBaseURL:     server.URL,
		ReleaseBaseURL: server.URL + "/download",
	}, ioDiscard{})
	if err == nil || !strings.Contains(err.Error(), "unsupported operating system") {
		t.Fatalf("expected unsupported OS error, got %v", err)
	}
	if called {
		t.Fatalf("unsupported platform should fail before network")
	}
}

func TestUnsupportedArchitectureFailsBeforeNetworkWork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	err := Updater{Client: server.Client()}.Update(context.Background(), Options{
		Version:        "v1.2.3",
		InstallPath:    filepath.Join(t.TempDir(), "ip-notify"),
		OS:             "linux",
		Arch:           "386",
		APIBaseURL:     server.URL,
		ReleaseBaseURL: server.URL + "/download",
	}, ioDiscard{})
	if err == nil || !strings.Contains(err.Error(), "unsupported architecture") {
		t.Fatalf("expected unsupported architecture error, got %v", err)
	}
	if called {
		t.Fatalf("unsupported platform should fail before network")
	}
}

type requestPathSet struct {
	archiveName string
	archive     string
	sums        string
}

type tarEntry struct {
	Name string
	Mode int64
	Body string
}

type recordingRunner struct {
	commands []string
	errors   map[string]error
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	command := name
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	r.commands = append(r.commands, command)
	if r.errors != nil {
		if err := r.errors[command]; err != nil {
			return err
		}
	}
	return nil
}

func requestPaths(t *testing.T, version, arch string) requestPathSet {
	t.Helper()
	archiveName := fmt.Sprintf("ip-notify_%s_linux_%s.tar.gz", version, arch)
	return requestPathSet{
		archiveName: archiveName,
		archive:     "/download/" + version + "/" + archiveName,
		sums:        "/download/" + version + "/SHA256SUMS",
	}
}

func buildArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		body := []byte(entry.Body)
		header := &tar.Header{
			Name: entry.Name,
			Mode: entry.Mode,
			Size: int64(len(body)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}

func writeArchive(t *testing.T, archive []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("content mismatch: got %q want %q", string(content), want)
	}
}

func assertCommands(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("command count mismatch:\ngot:  %#v\nwant: %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("command[%d] mismatch: got %q want %q", index, got[index], want[index])
		}
	}
}
