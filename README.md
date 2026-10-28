# Sigismundo

A self-hosted Discord Activity for hosting PC party games (Jackbox and friends) from a server box: it runs the game under Wine on a headless display and streams it, with sound, into a Discord Activity in your voice channel. Whoever opened the Activity first controls the game with mouse and keyboard from the Activity; everyone else joins on their phones as usual.

Bring your own games: launcher-free copies, mounted into the container with a small manifest.

## How it works

1. In a voice channel, open Sigismundo from the Activities shelf (rocket button) and pick a game; whoever opened it first is in control (bottom-left of the menu).
2. The game fills the Activity; the host's mouse and keyboard go straight to it. Players read the room code off the stream and join on jackbox.tv.
3. Quitting the game, Ctrl+Shift+X → Stop game, or 10 minutes without anyone watching ends it and brings back the catalog.

Two parts, two images: `server/` (API, game pipeline) and `activity/` (the Activity frontend, nginx proxying `/api` to the server).

## Run with Docker

1. Create a Discord application, enable Activities, set the root URL mapping to your public HTTPS host. Details: [.knowledge/defined/deploy.md](.knowledge/defined/deploy.md).
2. `cp server/.env.example server/.env` and fill in `CLIENT_ID`, `CLIENT_SECRET`, `OWNER_IDS`.
3. Put your games in `./games` with a `catalog.yaml` (see [server/catalog.example.yaml](server/catalog.example.yaml), format in [.knowledge/defined/catalog.md](.knowledge/defined/catalog.md)).
4. `docker compose up -d --build`, and put a reverse proxy with TLS in front of port 8080 (the activity container).

Try it without Discord (no Discord app needed, anyone reaching it can log in as anyone): `SIGISMUNDO_MODE=local docker compose up --build`, then open `http://localhost:8080/?user=1&name=me`.
