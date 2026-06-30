package ipdetect

import (
	"context"
	"net"
	"testing"
)

type fakeInterfaces []net.Interface

func (f fakeInterfaces) Interfaces() ([]net.Interface, error) {
	return []net.Interface(f), nil
}

type fakeAddresses map[string][]net.Addr

func (f fakeAddresses) Addrs(iface net.Interface) ([]net.Addr, error) {
	return f[iface.Name], nil
}

func TestInterfaceCollectorFiltersAndSortsAddresses(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
			{Name: "down0"},
			{Name: "eth1", Flags: net.FlagUp},
			{Name: "eth0", Flags: net.FlagUp},
		},
		Addresses: fakeAddresses{
			"lo": {
				mustCIDR(t, "127.0.0.1/8"),
			},
			"eth1": {
				mustCIDR(t, "fe80::1/64"),
				mustCIDR(t, "2001:db8::2/64"),
			},
			"eth0": {
				mustCIDR(t, "192.168.1.10/24"),
				mustCIDR(t, "169.254.1.1/16"),
			},
		},
	}

	results, err := collector.Collect(context.Background(), true, nil)
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}

	want := []InterfaceIP{
		{Interface: "eth0", IP: "192.168.1.10"},
		{Interface: "eth1", IP: "2001:db8::2"},
	}
	if len(results) != len(want) {
		t.Fatalf("expected %d addresses, got %d: %#v", len(want), len(results), results)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("result %d: expected %#v, got %#v", i, want[i], results[i])
		}
	}
}

func TestInterfaceCollectorHonorsAllowlist(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "eth0", Flags: net.FlagUp},
			{Name: "eth1", Flags: net.FlagUp},
		},
		Addresses: fakeAddresses{
			"eth0": {mustCIDR(t, "10.0.0.5/24")},
			"eth1": {mustCIDR(t, "10.0.0.6/24")},
		},
	}

	results, err := collector.Collect(context.Background(), true, []string{"eth1"})
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0].Interface != "eth1" {
		t.Fatalf("expected only eth1 address, got %#v", results)
	}
}

func mustCIDR(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	network.IP = ip
	return network
}
