# Deploy

## Images

- `server/Dockerfile`: Go stage builds a static binary (`CGO_ENABLED=0`, everything is pure Go), runtime is `debian:13-slim` + `wine` (64 and 32-bit), `xvfb`, `pulseaudio`, `ffmpeg`, `fonts-liberation`. Runs as uid 1000, `AUDIO_MODE=spawn`, API on 8080 (not published).
- `activity/Dockerfile`: pnpm build, then `nginx:alpine` serving `dist/` and proxying `/api` (WebSocket included) to `API_URL` (runtime env, default `http://sigismundo:8080`). Port 80.
- Release: tag `vX.Y.Z` → `.github/workflows/release.yml` pushes `ghcr.io/<owner>/sigismundo:{X.Y.Z,latest}` and `.../sigismundo/activity`. The images never contain games, so they can be public.

Server volumes and runtime:

- `/data`: `wine/` (the prefix, created on the first session, so game saves and in-game settings persist). `chown 1000:1000` it.
- `/games` (read-only): executables + `catalog.yaml`.
- `shm_size: 1g`; Wine and Xvfb need more than Docker's 64MB.

`docker-compose.yml` builds both from source (activity on host port 8080, `./games`, `./server/data`, `server/.env`). `docker-compose.prod.yml` uses the GHCR images. Expose the activity over public HTTPS through your own reverse proxy (must pass WebSocket upgrades).

## Discord application

1. OAuth2: client id → `CLIENT_ID`, client secret → `CLIENT_SECRET`. No bot user or token needed. Add any redirect (e.g. `https://127.0.0.1`); the Activity flow doesn't use it but the portal requires one.
2. Activities: enable them. URL Mappings: root `/` → the activity's public host (no scheme), e.g. `games.example.com`.
3. Installation: guild install with scope `applications.commands` only (no `bot` scope, no permissions), or user install. Enabling Activities creates the default `Launch` entry point command, so the app shows in every VC's Activities shelf.

## Development

Docker only: `docker compose up --build`, or `SIGISMUNDO_MODE=local docker compose up --build` for dev auth at `http://localhost:8080/?user=1&name=me`. For the real Discord Activity, tunnel port 8080 (e.g. `cloudflared tunnel --url http://localhost:8080`) and point the URL mapping at it.

