# ip-notify

`ip-notify` is a small Go daemon that watches the machine's public IP address and active non-loopback interface addresses. It stores the last observed snapshot and sends notifications only when the normalized snapshot changes.

Supported notification providers:

- Bark API V2
- Pushover Message API

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
  include_private: true
  interface_allowlist: []

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

Print version information:

```sh
ip-notify version
```
