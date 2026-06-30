package install

import (
	"bytes"
	"text/template"
)

type UnitOptions struct {
	BinaryPath string
	ConfigPath string
	User       string
	Group      string
	StateDir   string
}

func RenderUnit(options UnitOptions) string {
	var buffer bytes.Buffer
	_ = unitTemplate.Execute(&buffer, options)
	return buffer.String()
}

var unitTemplate = template.Must(template.New("systemd-unit").Parse(`[Unit]
Description=IP Notify daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{ .BinaryPath }} run --config {{ .ConfigPath }}
User={{ .User }}
Group={{ .Group }}
Restart=on-failure
RestartSec=10s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths={{ .StateDir }}

[Install]
WantedBy=multi-user.target
`))
