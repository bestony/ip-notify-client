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
	"io"
	"log/slog"
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

func TestExecRunner(t *testing.T) {
	if err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "exit 0"); err != nil {
		t.Fatalf("expected command success: %v", err)
	}
	if err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "echo nope; exit 1"); err == nil {
		t.Fatal("expected command failure")
	}
	if err := (ExecRunner{}).Run(context.Background(), "definitely-missing-ip-notify-command"); !errors.Is(err, ErrCommandUnavailable) {
		t.Fatalf("expected unavailable command error, got %v", err)
	}
}

func TestUpdateDryRunLatestAndNoRestart(t *testing.T) {
	var output bytes.Buffer
	err := Updater{
		Executable: func() (string, error) { return "/usr/local/bin/ip-notify", nil },
	}.Update(context.Background(), Options{
		DryRun:    true,
		NoRestart: true,
		OS:        "linux",
		Arch:      "amd64",
	}, &output)
	if err != nil {
		t.Fatalf("dry-run latest update: %v", err)
	}
	for _, fragment := range []string{
		"DRY-RUN: would resolve latest release from GitHub",
		"Version: <latest>",
		"DRY-RUN: service restart disabled for ip-notify.service",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("dry-run output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestUpdateNormalizesDefaultsAndHandlesNilWriter(t *testing.T) {
	err := Updater{
		Executable: func() (string, error) { return "/usr/local/bin/ip-notify", nil },
	}.Update(context.Background(), Options{
		Version: "v1.2.3",
		DryRun:  true,
		OS:      "linux",
		Arch:    "amd64",
	}, nil)
	if err != nil {
		t.Fatalf("dry-run with nil writer: %v", err)
	}
}

func TestUpdateNormalizeExecutableError(t *testing.T) {
	err := Updater{
		Executable: func() (string, error) { return "", errors.New("executable failed") },
	}.Update(context.Background(), Options{
		Version: "v1.2.3",
		DryRun:  true,
		OS:      "linux",
		Arch:    "amd64",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected executable error")
	}
}

func TestNormalizeOptionsFillsRuntimeDefaults(t *testing.T) {
	options, err := (Updater{}).normalizeOptions(Options{
		InstallPath: "/usr/local/bin/ip-notify",
	})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if options.OS == "" || options.Arch == "" {
		t.Fatalf("expected runtime platform defaults, got %#v", options)
	}
	if options.APIBaseURL != defaultAPIBaseURL {
		t.Fatalf("unexpected API base URL: %q", options.APIBaseURL)
	}
	if options.ReleaseBaseURL != defaultReleaseBaseURL {
		t.Fatalf("unexpected release base URL: %q", options.ReleaseBaseURL)
	}
	if options.ServiceName != DefaultServiceName {
		t.Fatalf("unexpected service name: %q", options.ServiceName)
	}
}

func TestNormalizeOptionsUsesCustomExecutable(t *testing.T) {
	options, err := Updater{
		Executable: func() (string, error) { return "/tmp/ip-notify", nil },
	}.normalizeOptions(Options{
		OS:             "linux",
		Arch:           "amd64",
		APIBaseURL:     "https://api.example.test",
		ReleaseBaseURL: "https://release.example.test",
		ServiceName:    "custom.service",
	})
	if err != nil {
		t.Fatalf("normalize options: %v", err)
	}
	if options.InstallPath != "/tmp/ip-notify" {
		t.Fatalf("unexpected install path: %q", options.InstallPath)
	}
}

func TestUpdateNonDryRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		version string
		prepare func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server
		seam    func(t *testing.T)
		want    string
	}{
		{
			name:    "latest lookup",
			version: "",
			prepare: func(t *testing.T, _ []byte, _ requestPathSet) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "bad", http.StatusInternalServerError)
				}))
			},
			want: "resolve latest release",
		},
		{
			name: "mkdir temp",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return releaseServer(t, archive, paths, nil)
			},
			seam: func(t *testing.T) {
				restoreUpdateSeams(t)
				osMkdirTemp = func(string, string) (string, error) { return "", errors.New("mkdir temp failed") }
			},
			want: "create update temp directory",
		},
		{
			name: "archive download",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return releaseServer(t, archive, paths, map[string]int{paths.archive: http.StatusInternalServerError})
			},
			want: "unexpected status",
		},
		{
			name: "checksums download",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return releaseServer(t, archive, paths, map[string]int{paths.sums: http.StatusInternalServerError})
			},
			want: "SHA256SUMS",
		},
		{
			name: "checksum verify",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					switch request.URL.Path {
					case paths.archive:
						_, _ = writer.Write(archive)
					case paths.sums:
						_, _ = fmt.Fprintf(writer, "%s  %s\n", strings.Repeat("0", 64), paths.archiveName)
					default:
						t.Fatalf("unexpected request path: %s", request.URL.Path)
					}
				}))
			},
			want: "checksum mismatch",
		},
		{
			name: "extract",
			prepare: func(t *testing.T, _ []byte, paths requestPathSet) *httptest.Server {
				archive := buildArchive(t, tarEntry{Name: "README.md", Mode: 0o644, Body: "readme"})
				return releaseServer(t, archive, paths, nil)
			},
			want: "does not contain",
		},
		{
			name: "replace",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return releaseServer(t, archive, paths, nil)
			},
			seam: func(t *testing.T) {
				restoreUpdateSeams(t)
				osRename = func(string, string) error { return errors.New("rename failed") }
			},
			want: "replace installed binary",
		},
		{
			name: "restart",
			prepare: func(t *testing.T, archive []byte, paths requestPathSet) *httptest.Server {
				return releaseServer(t, archive, paths, nil)
			},
			want: "restart ip-notify.service",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restoreUpdateSeams(t)
			archive := buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Body: "binary"})
			paths := requestPaths(t, "v1.2.3", "amd64")
			server := test.prepare(t, archive, paths)
			t.Cleanup(server.Close)
			if test.seam != nil {
				test.seam(t)
			}
			version := test.version
			if test.name != "latest lookup" {
				version = "v1.2.3"
			}
			runner := &recordingRunner{
				errors: map[string]error{
					"systemctl restart ip-notify.service": errors.New("restart failed"),
				},
			}

			err := Updater{
				Client: server.Client(),
				Runner: runner,
			}.Update(context.Background(), Options{
				Version:        version,
				InstallPath:    filepath.Join(t.TempDir(), "ip-notify"),
				OS:             "linux",
				Arch:           "amd64",
				APIBaseURL:     server.URL,
				ReleaseBaseURL: server.URL + "/download",
			}, &bytes.Buffer{})
			if err == nil {
				t.Fatal("expected update error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestUpdateNoRestartCompletes(t *testing.T) {
	archive := buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Body: "binary"})
	paths := requestPaths(t, "v1.2.3", "amd64")
	server := releaseServer(t, archive, paths, nil)
	t.Cleanup(server.Close)

	var output bytes.Buffer
	err := Updater{
		Client: server.Client(),
		Runner: &recordingRunner{},
	}.Update(context.Background(), Options{
		Version:        "v1.2.3",
		InstallPath:    filepath.Join(t.TempDir(), "ip-notify"),
		NoRestart:      true,
		OS:             "linux",
		Arch:           "amd64",
		ReleaseBaseURL: server.URL + "/download",
	}, &output)
	if err != nil {
		t.Fatalf("update no restart: %v", err)
	}
	if !strings.Contains(output.String(), "Skipping service restart") || !strings.Contains(output.String(), "Update complete") {
		t.Fatalf("unexpected output:\n%s", output.String())
	}
}

func TestUpdaterDefaults(t *testing.T) {
	client := &http.Client{}
	runner := &recordingRunner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	updater := Updater{Client: client, Runner: runner, Logger: logger}
	if updater.httpClient() != client {
		t.Fatal("expected custom client")
	}
	if updater.commandRunner() != runner {
		t.Fatal("expected custom runner")
	}
	if updater.logger() != logger {
		t.Fatal("expected custom logger")
	}
	if (Updater{}).httpClient() != http.DefaultClient {
		t.Fatal("expected default client")
	}
	if (Updater{}).commandRunner() == nil {
		t.Fatal("expected default runner")
	}
	if (Updater{}).logger() == nil {
		t.Fatal("expected default logger")
	}
}

func TestResolveLatestVersionErrors(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		client  *http.Client
		handler http.HandlerFunc
	}{
		{name: "bad request URL", baseURL: "://bad"},
		{
			name:    "transport",
			baseURL: "http://example.com",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network failed")
			})},
		},
		{
			name: "status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", http.StatusInternalServerError)
			},
		},
		{
			name: "decode",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{"))
			},
		},
		{
			name: "missing tag",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":" "}`))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseURL := test.baseURL
			client := test.client
			if test.handler != nil {
				server := httptest.NewServer(test.handler)
				t.Cleanup(server.Close)
				baseURL = server.URL
				client = server.Client()
			}
			if client == nil {
				client = http.DefaultClient
			}
			if _, err := (Updater{Client: client}).resolveLatestVersion(context.Background(), baseURL); err == nil {
				t.Fatal("expected latest version error")
			}
		})
	}
}

func TestDownloadFile(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("asset"))
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(dir, "asset.bin")
	if err := (Updater{Client: server.Client()}).downloadFile(context.Background(), server.URL, destination); err != nil {
		t.Fatalf("download file: %v", err)
	}
	assertFileContent(t, destination, "asset")
}

func TestDownloadFileErrors(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		client  *http.Client
		handler http.HandlerFunc
		seam    func(t *testing.T)
	}{
		{name: "bad request", url: "://bad"},
		{
			name:   "transport",
			url:    "http://example.com/asset",
			client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("send failed") })},
		},
		{
			name: "status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", http.StatusInternalServerError)
			},
		},
		{
			name: "create",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("asset"))
			},
			seam: func(t *testing.T) {
				restoreUpdateSeams(t)
				osCreate = func(string) (*os.File, error) { return nil, errors.New("create failed") }
			},
		},
		{
			name: "copy",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("asset"))
			},
			seam: func(t *testing.T) {
				restoreUpdateSeams(t)
				copyStream = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("copy failed") }
			},
		},
		{
			name: "close",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("asset"))
			},
			seam: func(t *testing.T) {
				restoreUpdateSeams(t)
				closeFile = func(*os.File) error { return errors.New("close failed") }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restoreUpdateSeams(t)
			url := test.url
			client := test.client
			if test.handler != nil {
				server := httptest.NewServer(test.handler)
				t.Cleanup(server.Close)
				url = server.URL
				client = server.Client()
			}
			if client == nil {
				client = http.DefaultClient
			}
			if test.seam != nil {
				test.seam(t)
			}
			err := (Updater{Client: client}).downloadFile(context.Background(), url, filepath.Join(t.TempDir(), "asset"))
			if err == nil {
				t.Fatal("expected download error")
			}
		})
	}
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

func TestVerifyArchiveChecksumErrors(t *testing.T) {
	dir := t.TempDir()
	archiveName := "ip-notify_v1.0.0_linux_amd64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	checksumsPath := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := verifyArchiveChecksum(archivePath, filepath.Join(dir, "missing"), archiveName); err == nil {
		t.Fatal("expected read sums error")
	}
	if err := os.WriteFile(checksumsPath, []byte("not-hex  "+archiveName+"\n"), 0o600); err != nil {
		t.Fatalf("write sums: %v", err)
	}
	if err := verifyArchiveChecksum(archivePath, checksumsPath, archiveName); err == nil {
		t.Fatal("expected invalid checksum error")
	}

	if err := os.WriteFile(checksumsPath, []byte(fmt.Sprintf("%s  %s\n", sha256Hex([]byte("archive bytes")), archiveName)), 0o600); err != nil {
		t.Fatalf("write sums: %v", err)
	}
	if err := verifyArchiveChecksum(filepath.Join(dir, "missing.tar.gz"), checksumsPath, archiveName); err == nil {
		t.Fatal("expected open archive error")
	}

	restoreUpdateSeams(t)
	copyStream = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("hash failed") }
	if err := verifyArchiveChecksum(archivePath, checksumsPath, archiveName); err == nil {
		t.Fatal("expected hash error")
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
	if err := os.WriteFile(checksumsPath, []byte("malformed-line\nabc  other.tar.gz\n"), 0o600); err != nil {
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

func TestExtractBinaryFromArchiveErrors(t *testing.T) {
	if _, err := extractBinaryFromArchive(filepath.Join(t.TempDir(), "missing.tar.gz")); err == nil {
		t.Fatal("expected open archive error")
	}

	invalidGzip := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(invalidGzip, []byte("not gzip"), 0o600); err != nil {
		t.Fatalf("write invalid gzip: %v", err)
	}
	if _, err := extractBinaryFromArchive(invalidGzip); err == nil {
		t.Fatal("expected gzip error")
	}

	invalidTar := writeGzipBytes(t, []byte("not a tar archive"))
	if _, err := extractBinaryFromArchive(invalidTar); err == nil {
		t.Fatal("expected tar read error")
	}

	duplicate := writeArchive(t, buildArchive(t,
		tarEntry{Name: "ip-notify", Mode: 0o755, Body: "one"},
		tarEntry{Name: "ip-notify", Mode: 0o755, Body: "two"},
	))
	if _, err := extractBinaryFromArchive(duplicate); err == nil {
		t.Fatal("expected duplicate binary error")
	}

	directoryEntry := writeArchive(t, buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Typeflag: tar.TypeDir}))
	if _, err := extractBinaryFromArchive(directoryEntry); err == nil {
		t.Fatal("expected non-regular binary error")
	}

	emptyBinary := writeArchive(t, buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Body: ""}))
	if _, err := extractBinaryFromArchive(emptyBinary); err == nil {
		t.Fatal("expected empty binary error")
	}

	restoreUpdateSeams(t)
	readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	valid := writeArchive(t, buildArchive(t, tarEntry{Name: "ip-notify", Mode: 0o755, Body: "binary"}))
	if _, err := extractBinaryFromArchive(valid); err == nil {
		t.Fatal("expected extract read error")
	}
}

func TestSafeTarName(t *testing.T) {
	for _, name := range []string{"", "dir/../ip-notify"} {
		t.Run(name, func(t *testing.T) {
			if _, err := safeTarName(name); err == nil {
				t.Fatal("expected unsafe name error")
			}
		})
	}
	clean, err := safeTarName("./ip-notify")
	if err != nil {
		t.Fatalf("safe tar name: %v", err)
	}
	if clean != "ip-notify" {
		t.Fatalf("unexpected clean name: %q", clean)
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

func TestReplaceBinaryErrors(t *testing.T) {
	if err := replaceBinary("", []byte("binary")); err == nil {
		t.Fatal("expected empty destination error")
	}

	destination := filepath.Join(t.TempDir(), "bin", "ip-notify")
	tests := []struct {
		name string
		seam func(t *testing.T)
	}{
		{name: "mkdir", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			osMkdirAll = func(string, os.FileMode) error { return errors.New("mkdir failed") }
		}},
		{name: "create temp", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			osCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("create temp failed") }
		}},
		{name: "write", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			osCreateTemp = func(dir, pattern string) (*os.File, error) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, err
				}
				return os.OpenFile(filepath.Join(dir, ".ip-notify-update-test"), os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			}
		}},
		{name: "chmod", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			chmodFile = func(*os.File, os.FileMode) error { return errors.New("chmod failed") }
		}},
		{name: "sync", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			syncFile = func(*os.File) error { return errors.New("sync failed") }
		}},
		{name: "close", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			closeFile = func(*os.File) error { return errors.New("close failed") }
		}},
		{name: "rename", seam: func(t *testing.T) {
			restoreUpdateSeams(t)
			osRename = func(string, string) error { return errors.New("rename failed") }
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.seam(t)
			if err := replaceBinary(destination, []byte("binary")); err == nil {
				t.Fatal("expected replace error")
			}
		})
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
	Name     string
	Mode     int64
	Body     string
	Typeflag byte
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
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.Name,
			Mode:     entry.Mode,
			Size:     int64(len(body)),
			Typeflag: typeflag,
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

func writeGzipBytes(t *testing.T, data []byte) string {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	if _, err := gzipWriter.Write(data); err != nil {
		t.Fatalf("write gzip bytes: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return writeArchive(t, buffer.Bytes())
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

func releaseServer(t *testing.T, archive []byte, paths requestPathSet, statuses map[string]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if status := statuses[request.URL.Path]; status != 0 {
			http.Error(writer, "forced error", status)
			return
		}
		switch request.URL.Path {
		case paths.archive:
			_, _ = writer.Write(archive)
		case paths.sums:
			_, _ = fmt.Fprintf(writer, "%s  %s\n", sha256Hex(archive), paths.archiveName)
		default:
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
	}))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func restoreUpdateSeams(t *testing.T) {
	t.Helper()

	osMkdirTemp = os.MkdirTemp
	osRemoveAll = os.RemoveAll
	osCreate = os.Create
	osReadFile = os.ReadFile
	osOpen = os.Open
	osMkdirAll = os.MkdirAll
	osCreateTemp = os.CreateTemp
	osRemove = os.Remove
	osRename = os.Rename
	copyStream = io.Copy
	readAll = io.ReadAll
	closeFile = func(file *os.File) error { return file.Close() }
	chmodFile = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	syncFile = func(file *os.File) error { return file.Sync() }
	t.Cleanup(func() {
		osMkdirTemp = os.MkdirTemp
		osRemoveAll = os.RemoveAll
		osCreate = os.Create
		osReadFile = os.ReadFile
		osOpen = os.Open
		osMkdirAll = os.MkdirAll
		osCreateTemp = os.CreateTemp
		osRemove = os.Remove
		osRename = os.Rename
		copyStream = io.Copy
		readAll = io.ReadAll
		closeFile = func(file *os.File) error { return file.Close() }
		chmodFile = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
		syncFile = func(file *os.File) error { return file.Sync() }
	})
}
