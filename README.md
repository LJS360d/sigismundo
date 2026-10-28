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

## Run locally

Wine and ffmpeg alone aren't enough: the server also starts a virtual X display and captures sound from it. What it runs per session: `Xvfb`, `wine`/`wineboot`/`wineserver`, `ffmpeg` (with `x11grab`, `pulse`, `libx264`, `libopus`), plus either `pulseaudio` or `pactl`.

### 1. Packages

Arch / CachyOS:

```sh
sudo pacman -S --needed wine xorg-server-xvfb ffmpeg libpulse   # libpulse provides pactl
```

Debian / Ubuntu:

```sh
sudo dpkg --add-architecture i386 && sudo apt update
sudo apt install wine wine64 wine32:i386 xvfb ffmpeg pulseaudio-utils
```

Toolchains: Go, Node and pnpm, versions pinned in `server/mise.toml` and `activity/mise.toml` (`mise install` in each dir).

**Audio:** on a desktop with PipeWire or PulseAudio already running, nothing else is needed. The server adds its own null sink to your sound server with `pactl` for the session and removes it afterwards, so the game is captured but never plays on your speakers (`AUDIO_MODE=host`, the default when no `pulseaudio` binary is installed). On a headless box with `pulseaudio` installed, it spawns a private one instead (`AUDIO_MODE=spawn`).

### 2. Config and games

```sh
cp server/.env.example server/.env
cp activity/.env.example activity/.env
mkdir -p games && cp server/catalog.example.yaml games/catalog.yaml
```

In `server/.env` set at least:

```sh
GO_ENV=development
GAMES_DIR=../games      # relative to server/
DATA_DIR=data
```

The example catalog has a Notepad entry pointing at Wine's own `notepad.exe` (Debian and Arch paths; the missing one is skipped) to test the pipeline without any game.

The Wine prefix goes in `server/data/wine`, separate from your `~/.wine`; the first session creates it (takes a minute). The game runs on display `:99` (`DISPLAY_NUM`), invisible on your desktop.

### 3. Start

Without Discord (dev auth, any user id):

```sh
cd server && make local             # API on 127.0.0.1:8080
cd activity && pnpm install && pnpm dev
```

Open `http://localhost:5173/?user=1&name=me`, pick a game. A second tab with `?user=2&name=friend` acts as another viewer (view-only).

With Discord: fill `CLIENT_ID` and `CLIENT_SECRET` in `server/.env`, run `make dev` instead of `make local`, expose the dev server over HTTPS (`cloudflared tunnel --url http://localhost:5173`) and set that host as the Activity's root URL mapping.
