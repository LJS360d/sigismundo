import { signal } from "@preact/signals";
import { sfx } from "./sfx";

export type Entry = { id: string; name: string; description?: string; image?: string };
export type SessionView = { id: string; key: string; name: string; status: string; running: boolean };
export type User = { id: string; name: string; avatar?: string };
// controller left; control passes on in `in` of `grace` ms
export type Away = { in: number; grace: number };
export type State = {
  session: SessionView | null;
  controller: User | null;
  away: Away | null;
  me: User;
  owner: boolean;
  // token for the relay, when the server publishes to one
  relay?: string;
};

export const catalog = signal<Entry[]>([]);
// undefined until the server's first state message
export const state = signal<State | undefined>(undefined);
export const toast = signal("");
// new controller, shown for a few seconds after control changes hands
export const announce = signal<User | null>(null);
// card the launcher clicked, pulsing until the session shows up
export const launching = signal("");
// game key whose card the stream shrinks back into; it skips the entrance stagger
export let returning = "";
// the stop menu, toggled by MENU_KEY; Esc goes to the game
export const menu = signal(false);
export const MENU_KEY = "Ctrl+Shift+X";
export const isMenuKey = (ev: KeyboardEvent) => ev.ctrlKey && ev.shiftKey && ev.code === "KeyX";
// Discord shrank the Activity to picture in picture: the stream alone, or an
// attract screen between games. ?pip forces it in a plain browser.
export const pip = signal(new URLSearchParams(location.search).has("pip"));
pip.subscribe((v) => v && (menu.value = false));

// Binary frames (video/audio) go to whoever streams them, not through signals.
export let onMedia: ((buf: ArrayBuffer) => void) | undefined;
export const setMedia = (fn?: (buf: ArrayBuffer) => void) => (onMedia = fn);

const KIND_AUDIO = 2;

let ws: WebSocket | undefined;
const proto = location.protocol === "https:" ? "wss" : "ws";

export function send(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

// The relay (relay/, reached through the /relay URL mapping) carries the
// stream when the server publishes to one, so a single copy leaves the server
// however many watch. Until it delivers, and whenever it drops, the server's
// own WebSocket streams instead.
let relay: WebSocket | undefined;
let relayToken = "";
let relayLive = false;
// retries back off from RELAY_RETRY to RELAY_RETRY_MAX, and start over once it delivers
const RELAY_RETRY = 2_000;
const RELAY_RETRY_MAX = 30_000;
let relayRetry = RELAY_RETRY;
// The last audio timestamp (µs) from the relay, acked every ACK_EVERY ms: the
// relay holds video back from a viewer that falls behind. Audio, as it is
// never replayed from a cache and keeps flowing while video is held back.
const ACK_EVERY = 250;
let newest = -1;

// what the stream overlay (STATS_KEY) shows
// lag: how far behind the relay's newest frame this viewer is; pub: how late
// the server's frames reached the relay (its uplink); held: the relay holds
// video back, this viewer having been behind for a while
export type Stats = { source: "relay" | "direct"; lag?: number; pub?: number; held?: boolean };
export const stats = signal<Stats>({ source: "direct" });
export const showStats = signal(false);
export const STATS_KEY = "Ctrl+Shift+L";
export const isStatsKey = (ev: KeyboardEvent) => ev.ctrlKey && ev.shiftKey && ev.code === "KeyL";

function openRelay() {
  if (relay || !relayToken) return;
  const r = (relay = new WebSocket(`${proto}://${location.host}/relay/view?token=${encodeURIComponent(relayToken)}`));
  r.binaryType = "arraybuffer";
  r.onopen = sendVisibility;
  const ack = setInterval(() => {
    if (newest >= 0 && r.readyState === WebSocket.OPEN) r.send(JSON.stringify({ t: "ack", ts: newest }));
  }, ACK_EVERY);
  r.onmessage = (ev) => {
    if (!(ev.data instanceof ArrayBuffer)) {
      const m = JSON.parse(ev.data);
      if (m.t === "lag") stats.value = { source: "relay", lag: m.ms, pub: m.pub, held: m.held };
      return;
    }
    if (!relayLive) {
      relayLive = true;
      relayRetry = RELAY_RETRY;
      stats.value = { source: "relay" };
      send({ t: "relay", d: true });
    }
    unbundle(ev.data);
  };
  r.onclose = () => {
    clearInterval(ack);
    relay = undefined;
    newest = -1;
    if (relayLive) {
      relayLive = false;
      stats.value = { source: "direct" };
      send({ t: "relay", d: false });
    }
    setTimeout(openRelay, relayRetry);
    relayRetry = Math.min(relayRetry * 2, RELAY_RETRY_MAX);
  };
}

// A relay message bundles frames, each prefixed by its length as u32 BE (see
// player.ts for the frame format).
function unbundle(buf: ArrayBuffer) {
  const view = new DataView(buf);
  for (let off = 0; off + 4 <= buf.byteLength; ) {
    const n = view.getUint32(off);
    if (n >= 10 && view.getUint8(off + 4) === KIND_AUDIO) newest = Number(view.getBigUint64(off + 6));
    onMedia?.(buf.slice(off + 4, off + 4 + n));
    off += 4 + n;
  }
}

const isController = () => !!state.value && state.value.controller?.id === state.value.me.id;
// input reaches the game
export const controlling = () => isController() && !!state.value?.session;
export const canLaunch = () => isController() || !!state.value?.owner;

export function flash(text: string) {
  toast.value = text;
  setTimeout(() => {
    if (toast.value === text) toast.value = "";
  }, 4000);
}

let announceTimer = 0;

function announceControl(prev: State | undefined, next: State) {
  if (!prev || !next.controller || prev.controller?.id === next.controller.id) return;
  announce.value = next.controller;
  sfx.chime();
  clearTimeout(announceTimer);
  announceTimer = setTimeout(() => (announce.value = null), 3500);
}

// While the Activity is off screen it gets no video, saving the upload.
function sendVisibility() {
  const msg = JSON.stringify({ t: "vis", d: !document.hidden });
  for (const s of [ws, relay]) if (s?.readyState === WebSocket.OPEN) s.send(msg);
}
document.addEventListener("visibilitychange", sendVisibility);

export function connect(token: string) {
  ws = new WebSocket(`${proto}://${location.host}/api/ws?token=${encodeURIComponent(token)}`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => {
    sendVisibility();
    if (relayLive) send({ t: "relay", d: true });
  };
  ws.onmessage = (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      if (!relayLive) onMedia?.(ev.data);
      return;
    }
    const msg = JSON.parse(ev.data);
    if (msg.t === "state") {
      if (msg.relay) {
        relayToken = msg.relay;
        openRelay();
      }
      announceControl(state.value, msg);
      const prev = state.value?.session;
      if (msg.session?.id === prev?.id) {
        state.value = msg;
        return;
      }
      menu.value = false;
      launching.value = "";
      returning = prev?.key ?? "";
      // first state (joining mid-session) or no API support: no animation
      if (!state.value || !document.startViewTransition) {
        state.value = msg;
        return;
      }
      msg.session ? sfx.launch() : sfx.exit();
      // Preact renders signal changes on a microtask, done before the timeout
      document.startViewTransition(() => {
        state.value = msg;
        return new Promise((r) => setTimeout(r));
      });
    } else if (msg.t === "error") {
      launching.value = "";
      flash(msg.message);
    }
  };
  ws.onclose = () => {
    flash("Disconnected, reconnecting…");
    setTimeout(() => connect(token), 2000);
  };
}
