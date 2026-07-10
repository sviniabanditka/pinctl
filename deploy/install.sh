#!/bin/sh
# pinctl installer — see docs/14-deployment.md#install for the full spec
# this script implements.
#
# Two ways to run it:
#
#   1. Release install:
#        curl -fsSL https://raw.githubusercontent.com/sviniabanditka/pinctl/main/deploy/install.sh | sudo sh
#      Downloads the binary matching this machine's arch from the latest
#      release at https://github.com/sviniabanditka/pinctl/releases,
#      verifies its checksum, and installs it.
#
#   2. Local-binary install (a binary you built yourself with
#      `go build -o pinctl ./cmd/pinctl` after `npm run build` in web/):
#        ./install.sh ./pinctl
#        sudo ./install.sh /path/to/pinctl
#      Skips the download/checksum step and installs the given binary
#      as-is. No checksum verification happens in this mode since there
#      is no published checksum to verify against — only use it with a
#      binary you trust (one you built yourself or got from a trusted
#      peer).
#
# Idempotent either way: re-running is how you upgrade (see
# docs/14-deployment.md#upgrade) — it never overwrites an existing
# /etc/pinctl/config.yaml, and it does not enable/start the service if
# it's already enabled/running (systemctl enable/daemon-reload are
# themselves idempotent).
#
# Must be run as root (or via sudo) — it installs to /usr/local/bin,
# creates a system user, and installs a systemd unit.

set -eu

REPO="sviniabanditka/pinctl"
BIN_DEST="/usr/local/bin/pinctl"
CONF_DIR="/etc/pinctl"
DATA_DIR="/var/lib/pinctl"
UNIT_DEST="/etc/systemd/system/pinctl.service"
SVC_USER="pinctl"
SVC_GROUP="pinctl"

# Directory this script lives in, so it can find deploy/pinctl.service
# whether invoked as ./install.sh, sh install.sh, or via a pipe (in the
# curl|sh case SCRIPT_DIR falls back to a fetched copy — see
# install_unit() below).
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd -P || true)

log() { printf 'pinctl-install: %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

require_root() {
    if [ "$(id -u)" -ne 0 ]; then
        die "must be run as root (try: sudo $0 $*)"
    fi
}

detect_arch() {
    # Maps `uname -m` to the release asset naming used by the `release`
    # CI job (see .github/workflows/release.yml and docs/13-testing.md#ci):
    # pinctl-linux-<arch>.
    _uname_m=$(uname -m)
    case "$_uname_m" in
        aarch64|arm64)
            echo arm64 ;;
        armv7l|arm)
            echo armv7 ;;
        x86_64|amd64)
            echo amd64 ;;
        armv6l)
            # The armv7 asset is built with GOARM=7; running it on an ARMv6
            # core (Pi Zero/Zero W, Pi 1) faults on an illegal instruction.
            # Fail loudly rather than install something that won't start.
            die "unsupported architecture: armv6 (Pi Zero/1). Build from source with GOARM=6: CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -o pinctl ./cmd/pinctl, then: sudo ./install.sh ./pinctl" ;;
        *)
            die "unsupported architecture: $_uname_m (pinctl supports linux/arm64, linux/armv7, linux/amd64 — see docs/14-deployment.md#supported-targets)" ;;
    esac
}

detect_os() {
    _uname_s=$(uname -s)
    case "$_uname_s" in
        Linux) echo linux ;;
        *) die "unsupported OS: $_uname_s (pinctl only deploys to Linux — see docs/14-deployment.md#supported-targets)" ;;
    esac
}

# download_release fetches the release binary + checksum for this
# machine's OS/arch from GitHub releases and verifies the checksum,
# leaving the verified binary at $1.
download_release() {
    _dest=$1
    _os=$(detect_os)
    _arch=$(detect_arch)
    _asset="pinctl-${_os}-${_arch}"
    _base_url="https://github.com/${REPO}/releases/latest/download"

    command -v curl >/dev/null 2>&1 || die "curl is required to download a release"

    _tmp=$(mktemp -d)
    trap 'rm -rf "$_tmp"' EXIT

    log "downloading ${_asset} from the latest ${REPO} release"
    curl -fsSL -o "$_tmp/$_asset" "${_base_url}/${_asset}" \
        || die "download failed: ${_base_url}/${_asset} (no release published yet? use local-binary install instead: ./install.sh /path/to/pinctl)"
    curl -fsSL -o "$_tmp/checksums.txt" "${_base_url}/checksums.txt" \
        || die "download failed: ${_base_url}/checksums.txt"

    log "verifying checksum"
    _expected_line=$(grep " ${_asset}\$" "$_tmp/checksums.txt" || true)
    [ -n "$_expected_line" ] || die "no checksum entry for ${_asset} in checksums.txt — refusing to install an unverifiable binary"
    _expected_hash=$(printf '%s\n' "$_expected_line" | awk '{print $1}')

    if command -v sha256sum >/dev/null 2>&1; then
        _actual_hash=$(sha256sum "$_tmp/$_asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        _actual_hash=$(shasum -a 256 "$_tmp/$_asset" | awk '{print $1}')
    else
        die "neither sha256sum nor shasum is available to verify the checksum"
    fi

    [ "$_actual_hash" = "$_expected_hash" ] \
        || die "checksum verification FAILED for ${_asset} — refusing to install a corrupted or tampered binary"

    cp "$_tmp/$_asset" "$_dest"
    chmod 0755 "$_dest"
}

install_binary() {
    _local_path="${1:-}"

    if [ -n "$_local_path" ]; then
        [ -f "$_local_path" ] || die "local binary not found: $_local_path"
        log "installing local binary: $_local_path (pre-release install — no checksum to verify, only use a binary you trust)"
        install -o root -g root -m 0755 "$_local_path" "$BIN_DEST"
    else
        _tmp_bin=$(mktemp)
        download_release "$_tmp_bin"
        install -o root -g root -m 0755 "$_tmp_bin" "$BIN_DEST"
        rm -f "$_tmp_bin"
    fi
    log "installed $BIN_DEST"
}

create_user() {
    if id "$SVC_USER" >/dev/null 2>&1; then
        log "user $SVC_USER already exists, skipping"
    else
        log "creating system user $SVC_USER (dialout group, no login, no home)"
        useradd --system --no-create-home --shell /usr/sbin/nologin \
            --groups dialout "$SVC_USER"
    fi
}

create_dirs() {
    mkdir -p "$CONF_DIR" "$DATA_DIR"
    chown "$SVC_USER:$SVC_GROUP" "$DATA_DIR"
    chmod 0755 "$CONF_DIR"
    chmod 0750 "$DATA_DIR"
    log "ensured $CONF_DIR and $DATA_DIR"
}

write_default_config() {
    _conf="$CONF_DIR/config.yaml"
    if [ -f "$_conf" ]; then
        log "$_conf already exists, leaving it untouched"
        return
    fi
    log "writing starter $_conf"
    cat > "$_conf" <<'EOF'
# /etc/pinctl/config.yaml
# Full reference: docs/14-deployment.md#configuration

http:
  # Listen address for REST + WebSocket + the embedded web UI.
  addr: ":8080"

# Root directory for device/pipeline JSON files.
data_dir: /var/lib/pinctl

serial:
  # Serial device paths pinctl is permitted to open. An empty list ([])
  # means no serial boards; ["*"] means any port. Omit the key entirely
  # for the default (/dev/ttyACM*, /dev/ttyUSB*).
  allowlist:
    - /dev/ttyACM0
    - /dev/ttyUSB0

  # Baud rate for Firmata sessions (StandardFirmata's default).
  baud: 57600

log:
  # debug | info | warn | error
  level: info
  # json (production/journald-friendly) or text
  format: json

# Optional MQTT bridge and Home Assistant discovery — off by default.
# See docs/14-deployment.md#configuration and docs/15-mqtt-bridge.md
# before enabling: broker auth, base_topic, and the pipeline-conflict
# rule are all worth understanding first.
#
# mqtt:
#   enabled: false
#   broker: tcp://192.168.1.10:1883
#   client_id: pinctl
#   username: pinctl
#   password_file: /etc/pinctl/mqtt-password
#   base_topic: pinctl
#   discovery_prefix: homeassistant
#   analog_deadband: 4
#   min_publish_interval: 1s
#   max_publish_interval: 60s
EOF
    chmod 0644 "$_conf"
}

install_unit() {
    _unit_src="$SCRIPT_DIR/pinctl.service"
    if [ ! -f "$_unit_src" ]; then
        # Fallback for curl|sh, where $0 isn't a real file on disk:
        # fetch the unit file from the repo directly.
        log "fetching pinctl.service from ${REPO}"
        _unit_src=$(mktemp)
        curl -fsSL -o "$_unit_src" \
            "https://raw.githubusercontent.com/${REPO}/main/deploy/pinctl.service" \
            || die "could not fetch deploy/pinctl.service"
    fi
    install -o root -g root -m 0644 "$_unit_src" "$UNIT_DEST"
    systemctl daemon-reload
    log "installed $UNIT_DEST (not enabled/started automatically)"
}

main() {
    require_root
    install_binary "${1:-}"
    create_user
    create_dirs
    write_default_config
    install_unit

    log "done."
    log "Next steps:"
    log "  1. Review $CONF_DIR/config.yaml, especially serial.allowlist."
    log "  2. sudo systemctl enable --now pinctl"
    log "  3. journalctl -u pinctl -f"
    log "See docs/14-deployment.md for configuration, udev, and troubleshooting."
}

main "$@"
