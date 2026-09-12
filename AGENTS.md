# CatchUp DVR agent guide

## Purpose

CatchUp DVR is a subscription-free, self-hosted DVR for HDHomeRun tuners. It
imports guide data, schedules and records programs, and serves recordings as
append-only HLS EVENT streams so a recording can be watched while it is still
being written (chase play). It is designed for a Linux server running Podman.

The product should remain self-hosted: avoid adding a subscription requirement
or depending on a paid service for its core DVR functionality.

## Repository map

- `backend/`: Go service, SQLite persistence, HDHomeRun/XMLTV guide ingestion,
  recording scheduler, FFmpeg/HLS handling, and HTTP API.
- `backend/cmd/catchupd/`: application entry point and environment wiring.
- `backend/internal/httpapi/`: authenticated API routes and static-web serving.
- `backend/internal/store/`: SQLite schema/migrations and persistence methods.
- `backend/testdata/guide.xml`: local XMLTV fixture.
- `web/`: React + TypeScript single-page application.
- `deploy/`: ready-to-run deployment Compose file and its usage notes.
- `Containerfile`: multi-stage production image build.
- `compose.yml`: source-based Linux/Podman development deployment.

Read `README.md` and `docs/REAL_SERVER_VALIDATION.md` before changing
recording, tuner, GPU, or deployment behavior.

## Maintaining this guide

Keep `AGENTS.md` updated when a durable architectural decision, workflow rule,
deployment constraint, or local-development workaround would help a future
agent work safely. Keep it concise: summarize stable knowledge, remove stale
details, and link to the authoritative code or document instead of copying
large implementation notes here.

## Product and data-model conventions

- Recordings are HLS EVENT playlists, not a single growing transport-stream
  file. Preserve append-only playlist behavior and chase-play compatibility.
- The guide can come from HDHomeRun or a generic XMLTV fallback. Keep cached
  guide data available if a refresh fails.
- Authentication is first-run account creation with a bcrypt password hash and
  signed session stored locally in SQLite. Never add real credentials, tokens,
  hostnames, IP addresses, or user-specific paths to committed files.
- The HDHomeRun address is collected and connection-tested after first-run
  account creation, then saved in the local database. Do not restore it as an
  environment variable or external setup prompt.
- Preferences such as favorite channels belong to the authenticated user on the
  server, not only in browser storage.
- Station logos are automatic and server-side. Prefer a valid XMLTV channel
  icon; otherwise use an exact catalog match or an explicit affiliate
  description for a known broadcast network. Persist the result in
  `channel_logos` and leave uncertain or unavailable matches blank—do not add
  a user-facing logo configuration workflow.
- Live TV creates a temporary rewind buffer. It must not become a library
  recording unless the user explicitly records it, and it must be cleaned up
  when playback ends.
- Live TV is retained by HLS media reads and expires after a short idle period.
  Wait for at least three complete HLS segments before exposing a new live
  session, and do not use browser lifecycle events alone to stop it, because
  native Safari playback can outlive or temporarily hide the page.
- Google Cast uses Google's Default Media Receiver: keep the controls in the
  web player and issue an in-memory, short-idle, stream-scoped URL for the TV.
  Never make recordings public or rely on the browser's session cookie for a
  receiver fetch. An idle live Cast link must stop its live session and release
  the tuner.

## Change-control rules

- Do **not** commit, push, publish an image, deploy, or modify a user's server
  configuration unless the user explicitly asks in the current conversation.
- Staging files is allowed only when the user asks to stage them. Staging is not
  permission to commit.
- Keep work-in-progress version numbers unchanged. Increase the displayed app
  version and `web/package.json` version only immediately before a
  user-requested commit and image deployment. Do not create version-only
  commits.
- Preserve unrelated working-tree changes. Inspect `git status` before staging
  and stage only the requested files.
- Do not claim that browser, tuner, GPU, recording, or chase-play behavior was
  validated unless it was actually exercised in the relevant environment.
- Every code change must add or update focused automated unit or function
  tests, including a regression test for each bug fix. GitHub Actions has a
  required `test` merge gate on `main`; rely on that full suite rather than
  spending turns running it locally, unless the user explicitly asks for local
  testing or a focused check is needed while debugging.

## Local development

For source development, use a local `.env` copied from `.env.example` and do
not commit it. Run the backend and web app separately:

```sh
make dev-backend
make dev-web
```

The web app is normally available at `http://localhost:5173`. If no tuner is
available, load the checked-in guide fixture through the admin refresh API:

```sh
curl -X POST http://localhost:8080/api/admin/guide/refresh \
  -H 'Content-Type: application/json' \
  -d '{"source":"file","location":"backend/testdata/guide.xml"}'
```

On a Linux machine with Podman and `/dev/dri`, the repository compose file can
build and run the source image:

```sh
podman compose up -d --build
```

This compose file uses host networking and persistent `data/` and
`recordings/` directories. Do not use it casually against an existing server
stack; prefer an isolated container, data directory, and non-conflicting host
port for local UI work.

### Isolated local container pattern

Build a local-only tag and run it with fresh directories and a high host port,
for example `18095`. Mount the XMLTV fixture so the guide can populate without
a tuner. This container must have a distinct name, database directory, and
recordings directory from every real deployment.

```sh
podman build -t catchupdvr:local-test .
podman run -d --name catchup-dvr-local-test -p 18095:8080 \
  -e HTTP_ADDR=:8080 \
  -e DATABASE_PATH=/data/catchup.db \
  -e RECORDINGS_DIR=/recordings \
  -e XMLTV_FALLBACK=/fixtures/guide.xml \
  -v "$PWD/.local-test/data:/data:Z" \
  -v "$PWD/.local-test/recordings:/recordings:Z" \
  -v "$PWD/backend/testdata/guide.xml:/fixtures/guide.xml:ro,Z" \
  catchupdvr:local-test
```

On Apple Silicon, the production image's Intel-only `intel-media-driver`
package is unavailable for an ARM64 test image. For a **temporary local-only**
build variant, remove that one package in a copied `Containerfile.local`; do
not change the production `Containerfile` merely to accommodate a Mac UI test.
The production Linux image retains the Intel driver for VA-API support.

Local container startup is not a substitute for real-server validation: tuner
reachability, GPU acceleration, HLS playback in target browsers, and long
recordings must be verified on the eventual Linux host when the user asks.
