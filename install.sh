#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ $EUID -eq 0 ]] || { echo "Run with sudo: sudo ./install.sh" >&2; exit 1; }
command -v docker >/dev/null || { echo "Docker CLI is required." >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "Docker Compose v2 is required." >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "Docker daemon is not reachable." >&2; exit 1; }

ARCH="$(uname -m)"
case "$ARCH" in x86_64|amd64) GOARCH=amd64 ;; aarch64|arm64) GOARCH=arm64 ;; *) echo "Unsupported CPU architecture: $ARCH" >&2; exit 1 ;; esac
TMP="$(mktemp -d)"
cleanup(){ rm -rf "$TMP"; }
trap cleanup EXIT

if [[ -f "$ROOT/dist/container-watch-linux-$GOARCH" ]]; then
  install -m 755 "$ROOT/dist/container-watch-linux-$GOARCH" "$TMP/container-watch"
elif command -v go >/dev/null 2>&1; then
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags='-s -w' -o "$TMP/container-watch" .)
else
  IMG="container-watch-builder:local"
  docker build --target build --build-arg TARGETOS=linux --build-arg TARGETARCH="$GOARCH" -t "$IMG" "$ROOT"
  CID="$(docker create "$IMG")"
  docker cp "$CID:/out/container-watch" "$TMP/container-watch"
  docker rm "$CID" >/dev/null
  docker image rm "$IMG" >/dev/null 2>&1 || true
fi

install -d -m 755 /opt/container-watch
install -m 755 "$TMP/container-watch" /opt/container-watch/container-watch
install -d -m 755 /opt/container-watch/web
install -m 644 "$ROOT/web/index.html" /opt/container-watch/web/index.html
install -m 644 "$ROOT/container-watch.service" /etc/systemd/system/container-watch.service

if [[ ! -f /etc/container-watch.env ]]; then
  PANEL_USER="${PANEL_USER:-admin}"
  PANEL_PASSWORD="${PANEL_PASSWORD:-$(openssl rand -hex 24)}"
  LISTEN_ADDR="${LISTEN_ADDR:-0.0.0.0:8744}"
  HOST_DOCKER_BIN="${HOST_DOCKER_BIN:-$(command -v docker)}"
  umask 077
  printf 'PANEL_USER=%s\nPANEL_PASSWORD=%s\nLISTEN_ADDR=%s\nWEB_DIR=/opt/container-watch/web\nHOST_DOCKER_BIN=%s\n' "$PANEL_USER" "$PANEL_PASSWORD" "$LISTEN_ADDR" "$HOST_DOCKER_BIN" > /etc/container-watch.env
  chmod 600 /etc/container-watch.env
  NEW_PASSWORD=1
else
  echo "Keeping existing /etc/container-watch.env credentials and settings."
fi

systemctl daemon-reload
systemctl enable --now container-watch.service
systemctl --no-pager --full status container-watch.service || true
if [[ "${NEW_PASSWORD:-0}" == 1 ]]; then
  echo
  echo "Initial panel credentials (save now):"
  echo "  username: $PANEL_USER"
  echo "  password: $PANEL_PASSWORD"
  echo "Listening at: $LISTEN_ADDR"
  echo "Edit /etc/container-watch.env to change the bind address or credentials, then restart the service."
fi
