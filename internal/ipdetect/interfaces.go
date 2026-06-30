package ipdetect

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
)

type InterfaceProvider interface {
	Interfaces() ([]net.Interface, error)
}

type AddressProvider interface {
	Addrs(iface net.Interface) ([]net.Addr, error)
}

type NetProvider struct{}

func (NetProvider) Interfaces() ([]net.Interface, error) {
	return net.Interfaces()
}

func (NetProvider) Addrs(iface net.Interface) ([]net.Addr, error) {
	return iface.Addrs()
}

type InterfaceCollector struct {
	Interfaces InterfaceProvider
	Addresses  AddressProvider
	Logger     *slog.Logger
}

func (c InterfaceCollector) Collect(ctx context.Context, includePrivate bool, allowlist []string, excludePrefixes []string) ([]InterfaceIP, error) {
	if !includePrivate {
		loggerOrDiscard(c.Logger).Debug("interface IP collection disabled")
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	interfaceProvider := c.Interfaces
	if interfaceProvider == nil {
		interfaceProvider = NetProvider{}
	}
	addressProvider := c.Addresses
	if addressProvider == nil {
		addressProvider = NetProvider{}
	}

	interfaces, err := interfaceProvider.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}

	allowed := map[string]struct{}{}
	for _, name := range allowlist {
		allowed[name] = struct{}{}
	}

	var results []InterfaceIP
	for _, iface := range interfaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		logger := loggerOrDiscard(c.Logger)
		logger.Debug("scanning network interface", "interface", iface.Name, "flags", iface.Flags.String())

		if len(allowed) > 0 {
			if _, ok := allowed[iface.Name]; !ok {
				logger.Debug("skipping interface not in allowlist", "interface", iface.Name)
				continue
			}
		}
		if excludedByPrefix(iface.Name, excludePrefixes) {
			logger.Debug("skipping interface matching exclude prefix", "interface", iface.Name)
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			logger.Debug("skipping interface that is down", "interface", iface.Name)
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			logger.Debug("skipping loopback interface", "interface", iface.Name)
			continue
		}

		addrs, err := addressProvider.Addrs(iface)
		if err != nil {
			logger.Warn("failed to list interface addresses", "interface", iface.Name, "error", err)
			continue
		}
		for _, rawAddr := range addrs {
			addr, ok := parseInterfaceAddr(rawAddr)
			if !ok {
				logger.Debug("skipping unsupported interface address", "interface", iface.Name, "address", interfaceAddrString(rawAddr))
				continue
			}
			if !isUsableInterfaceAddr(addr) {
				logger.Debug("skipping unusable interface address", "interface", iface.Name, "address", addr.String())
				continue
			}
			results = append(results, InterfaceIP{
				Interface: iface.Name,
				IP:        addr.Unmap().String(),
			})
		}
	}

	return Snapshot{InterfaceIPs: results}.Normalize().InterfaceIPs, nil
}

func excludedByPrefix(interfaceName string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(interfaceName, prefix) {
			return true
		}
	}
	return false
}

func parseInterfaceAddr(raw net.Addr) (netip.Addr, bool) {
	if raw == nil {
		return netip.Addr{}, false
	}
	if prefix, err := netip.ParsePrefix(raw.String()); err == nil {
		return prefix.Addr().Unmap(), true
	}
	if addr, err := netip.ParseAddr(raw.String()); err == nil {
		return addr.Unmap(), true
	}
	return netip.Addr{}, false
}

func interfaceAddrString(raw net.Addr) string {
	if raw == nil {
		return "<nil>"
	}
	return raw.String()
}

func isUsableInterfaceAddr(addr netip.Addr) bool {
	return addr.IsValid() &&
		addr.IsGlobalUnicast() &&
		!addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified()
}
