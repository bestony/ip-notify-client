package ipdetect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicResolverUsesFirstValidSource(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.10\n"))
	}))
	defer second.Close()

	resolver := PublicResolver{Client: second.Client()}
	ip, err := resolver.Resolve(context.Background(), []string{first.URL, second.URL})
	if err != nil {
		t.Fatalf("resolve public IP: %v", err)
	}
	if ip != "203.0.113.10" {
		t.Fatalf("expected public IP 203.0.113.10, got %q", ip)
	}
}

func TestPublicResolverRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not an ip"))
	}))
	defer server.Close()

	resolver := PublicResolver{Client: server.Client()}
	if _, err := resolver.Resolve(context.Background(), []string{server.URL}); err == nil {
		t.Fatal("expected invalid IP response error")
	}
}

func TestPublicResolverErrors(t *testing.T) {
	if _, err := (PublicResolver{}).Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected no source error")
	}

	if _, err := (PublicResolver{}).resolveOne(context.Background(), "://bad"); err == nil {
		t.Fatal("expected request build error")
	}

	resolver := PublicResolver{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("send failed")
		})},
	}
	if _, err := resolver.Resolve(context.Background(), []string{"http://example.com"}); err == nil {
		t.Fatal("expected send error")
	}

	resolver = PublicResolver{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(errReader{}),
				Header:     http.Header{},
			}, nil
		})},
	}
	if _, err := resolver.Resolve(context.Background(), []string{"http://example.com"}); err == nil {
		t.Fatal("expected read error")
	}
}

func TestPublicResolverUsesDefaultClientAndStripsPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.10:443\n"))
	}))
	defer server.Close()

	ip, err := (PublicResolver{}).Resolve(context.Background(), []string{server.URL})
	if err != nil {
		t.Fatalf("resolve public IP: %v", err)
	}
	if ip != "203.0.113.10" {
		t.Fatalf("expected public IP without port, got %q", ip)
	}
}

func TestLoggerOrDiscard(t *testing.T) {
	logger := loggerOrDiscard(nil)
	if logger == nil {
		t.Fatal("expected fallback logger")
	}
	if got := loggerOrDiscard(logger); got != logger {
		t.Fatal("expected existing logger to be returned")
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
