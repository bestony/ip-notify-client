# ip-notify

`ip-notify` is a small Go daemon and CLI that watches a machine's public IP address and active non-loopback interface addresses. It stores the last normalized IP snapshot locally and sends notifications only when that snapshot changes.

Supported notification providers:

- Bark API V2
- Pushover Message API

## Features

- Monitors public IP addresses through configurable HTTP sources.
- Monitors local interface IP addresses from active, non-loopback network interfaces.
- Supports public-only, interface-only, or combined snapshots.
- Deduplicates notifications per provider by snapshot hash.
- Retries transient provider failures on the next check.
- Suppresses repeated retries for permanent provider failures such as invalid credentials.
- Installs as a hardened Linux systemd service.
- Updates installed Linux binaries from GitHub Releases with `SHA256SUMS` verification.

## Requirements

Runtime:

- Linux for the installer, systemd service, and built-in updater.
- `amd64` or `arm64` release artifacts.
- A Bark device key or Pushover application token/user key.

Development:

- Go 1.24.4 or newer compatible with the version in `go.mod`.

## Quick Install

Install the latest Linux release without installing Go:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | bash
```

The installer supports Linux with systemd on `amd64` and `arm64`. It downloads the matching GitHub Release tarball, verifies it with `SHA256SUMS`, installs the binary to `/usr/local/bin/ip-notify`, installs `ip-notify.service`, and writes `/etc/ip-notify/config.yaml` when provider credentials are available.

If the config validates successfully, the installer restarts `ip-notify.service`. If credentials are missing or validation fails, it leaves the service stopped and prints the commands needed after you edit the config.

Install and configure Bark non-interactively:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | \
  IP_NOTIFY_PROVIDER=bark \
  BARK_DEVICE_KEYS=your-device-key,another-device-key \
  BARK_SERVER_URL=https://api.day.app \
  BARK_GROUP=ip-notify \
  bash
```

Install and configure Pushover non-interactively:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | \
  IP_NOTIFY_PROVIDER=pushover \
  PUSHOVER_TOKEN=your-application-token \
  PUSHOVER_USER=your-user-key \
  PUSHOVER_DEVICE=optional-device \
  bash
```

Run the local install script for a specific release:

```sh
IP_NOTIFY_PROVIDER=bark BARK_DEVICE_KEYS=your-device-key \
  bash scripts/install.sh --version v0.0.4
```

Useful installer options:

```sh
bash scripts/install.sh --dry-run
bash scripts/install.sh --no-start
bash scripts/install.sh --force-config
bash scripts/install.sh --config /etc/ip-notify/config.yaml --install-path /usr/local/bin/ip-notify
```

Installer environment variables:

| Variable | Description |
| --- | --- |
| `IP_NOTIFY_VERSION` | Release tag to install. Defaults to the latest GitHub Release. |
| `IP_NOTIFY_PROVIDER` | Provider to configure: `bark` or `pushover`. |
| `IP_NOTIFY_CONFIG` | Config path. Defaults to `/etc/ip-notify/config.yaml`. |
| `IP_NOTIFY_INSTALL_PATH` | Binary path. Defaults to `/usr/local/bin/ip-notify`. |
| `BARK_SERVER_URL` | Bark server URL. Defaults to `https://api.day.app`. |
| `BARK_DEVICE_KEYS` | Comma-separated Bark device keys. |
| `BARK_GROUP` | Bark notification group. Defaults to `ip-notify`. |
| `PUSHOVER_TOKEN` | Pushover application API token. |
| `PUSHOVER_USER` | Pushover user or group key. |
| `PUSHOVER_DEVICE` | Optional Pushover device name. |

## Updating

When `scripts/install.sh` finds an existing executable at `--install-path`, it switches to update mode. Update mode only downloads, verifies, extracts, and replaces the binary. It does not prompt for provider settings, write config, run `install-daemon`, or validate first-time config.

By default, update mode restarts `ip-notify.service` only when the service is already active:

```sh
curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | bash
```

Use `--no-start` to replace the binary without service restart handling:

```sh
bash scripts/install.sh --no-start
```

You can also update from the installed CLI:

```sh
sudo ip-notify update
sudo ip-notify update --version v0.0.4
sudo ip-notify update --install-path /usr/local/bin/ip-notify
sudo ip-notify update --no-restart
ip-notify update --dry-run
```

The built-in updater supports Linux `amd64` and `arm64`. It resolves the latest GitHub Release unless `--version` is set, verifies the archive against `SHA256SUMS`, replaces the target binary atomically, and restarts `ip-notify.service` only if it is active.

## Configuration

The default config path is `/etc/ip-notify/config.yaml`. Start from the example config for manual setup:

```sh
sudo mkdir -p /etc/ip-notify
sudo cp configs/config.example.yaml /etc/ip-notify/config.yaml
sudo editor /etc/ip-notify/config.yaml
```

At least one notifier must be enabled before `run` or `once` will start.

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

### Config Reference

| Field | Description |
| --- | --- |
| `log.level` | One of `debug`, `info`, `warn`, or `error`. |
| `check.interval` | Daemon polling interval. Go duration strings such as `10m`, `30s`, and `1h` are supported. Integer YAML values are interpreted as seconds. |
| `check.timeout` | HTTP client timeout for public IP sources and provider requests. |
| `check.notify_initial` | Send a notification for the first recorded snapshot when no state exists. Set to `false` to record the initial snapshot silently. |
| `check.public_sources` | Ordered HTTP sources used to resolve the public IP. The first source returning a valid IP wins. |
| `check.include_public` | Include public IP detection in the snapshot and notification body. |
| `check.include_private` | Include active interface IP detection in the snapshot and notification body. |
| `check.interface_allowlist` | Optional list of exact interface names to monitor. Empty means all eligible interfaces. |
| `check.interface_exclude_prefixes` | Interface name prefixes to skip after the allowlist is applied. |
| `state.path` | JSON state file used for snapshot and provider deduplication. |
| `notifiers.bark.enabled` | Enable Bark notifications. |
| `notifiers.bark.server_url` | Bark server base URL. |
| `notifiers.bark.device_keys` | Bark device keys. Multiple keys are sent in one Bark API request. |
| `notifiers.bark.device_key` | Optional legacy single Bark key field; still accepted for compatibility. |
| `notifiers.bark.group` | Optional Bark notification group. |
| `notifiers.pushover.enabled` | Enable Pushover notifications. |
| `notifiers.pushover.token` | Pushover application API token. |
| `notifiers.pushover.user` | Pushover user or group key. |
| `notifiers.pushover.device` | Optional Pushover device name. |

At least one of `check.include_public` or `check.include_private` must be enabled. When `check.include_public` is `false`, `check.public_sources` is ignored and may be omitted.

Interface detection includes active, non-loopback, usable global-unicast addresses and skips loopback, link-local, multicast, unspecified, and down interfaces. When both `interface_allowlist` and `interface_exclude_prefixes` are set, the allowlist is applied first and prefix exclusions are applied afterward.

Secrets are not included in normal logs, and provider debug logs only include request metadata such as endpoints, key counts, and device/group presence. Use `debug` logging to inspect public source attempts, interface scanning, and provider request metadata without credential values.

## CLI Usage

Run one check and print a human-readable result:

```sh
ip-notify once --config /etc/ip-notify/config.yaml
```

Run one check and print JSON:

```sh
ip-notify once --json --config /etc/ip-notify/config.yaml
```

Force notifications for the current snapshot even when it has already been handled successfully:

```sh
ip-notify once --force --config /etc/ip-notify/config.yaml
```

Run the daemon in the foreground:

```sh
ip-notify run --config /etc/ip-notify/config.yaml
```

Print version information:

```sh
ip-notify version
```

The `run` command performs one check immediately and then repeats at `check.interval` until it receives `SIGINT` or `SIGTERM`.

## Systemd Service

Install the currently running binary as a systemd service:

```sh
sudo ip-notify install-daemon --config /etc/ip-notify/config.yaml
```

The command:

- Copies the current executable to `/usr/local/bin/ip-notify`.
- Creates the `ip-notify` system group and user when missing.
- Creates the config directory and state directory.
- Writes a default config only when the config file does not already exist.
- Writes `/etc/systemd/system/ip-notify.service`.
- Runs `systemctl daemon-reload`.
- Enables `ip-notify.service`.

It does not start the service unless `--start` is passed:

```sh
sudo ip-notify install-daemon --config /etc/ip-notify/config.yaml --start
```

Preview service installation without changing the system:

```sh
ip-notify install-daemon --dry-run
```

Useful service commands:

```sh
systemctl status ip-notify.service
journalctl -u ip-notify.service -f
sudo systemctl restart ip-notify.service
```

The generated unit runs as the `ip-notify` user, restarts on failure, uses `NoNewPrivileges=true`, protects the filesystem with `ProtectSystem=strict`, hides home directories with `ProtectHome=true`, and only grants write access to the configured state directory.

## Notification Semantics

Each check builds a normalized snapshot from the enabled sources and computes a SHA-256 hash. The state file stores the current snapshot, previous snapshot, provider success hashes, permanent provider failure hashes, and the last update time.

Notification behavior:

- A changed snapshot is sent to every enabled notifier.
- A provider that successfully handled the current hash is skipped on later checks.
- A transient delivery error is retried on the next check.
- A permanent delivery error is recorded and skipped for the same hash on later checks.
- `once --force` resends to providers that already succeeded for the current hash, but it does not override a recorded permanent failure.
- If `check.notify_initial` is `false`, the first snapshot is recorded without notification and skipped while the hash remains unchanged.

## Build From Source

Build a local binary:

```sh
go build -o ip-notify ./cmd/ip-notify
```

Run from source:

```sh
go run ./cmd/ip-notify once --config /path/to/config.yaml
```

Run tests:

```sh
go test ./...
```

Run the repository coverage gate:

```sh
bash scripts/check-go-coverage.sh
```

The CI coverage gate requires total Go statement coverage to be `100.0%`.

## Release Artifacts

Tagged releases are built by GitHub Actions when a `v*` tag is pushed. The release workflow builds static Linux binaries for `amd64` and `arm64`, packages each archive with:

- `ip-notify`
- `README.md`
- `configs/config.example.yaml`

Release assets use this naming pattern:

```text
ip-notify_<tag>_linux_<arch>.tar.gz
SHA256SUMS
```

The installer and built-in updater both expect that artifact layout.
