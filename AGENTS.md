# Sigismundo

Discord Activity that hosts PC party games (Jackbox first): it runs the game under Wine on a headless X display and streams it into a Discord Activity, where the host also controls it. Monorepo: Go server (API + game pipeline) in `server/`, the Activity frontend (Vite + TS + Preact with signals) in `activity/`; each is its own image with its own `Dockerfile` and `mise.toml`. Root holds docs, license and the example `docker-compose.yml` (dev, builds source) and `docker-compose.prod.yml` (GHCR images).

## Rules

- Go: from `server/`, build with `make build`, test with `make test`. Config comes only from process env (no `.env` loading in Go; `make run`/`dev`/`local` source `server/.env`). Activity uses pnpm: `cd activity && pnpm dev` (proxies `/api`, WebSocket included, to `API_URL`, default `:8080`; config in `activity/.env`, see `activity/.env.example`), `pnpm build` type-checks.
- Without Discord: `make local` runs the API with dev auth; open the Activity dev server at `/?user=1&name=me`. Running locally: README "Run locally".
- Game binaries are never part of the repo or the images; they are mounted at `GAMES_DIR` with a hand-written `catalog.yaml`.
- Before changing an area, read its doc in `.knowledge/defined/` (index: `.knowledge/defined/README.md`). Update it in the same change; fix wrong lines on sight.
- No comments where the code is clear.

## Pointers

- Architecture: `.knowledge/defined/architecture.md`
- Catalog format: `.knowledge/defined/catalog.md`
- Env vars: `server/.env.example`, `activity/.env.example`
- Images, Discord app setup, deploy: `.knowledge/defined/deploy.md`
- Original design decisions: `.knowledge/raw/`
