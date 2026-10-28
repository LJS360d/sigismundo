# Sigismundo

A self-hosted Discord Activity for hosting PC party games (Jackbox and friends) from a server box: it runs the game under Wine on a headless display and streams it, with sound, into a Discord Activity in your voice channel. Whoever opened the Activity first controls the game with mouse and keyboard from the Activity; everyone else joins on their phones as usual.

Bring your own games: launcher-free copies, mounted into the container with a small manifest.

## How it works

1. In a voice channel, open Sigismundo from the Activities shelf (rocket button) and pick a game; whoever opened it first is in control (bottom-left of the menu).
2. The game fills the Activity; the host's mouse and keyboard go straight to it. Players read the room code off the stream and join on jackbox.tv.
3. Quitting the game, Ctrl+Shift+X → Stop game, or 10 minutes without anyone watching ends it and brings back the catalog.

Two parts, two images: `server/` (API, game pipeline) and `activity/` (the Activity frontend, nginx proxying `/api` to the server).

## Run with Docker

1. Create a Discord application. From OAuth2, note the client id and secret and add any redirect (e.g. `https://127.0.0.1`, unused but required). Enable Activities and map the root `/` to your public HTTPS host (no scheme). Install the app so it shows up in voice channels' Activities shelf.
2. `cp server/.env.example server/.env` and fill in `CLIENT_ID`, `CLIENT_SECRET`, `OWNER_IDS`.
3. Put your games in `./games` with a `catalog.yaml`, see [server/catalog.example.yaml](server/catalog.example.yaml).
4. `docker compose up -d --build`, and put a reverse proxy with TLS in front of port 8080 (the activity container).
5. Optional but recommended: give it the GPU. Uncomment `devices`/`group_add` in the compose file and set `RENDER_GID` to `stat -c %g /dev/dri/renderD128`. Games then render on the GPU and the stream is encoded there too (VA-API on Intel/AMD, NVENC on NVIDIA); without one, everything runs on the CPU, which can take most of a small box. Knobs: `GPU_DEVICE`, `GPU_RENDER`, `VIDEO_ENCODER` in [server/.env.example](server/.env.example).

Try it without Discord (no Discord app needed, anyone reaching it can log in as anyone): `SIGISMUNDO_MODE=local docker compose up --build`, then open `http://localhost:8080/?user=1&name=me`.
