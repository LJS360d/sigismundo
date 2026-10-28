# Architecture

## Process

Two images: `server/` (one binary: API + game pipeline; no bot user, no gateway connection) and `activity/` (static frontend served by nginx, which proxies `/api` and the `/api/ws` WebSocket to the server). One game session at a time (global, not per guild).

- `server/cmd/sigismundo/main.go` – wiring; `sigismundo local` skips the `CLIENT_ID`/`CLIENT_SECRET` check and turns dev auth on.
- `server/internal/config` – env, including stream settings (`STREAM_*`, `IDLE_TIMEOUT`); changes need a restart.
- `server/internal/catalog` – `catalog.yaml` loader, entries whose exe is missing are dropped at start (see `catalog.md`).
- `server/internal/session` – the `Manager`: controller, viewers, current session and its lifecycle.
- `server/internal/runner` – the processes of one session.
- `server/internal/stream` – H.264 access-unit splitter, Ogg Opus demuxer, fan-out `Hub`.
- `server/internal/input` – XTEST mouse/keyboard injection (pure Go, `jezek/xgb`).
- `server/internal/httpapi` – `/api/config` (client id), `/api/catalog`, `/api/catalog/{id}/image`, `/api/token`, `/api/ws`.
- `activity/` – the Activity frontend. nginx sends `frame-ancestors` for Discord's domains instead of `X-Frame-Options`, and gives `/api/ws` 1h timeouts with buffering off.

No voice connection: nothing joins the VC. Audio and video both travel in the Activity stream.

## Discord surface

- Only the Activity: launched from a VC's Activities shelf via the default `Launch` entry point command (Discord-handled, created by the portal). With no session it shows the catalog; only the controller (or an owner) launches.

## Session lifecycle

- `Start` fails with `ErrBusy` while any session exists. The pipeline comes up in the background; `Status` strings reach viewers.
- Activity UI (Preact + signals, Tailwind v4 via `@tailwindcss/vite`, theme tokens and `.btn` in `src/style.css`: `src/store.ts` holds state and the WebSocket, `src/components/` the views): boot splash (`Splash.tsx`: animated wordmark, orbit loader, real boot steps ticking off, retry on error; held at least 1.6s, then a View Transition crossfade into the app); catalog (glassy app cards that launch on click, disabled for non-controllers; bottom-left pill naming the controller, with Seize control for owners; over an aurora backdrop that crossfades to the focused game's blurred cover; cards stagger in) with no session, otherwise only the fullscreen stream (loading screen with cover, status, shimmer and shortcut tip until running). Session start/end runs a View Transition for every viewer (`store.ts`): the card zooms into the stage and back, keyed by `view-transition-name` = game key; skipped on the first state (joining mid-session) and when unsupported. UI sounds are synthesized in `src/sfx.ts` (no assets). Keyboard goes to the game window-wide for the controller; first click/key unlocks audio. Ctrl+Shift+X (`MENU_KEY` in `src/store.ts`) is never forwarded: it toggles a Back/Stop dialog (Esc also closes it; Stop only for controller/owners); Esc otherwise reaches the game. A controller change shows a self-fading announcement (`components/Control.tsx`) to everyone.
- Ends on: menu Stop (controller or owner); game exit (runner); pipeline failure; no viewers for `idle_timeout` (counted from the later of session start and last viewer leaving).
- Control: global, independent of sessions. The first viewer of an empty Activity takes it; when everyone leaves it resets. The controller being absent for 1 minute passes it to the longest-connected viewer. Owners (`OWNER_IDS`) can `seize` it anytime and may always launch/stop. Held keys/buttons are released on every change.
- Viewers from any VC/guild may watch (view-only unless handed control).

## Runner

Per session, fresh: `Xvfb :<DISPLAY_NUM>` (default 99) at the configured size → audio → `wineboot --init` if the prefix has no `system.reg` → `wine explorer /desktop=sigismundo,WxH <exe> <args>` with cwd = exe dir (the virtual desktop pins the game to the capture size; Xvfb can't mode-switch) → two ffmpeg:

- video: `x11grab` → libx264 ultrafast/zerolatency, baseline, no B-frames, GOP = 2s, `repeat-headers=1` (SPS/PPS on every IDR, for late joiners), `h264_metadata=aud=insert` (AUD before each frame = split points), Annex B on stdout.
- audio: `pulse` `game.monitor` → libopus 20ms lowdelay, Ogg with `page_duration` 20ms so packets aren't batched.

Audio (`AUDIO_MODE`): `spawn` (the image's default) runs a private `pulseaudio` with null sink `game`, socket in a temp `XDG_RUNTIME_DIR`. `host` (default when no `pulseaudio` binary, i.e. PipeWire desktops) adds a null sink `sigismundo_<pid>` to the running server with `pactl load-module` and unloads it on stop, so the game never plays on the host's speakers. Wine gets the sink through `PULSE_SINK`; ffmpeg reads `<sink>.monitor`.

Paths from env (`DATA_DIR`, `GAMES_DIR`, `CATALOG_PATH`, `WINEPREFIX`) are made absolute at start, since Wine runs from the exe's directory.

Every process gets its own process group; stop = `wineserver -k`, then SIGTERM (SIGKILL after 3s) in reverse start order. Game exit is detected by polling `/proc` for a process whose argv0 basename is the exe name (launchers hand off to children, so the `wine` process itself isn't waited on); not seen within 90s = failure.

## Stream and Activity protocol

- WebSocket `/api/ws?token=…`. Connecting attaches the viewer; server sends `state {me, owner, controller, session}` on every change. Client: `start {key}`, `stop`, `seize`, input `m {x,y}` (0–1 of the frame), `b {b,d}` (X buttons 1–5, 4/5 wheel), `k {code,d}` (`KeyboardEvent.code`, mapped to keysyms in `input`). Input from anyone but the controller is dropped silently.
- Server text messages: `{t:"state", me, session, viewers, role}` on every change, `{t:"error", message}`.
- Binary messages: `kind u8 (1 video, 2 audio) | flags u8 (1 key) | ts u64 BE µs | payload`. Timestamps are arrival time at the server on one clock for both streams. Video payload is one Annex B access unit.
- Hub: each subscriber has a buffered channel; a full channel drops the frame, and for video then waits for the next keyframe. New subscribers get the current GOP replayed first.
- Client: `VideoDecoder` (codec string from the SPS, no description = Annex B). Audio via `AudioDecoder` when it supports Opus, else WASM `opus-decoder`. Audio buffers play back to back ~120ms ahead; each one maps server time to `AudioContext` time, and video frames are shown when due on that clock (immediately while audio is blocked by autoplay).
- Auth: Embedded App SDK `authorize` → `POST /api/token {code}` (server exchanges with `CLIENT_SECRET`, reads `/users/@me`) → returns the access token for `authenticate` plus an HMAC token (`SESSION_SECRET`, 12h) used on the WebSocket. Outside Discord (no `frame_id` query) the client uses `/api/dev-token`, served only with `DEV_AUTH` in development or in `local` mode.
