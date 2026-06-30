package ipdetect

import (
	"context"
	"errors"
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

	results, err := collector.Collect(context.Background(), true, nil, nil)
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

	results, err := collector.Collect(context.Background(), true, []string{"eth1"}, nil)
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0].Interface != "eth1" {
		t.Fatalf("expected only eth1 address, got %#v", results)
	}
}

func TestInterfaceCollectorExcludesInterfacesByPrefix(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "docker0", Flags: net.FlagUp},
			{Name: "br-1234", Flags: net.FlagUp},
			{Name: "tailscale0", Flags: net.FlagUp},
			{Name: "eth0", Flags: net.FlagUp},
		},
		Addresses: fakeAddresses{
			"docker0":    {mustCIDR(t, "10.1.0.2/24")},
			"br-1234":    {mustCIDR(t, "10.2.0.2/24")},
			"tailscale0": {mustCIDR(t, "100.64.0.2/10")},
			"eth0":       {mustCIDR(t, "192.0.2.44/24")},
		},
	}

	results, err := collector.Collect(context.Background(), true, nil, []string{" docker ", "br", "", "tailscale"})
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0] != (InterfaceIP{Interface: "eth0", IP: "192.0.2.44"}) {
		t.Fatalf("expected only eth0 address, got %#v", results)
	}
}

func TestInterfaceCollectorAppliesExcludePrefixesAfterAllowlist(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "docker0", Flags: net.FlagUp},
			{Name: "eth0", Flags: net.FlagUp},
		},
		Addresses: fakeAddresses{
			"docker0": {mustCIDR(t, "10.1.0.2/24")},
			"eth0":    {mustCIDR(t, "192.0.2.44/24")},
		},
	}

	results, err := collector.Collect(context.Background(), true, []string{"docker0", "eth0"}, []string{"docker"})
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0].Interface != "eth0" {
		t.Fatalf("expected allowlisted eth0 and excluded docker0, got %#v", results)
	}
}

func TestInterfaceCollectorKeepsInterfacesWithoutExcludedPrefix(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "eno1", Flags: net.FlagUp},
		},
		Addresses: fakeAddresses{
			"eno1": {mustCIDR(t, "192.0.2.45/24")},
		},
	}

	results, err := collector.Collect(context.Background(), true, nil, []string{"docker", "br", "tailscale"})
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0] != (InterfaceIP{Interface: "eno1", IP: "192.0.2.45"}) {
		t.Fatalf("expected eno1 address, got %#v", results)
	}
}

func TestInterfaceCollectorDisabled(t *testing.T) {
	results, err := InterfaceCollector{}.Collect(context.Background(), false, nil, []string{"docker"})
	if err != nil {
		t.Fatalf("collect disabled interfaces: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results when disabled, got %#v", results)
	}
}

func TestInterfaceCollectorErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (InterfaceCollector{}).Collect(cancelled, true, nil, nil); err == nil {
		t.Fatal("expected context error")
	}

	_, err := InterfaceCollector{
		Interfaces: failingInterfaces{},
	}.Collect(context.Background(), true, nil, nil)
	if err == nil {
		t.Fatal("expected interface provider error")
	}

	ctx, cancelDuringLoop := context.WithCancel(context.Background())
	_, err = InterfaceCollector{
		Interfaces: cancelingInterfaces{
			cancel: cancelDuringLoop,
			interfaces: []net.Interface{
				{Name: "eth0", Flags: net.FlagUp},
			},
		},
	}.Collect(ctx, true, nil, nil)
	if err == nil {
		t.Fatal("expected context error during interface loop")
	}
}

func TestInterfaceCollectorSkipsAddressErrorsAndUnsupportedValues(t *testing.T) {
	collector := InterfaceCollector{
		Interfaces: fakeInterfaces{
			{Name: "eth0", Flags: net.FlagUp},
			{Name: "eth1", Flags: net.FlagUp},
		},
		Addresses: mixedAddresses{
			values: map[string][]net.Addr{
				"eth1": {
					nil,
					stringAddr("not an address"),
					stringAddr("192.0.2.20"),
				},
			},
			errors: map[string]error{
				"eth0": errors.New("address failed"),
			},
		},
	}

	results, err := collector.Collect(context.Background(), true, nil, nil)
	if err != nil {
		t.Fatalf("collect interface IPs: %v", err)
	}
	if len(results) != 1 || results[0] != (InterfaceIP{Interface: "eth1", IP: "192.0.2.20"}) {
		t.Fatalf("expected one parsed string address, got %#v", results)
	}
}

func TestInterfaceCollectorDefaultProviders(t *testing.T) {
	if _, err := (InterfaceCollector{}).Collect(context.Background(), true, []string{"definitely-missing-ip-notify-test-iface"}, nil); err != nil {
		t.Fatalf("collect with default providers: %v", err)
	}

	if _, err := (NetProvider{}).Interfaces(); err != nil {
		t.Fatalf("list interfaces through net provider: %v", err)
	}
	_, _ = NetProvider{}.Addrs(net.Interface{Name: "definitely-missing-ip-notify-test-iface"})
}

func TestParseInterfaceAddr(t *testing.T) {
	if _, ok := parseInterfaceAddr(nil); ok {
		t.Fatal("nil address should not parse")
	}
	addr, ok := parseInterfaceAddr(stringAddr("192.0.2.30"))
	if !ok || addr.String() != "192.0.2.30" {
		t.Fatalf("expected plain IP address, got %s ok=%t", addr, ok)
	}
	if _, ok := parseInterfaceAddr(stringAddr("not an address")); ok {
		t.Fatal("invalid address should not parse")
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

type failingInterfaces struct{}

func (failingInterfaces) Interfaces() ([]net.Interface, error) {
	return nil, errors.New("interfaces failed")
}

type cancelingInterfaces struct {
	cancel     context.CancelFunc
	interfaces []net.Interface
}

func (c cancelingInterfaces) Interfaces() ([]net.Interface, error) {
	c.cancel()
	return c.interfaces, nil
}

type mixedAddresses struct {
	values map[string][]net.Addr
	errors map[string]error
}

func (m mixedAddresses) Addrs(iface net.Interface) ([]net.Addr, error) {
	if err := m.errors[iface.Name]; err != nil {
		return nil, err
	}
	return m.values[iface.Name], nil
}

type stringAddr string

func (a stringAddr) Network() string {
	return "test"
}

func (a stringAddr) String() string {
	return string(a)
}
