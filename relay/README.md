# Sigismundo relay

A Cloudflare Worker that fans the game stream out to the Activity's viewers. Without it, the server sends every viewer its own copy of the stream, so its upload is the bitrate times the number of viewers. With it, the server uploads one copy to Cloudflare and Cloudflare sends it to everyone. Input and state still go straight to the server.

```
                         one copy                    one copy per viewer
game server ── wss /publish ──► Worker + Durable Object ──► /relay/view ──► Activity viewers
     ▲                                                                        │
     └──────────────── /api/ws: input, state (and the stream, as fallback) ───┘
```

It runs on the **Workers Free plan**, which has no billing: nothing is ever charged. See [Limits](#limits) for what that plan allows.

## 1. Deploy the Worker

You need a Cloudflare account (free, no card) and Node with pnpm (versions in `mise.toml`).

```sh
cd relay
pnpm install
pnpm wrangler login                # opens the browser to authorize wrangler
openssl rand -hex 32               # generate the shared secret, keep it for step 2
pnpm wrangler secret put RELAY_SECRET   # paste the secret when asked
pnpm run deploy
```

The deploy prints the Worker's URL, `https://sigismundo-relay.<your-subdomain>.workers.dev`. The first deploy asks you to pick a `workers.dev` subdomain if your account has none yet.

Check it is up. A plain request answers `426` with `sigismundo relay`, since it only speaks WebSocket:

```sh
curl -i https://sigismundo-relay.<your-subdomain>.workers.dev
```

To change the secret later, run `pnpm wrangler secret put RELAY_SECRET` again and update the server to match. To update the code, `pnpm run deploy`.

## 2. Configure the server

Add to the server's env file (`.env` next to `docker-compose.prod.yml`, or `server/.env` with `docker-compose.yml`):

```sh
RELAY_URL=wss://sigismundo-relay.<your-subdomain>.workers.dev
RELAY_SECRET=<the same secret as step 1>
```

Both must be set, or the server streams directly and logs `RELAY_URL set without RELAY_SECRET`. The server connects out to the Worker, so nothing new needs to be opened on your network.

Restart it (`docker compose up -d`). The log should show:

```
relay: publishing to wss://sigismundo-relay.<your-subdomain>.workers.dev
```

If it shows `relay: <error>, retrying in …` instead, the URL or the secret is wrong (a `bad handshake` is usually the secret).

Also update the activity image: it carries the client side of the relay, and nginx's smaller send buffer.

## 3. Configure the Discord Activity

In the [Developer Portal](https://discord.com/developers/applications), open your application → **Activities** → **URL Mappings**, and add a mapping next to the root one:

| Prefix   | Target                                          |
| -------- | ----------------------------------------------- |
| `/`      | your server's public host (already there)       |
| `/relay` | `sigismundo-relay.<your-subdomain>.workers.dev` |

Targets are hosts without `https://`. Discord matches the most specific prefix, so `/relay` doesn't disturb `/`.

## 4. Check it works

Open the Activity with a game running. In the browser devtools of the Activity (Discord desktop: Ctrl+Shift+I, if enabled), the Network tab shows a WebSocket to `/relay/view` receiving binary messages. On the server, the upload stays about the same as more viewers join.

Viewers fall back to the server's own stream, and retry the relay every 30 seconds, whenever the relay:

- isn't mapped or deployed,
- has no publisher (the server is down or can't reach it),
- is over the free plan's daily limit.

So a broken relay costs upload, never the stream.

## Limits

The Workers Free plan allows, per day:

- **100,000 requests.** Incoming WebSocket messages count 20 to 1, and the server sends one message per video frame (audio is bundled with it): about 23 hours of streaming a day at `STREAM_FPS=24`, 18 at 30. Messages sent to viewers are free, so the number of viewers doesn't matter. Past the limit, requests fail until 00:00 UTC and viewers fall back as above.
- **13,000 GB-s of Durable Object time.** The server keeps one connection open all day, which uses about 10,800.

Usage is on the Cloudflare dashboard under Workers & Pages → `sigismundo-relay` → Metrics.

Other limits:

- The Worker can't tell how far behind each viewer is, so it can't drop frames per viewer the way the server does. A viewer on a connection too slow for the stream falls behind until their decoder skips ahead.
- Cloudflare's terms restrict serving mostly video on the free plan without one of its video products. Whether that covers a Worker relaying WebSocket messages isn't spelled out.

## Run it locally

```sh
cd relay
printf 'RELAY_SECRET=devsecret\n' > .dev.vars
pnpm dev                              # http://localhost:8787
```

Then point the server at it with `RELAY_URL=ws://localhost:8787` and `RELAY_SECRET=devsecret`. In a plain browser (local mode) there is no `/relay` mapping, so viewers keep using the direct stream. To see the relay in use, test inside Discord with a tunnel to the Worker, or run a script against `/view` with a token from the server's `state` message.

## How it works

- `/publish` (bearer `RELAY_SECRET`): the server's single connection. Each binary message is a bundle of frames, each prefixed by its length as u32 BE: the audio packets since the last video frame, then that frame. `{"t":"reset"}` marks a new stream. See `server/internal/relay`.
- `/view?token=…`: a viewer. The token comes from the server's `state` message, signed with `RELAY_SECRET` and valid for 12 hours. A new viewer gets the last keyframe at once, and video from the next keyframe on. `{"t":"vis","d":false}` (the Activity is off screen) pauses its video; audio keeps coming.
- One Durable Object holds every connection, since the server runs one session at a time. It is placed near whoever reaches it first, normally the server.
