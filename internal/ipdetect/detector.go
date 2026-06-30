package ipdetect

import (
	"context"
	"fmt"
	"log/slog"
)

type Options struct {
	PublicSources      []string
	IncludePrivate     bool
	InterfaceAllowlist []string
}

type Detector struct {
	Public    PublicResolver
	Interface InterfaceCollector
	Logger    *slog.Logger
}

func (d Detector) Detect(ctx context.Context, options Options) (Snapshot, error) {
	publicIP, err := d.Public.Resolve(ctx, options.PublicSources)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve public IP: %w", err)
	}

	interfaceIPs, err := d.Interface.Collect(ctx, options.IncludePrivate, options.InterfaceAllowlist)
	if err != nil {
		return Snapshot{}, fmt.Errorf("collect interface IPs: %w", err)
	}

	snapshot := Snapshot{
		PublicIP:     publicIP,
		InterfaceIPs: interfaceIPs,
	}.Normalize()
	loggerOrDiscard(d.Logger).Debug("detected IP snapshot", "public_ip", snapshot.PublicIP, "interface_ip_count", len(snapshot.InterfaceIPs))
	return snapshot, nil
}
