// Fans the stream out to the Activity's viewers, so the game server's uplink
// carries one copy however many watch. The server publishes on /publish (see
// server/internal/relay for the bundle format); viewers read /view, reached
// through a Discord URL mapping, with a token from the server's state.
import { DurableObject } from "cloudflare:workers";

interface Env {
  ROOM: DurableObjectNamespace<Room>;
  RELAY_SECRET: string;
}

const KIND_VIDEO = 1;
const FLAG_KEY = 1;
const enc = new TextEncoder();

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    if (req.headers.get("Upgrade") !== "websocket") return new Response("sigismundo relay\n", { status: 426 });
    // the URL mapping may or may not strip its /relay prefix
    const { pathname, searchParams } = new URL(req.url);
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
    // One room, as the server runs one session at a time. It is placed near
    // whoever reaches it first, normally the publisher.
    const room = env.ROOM.get(env.ROOM.idFromName("main"));
    return room.fetch(new Request(`https://room/${role}`, req));
  },
};

type Viewer = { needKey: boolean; paused: boolean };

export class Room extends DurableObject<Env> {
  private pub?: WebSocket;
  private viewers = new Map<WebSocket, Viewer>();
  // the last keyframe as a one-frame bundle: a picture for whoever joins
  private key?: ArrayBuffer;

  async fetch(req: Request): Promise<Response> {
    const { 0: client, 1: ws } = new WebSocketPair();
    ws.accept();
    if (new URL(req.url).pathname === "/publish") this.publish(ws);
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
    const v: Viewer = { needKey: true, paused: false };
    this.viewers.set(ws, v);
    this.sendKey(ws, v);
    // {"t":"vis","d":false} while the Activity is off screen: no video then
    ws.addEventListener("message", (ev) => {
      const m = typeof ev.data === "string" ? parse(ev.data) : undefined;
      if (m?.t !== "vis") return;
      const paused = !m.d;
      if (v.paused && !paused) this.sendKey(ws, v);
      v.paused = paused;
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
    for (const [ws, v] of this.viewers) {
      if (video && (v.paused || (v.needKey && !key))) {
        if (audio.byteLength > 0) this.send(ws, audio);
        continue;
      }
      if (key) v.needKey = false;
      this.send(ws, b);
    }
  }

  // Deltas are useless without the frames they reference: after the cached
  // keyframe, video resumes at the next one.
  private sendKey(ws: WebSocket, v: Viewer) {
    v.needKey = true;
    if (this.key) this.send(ws, this.key);
  }

  private reset() {
    this.key = undefined;
    for (const v of this.viewers.values()) v.needKey = true;
  }

  private send(ws: WebSocket, b: ArrayBuffer) {
    try {
      ws.send(b);
    } catch {
      this.viewers.delete(ws);
    }
  }
}

function parse(s: string): { t?: string; d?: boolean } | undefined {
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
