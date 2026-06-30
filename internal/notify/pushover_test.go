package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushoverRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/messages.json" {
			t.Fatalf("expected /1/messages.json path, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		assertFormValue(t, r, "token", "token-1")
		assertFormValue(t, r, "user", "user-1")
		assertFormValue(t, r, "device", "iphone")
		assertFormValue(t, r, "title", "Title")
		assertFormValue(t, r, "message", "Body")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 1, "request": "request-1"})
	}))
	defer server.Close()

	notifier := NewPushover(PushoverOptions{
		Endpoint: server.URL + "/1/messages.json",
		Token:    "token-1",
		User:     "user-1",
		Device:   "iphone",
	}, server.Client(), nil)

	if err := notifier.Notify(context.Background(), Message{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("notify Pushover: %v", err)
	}
}

func TestNewPushoverDefaultsAndTrimsOptions(t *testing.T) {
	notifier := NewPushover(PushoverOptions{
		Token:  " token ",
		User:   " user ",
		Device: " phone ",
	}, nil, nil)

	if notifier.client != http.DefaultClient {
		t.Fatal("expected default HTTP client")
	}
	if notifier.endpoint != DefaultPushoverEndpoint {
		t.Fatalf("expected default endpoint, got %s", notifier.endpoint)
	}
	if notifier.token != "token" || notifier.user != "user" || notifier.device != "phone" {
		t.Fatalf("options were not trimmed: %#v", notifier)
	}
}

func TestPushoverRequestShapeWithoutOptionalFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "ip-notify/1" {
			t.Fatalf("unexpected user agent: %s", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("title") != "" {
			t.Fatalf("did not expect title: %#v", r.Form)
		}
		if r.Form.Get("device") != "" {
			t.Fatalf("did not expect device: %#v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 1})
	}))
	defer server.Close()

	notifier := NewPushover(PushoverOptions{
		Endpoint: server.URL,
		Token:    "token-1",
		User:     "user-1",
	}, server.Client(), nil)

	if err := notifier.Notify(context.Background(), Message{Body: "Body"}); err != nil {
		t.Fatalf("notify Pushover: %v", err)
	}
}

func TestPushoverClassifies4xxPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 0, "errors": []string{"bad token"}})
	}))
	defer server.Close()

	notifier := NewPushover(PushoverOptions{
		Endpoint: server.URL,
		Token:    "token-1",
		User:     "user-1",
	}, server.Client(), nil)

	err := notifier.Notify(context.Background(), Message{Body: "Body"})
	if err == nil {
		t.Fatal("expected Pushover error")
	}
	if !IsPermanent(err) {
		t.Fatal("expected 4xx Pushover response to be permanent")
	}
}

func TestPushoverClassifies5xxTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 0})
	}))
	defer server.Close()

	notifier := NewPushover(PushoverOptions{
		Endpoint: server.URL,
		Token:    "token-1",
		User:     "user-1",
	}, server.Client(), nil)

	err := notifier.Notify(context.Background(), Message{Body: "Body"})
	if err == nil {
		t.Fatal("expected Pushover error")
	}
	if IsPermanent(err) {
		t.Fatal("expected 5xx Pushover response to be transient")
	}
}

func TestPushoverClassifiesOKFailurePermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 0, "errors": []string{"bad user"}})
	}))
	defer server.Close()

	notifier := NewPushover(PushoverOptions{
		Endpoint: server.URL,
		Token:    "token-1",
		User:     "user-1",
	}, server.Client(), nil)

	err := notifier.Notify(context.Background(), Message{Body: "Body"})
	if err == nil {
		t.Fatal("expected Pushover error")
	}
	if !IsPermanent(err) {
		t.Fatal("expected OK response with failed Pushover status to be permanent")
	}
}

func TestPushoverNotifyErrors(t *testing.T) {
	tests := []struct {
		name     string
		notifier *PushoverNotifier
	}{
		{
			name: "invalid url",
			notifier: NewPushover(PushoverOptions{
				Endpoint: "://bad",
				Token:    "token-1",
				User:     "user-1",
			}, http.DefaultClient, nil),
		},
		{
			name: "transport error",
			notifier: NewPushover(PushoverOptions{
				Endpoint: "http://example.com",
				Token:    "token-1",
				User:     "user-1",
			}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network failed")
			})}, nil),
		},
		{
			name: "read error",
			notifier: NewPushover(PushoverOptions{
				Endpoint: "http://example.com",
				Token:    "token-1",
				User:     "user-1",
			}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(errReader{}),
					Header:     http.Header{},
				}, nil
			})}, nil),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.notifier.Notify(context.Background(), Message{Body: "Body"}); err == nil {
				t.Fatal("expected notify error")
			}
		})
	}
}

func assertFormValue(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	if got := r.Form.Get(key); got != want {
		t.Fatalf("form %s: expected %q, got %q", key, want, got)
	}
}
