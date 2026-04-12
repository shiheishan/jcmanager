#!/bin/bash
set -euo pipefail

# JCManager Agent installer
#
# ===================== EDIT HERE =====================
# Fill in your server info ONCE, then copy this script
# to every VPS and just run: bash install.sh
# =====================================================
SERVER_IP=""
SERVER_TOKEN=""
# =====================================================
#
# That's it. Each VPS auto-uses its hostname as display name.
# Want a custom name? bash install.sh --name "HK-01"
#
# Or skip editing and pass everything as args:
#   bash install.sh --server 1.2.3.4 --token xxx --name "HK-01"
#
# Or download from running server (auto-configured):
#   curl -fsSL http://SERVER:8080/install.sh | bash

# Internal variables (set from SERVER_IP/SERVER_TOKEN or CLI args or server injection)
SERVER_GRPC="__SERVER_GRPC__"
SERVER_HTTP="__SERVER_HTTP__"
AGENT_TOKEN="__AGENT_TOKEN__"
NODE_ID="__NODE_ID__"
INSTALL_SECRET="__INSTALL_SECRET__"
INJECTED_DISPLAY_NAME="__DISPLAY_NAME__"

# Apply the easy config if filled in
if [ -n "$SERVER_IP" ]; then
    SERVER_GRPC="${SERVER_IP}:50051"
    SERVER_HTTP="http://${SERVER_IP}:8080"
fi
if [ -n "$SERVER_TOKEN" ]; then
    AGENT_TOKEN="$SERVER_TOKEN"
fi

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/jcmanager"
SERVICE_NAME="jcmanager-agent"
BINARY_NAME="jcmanager-agent"

# ── helpers ──────────────────────────────────────────────

red()   { printf '\033[0;31m%s\033[0m\n' "$*"; }
green() { printf '\033[0;32m%s\033[0m\n' "$*"; }
info()  { printf '  %s\n' "$*"; }

die() { red "ERROR: $*" >&2; exit 1; }

need_root() {
    [ "$(id -u)" -eq 0 ] || die "Please run as root (sudo)"
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) die "Unsupported architecture: $(uname -m). Only amd64 and arm64 are supported." ;;
    esac
}

detect_init() {
    if command -v systemctl >/dev/null 2>&1 && systemctl --version >/dev/null 2>&1; then
        echo "systemd"
    else
        echo "other"
    fi
}

# ── uninstall ────────────────────────────────────────────

do_uninstall() {
    need_root
    green "Uninstalling JCManager Agent..."

    if [ "$(detect_init)" = "systemd" ]; then
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
        systemctl disable "$SERVICE_NAME" 2>/dev/null || true
        rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
        systemctl daemon-reload 2>/dev/null || true
        info "Systemd service removed"
    fi

    rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    info "Binary removed"

    if [ -d "$CONFIG_DIR" ]; then
        printf "  Remove config directory %s? [y/N] " "$CONFIG_DIR"
        read -r answer
        if [ "$answer" = "y" ] || [ "$answer" = "Y" ]; then
            rm -rf "$CONFIG_DIR"
            info "Config directory removed"
        else
            info "Config directory kept"
        fi
    fi

    green "JCManager Agent uninstalled."
    exit 0
}

# ── install ──────────────────────────────────────────────

do_install() {
    need_root

    local display_name=""
    local xrayr_path=""
    local v2bx_path=""
    local cli_server=""
    local cli_token=""
    local cli_http_port="8080"
    local cli_grpc_port="50051"

    # parse args
    while [ $# -gt 0 ]; do
        case "$1" in
            --server)     cli_server="$2"; shift 2 ;;
            --server=*)   cli_server="${1#--server=}"; shift ;;
            --token)      cli_token="$2"; shift 2 ;;
            --token=*)    cli_token="${1#--token=}"; shift ;;
            --http-port)  cli_http_port="$2"; shift 2 ;;
            --http-port=*)cli_http_port="${1#--http-port=}"; shift ;;
            --grpc-port)  cli_grpc_port="$2"; shift 2 ;;
            --grpc-port=*)cli_grpc_port="${1#--grpc-port=}"; shift ;;
            --name)       display_name="$2"; shift 2 ;;
            --name=*)     display_name="${1#--name=}"; shift ;;
            --xrayr)      xrayr_path="$2"; shift 2 ;;
            --xrayr=*)    xrayr_path="${1#--xrayr=}"; shift ;;
            --v2bx)       v2bx_path="$2"; shift 2 ;;
            --v2bx=*)     v2bx_path="${1#--v2bx=}"; shift ;;
            --uninstall)  do_uninstall ;;
            *)            die "Unknown option: $1" ;;
        esac
    done

    # If CLI --server/--token provided, override the placeholders.
    if [ -n "$cli_server" ]; then
        SERVER_GRPC="${cli_server}:${cli_grpc_port}"
        SERVER_HTTP="http://${cli_server}:${cli_http_port}"
    fi
    if [ -n "$cli_token" ]; then
        AGENT_TOKEN="$cli_token"
    fi

    # Validate: catch common copy-paste mistakes (Chinese placeholder text, example values).
    if echo "$cli_server" | grep -qP '[^\x00-\x7F]' 2>/dev/null || echo "$cli_server" | grep -q '[^a-zA-Z0-9\.:\-]' 2>/dev/null; then
        echo ""
        red "Invalid --server value: $cli_server"
        echo ""
        info "--server must be a real IP address or domain name, for example:"
        info "  bash install.sh --server 123.45.67.89 --token mytoken123 --name HK-01"
        echo ""
        exit 1
    fi
    if echo "$cli_token" | grep -qP '[^\x00-\x7F]' 2>/dev/null; then
        echo ""
        red "Invalid --token value: $cli_token"
        echo ""
        info "--token must be the actual token string from your server, not Chinese placeholder text."
        info "  Example: bash install.sh --server 123.45.67.89 --token mytoken123 --name HK-01"
        echo ""
        exit 1
    fi

    # Validate: placeholders must have been replaced (either by server or CLI args).
    if echo "$SERVER_HTTP" | grep -q '__SERVER_HTTP__'; then
        echo ""
        red "Server address not configured!"
        echo ""
        info "You're running the install script directly. You need to provide server info:"
        echo ""
        info "  bash install.sh --server 123.45.67.89 --token mytoken123 --name HK-01"
        info "                          ^^^^^^^^^^^^        ^^^^^^^^^^"
        info "                          real server IP      real token from server"
        echo ""
        info "Or download the pre-configured script from your running server:"
        echo ""
        info "  curl -fsSL http://YOUR_SERVER:8080/install.sh | bash -s -- --name HK-01"
        echo ""
        exit 1
    fi
    if echo "$AGENT_TOKEN" | grep -q '__AGENT_TOKEN__'; then
        AGENT_TOKEN=""
    fi
    if [ -z "$AGENT_TOKEN" ] && [ -z "$INSTALL_SECRET" ]; then
        die "Agent token not configured. Use --token YOUR_REAL_TOKEN (not placeholder text)"
    fi

    # default display name = pre-assigned value, otherwise hostname
    if [ -z "$display_name" ]; then
        if [ -n "$INJECTED_DISPLAY_NAME" ]; then
            display_name="$INJECTED_DISPLAY_NAME"
        else
            display_name="$(hostname -s 2>/dev/null || hostname)"
        fi
    fi

    green "Installing JCManager Agent..."
    info "Server (gRPC): $SERVER_GRPC"
    info "Display name:  $display_name"

    # detect arch
    local arch
    arch="$(detect_arch)"
    info "Architecture:  $arch"

    # auto-detect xrayr / v2bx config paths
    if [ -z "$xrayr_path" ]; then
        for p in /etc/XrayR/config.yml /usr/local/XrayR/config.yml; do
            [ -f "$p" ] && { xrayr_path="$p"; break; }
        done
    fi
    if [ -z "$v2bx_path" ]; then
        for p in /etc/V2bX/config.yml /usr/local/V2bX/config.yml; do
            [ -f "$p" ] && { v2bx_path="$p"; break; }
        done
    fi
    [ -n "$xrayr_path" ] && info "XrayR config:  $xrayr_path"
    [ -n "$v2bx_path" ]  && info "V2bX config:   $v2bx_path"

    # build allowed_paths from detected configs
    local allowed_paths=""
    [ -n "$xrayr_path" ] && allowed_paths="  - \"$(dirname "$xrayr_path")/\""
    if [ -n "$v2bx_path" ]; then
        [ -n "$allowed_paths" ] && allowed_paths="$allowed_paths
"
        allowed_paths="${allowed_paths}  - \"$(dirname "$v2bx_path")/\""
    fi

    # download agent binary
    info "Downloading agent binary..."
    local download_url="${SERVER_HTTP}/download/agent?arch=${arch}"
    if [ -n "$INSTALL_SECRET" ]; then
        download_url="${download_url}&secret=${INSTALL_SECRET}"
    else
        download_url="${download_url}&token=${AGENT_TOKEN}"
    fi
    local tmp_bin
    tmp_bin="$(mktemp)"
    if ! curl -fsSL -o "$tmp_bin" "$download_url"; then
        rm -f "$tmp_bin"
        die "Failed to download agent binary. Is the server running? Is the token correct?"
    fi

    # verify we got an actual binary (not an HTML error page)
    local file_type
    file_type="$(file -b "$tmp_bin" 2>/dev/null || echo "unknown")"
    case "$file_type" in
        *ELF*) ;;
        *) rm -f "$tmp_bin"; die "Downloaded file is not a valid binary (got: $file_type)" ;;
    esac

    # stop existing service if running
    if [ "$(detect_init)" = "systemd" ]; then
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    fi

    # install binary
    install -m 755 "$tmp_bin" "${INSTALL_DIR}/${BINARY_NAME}"
    rm -f "$tmp_bin"
    info "Binary installed: ${INSTALL_DIR}/${BINARY_NAME}"

    # create config directory
    mkdir -p "$CONFIG_DIR"
    chmod 700 "$CONFIG_DIR"

    # write config (only if not exists, to preserve existing agent.id)
    local config_file="${CONFIG_DIR}/agent.yaml"
    if [ -f "$config_file" ]; then
        info "Config already exists, updating server address and token only..."
        # backup existing config
        cp "$config_file" "${config_file}.bak"
    fi

    cat > "$config_file" << YAML
server:
  address: "${SERVER_GRPC}"
  token: "${AGENT_TOKEN}"
  insecure: true
node_id: "${NODE_ID}"
install_secret: "${INSTALL_SECRET}"
display_name: "${display_name}"
xrayr_config_path: "${xrayr_path}"
v2bx_config_path: "${v2bx_path}"
allowed_paths:
${allowed_paths:-  []}
YAML
    chmod 600 "$config_file"
    info "Config written: $config_file"

    # systemd service
    if [ "$(detect_init)" = "systemd" ]; then
        cat > "/etc/systemd/system/${SERVICE_NAME}.service" << UNIT
[Unit]
Description=JCManager Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY_NAME} -config ${CONFIG_DIR}/agent.yaml
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT
        systemctl daemon-reload
        systemctl enable "$SERVICE_NAME"
        systemctl start "$SERVICE_NAME"
        info "Systemd service created and started"
    else
        info "No systemd detected. Start manually: ${INSTALL_DIR}/${BINARY_NAME} -config ${CONFIG_DIR}/agent.yaml"
    fi

    echo ""
    green "JCManager Agent installed successfully!"
    info "Display name: $display_name"
    info "Config:       $config_file"
    info "Binary:       ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""
    info "Useful commands:"
    info "  Check status:  systemctl status $SERVICE_NAME"
    info "  View logs:     journalctl -u $SERVICE_NAME -f"
    info "  Restart:       systemctl restart $SERVICE_NAME"
    info "  Uninstall:     curl -fsSL ${SERVER_HTTP}/install.sh | bash -s -- --uninstall"
}

# ── main ─────────────────────────────────────────────────

# check if --uninstall is in args before entering do_install
for arg in "$@"; do
    [ "$arg" = "--uninstall" ] && do_uninstall
done

do_install "$@"
