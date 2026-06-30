# ip-notify

`ip-notify` is a small Go daemon that watches the machine's public IP address and active non-loopback interface addresses. It stores the last observed snapshot and sends notifications only when the normalized snapshot changes.

Supported notification providers:

- Bark API V2
- Pushover Message API

## Quick install

Install the latest Linux release without installing Go:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | bash
```

The installer supports Linux with systemd on `amd64` and `arm64`. It downloads the matching GitHub Release tarball, verifies it with `SHA256SUMS`, installs the binary to `/usr/local/bin/ip-notify`, writes `/etc/ip-notify/config.yaml` when credentials are available, and restarts `ip-notify.service` after a successful config check.

When the target `--install-path` already contains an executable `ip-notify` binary, running the installer again switches to update mode. Update mode only downloads, verifies, extracts, and replaces the binary. It does not prompt for provider settings, write config, run `install-daemon`, or validate first-time config. By default it restarts `ip-notify.service` only when the service is already active; pass `--no-start` to replace the binary without restart handling.

To run the local script instead:

```sh
BARK_DEVICE_KEYS=your-device-key IP_NOTIFY_PROVIDER=bark bash scripts/install.sh --version v0.1.0
```

Non-interactive Bark setup:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | \
  IP_NOTIFY_PROVIDER=bark \
  BARK_DEVICE_KEYS=your-device-key,another-device-key \
  BARK_SERVER_URL=https://api.day.app \
  BARK_GROUP=ip-notify \
  bash
```

Non-interactive Pushover setup:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | \
  IP_NOTIFY_PROVIDER=pushover \
  PUSHOVER_TOKEN=your-application-token \
  PUSHOVER_USER=your-user-key \
  PUSHOVER_DEVICE=optional-device \
  bash
```

Useful options:

```sh
bash scripts/install.sh --version v0.1.0 --config /etc/ip-notify/config.yaml --install-path /usr/local/bin/ip-notify
bash scripts/install.sh --dry-run
bash scripts/install.sh --no-start
bash scripts/install.sh --force-config
```

After installation:

```sh
systemctl status ip-notify.service
journalctl -u ip-notify.service -f
```

## Build

```sh
go build -o ip-notify ./cmd/ip-notify
```

## Configuration

The default config path is `/etc/ip-notify/config.yaml`. `install-daemon` creates a default config at that path if it does not already exist.

For manual setup, start from:

```sh
cp configs/config.example.yaml /etc/ip-notify/config.yaml
```

At least one notifier must be enabled before `run` will start.

```yaml
log:
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
```

Secrets are never logged. Use `debug` logging to inspect source attempts, interface scanning, and provider request metadata without credentials.

Set `check.include_public: false` to skip public IP providers and monitor only local interface IP addresses. When public IP monitoring is disabled, `check.public_sources` is ignored and may be omitted. At least one of `check.include_public` or `check.include_private` must be enabled.

Use `check.interface_allowlist` to monitor only specific interface names. Use `check.interface_exclude_prefixes` to skip interface names with matching prefixes, such as Docker bridges, Linux bridges, or Tailscale devices. When both are set, the allowlist is applied first and prefix exclusions are applied afterward.

## Usage

Run the daemon in the foreground:

```sh
ip-notify run --config /etc/ip-notify/config.yaml
```

Run one production check and exit:

```sh
ip-notify once --config /etc/ip-notify/config.yaml
ip-notify once --json --config /etc/ip-notify/config.yaml
```

Install as a systemd service:

```sh
sudo ip-notify install-daemon --config /etc/ip-notify/config.yaml
```

The installer copies the current executable to `/usr/local/bin/ip-notify`, creates an `ip-notify` system user and group, creates `/etc/ip-notify` and `/var/lib/ip-notify`, writes a default config to `/etc/ip-notify/config.yaml` when the file is missing, writes `/etc/systemd/system/ip-notify.service`, reloads systemd, and enables the service.

The generated config does not enable Bark or Pushover and does not include credentials. Edit `/etc/ip-notify/config.yaml` to enable at least one notifier before starting or restarting the service.

It does not start the service unless `--start` is passed:

```sh
sudo ip-notify install-daemon --config /etc/ip-notify/config.yaml --start
```

Preview the installer actions without changing the system:

```sh
ip-notify install-daemon --dry-run
```

Update the installed binary from GitHub Releases:

```sh
sudo ip-notify update
```

By default, `update` installs the latest GitHub Release to the current executable path and restarts `ip-notify.service` only when the service is currently active. Root privileges may be required when replacing `/usr/local/bin/ip-notify`.

Install a specific release tag:

```sh
sudo ip-notify update --version v0.1.0
```

Preview the update without downloading or changing the system:

```sh
ip-notify update --dry-run
```

Replace a custom binary path or skip service restart handling:

```sh
sudo ip-notify update --install-path /usr/local/bin/ip-notify
sudo ip-notify update --no-restart
```

Print version information:

```sh
ip-notify version
```
