package ipdetect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectorDetectsSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.44"))
	}))
	defer server.Close()

	detector := Detector{
		Public: PublicResolver{Client: server.Client()},
		Interface: InterfaceCollector{
			Interfaces: fakeInterfaces{
				{Name: "eth0", Flags: net.FlagUp},
			},
			Addresses: fakeAddresses{
				"eth0": {mustCIDR(t, "192.0.2.44/24")},
			},
		},
	}

	snapshot, err := detector.Detect(context.Background(), Options{
		PublicSources:  []string{server.URL},
		IncludePrivate: true,
	})
	if err != nil {
		t.Fatalf("detect snapshot: %v", err)
	}
	if snapshot.PublicIP != "203.0.113.44" {
		t.Fatalf("unexpected public IP: %#v", snapshot)
	}
	if len(snapshot.InterfaceIPs) != 1 || snapshot.InterfaceIPs[0].IP != "192.0.2.44" {
		t.Fatalf("unexpected interface IPs: %#v", snapshot.InterfaceIPs)
	}
}

func TestDetectorWrapsPublicResolverError(t *testing.T) {
	_, err := (Detector{}).Detect(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected public resolver error")
	}
	if !strings.Contains(err.Error(), "resolve public IP") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetectorWrapsInterfaceCollectorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.44"))
	}))
	defer server.Close()

	detector := Detector{
		Public: PublicResolver{Client: server.Client()},
		Interface: InterfaceCollector{
			Interfaces: failingInterfaces{},
		},
	}
	_, err := detector.Detect(context.Background(), Options{
		PublicSources:  []string{server.URL},
		IncludePrivate: true,
	})
	if err == nil {
		t.Fatal("expected interface collector error")
	}
	if !strings.Contains(err.Error(), "collect interface IPs") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !errors.Is(err, errors.New("interfaces failed")) && !strings.Contains(err.Error(), "interfaces failed") {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}
}
