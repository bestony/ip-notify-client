#!/usr/bin/env bash
set -Eeuo pipefail

readonly REPO="bestony/ip-notify-client"
readonly GITHUB_API="https://api.github.com"
readonly RELEASE_BASE_URL="https://github.com/${REPO}/releases/download"
readonly DEFAULT_CONFIG="/etc/ip-notify/config.yaml"
readonly DEFAULT_INSTALL_PATH="/usr/local/bin/ip-notify"
readonly SERVICE_NAME="ip-notify.service"
readonly STATE_DIR="/var/lib/ip-notify"

version="${IP_NOTIFY_VERSION:-}"
provider="${IP_NOTIFY_PROVIDER:-}"
config_path="${IP_NOTIFY_CONFIG:-${DEFAULT_CONFIG}}"
install_path="${IP_NOTIFY_INSTALL_PATH:-${DEFAULT_INSTALL_PATH}}"
dry_run=false
start_service=true
force_config=false
config_preexisting=false
config_reused=false
config_validated=false
validation_failure_reason=""
prompt_answer=""

bark_server_url="${BARK_SERVER_URL:-https://api.day.app}"
bark_device_keys="${BARK_DEVICE_KEYS:-}"
bark_group="${BARK_GROUP:-ip-notify}"
pushover_token="${PUSHOVER_TOKEN:-}"
pushover_user="${PUSHOVER_USER:-}"
pushover_device="${PUSHOVER_DEVICE:-}"

tmp_dir=""

log() {
  printf '[ip-notify install] %s\n' "$*"
}

warn() {
  printf '[ip-notify install] WARN: %s\n' "$*" >&2
}

fail() {
  printf '[ip-notify install] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install ip-notify from GitHub Releases on Linux + systemd.

Usage:
  scripts/install.sh [flags]

Flags:
  --version <tag>          Release tag to install, for example v1.2.3. Defaults to latest.
  --provider <provider>    Notification provider to configure: bark or pushover.
  --config <path>          Config file path. Defaults to /etc/ip-notify/config.yaml.
  --install-path <path>    Binary install path. Defaults to /usr/local/bin/ip-notify.
  --dry-run                Print planned actions without downloading or changing the system.
  --no-start               Install and configure without restarting ip-notify.service.
  --force-config           Back up and overwrite an existing config file.
  --help                   Show this help.

Environment:
  IP_NOTIFY_VERSION        Same as --version.
  IP_NOTIFY_PROVIDER       Same as --provider.
  IP_NOTIFY_CONFIG         Same as --config.
  IP_NOTIFY_INSTALL_PATH   Same as --install-path.

  BARK_SERVER_URL          Bark server URL. Defaults to https://api.day.app.
  BARK_DEVICE_KEYS         Comma-separated Bark device keys.
  BARK_GROUP               Bark notification group. Defaults to ip-notify.

  PUSHOVER_TOKEN           Pushover application API token.
  PUSHOVER_USER            Pushover user or group key.
  PUSHOVER_DEVICE          Optional Pushover device name.

Examples:
  curl -fsSL https://raw.githubusercontent.com/bestony/ip-notify-client/main/scripts/install.sh | bash
  BARK_DEVICE_KEYS=xxx IP_NOTIFY_PROVIDER=bark bash scripts/install.sh
  PUSHOVER_TOKEN=app PUSHOVER_USER=user IP_NOTIFY_PROVIDER=pushover bash scripts/install.sh
EOF
}

cleanup() {
  if [[ -n "${tmp_dir}" && -d "${tmp_dir}" ]]; then
    rm -rf "${tmp_dir}"
  fi
}

trap cleanup EXIT

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version)
        [[ $# -ge 2 ]] || fail "--version requires a value"
        version="$2"
        shift 2
        ;;
      --provider)
        [[ $# -ge 2 ]] || fail "--provider requires a value"
        provider="$2"
        shift 2
        ;;
      --config)
        [[ $# -ge 2 ]] || fail "--config requires a value"
        config_path="$2"
        shift 2
        ;;
      --install-path)
        [[ $# -ge 2 ]] || fail "--install-path requires a value"
        install_path="$2"
        shift 2
        ;;
      --dry-run)
        dry_run=true
        shift
        ;;
      --no-start)
        start_service=false
        shift
        ;;
      --force-config)
        force_config=true
        shift
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done
}

require_linux_systemd() {
  [[ "$(uname -s)" == "Linux" ]] || fail "this installer only supports Linux"
  if ! command -v systemctl >/dev/null 2>&1; then
    if [[ "${dry_run}" == "true" ]]; then
      warn "systemctl is not available; continuing because --dry-run is set"
      return 0
    fi
    fail "this installer requires systemd"
  fi
  if [[ ! -d /run/systemd/system ]]; then
    if [[ "${dry_run}" == "true" ]]; then
      warn "systemd does not appear to be running; continuing because --dry-run is set"
      return 0
    fi
    fail "this installer requires systemd"
  fi
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
}

require_commands() {
  require_command curl
  require_command date
  require_command dirname
  require_command grep
  require_command head
  require_command install
  require_command mktemp
  require_command tar
  require_command tr
  require_command uname
  require_command sha256sum
  require_command sed
}

ensure_privilege_available() {
  if [[ "${dry_run}" == "false" && "${EUID}" -ne 0 ]]; then
    require_command sudo
  fi
}

normalize_provider() {
  provider="$(printf '%s' "${provider}" | tr '[:upper:]' '[:lower:]')"
  case "${provider}" in
    ""|bark|pushover)
      ;;
    *)
      fail "--provider must be bark or pushover"
      ;;
  esac
}

map_arch() {
  local machine
  machine="$(uname -m)"
  case "${machine}" in
    x86_64|amd64)
      printf 'amd64'
      ;;
    aarch64|arm64)
      printf 'arm64'
      ;;
    *)
      fail "unsupported architecture: ${machine}. Supported architectures: amd64, arm64."
      ;;
  esac
}

run_privileged() {
  if [[ "${dry_run}" == "true" ]]; then
    printf 'DRY-RUN:'
    printf ' %q' "$@"
    printf '\n'
    return 0
  fi

  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

download_text() {
  local url="$1"
  curl -fsSL "${url}"
}

can_prompt() {
  if [[ -t 0 && -t 1 ]]; then
    return 0
  fi
  { true < /dev/tty > /dev/tty; } 2>/dev/null
}

prompt_read() {
  local prompt="$1"
  prompt_answer=""
  if { true < /dev/tty > /dev/tty; } 2>/dev/null; then
    printf '%s' "${prompt}" > /dev/tty
    IFS= read -r prompt_answer < /dev/tty
    return $?
  fi
  if [[ -t 0 && -t 1 ]]; then
    read -r -p "${prompt}" prompt_answer
    return $?
  fi
  return 1
}

resolve_latest_version() {
  if [[ -n "${version}" ]]; then
    return 0
  fi
  if [[ "${dry_run}" == "true" ]]; then
    version="<latest>"
    log "Would resolve latest release from GitHub"
    return 0
  fi
  log "Resolving latest release from GitHub"
  version="$(download_text "${GITHUB_API}/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [[ -n "${version}" ]] || fail "could not resolve latest release tag"
}

prompt_if_missing() {
  if [[ "${config_preexisting}" == "true" && "${force_config}" != "true" ]]; then
    return 0
  fi

  if ! can_prompt; then
    return 0
  fi

  if [[ -z "${provider}" ]]; then
    local answer
    while true; do
      prompt_read "Notification provider [bark/pushover]: " || return 0
      answer="${prompt_answer}"
      answer="$(printf '%s' "${answer}" | tr '[:upper:]' '[:lower:]')"
      case "${answer}" in
        bark|pushover)
          provider="${answer}"
          break
          ;;
        *)
          printf 'Please enter bark or pushover.\n' >&2
          ;;
      esac
    done
  fi

  case "${provider}" in
    bark)
      if [[ -z "${bark_server_url}" ]]; then
        prompt_read "Bark server URL [https://api.day.app]: " || return 0
        bark_server_url="${prompt_answer}"
        bark_server_url="${bark_server_url:-https://api.day.app}"
      fi
      while [[ -z "${bark_device_keys}" ]]; do
        prompt_read "Bark device keys (comma-separated): " || return 0
        bark_device_keys="${prompt_answer}"
      done
      if [[ -z "${bark_group}" ]]; then
        prompt_read "Bark group [ip-notify]: " || return 0
        bark_group="${prompt_answer}"
        bark_group="${bark_group:-ip-notify}"
      fi
      ;;
    pushover)
      while [[ -z "${pushover_token}" ]]; do
        prompt_read "Pushover application token: " || return 0
        pushover_token="${prompt_answer}"
      done
      while [[ -z "${pushover_user}" ]]; do
        prompt_read "Pushover user or group key: " || return 0
        pushover_user="${prompt_answer}"
      done
      if [[ -z "${pushover_device}" ]]; then
        prompt_read "Pushover device (optional): " || return 0
        pushover_device="${prompt_answer}"
      fi
      ;;
  esac
}

provider_ready() {
  case "${provider}" in
    bark)
      [[ -n "${bark_server_url}" ]] && has_bark_device_keys
      ;;
    pushover)
      [[ -n "${pushover_token}" && -n "${pushover_user}" ]]
      ;;
    *)
      return 1
      ;;
  esac
}

print_not_started_next_steps() {
  case "${validation_failure_reason}" in
    missing_credentials)
      warn "notifier credentials are incomplete; the service will not be started"
      ;;
    validation_failed)
      warn "config validation failed; the service will not be started"
      ;;
    state_ownership_failed)
      warn "state directory ownership could not be repaired; the service will not be started"
      ;;
    *)
      warn "the service will not be started"
      ;;
  esac
  warn "edit ${config_path} and enable at least one notifier, then run:"
  warn "  ${install_path} once --config ${config_path}"
  warn "  systemctl restart ${SERVICE_NAME}"
}

config_exists() {
  if [[ "${dry_run}" == "true" ]]; then
    [[ -e "${config_path}" ]]
    return
  fi
  run_privileged test -e "${config_path}" >/dev/null 2>&1
}

detect_preexisting_config() {
  if config_exists; then
    config_preexisting=true
  fi
}

escape_yaml_double_quoted() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "${value}"
}

write_device_keys_yaml() {
  local keys="$1"
  local old_ifs="${IFS}"
  local key
  local -a key_array=()
  IFS=','
  read -r -a key_array <<< "${keys}"
  IFS="${old_ifs}"

  for key in "${key_array[@]}"; do
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    [[ -n "${key}" ]] || continue
    printf '      - "%s"\n' "$(escape_yaml_double_quoted "${key}")"
  done
}

has_bark_device_keys() {
  local keys="${bark_device_keys}"
  local old_ifs="${IFS}"
  local key
  local -a key_array=()
  IFS=','
  read -r -a key_array <<< "${keys}"
  IFS="${old_ifs}"

  for key in "${key_array[@]}"; do
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    [[ -n "${key}" ]] && return 0
  done
  return 1
}

render_bark_device_keys() {
  if [[ "${provider}" != "bark" ]]; then
    printf '    device_keys: []\n'
    return 0
  fi

  printf '    device_keys:\n'
  write_device_keys_yaml "${bark_device_keys}"
}

render_config() {
  cat <<EOF
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
  path: ${STATE_DIR}/state.json

notifiers:
  bark:
    enabled: $([[ "${provider}" == "bark" ]] && printf 'true' || printf 'false')
    server_url: "$(escape_yaml_double_quoted "${bark_server_url}")"
$(render_bark_device_keys)
    group: "$(escape_yaml_double_quoted "${bark_group}")"

  pushover:
    enabled: $([[ "${provider}" == "pushover" ]] && printf 'true' || printf 'false')
    token: "$(escape_yaml_double_quoted "${pushover_token}")"
    user: "$(escape_yaml_double_quoted "${pushover_user}")"
    device: "$(escape_yaml_double_quoted "${pushover_device}")"
EOF
}

write_config() {
  if [[ "${config_preexisting}" == "true" && "${force_config}" != "true" ]]; then
    log "Config already exists at ${config_path}; leaving it unchanged"
    config_reused=true
    return 0
  fi

  if ! provider_ready; then
    validation_failure_reason="missing_credentials"
    return 0
  fi

  if [[ "${config_preexisting}" == "true" ]]; then
    local backup_path
    backup_path="${config_path}.bak.$(date +%Y%m%d%H%M%S)"
    log "Backing up existing config to ${backup_path}"
    run_privileged cp "${config_path}" "${backup_path}"
  fi

  log "Writing ${provider} config to ${config_path}"
  if [[ "${dry_run}" == "true" ]]; then
    printf 'DRY-RUN: write config %q with provider %q\n' "${config_path}" "${provider}"
    return 0
  fi

  local rendered
  rendered="$(render_config)"
  local temp_config
  temp_config="$(mktemp)"
  printf '%s\n' "${rendered}" > "${temp_config}"
  run_privileged mkdir -p "$(dirname "${config_path}")"
  if ! run_privileged install -m 0640 "${temp_config}" "${config_path}"; then
    rm -f "${temp_config}"
    return 1
  fi
  rm -f "${temp_config}"
  run_privileged chown "root:ip-notify" "${config_path}"
}

asset_url() {
  local asset="$1"
  printf '%s/%s/%s' "${RELEASE_BASE_URL}" "${version}" "${asset}"
}

download_and_verify() {
  local arch="$1"
  local archive_name="ip-notify_${version}_linux_${arch}.tar.gz"
  local sums_name="SHA256SUMS"
  local archive_url
  local sums_url
  archive_url="$(asset_url "${archive_name}")"
  sums_url="$(asset_url "${sums_name}")"

  if [[ "${dry_run}" == "true" ]]; then
    log "Would download ${archive_url}"
    log "Would download ${sums_url}"
    log "Would verify ${archive_name} with SHA256SUMS"
    return 0
  fi

  tmp_dir="$(mktemp -d)"
  log "Downloading ${archive_name}"
  curl -fL --retry 3 --retry-delay 2 -o "${tmp_dir}/${archive_name}" "${archive_url}"
  log "Downloading ${sums_name}"
  curl -fL --retry 3 --retry-delay 2 -o "${tmp_dir}/${sums_name}" "${sums_url}"

  log "Verifying SHA256 checksum"
  (cd "${tmp_dir}" && grep -F "  ${archive_name}" "${sums_name}" | sha256sum -c -)

  log "Extracting release archive"
  tar -xzf "${tmp_dir}/${archive_name}" -C "${tmp_dir}"
  [[ -x "${tmp_dir}/ip-notify" ]] || fail "release archive does not contain an executable ip-notify binary"
}

install_daemon() {
  if [[ "${dry_run}" == "true" ]]; then
    log "Would run ${tmp_dir:-<temp>}/ip-notify install-daemon --config ${config_path} --install-path ${install_path} --dry-run"
    return 0
  fi

  log "Installing daemon files"
  run_privileged "${tmp_dir}/ip-notify" install-daemon \
    --config "${config_path}" \
    --install-path "${install_path}"
}

validate_config() {
  if [[ "${config_reused}" != "true" ]] && ! provider_ready; then
    validation_failure_reason="missing_credentials"
    return 1
  fi

  if [[ "${dry_run}" == "true" ]]; then
    log "Would validate config with ${install_path} once --config ${config_path}"
    config_validated=true
    return 0
  fi

  log "Validating notifier config with one check"
  if ! run_privileged "${install_path}" once --config "${config_path}"; then
    validation_failure_reason="validation_failed"
    return 1
  fi
  if ! run_privileged chown -R "ip-notify:ip-notify" "${STATE_DIR}"; then
    validation_failure_reason="state_ownership_failed"
    return 1
  fi
  config_validated=true
}

restart_service() {
  if [[ "${start_service}" != "true" ]]; then
    log "Skipping service restart because --no-start was set"
    return 0
  fi

  if [[ "${config_validated}" != "true" ]]; then
    print_not_started_next_steps
    return 0
  fi

  if [[ "${dry_run}" == "true" ]]; then
    log "Would restart ${SERVICE_NAME}"
    return 0
  fi

  log "Restarting ${SERVICE_NAME}"
  run_privileged systemctl restart "${SERVICE_NAME}"
}

print_summary() {
  if [[ "${dry_run}" == "true" ]]; then
    log "Dry run complete for ${REPO} ${version}"
  else
    log "Installed ${REPO} ${version}"
  fi
  log "Binary: ${install_path}"
  log "Config: ${config_path}"
  log "Service: ${SERVICE_NAME}"
  if [[ "${start_service}" == "true" && "${dry_run}" != "true" && "${config_validated}" == "true" ]]; then
    log "Check status with: systemctl status ${SERVICE_NAME}"
    log "Watch logs with: journalctl -u ${SERVICE_NAME} -f"
  fi
}

main() {
  parse_args "$@"
  normalize_provider
  require_linux_systemd
  require_commands
  ensure_privilege_available
  detect_preexisting_config
  prompt_if_missing
  normalize_provider
  local arch
  arch="$(map_arch)"
  resolve_latest_version

  log "Version: ${version}"
  log "Architecture: ${arch}"
  log "Config path: ${config_path}"
  log "Install path: ${install_path}"
  if [[ -n "${provider}" ]]; then
    log "Provider: ${provider}"
  else
    warn "no provider configured"
  fi
  if [[ "${config_preexisting}" == "true" && "${force_config}" != "true" ]]; then
    log "Existing config detected; it will not be overwritten without --force-config"
  fi

  if [[ "${dry_run}" == "true" ]]; then
    log "Dry run enabled; no changes will be made"
  fi

  download_and_verify "${arch}"
  install_daemon
  write_config
  if validate_config; then
    restart_service
  else
    if [[ -z "${validation_failure_reason}" ]]; then
      validation_failure_reason="missing_credentials"
    fi
    print_not_started_next_steps
  fi
  print_summary
}

main "$@"
