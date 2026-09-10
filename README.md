# CatchUp DVR

CatchUp DVR is a subscription-free, self-hosted DVR for an HDHomeRun. The first implementation slice imports a two-day XMLTV guide, exposes a responsive grid, creates persistent recording jobs with conflict detection, and builds the FFmpeg command used to create chase-playable HLS EVENT recordings.

## What works in this slice

- SQLite-backed channels, programs, and recording jobs
- HDHomeRun guide download with a fresh `DeviceAuth` lookup on every refresh, gzip support, and last-good cache fallback
- Generic XMLTV file or URL fallback
- Responsive React/TypeScript guide and recording library
- One-click recording with pre/post padding and two-tuner conflict detection
- HLS playback controls for start-over, pause, -10, +30, +60, and Go Live
- Intel VA-API render-device detection and deterministic FFmpeg HLS EVENT command construction
- Podman deployment definition with host networking and `/dev/dri` passthrough

The scheduler process and real FFmpeg lifecycle are the next slice. This repository does not claim hardware validation from the development Mac; see [Real-server validation](docs/REAL_SERVER_VALIDATION.md).

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

## Install on Linux Mint

The development image is `ghcr.io/hewbacca/catchupdvr:edge`; a stable release will use `:latest` after real chase-play passes its acceptance test. Publishing is automated by [the GitHub workflow](.github/workflows/container.yml) after this repository is pushed to GitHub. The package must be changed from private to public once after its first publish so the Linux server can pull it without a registry login.

On the Linux server, run:

```sh
curl -fsSL https://raw.githubusercontent.com/Hewbacca/CatchupDVR/main/deploy/install.sh | bash -s -- HDHOMERUN_IP /optional/recordings/path
```

The installer detects the Intel render device, pulls the image, writes the local configuration, starts the service, and verifies its health. See [the installation guide](deploy/README.md) for overrides and updates.

For development from source, copy `.env.example` to `.env`, set `HDHOMERUN_IP`, and run `podman compose up -d --build`.
