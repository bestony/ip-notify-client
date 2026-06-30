package install

import (
	"errors"
	"fmt"
	"os"
)

const defaultConfigYAML = `log:
  level: info

check:
  interval: 10m
  timeout: 5s
  notify_initial: true
  public_sources:
    - https://api.ipify.org
    - https://ifconfig.me/ip
    - https://icanhazip.com
  include_public: true
  include_private: true
  interface_allowlist: []
  interface_exclude_prefixes:
    - docker
    - br
    - tailscale

state:
  path: /var/lib/ip-notify/state.json

notifiers:
  bark:
    enabled: false
    server_url: https://api.day.app
    device_keys: []
    group: ip-notify

  pushover:
    enabled: false
    token: ""
    user: ""
    device: ""
`

func writeDefaultConfigIfMissing(path string, mode os.FileMode) (bool, error) {
	file, err := osOpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create default config: %w", err)
	}

	created := true
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = osRemove(path)
		}
	}()

	if _, err := writeString(file, defaultConfigYAML); err != nil {
		_ = closeFile(file)
		return created, fmt.Errorf("write default config: %w", err)
	}
	if err := chmodFile(file, mode); err != nil {
		_ = closeFile(file)
		return created, fmt.Errorf("chmod default config: %w", err)
	}
	if err := closeFile(file); err != nil {
		return created, fmt.Errorf("close default config: %w", err)
	}

	removeOnError = false
	return created, nil
}
