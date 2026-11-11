// Fans the stream out to the Activity's viewers, so the game server's uplink
// carries one copy however many watch. The server publishes on /publish (see
// server/internal/relay for the bundle format); viewers read /view, reached
// through a Discord URL mapping, with a token from the server's state.
import { DurableObject } from "cloudflare:workers";

interface Env {
  ROOM: DurableObjectNamespace<Room>;
  RELAY_SECRET: string;
  // region to place the room in, near the server (see wrangler.toml)
  LOCATION?: DurableObjectLocationHint;
}

const KIND_VIDEO = 1;
const FLAG_KEY = 1;
// A viewer this far (µs of stream time) behind the newest frame for SUSTAIN
// acks in a row (2s) gets no video until it catches up, then resumes at a
// keyframe: a link too slow for the stream costs frames instead of piling up
// delay that never goes away. A single spike is left alone: after a stall the
// backlog is already on its way, and cutting video would only add a wait for
// the next keyframe.
const MAX_LAG = 300_000;
const SUSTAIN = 8;
const enc = new TextEncoder();

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    // the URL mapping may or may not strip its /relay prefix
    const { pathname, searchParams } = new URL(req.url);
    if (req.headers.get("Upgrade") !== "websocket") {
      // where the room runs, to check it sits near the server
      if (pathname.endsWith("/where")) return room(env).fetch("https://room/where");
      return new Response("sigismundo relay\n", { status: 426 });
    }
    let role: string;
    if (pathname.endsWith("/publish")) {
      if (!equal(req.headers.get("Authorization") ?? "", `Bearer ${env.RELAY_SECRET}`)) return unauthorized();
      role = "publish";
    } else if (pathname.endsWith("/view")) {
      if (!(await verifyToken(searchParams.get("token") ?? "", env.RELAY_SECRET))) return unauthorized();
      role = "view";
    } else {
      return new Response("not found", { status: 404 });
    }
    return room(env).fetch(new Request(`https://room/${role}`, req));
  },
};

// One room, as the server runs one session at a time. A Durable Object stays
// where it was first created, so the name carries the region: changing
// LOCATION makes a new room there.
function room(env: Env) {
  const loc = env.LOCATION;
  return env.ROOM.get(env.ROOM.idFromName(loc ? `main-${loc}` : "main"), loc ? { locationHint: loc } : undefined);
}

// over: acks in a row past MAX_LAG; behind: video held back for it
type Viewer = { needKey: boolean; paused: boolean; behind: boolean; over: number };

export class Room extends DurableObject<Env> {
  private pub?: WebSocket;
  private viewers = new Map<WebSocket, Viewer>();
  // the last keyframe as a one-frame bundle: a picture for whoever joins
  private key?: ArrayBuffer;
  // timestamp of the newest frame published
  private newest = 0;
  // How late the publisher's frames arrive, to tell its uplink's stalls from
  // the viewers' links: arrival minus timestamp (ms), relative to its minimum
  // over the last 10-20s. pubPeak is the worst since the last report.
  private pubMin = [Infinity, Infinity];
  private pubMinAt = 0;
  private pubPeak = 0;
  private pubPeakAt = 0;

  async fetch(req: Request): Promise<Response> {
    const path = new URL(req.url).pathname;
    if (path === "/where") {
      const trace = await (await fetch("https://cloudflare.com/cdn-cgi/trace")).text();
      return new Response(`room colo: ${/^colo=(.*)$/m.exec(trace)?.[1]}\nviewers: ${this.viewers.size}\npublisher: ${!!this.pub}\n`);
    }
    const { 0: client, 1: ws } = new WebSocketPair();
    ws.accept();
    if (path === "/publish") this.publish(ws);
    else this.view(ws);
    return new Response(null, { status: 101, webSocket: client });
  }

  private publish(ws: WebSocket) {
    this.pub?.close(1000, "replaced");
    this.pub = ws;
    this.reset();
    ws.addEventListener("message", (ev) => {
      if (typeof ev.data !== "string") this.fanOut(ev.data as ArrayBuffer);
      else if (parse(ev.data)?.t === "reset") this.reset();
    });
    const gone = () => {
      if (this.pub !== ws) return;
      this.pub = undefined;
      this.key = undefined;
      // viewers go back to the server's own stream until it publishes again
      for (const v of this.viewers.keys()) v.close(1012, "publisher gone");
      this.viewers.clear();
    };
    ws.addEventListener("close", gone);
    ws.addEventListener("error", gone);
  }

  private view(ws: WebSocket) {
    if (!this.pub) {
      ws.close(1013, "no stream");
      return;
    }
    const v: Viewer = { needKey: true, paused: false, behind: false, over: 0 };
    this.viewers.set(ws, v);
    this.sendKey(ws, v);
    ws.addEventListener("message", (ev) => {
      const m = typeof ev.data === "string" ? parse(ev.data) : undefined;
      if (m?.t === "vis") {
        // {"t":"vis","d":false} while the Activity is off screen: no video then
        const paused = !m.d;
        if (v.paused && !paused) this.sendKey(ws, v);
        v.paused = paused;
      } else if (m?.t === "ack" && typeof m.ts === "number") {
        // {"t":"ack","ts":µs}: the newest frame the viewer has. Audio keeps
        // flowing while it is behind, so acks keep coming and tell when the
        // backlog has drained.
        const lag = Math.max(0, this.newest - m.ts);
        v.over = lag > MAX_LAG ? v.over + 1 : 0;
        if (v.over >= SUSTAIN && !v.behind) {
          v.behind = true;
          v.needKey = true;
        } else if (v.over === 0) {
          v.behind = false;
        }
        this.send(ws, JSON.stringify({ t: "lag", ms: Math.round(lag / 1000), pub: this.pubPeak, held: v.behind }));
      }
    });
    const gone = () => this.viewers.delete(ws);
    ws.addEventListener("close", gone);
    ws.addEventListener("error", gone);
  }

  // A bundle holds audio packets, then at most one video frame, last. A viewer
  // waiting for a keyframe (or paused) gets the audio part alone.
  private fanOut(b: ArrayBuffer) {
    const view = new DataView(b);
    let last = -1;
    for (let off = 0; off + 4 <= b.byteLength; off += 4 + view.getUint32(off)) last = off;
    if (last < 0 || last + 6 > b.byteLength) return;
    const video = view.getUint8(last + 4) === KIND_VIDEO;
    const key = video && (view.getUint8(last + 5) & FLAG_KEY) !== 0;
    const audio = video ? b.slice(0, last) : b;
    if (key) this.key = b.slice(last);
    if (last + 14 <= b.byteLength) {
      this.newest = Number(view.getBigUint64(last + 6));
      this.notePub(this.newest);
    }
    for (const [ws, v] of this.viewers) {
      if (video && (v.paused || v.behind || (v.needKey && !key))) {
        if (audio.byteLength > 0) this.send(ws, audio);
        continue;
      }
      if (key) v.needKey = false;
      this.send(ws, b);
    }
  }

  private notePub(ts: number) {
    const now = Date.now();
    const d = now - ts / 1000;
    if (now - this.pubMinAt > 10_000) {
      this.pubMin = [this.pubMin[1], Infinity];
      this.pubMinAt = now;
    }
    this.pubMin[1] = Math.min(this.pubMin[1], d);
    const late = Math.round(d - Math.min(...this.pubMin));
    // the peak covers the last second
    if (now - this.pubPeakAt > 1000) {
      this.pubPeak = 0;
      this.pubPeakAt = now;
    }
    this.pubPeak = Math.max(this.pubPeak, late);
  }

  // Deltas are useless without the frames they reference: after the cached
  // keyframe, video resumes at the next one.
  private sendKey(ws: WebSocket, v: Viewer) {
    v.needKey = true;
    if (this.key) this.send(ws, this.key);
  }

  private reset() {
    this.key = undefined;
    this.newest = 0;
    this.pubMin = [Infinity, Infinity];
    for (const v of this.viewers.values()) {
      v.needKey = true;
      v.behind = false;
      v.over = 0;
    }
  }

  private send(ws: WebSocket, b: ArrayBuffer | string) {
    try {
      ws.send(b);
    } catch {
      this.viewers.delete(ws);
    }
  }
}

function parse(s: string): { t?: string; d?: boolean; ts?: number } | undefined {
  try {
    return JSON.parse(s);
  } catch {
    return undefined;
  }
}

function unauthorized() {
  return new Response("unauthorized", { status: 401 });
}

function equal(a: string, b: string) {
  const x = enc.encode(a);
  const y = enc.encode(b);
  return x.byteLength === y.byteLength && crypto.subtle.timingSafeEqual(x, y);
}

// verifyToken checks a token from the server's relayToken: base64url JSON
// {exp} "." base64url HMAC-SHA256 of that, keyed with RELAY_SECRET.
async function verifyToken(tok: string, secret: string) {
  const [body, sig] = tok.split(".");
  if (!body || !sig) return false;
  try {
    const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, [
      "verify",
    ]);
    if (!(await crypto.subtle.verify("HMAC", key, b64url(sig), enc.encode(body)))) return false;
    const { exp } = JSON.parse(new TextDecoder().decode(b64url(body)));
    return typeof exp === "number" && Date.now() / 1000 <= exp;
  } catch {
    return false;
  }
}

function b64url(s: string) {
  return Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));
}
