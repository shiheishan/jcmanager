#!/bin/bash
set -euo pipefail

REPO="${JCMANAGER_REPO:-shiheishan/jcmanager}"
SERVICE_NAME="jcmanager-server"
INSTALL_DIR="/opt/jcmanager"
CONFIG_DIR="/etc/jcmanager"
CONFIG_PATH="${CONFIG_DIR}/server.yaml"
DATA_DIR="/var/lib/jcmanager"
DOCKER_DIR="/opt/jcmanager-docker"
HTTP_PORT="8080"
GRPC_PORT="50051"
DOCKER_MODE="false"
AGENT_TOKEN="${JCMANAGER_AGENT_TOKEN:-}"
API_TOKEN="${JCMANAGER_API_TOKEN:-}"
EXTERNAL_URL="${JCMANAGER_EXTERNAL_URL:-}"

red() { printf '\033[0;31m%s\033[0m\n' "$*"; }
green() { printf '\033[0;32m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

die() {
  red "ERROR: $*" >&2
  exit 1
}

need_root() {
  [ "$(id -u)" -eq 0 ] || die "Please run as root (sudo)"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "Unsupported architecture: $(uname -m)" ;;
  esac
}

random_token() {
  tr -dc 'a-f0-9' </dev/urandom | head -c 64
}

read_config_value() {
  local key="$1"
  local path="$2"

  [ -f "$path" ] || return 0
  awk -F ': ' -v wanted="$key" '$1 == wanted { print $2; exit }' "$path" | tr -d '"'
}

detect_public_host() {
  local detected=""
  if command -v curl >/dev/null 2>&1; then
    detected="$(curl -fsSL https://api.ipify.org 2>/dev/null || true)"
  fi
  if [ -z "$detected" ] && command -v hostname >/dev/null 2>&1; then
    detected="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [ -z "$detected" ]; then
    detected="127.0.0.1"
  fi
  printf '%s\n' "$detected"
}

ensure_user() {
  if id -u jcmanager >/dev/null 2>&1; then
    return
  fi
  useradd --system --home "$DATA_DIR" --shell /usr/sbin/nologin jcmanager
}

download_release_bundle() {
  local arch="$1"
  local target="$2"
  local url="https://github.com/${REPO}/releases/latest/download/jcmanager-server-bundle-linux-${arch}.tar.gz"
  info "Downloading ${url}"
  curl -fsSL "$url" -o "$target"
}

write_server_config() {
  mkdir -p "$CONFIG_DIR" "$DATA_DIR"
  cat >"$CONFIG_PATH" <<YAML
grpc_addr: ":${GRPC_PORT}"
http_addr: ":${HTTP_PORT}"
db_path: "${DATA_DIR}/jcmanager.db"
token: "${AGENT_TOKEN}"
api_token: "${API_TOKEN}"
external_url: "${EXTERNAL_URL}"
YAML
  chmod 600 "$CONFIG_PATH"
}

install_systemd_service() {
  cat >/etc/systemd/system/${SERVICE_NAME}.service <<UNIT
[Unit]
Description=JCManager Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=jcmanager
Group=jcmanager
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/jcmanager-server -config ${CONFIG_PATH}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT

  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
}

install_bare_metal() {
  local arch="$1"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  download_release_bundle "$arch" "${tmp_dir}/bundle.tar.gz"
  mkdir -p "$tmp_dir/extract"
  tar -xzf "${tmp_dir}/bundle.tar.gz" -C "$tmp_dir/extract"

  ensure_user
  mkdir -p "$INSTALL_DIR" "$DATA_DIR"
  cp -R "${tmp_dir}/extract/." "$INSTALL_DIR/"
  chmod +x "${INSTALL_DIR}/jcmanager-server"
  if [ -d "${INSTALL_DIR}/agents" ]; then
    chmod +x "${INSTALL_DIR}"/agents/* || true
  fi
  chown -R jcmanager:jcmanager "$INSTALL_DIR" "$DATA_DIR"

  write_server_config
  install_systemd_service

  trap - EXIT
  rm -rf "$tmp_dir"
}

require_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker is required for --docker mode"
  docker compose version >/dev/null 2>&1 || die "docker compose is required for --docker mode"
}

install_docker_assets() {
  mkdir -p "$DOCKER_DIR" "$DOCKER_DIR/data"
  cat >"${DOCKER_DIR}/docker-compose.yml" <<'YAML'
services:
  jcmanager:
    image: ${JCMANAGER_IMAGE:-ghcr.io/shiheishan/jcmanager:latest}
    restart: unless-stopped
    ports:
      - "${JCMANAGER_HTTP_PORT:-8080}:8080"
      - "${JCMANAGER_GRPC_PORT:-50051}:50051"
    environment:
      JCMANAGER_AGENT_TOKEN: ${JCMANAGER_AGENT_TOKEN:?set JCMANAGER_AGENT_TOKEN}
      JCMANAGER_API_TOKEN: ${JCMANAGER_API_TOKEN:?set JCMANAGER_API_TOKEN}
      JCMANAGER_EXTERNAL_URL: ${JCMANAGER_EXTERNAL_URL:-}
    volumes:
      - ./data:/var/lib/jcmanager
YAML

  cat >"${DOCKER_DIR}/.env" <<ENV
JCMANAGER_IMAGE=ghcr.io/${REPO}:latest
JCMANAGER_HTTP_PORT=${HTTP_PORT}
JCMANAGER_GRPC_PORT=${GRPC_PORT}
JCMANAGER_AGENT_TOKEN=${AGENT_TOKEN}
JCMANAGER_API_TOKEN=${API_TOKEN}
JCMANAGER_EXTERNAL_URL=${EXTERNAL_URL}
ENV
}

run_docker_install() {
  require_docker
  install_docker_assets
  (
    cd "$DOCKER_DIR"
    docker compose up -d
  )
}

while [ $# -gt 0 ]; do
  case "$1" in
    --docker) DOCKER_MODE="true"; shift ;;
    --repo) REPO="$2"; shift 2 ;;
    --repo=*) REPO="${1#--repo=}"; shift ;;
    --http-port) HTTP_PORT="$2"; shift 2 ;;
    --http-port=*) HTTP_PORT="${1#--http-port=}"; shift ;;
    --grpc-port) GRPC_PORT="$2"; shift 2 ;;
    --grpc-port=*) GRPC_PORT="${1#--grpc-port=}"; shift ;;
    --external-url) EXTERNAL_URL="$2"; shift 2 ;;
    --external-url=*) EXTERNAL_URL="${1#--external-url=}"; shift ;;
    --agent-token) AGENT_TOKEN="$2"; shift 2 ;;
    --agent-token=*) AGENT_TOKEN="${1#--agent-token=}"; shift ;;
    --api-token) API_TOKEN="$2"; shift 2 ;;
    --api-token=*) API_TOKEN="${1#--api-token=}"; shift ;;
    *) die "Unknown option: $1" ;;
  esac
done

need_root

if [ -z "$AGENT_TOKEN" ]; then
  AGENT_TOKEN="$(read_config_value token "$CONFIG_PATH")"
fi
if [ -z "$API_TOKEN" ]; then
  API_TOKEN="$(read_config_value api_token "$CONFIG_PATH")"
fi
if [ -z "$EXTERNAL_URL" ]; then
  EXTERNAL_URL="$(read_config_value external_url "$CONFIG_PATH")"
fi

[ -n "$AGENT_TOKEN" ] || AGENT_TOKEN="$(random_token)"
[ -n "$API_TOKEN" ] || API_TOKEN="$(random_token)"
[ -n "$EXTERNAL_URL" ] || EXTERNAL_URL="http://$(detect_public_host):${HTTP_PORT}"

green "Installing JCManager Server..."
info "Repository:   ${REPO}"
info "HTTP port:    ${HTTP_PORT}"
info "gRPC port:    ${GRPC_PORT}"
info "External URL: ${EXTERNAL_URL}"

if [ "$DOCKER_MODE" = "true" ]; then
  run_docker_install
  echo ""
  green "JCManager Server installed with Docker Compose."
  info "Compose dir: ${DOCKER_DIR}"
else
  install_bare_metal "$(detect_arch)"
  echo ""
  green "JCManager Server installed."
  info "Binary dir: ${INSTALL_DIR}"
  info "Config:     ${CONFIG_PATH}"
fi

echo ""
info "Panel:     ${EXTERNAL_URL}"
info "API Token: ${API_TOKEN}"
echo ""
info "Remember to open firewall ports ${HTTP_PORT}/tcp and ${GRPC_PORT}/tcp."
