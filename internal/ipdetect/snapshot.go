package ipdetect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

type Snapshot struct {
	PublicIP     string        `json:"public_ip,omitempty"`
	InterfaceIPs []InterfaceIP `json:"interface_ips,omitempty"`
}

type InterfaceIP struct {
	Interface string `json:"interface"`
	IP        string `json:"ip"`
}

type BodyOptions struct {
	IncludePublic  bool
	IncludePrivate bool
}

func (s Snapshot) Normalize() Snapshot {
	normalized := Snapshot{
		InterfaceIPs: make([]InterfaceIP, 0, len(s.InterfaceIPs)),
	}

	if public := normalizeIP(s.PublicIP); public != "" {
		normalized.PublicIP = public
	}

	seen := map[string]struct{}{}
	for _, item := range s.InterfaceIPs {
		iface := strings.TrimSpace(item.Interface)
		ip := normalizeIP(item.IP)
		if iface == "" || ip == "" {
			continue
		}
		key := iface + "\x00" + ip
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized.InterfaceIPs = append(normalized.InterfaceIPs, InterfaceIP{
			Interface: iface,
			IP:        ip,
		})
	}

	sort.Slice(normalized.InterfaceIPs, func(i, j int) bool {
		left := normalized.InterfaceIPs[i]
		right := normalized.InterfaceIPs[j]
		if left.Interface != right.Interface {
			return left.Interface < right.Interface
		}
		leftAddr := netip.MustParseAddr(left.IP)
		rightAddr := netip.MustParseAddr(right.IP)
		return leftAddr.Less(rightAddr)
	})

	return normalized
}

func (s Snapshot) Hash() string {
	normalized := s.Normalize()
	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s Snapshot) Body() string {
	return s.BodyWithOptions(BodyOptions{
		IncludePublic:  true,
		IncludePrivate: true,
	})
}

func (s Snapshot) BodyWithOptions(options BodyOptions) string {
	normalized := s.Normalize()
	var builder strings.Builder

	if options.IncludePublic {
		if normalized.PublicIP != "" {
			fmt.Fprintf(&builder, "Public IP: %s", normalized.PublicIP)
		} else {
			builder.WriteString("Public IP: unavailable")
		}
	}

	if options.IncludePrivate {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		if len(normalized.InterfaceIPs) == 0 {
			builder.WriteString("Interface IPs: none")
			return builder.String()
		}

		builder.WriteString("Interface IPs:")
		for _, item := range normalized.InterfaceIPs {
			fmt.Fprintf(&builder, "\n- %s: %s", item.Interface, item.IP)
		}
	}
	return builder.String()
}

func normalizeIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}
