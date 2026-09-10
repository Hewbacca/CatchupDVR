#!/usr/bin/env bash
set -euo pipefail

DEFAULT_IMAGE="ghcr.io/hewbacca/catchupdvr:edge"
DEFAULT_PORT="8095"

say() { printf '%s\n' "$*"; }
fail() { say "Error: $*" >&2; exit 1; }

command -v podman >/dev/null 2>&1 || fail "Podman is not installed. Install Podman first, then run this installer again."
podman compose version >/dev/null 2>&1 || fail "Podman Compose is unavailable. Install the podman-compose package first."

INSTALL_DIR="${CATCHUP_INSTALL_DIR:-${PWD}/catchup-dvr}"
IMAGE_REF="${CATCHUP_IMAGE:-$DEFAULT_IMAGE}"
PORT="${CATCHUP_PORT:-$DEFAULT_PORT}"
TUNER_IP="${1:-${HDHOMERUN_IP:-}}"
RECORDINGS_PATH="${2:-${CATCHUP_RECORDINGS_DIR:-${INSTALL_DIR}/recordings}}"
DATA_PATH="${CATCHUP_DATA_DIR:-${INSTALL_DIR}/data}"

if [[ -z "$TUNER_IP" && -t 0 ]]; then
  read -r -p "HDHomeRun IP address: " TUNER_IP
fi
[[ -n "$TUNER_IP" ]] || fail "Provide the HDHomeRun IP address: ./install.sh 192.168.1.50"
[[ "$PORT" =~ ^[0-9]+$ ]] || fail "CATCHUP_PORT must be a number."
[[ -d /dev/dri ]] || fail "/dev/dri was not found. Confirm the Intel GPU driver is loaded."

if command -v curl >/dev/null 2>&1; then
  curl -fsS --max-time 5 "http://${TUNER_IP}/discover.json" >/dev/null || fail "The HDHomeRun did not answer at ${TUNER_IP}. Check its address and wired-network connection."
fi

INTEL_RENDER_DEVICE=""
for vendor_file in /sys/class/drm/renderD*/device/vendor; do
  [[ -f "$vendor_file" ]] || continue
  if [[ "$(tr '[:upper:]' '[:lower:]' < "$vendor_file" | tr -d '[:space:]')" == "0x8086" ]]; then
    INTEL_RENDER_DEVICE="/dev/dri/$(basename "$(dirname "$(dirname "$vendor_file")")")"
    break
  fi
done
[[ -n "$INTEL_RENDER_DEVICE" ]] || fail "No Intel render device was found under /dev/dri."

mkdir -p "$INSTALL_DIR" "$DATA_PATH" "$RECORDINGS_PATH"
cat > "$INSTALL_DIR/compose.yml" <<'COMPOSE'
services:
  catchup:
    image: ${CATCHUP_IMAGE}
    container_name: catchup-dvr
    restart: unless-stopped
    network_mode: host
    environment:
      HTTP_ADDR: :${CATCHUP_PORT:-8095}
      DATABASE_PATH: /data/catchup.db
      RECORDINGS_DIR: /recordings
      HDHOMERUN_IP: ${HDHOMERUN_IP}
      XMLTV_FALLBACK: ${XMLTV_FALLBACK:-}
      TUNER_COUNT: ${TUNER_COUNT:-2}
      FFMPEG_PATH: ffmpeg
      GPU_MODE: ${GPU_MODE:-auto}
      GPU_RENDER_DEVICE: ${GPU_RENDER_DEVICE:-}
      PRE_PADDING_MINUTES: ${PRE_PADDING_MINUTES:-2}
      POST_PADDING_MINUTES: ${POST_PADDING_MINUTES:-5}
    devices:
      - /dev/dri:/dev/dri
    volumes:
      - ${CATCHUP_DATA_DIR}:/data:Z
      - ${CATCHUP_RECORDINGS_DIR}:/recordings:Z
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:${CATCHUP_PORT:-8095}/api/health"]
      interval: 30s
      timeout: 5s
      retries: 3
COMPOSE

umask 077
{
  printf 'CATCHUP_IMAGE=%s\n' "$IMAGE_REF"
  printf 'CATCHUP_PORT=%s\n' "$PORT"
  printf 'CATCHUP_DATA_DIR=%s\n' "$DATA_PATH"
  printf 'CATCHUP_RECORDINGS_DIR=%s\n' "$RECORDINGS_PATH"
  printf 'HDHOMERUN_IP=%s\n' "$TUNER_IP"
  printf 'TUNER_COUNT=2\n'
  printf 'GPU_MODE=auto\n'
  printf 'GPU_RENDER_DEVICE=%s\n' "$INTEL_RENDER_DEVICE"
  printf 'PRE_PADDING_MINUTES=2\n'
  printf 'POST_PADDING_MINUTES=5\n'
} > "$INSTALL_DIR/.env"

say "Pulling ${IMAGE_REF}…"
podman pull "$IMAGE_REF"
say "Starting CatchUp DVR…"
(
  cd "$INSTALL_DIR"
  podman compose --env-file .env -f compose.yml up -d
)

if command -v curl >/dev/null 2>&1; then
  ready=""
  for _ in {1..20}; do
    if curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/api/health" >/dev/null 2>&1; then ready="yes"; break; fi
    sleep 1
  done
  [[ -n "$ready" ]] || fail "The container started but its health check did not become ready. Run: podman logs catchup-dvr"
fi

say ""
say "CatchUp DVR is running at http://$(hostname -I 2>/dev/null | awk '{print $1}'):${PORT}"
say "Recordings: ${RECORDINGS_PATH}"
say "Configuration: ${INSTALL_DIR}/.env"
