package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
