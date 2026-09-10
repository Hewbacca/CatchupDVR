#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${CATCHUP_INSTALL_DIR:-${PWD}/catchup-dvr}"
[[ -f "$INSTALL_DIR/.env" && -f "$INSTALL_DIR/compose.yml" ]] || {
  printf 'CatchUp DVR is not installed at %s.\n' "$INSTALL_DIR" >&2
  exit 1
}

(
  cd "$INSTALL_DIR"
  podman compose --env-file .env -f compose.yml pull
  podman compose --env-file .env -f compose.yml up -d
)
podman image prune -f
printf 'CatchUp DVR is updated.\n'
