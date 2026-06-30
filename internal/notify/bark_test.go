package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBarkRequestShape(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/push" {
			t.Fatalf("expected /push path, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer server.Close()

	notifier := NewBark(BarkOptions{
		ServerURL:  server.URL,
		DeviceKeys: []string{"key-1", "key-2"},
		Group:      "ip-notify",
	}, server.Client(), nil)

	if err := notifier.Notify(context.Background(), Message{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("notify Bark: %v", err)
	}
	if payload["title"] != "Title" || payload["body"] != "Body" || payload["group"] != "ip-notify" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	keys, ok := payload["device_keys"].([]any)
	if !ok || len(keys) != 2 {
		t.Fatalf("expected two device_keys, got %#v", payload["device_keys"])
	}
	if _, ok := payload["device_key"]; ok {
		t.Fatalf("did not expect device_key when device_keys is used: %#v", payload)
	}
}

func TestNewBarkDefaultsClientAndNormalizesOptions(t *testing.T) {
	notifier := NewBark(BarkOptions{
		ServerURL: "http://example.com///",
		DeviceKey: " key-1 ",
		DeviceKeys: []string{
			"key-1",
			" key-2 ",
			"",
		},
		Group: " group ",
	}, nil, nil)

	if notifier.client != http.DefaultClient {
		t.Fatal("expected default HTTP client")
	}
	if notifier.endpoint != "http://example.com/push" {
		t.Fatalf("unexpected endpoint: %s", notifier.endpoint)
	}
	if notifier.group != "group" {
		t.Fatalf("unexpected group: %q", notifier.group)
	}
	keys := normalizedKeys(notifier.deviceKey, notifier.deviceKeys)
	if strings.Join(keys, ",") != "key-1,key-2" {
		t.Fatalf("unexpected keys: %#v", keys)
	}
}

func TestBarkRequestShapeWithSingleKeyAndNoTitle(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "ip-notify/1" {
			t.Fatalf("unexpected user agent: %s", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewBark(BarkOptions{
		ServerURL: server.URL,
		DeviceKey: "key-1",
	}, server.Client(), nil)

	if err := notifier.Notify(context.Background(), Message{Body: "Body"}); err != nil {
		t.Fatalf("notify Bark: %v", err)
	}
	if payload["device_key"] != "key-1" {
		t.Fatalf("expected single device_key, got %#v", payload)
	}
	if _, ok := payload["title"]; ok {
		t.Fatalf("did not expect title for empty message title: %#v", payload)
	}
	if _, ok := payload["group"]; ok {
		t.Fatalf("did not expect group for empty group: %#v", payload)
	}
}

func TestBarkErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := NewBark(BarkOptions{
		ServerURL: server.URL,
		DeviceKey: "key-1",
	}, server.Client(), nil)

	err := notifier.Notify(context.Background(), Message{Body: "Body"})
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if IsPermanent(err) {
		t.Fatal("Bark errors should not be classified as permanent")
	}
}

func TestBarkNotifyErrors(t *testing.T) {
	tests := []struct {
		name     string
		notifier *BarkNotifier
	}{
		{
			name: "invalid url",
			notifier: NewBark(BarkOptions{
				ServerURL: "://bad",
				DeviceKey: "key-1",
			}, http.DefaultClient, nil),
		},
		{
			name: "transport error",
			notifier: NewBark(BarkOptions{
				ServerURL: "http://example.com",
				DeviceKey: "key-1",
			}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network failed")
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

func TestBarkErrorStatusIgnoresUnreadableBody(t *testing.T) {
	notifier := NewBark(BarkOptions{
		ServerURL: "http://example.com",
		DeviceKey: "key-1",
	}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(errReader{}),
			Header:     http.Header{},
		}, nil
	})}, nil)

	if err := notifier.Notify(context.Background(), Message{Body: "Body"}); err == nil {
		t.Fatal("expected delivery error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
