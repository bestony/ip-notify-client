package ipdetect

import "testing"

func TestSnapshotNormalizeAndHash(t *testing.T) {
	first := Snapshot{
		PublicIP: " 203.0.113.5 ",
		InterfaceIPs: []InterfaceIP{
			{Interface: "eth1", IP: "2001:db8::2"},
			{Interface: "eth0", IP: "192.168.1.3"},
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
			{Interface: "eth0", IP: "192.168.1.3"},
			{Interface: "eth1", IP: "2001:db8::2"},
		},
	}

	normalized := first.Normalize()
	if len(normalized.InterfaceIPs) != 3 {
		t.Fatalf("expected three normalized interface IPs, got %#v", normalized.InterfaceIPs)
	}
	if normalized.InterfaceIPs[0].IP != "192.168.1.2" || normalized.InterfaceIPs[1].IP != "192.168.1.3" {
		t.Fatalf("expected same-interface addresses sorted numerically, got %#v", normalized.InterfaceIPs)
	}
	firstHash := first.Hash()
	secondHash := second.Hash()
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

func TestSnapshotBodyWithOptions(t *testing.T) {
	snapshot := Snapshot{
		PublicIP: "203.0.113.5",
		InterfaceIPs: []InterfaceIP{
			{Interface: "eth0", IP: "192.168.1.2"},
		},
	}
	tests := []struct {
		name    string
		options BodyOptions
		want    string
	}{
		{
			name: "includes public and private",
			options: BodyOptions{
				IncludePublic:  true,
				IncludePrivate: true,
			},
			want: "Public IP: 203.0.113.5\nInterface IPs:\n- eth0: 192.168.1.2",
		},
		{
			name: "omits public when disabled",
			options: BodyOptions{
				IncludePublic:  false,
				IncludePrivate: true,
			},
			want: "Interface IPs:\n- eth0: 192.168.1.2",
		},
		{
			name: "omits private when disabled",
			options: BodyOptions{
				IncludePublic:  true,
				IncludePrivate: false,
			},
			want: "Public IP: 203.0.113.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapshot.BodyWithOptions(tt.options); got != tt.want {
				t.Fatalf("unexpected body:\n%s", got)
			}
		})
	}
}

func TestSnapshotBodyWithoutAddresses(t *testing.T) {
	body := Snapshot{}.Body()
	if body != "Public IP: unavailable\nInterface IPs: none" {
		t.Fatalf("unexpected empty body:\n%s", body)
	}
}

func TestSnapshotBodyWithOptionsWithoutAddresses(t *testing.T) {
	tests := []struct {
		name    string
		options BodyOptions
		want    string
	}{
		{
			name: "includes public fallback",
			options: BodyOptions{
				IncludePublic:  true,
				IncludePrivate: false,
			},
			want: "Public IP: unavailable",
		},
		{
			name: "includes private fallback",
			options: BodyOptions{
				IncludePublic:  false,
				IncludePrivate: true,
			},
			want: "Interface IPs: none",
		},
		{
			name: "includes both fallbacks",
			options: BodyOptions{
				IncludePublic:  true,
				IncludePrivate: true,
			},
			want: "Public IP: unavailable\nInterface IPs: none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Snapshot{}).BodyWithOptions(tt.options); got != tt.want {
				t.Fatalf("unexpected empty body:\n%s", got)
			}
		})
	}
}
