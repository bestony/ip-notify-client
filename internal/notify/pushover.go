package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

const DefaultPushoverEndpoint = "https://api.pushover.net/1/messages.json"

type PushoverOptions struct {
	Endpoint string
	Token    string
	User     string
	Device   string
}

type PushoverNotifier struct {
	endpoint string
	token    string
	user     string
	device   string
	client   *http.Client
	logger   *slog.Logger
}

type pushoverResponse struct {
	Status  int      `json:"status"`
	Errors  []string `json:"errors"`
	Request string   `json:"request"`
}

func NewPushover(options PushoverOptions, client *http.Client, logger *slog.Logger) *PushoverNotifier {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = DefaultPushoverEndpoint
	}
	return &PushoverNotifier{
		endpoint: endpoint,
		token:    strings.TrimSpace(options.Token),
		user:     strings.TrimSpace(options.User),
		device:   strings.TrimSpace(options.Device),
		client:   client,
		logger:   logger,
	}
}

func (n *PushoverNotifier) Name() string {
	return "pushover"
}

func (n *PushoverNotifier) Notify(ctx context.Context, message Message) error {
	values := url.Values{}
	values.Set("token", n.token)
	values.Set("user", n.user)
	values.Set("message", message.Body)
	if message.Title != "" {
		values.Set("title", message.Title)
	}
	if n.device != "" {
		values.Set("device", n.device)
	}

	loggerOrDiscard(n.logger).Debug("sending Pushover notification", "endpoint", n.endpoint, "has_device", n.device != "")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("build Pushover request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "ip-notify/1")

	resp, err := n.client.Do(req)
	if err != nil {
		return &DeliveryError{Provider: n.Name(), Err: err}
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil {
		return &DeliveryError{Provider: n.Name(), StatusCode: resp.StatusCode, Err: fmt.Errorf("read response body: %w", readErr)}
	}

	var parsed pushoverResponse
	if len(strings.TrimSpace(string(responseBody))) > 0 {
		_ = json.Unmarshal(responseBody, &parsed)
	}

	if resp.StatusCode == http.StatusOK && parsed.Status == 1 {
		return nil
	}

	permanent := resp.StatusCode >= 400 && resp.StatusCode <= 499
	if resp.StatusCode == http.StatusOK && parsed.Status != 1 {
		permanent = true
	}

	return &DeliveryError{
		Provider:   n.Name(),
		StatusCode: resp.StatusCode,
		Permanent:  permanent,
		Err:        fmt.Errorf("pushover status=%d errors=%v", parsed.Status, parsed.Errors),
	}
}
