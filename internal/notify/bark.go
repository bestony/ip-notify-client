package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type BarkOptions struct {
	ServerURL  string
	DeviceKey  string
	DeviceKeys []string
	Group      string
}

type BarkNotifier struct {
	endpoint   string
	deviceKey  string
	deviceKeys []string
	group      string
	client     *http.Client
	logger     *slog.Logger
}

func NewBark(options BarkOptions, client *http.Client, logger *slog.Logger) *BarkNotifier {
	if client == nil {
		client = http.DefaultClient
	}
	return &BarkNotifier{
		endpoint:   strings.TrimRight(options.ServerURL, "/") + "/push",
		deviceKey:  strings.TrimSpace(options.DeviceKey),
		deviceKeys: append([]string(nil), options.DeviceKeys...),
		group:      strings.TrimSpace(options.Group),
		client:     client,
		logger:     logger,
	}
}

func (n *BarkNotifier) Name() string {
	return "bark"
}

func (n *BarkNotifier) Notify(ctx context.Context, message Message) error {
	payload := map[string]any{
		"body": message.Body,
	}
	if message.Title != "" {
		payload["title"] = message.Title
	}
	if n.group != "" {
		payload["group"] = n.group
	}

	keys := normalizedKeys(n.deviceKey, n.deviceKeys)
	if len(keys) == 1 {
		payload["device_key"] = keys[0]
	} else {
		payload["device_keys"] = keys
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Bark payload: %w", err)
	}

	loggerOrDiscard(n.logger).Debug("sending Bark notification", "endpoint", n.endpoint, "device_key_count", len(keys), "has_group", n.group != "")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Bark request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "ip-notify/1")

	resp, err := n.client.Do(req)
	if err != nil {
		return &DeliveryError{Provider: n.Name(), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &DeliveryError{
			Provider:   n.Name(),
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(responseBody))),
		}
	}
	return nil
}

func normalizedKeys(primary string, values []string) []string {
	seen := map[string]struct{}{}
	keys := make([]string, 0, len(values)+1)
	if primary = strings.TrimSpace(primary); primary != "" {
		seen[primary] = struct{}{}
		keys = append(keys, primary)
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		keys = append(keys, value)
	}
	return keys
}
