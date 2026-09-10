# Implementation plan

## Slice 1 — guide to durable recording intent (implemented)

- Import official HDHomeRun XMLTV or a generic XMLTV source.
- Persist guide rows in SQLite and display a responsive two-day grid.
- Create/delete recording jobs with padding and immediate two-tuner conflict detection.
- Define and test Intel GPU discovery and the chase-play HLS FFmpeg contract.
- Provide the PWA shell, library, and HLS player controls.

Exit evidence: backend unit/API tests, web unit/build checks, and fixture-driven local use.

## Slice 2 — supervised recording and real chase play

- Poll and claim due jobs transactionally.
- Allocate a tuner immediately before start.
- Supervise FFmpeg, persist PID/heartbeat/output metadata, and capture diagnostics.
- Finalize playlists with `ENDLIST`; recover or finalize interrupted recordings after restart.
- Exercise a long-running synthetic input test while repeatedly seeking from the beginning toward the advancing live edge.

Primary exit criterion: a two-hour active recording can be opened late, played from the start, paused, and repeatedly jumped forward until Go Live reaches the moving edge without playlist or segment errors.

## Slice 3 — rules and operations

- Persistent series rules and team/title matching rules with duplicate suppression.
- Conflict resolution UI, live extension policy, disk thresholds, and richer diagnostics.
- Notifications and guide-refresh scheduling with jitter/backoff.

## Later

- Comskip evaluation against preserved test recordings.
- Optional untouched transport-stream retention.
- Chromecast/Android TV and native Apple TV clients.

