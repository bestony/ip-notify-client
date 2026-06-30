package ipdetect

import (
	"context"
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
