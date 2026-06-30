package ipdetect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type PublicResolver struct {
	Client *http.Client
	Logger *slog.Logger
}

func (r PublicResolver) Resolve(ctx context.Context, sources []string) (string, error) {
	if len(sources) == 0 {
		return "", errors.New("no public IP sources configured")
	}

	var failures []error
	for index, source := range sources {
		logger := loggerOrDiscard(r.Logger)
		logger.Debug("attempting public IP source", "source_index", index, "source_url", source)

		ip, err := r.resolveOne(ctx, source)
		if err != nil {
			logger.Warn("public IP source failed", "source_index", index, "source_url", source, "error", err)
			failures = append(failures, fmt.Errorf("%s: %w", source, err))
			continue
		}

		logger.Debug("public IP source returned valid address", "source_index", index, "source_url", source)
		return ip, nil
	}

	return "", errors.Join(failures...)
}

func (r PublicResolver) resolveOne(ctx context.Context, source string) (string, error) {
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "ip-notify/1")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	raw := strings.TrimSpace(string(body))
	host, _, splitErr := net.SplitHostPort(raw)
	if splitErr == nil {
		raw = host
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return "", fmt.Errorf("response is not an IP address: %w", err)
	}
	return addr.Unmap().String(), nil
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
