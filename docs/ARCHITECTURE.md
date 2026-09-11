# Architecture

## Product boundary

CatchUp DVR is one Go service, one SQLite database, a local recording store, and an installable web client. It talks directly to the HDHomeRun on the home LAN and never depends on a paid or proprietary DVR backend.

```text
HDHomeRun ── MPEG-TS ──> FFmpeg worker ──> append-only HLS EVENT directory
    │                         │                         │
    └── discovery + XMLTV ───> Go service + SQLite <───┘
                                  │
                         React PWA over HTTP/HLS
```

## Runtime components

- `cmd/catchupd`: composition root, configuration, schema migration, HTTP server.
- `internal/guide`: XMLTV download and parsing. HDHomeRun mode fetches `/discover.json` before every XMLTV request so rotating authorization is never cached. Successful XML is atomically retained as the last-good guide.
- `internal/store`: SQLite persistence and overlap-aware two-tuner reservation.
- `internal/recording`: GPU discovery, FFmpeg argument construction, supervised recording, temporary live-TV buffers, and shared tuner allocation. Output uses HLS EVENT playlists with four-second independent segments.
- `internal/httpapi`: JSON API plus production PWA/static and recording-file serving.
- `web`: React/TypeScript PWA. Safari uses its native HLS support; other browsers use hls.js.

## Chase-play invariants

These are design constraints, not optional optimizations:

1. Playback begins from an append-only HLS EVENT playlist, never an ordinary growing transport-stream file.
2. Old segments remain addressable for the lifetime of an active recording.
3. Segments target four seconds and start at independent video boundaries.
4. The live edge advances without rewriting prior media.
5. Finishing a recording adds `#EXT-X-ENDLIST`; crash recovery must either resume safely or finalize the playable prefix.
6. Seek controls operate on the media timeline, with Go Live targeting the current seekable range end.

## Recording state model

`scheduled → recording → completed`

Exceptional terminal states are `conflict`, `failed`, and `cancelled`. Pre/post padding and user-requested live extension are stored with each job so restarts reproduce the same boundaries. A future `recording_rules` table will generate jobs idempotently for series and team-style rules.

## Tuner allocation

The store checks padded intervals and rejects a new job when two accepted jobs already overlap it. A shared runtime pool accounts for both scheduled recordings and temporary live-TV sessions. Scheduled recordings take priority: when every tuner is occupied, a due recording stops one temporary live buffer, waits for its FFmpeg process to release the tuner, and then begins capture.

Live TV uses the same append-only HLS pipeline under a hidden `.live` directory. These sessions never create database recording rows, are deleted when the player closes, and also expire after six hours as a safety net.

## GPU selection

At startup on Linux, `recording.DetectRenderDevice` enumerates `/dev/dri/renderD*`, reads sysfs vendor metadata, and prefers Intel (`0x8086`). `GPU_RENDER_DEVICE` overrides detection. The initial FFmpeg profile uses VA-API H.264 encoding; QSV can be selected explicitly once the real server proves its driver/filter combination. Software H.264 is the safe fallback.

## Guide refresh policy

After each successful HDHomeRun refresh, the next refresh is randomly scheduled 20–28 hours later. Failures retain the cached guide and retry after 30 minutes. Generic XMLTV file/URL import is available for setup and outages. The service also exposes an explicit refresh endpoint for diagnostics and fixture-driven development.

## Security and network assumptions

The initial deployment is trusted-LAN only. Podman uses host networking so HDHomeRun discovery and direct streams work reliably. Before remote access, add a TLS reverse proxy and authentication; do not expose the service directly to the internet.
