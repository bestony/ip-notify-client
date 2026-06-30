package ipdetect

import "testing"

func TestSnapshotNormalizeAndHash(t *testing.T) {
	first := Snapshot{
		PublicIP: " 203.0.113.5 ",
		InterfaceIPs: []InterfaceIP{
			{Interface: "eth1", IP: "2001:db8::2"},
			{Interface: "eth0", IP: "192.168.1.2"},
			{Interface: "eth0", IP: "192.168.1.2"},
			{Interface: "", IP: "10.0.0.1"},
			{Interface: "bad", IP: "not-ip"},
		},
	}
	second := Snapshot{
		PublicIP: "203.0.113.5",
		InterfaceIPs: []InterfaceIP{
			{Interface: "eth0", IP: "192.168.1.2"},
			{Interface: "eth1", IP: "2001:db8::2"},
		},
	}

	normalized := first.Normalize()
	if len(normalized.InterfaceIPs) != 2 {
		t.Fatalf("expected two normalized interface IPs, got %#v", normalized.InterfaceIPs)
	}
	firstHash, err := first.Hash()
	if err != nil {
		t.Fatalf("hash first snapshot: %v", err)
	}
	secondHash, err := second.Hash()
	if err != nil {
		t.Fatalf("hash second snapshot: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("expected equivalent snapshots to hash equally: %s != %s", firstHash, secondHash)
	}
}

func TestSnapshotBody(t *testing.T) {
	body := Snapshot{
		PublicIP: "203.0.113.5",
		InterfaceIPs: []InterfaceIP{
			{Interface: "eth0", IP: "192.168.1.2"},
		},
	}.Body()
	if body != "Public IP: 203.0.113.5\nInterface IPs:\n- eth0: 192.168.1.2" {
		t.Fatalf("unexpected body:\n%s", body)
	}
}
