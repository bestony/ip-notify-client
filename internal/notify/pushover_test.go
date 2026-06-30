package notify

import (
	"context"
	"encoding/json"
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

func assertFormValue(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	if got := r.Form.Get(key); got != want {
		t.Fatalf("form %s: expected %q, got %q", key, want, got)
	}
}
