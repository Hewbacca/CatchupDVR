# CatchUp DVR

CatchUp DVR is a subscription-free, self-hosted DVR for an HDHomeRun. It imports a two-day XMLTV guide, exposes a responsive grid, and records programs as chase-playable HLS EVENT streams.

## What works

- SQLite-backed channels, programs, and recording jobs
- Automatically resolved station logos, persisted across guide refreshes; XMLTV icons take priority, exact station and explicit affiliate matches supply a fallback, and uncertain matches remain blank
- HDHomeRun guide download with a fresh `DeviceAuth` lookup on every refresh, gzip support, and last-good cache fallback
- Generic XMLTV file or URL fallback
- Responsive React/TypeScript guide and recording library
- One-click recording with pre/post padding and two-tuner conflict detection
- HLS playback controls for start-over, pause, -10, +30, +60, and Go Live
- Per-browser resume positions for recordings that have been watched
- First-run account setup with locally stored bcrypt password hashes and signed browser sessions
- Rewindable live TV from the guide, backed by temporary buffers that are removed when the player closes
- Live tuner availability with scheduled recordings taking priority over temporary live-TV sessions
- Intel VA-API render-device detection and deterministic FFmpeg HLS EVENT command construction
- Supervised FFmpeg recording with live status, graceful playlist finalization, and restart recovery
- Watch-from-start playback while a recording is still in progress
- Human-readable recording folders such as `the-matrix-09-10-2026-0600pm`
- Podman deployment definition with host networking and `/dev/dri` passthrough

Real tuner, GPU, and long-running chase-play validation must be performed on the Linux server; see [Real-server validation](docs/REAL_SERVER_VALIDATION.md).

## Local development

Prerequisites: Go, Node.js, and npm. FFmpeg is optional until running a real recording.

```sh
cp .env.example .env
make dev-backend
make dev-web
```

Open `http://localhost:5173`. With no tuner configured, load the fixture guide:

```sh
curl -X POST http://localhost:8080/api/admin/guide/refresh \
  -H 'Content-Type: application/json' \
  -d '{"source":"file","location":"backend/testdata/guide.xml"}'
```

## Run the container

Podman or Docker is a prerequisite. Pull and run the published image with persistent folders for the database and recordings:

```sh
mkdir -p catchup-dvr/data catchup-dvr/recordings
podman pull ghcr.io/hewbacca/catchupdvr:edge
podman run -d --name catchup-dvr --restart unless-stopped --network host \
  -e HTTP_ADDR=:8095 \
  -e DATABASE_PATH=/data/catchup.db \
  -e RECORDINGS_DIR=/recordings \
  -e TUNER_COUNT=2 \
  --device /dev/dri:/dev/dri \
  -v "$(pwd)/catchup-dvr/data:/data:Z" \
  -v "$(pwd)/catchup-dvr/recordings:/recordings:Z" \
  ghcr.io/hewbacca/catchupdvr:edge
```

Open `http://SERVER_IP:8095`, create the first account, then enter the HDHomeRun address or use `hdhomerun.local` and test it. The password and tuner address are stored in the mounted database. For the ready-to-run Compose definition, see [deploy/README.md](deploy/README.md).

To update, run `podman pull ghcr.io/hewbacca/catchupdvr:edge`, then recreate the container using the same command. Docker users can substitute `docker` for `podman`.

For development from source, copy `.env.example` to `.env` and run `podman compose up -d --build`. Connect the tuner from first-run setup.
