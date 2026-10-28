import { signal } from "@preact/signals";
import type { Auth } from "./auth";

export type Entry = { id: string; name: string; description?: string; image?: string };
export type SessionView = { id: string; status: string; running: boolean };
export type State = { session: SessionView | null; role: "" | "controller" | "host" | "viewer" };

export const catalog = signal<Entry[]>([]);
// undefined until the server's first state message
export const state = signal<State | undefined>(undefined);
export const toast = signal("");
// the Esc stop menu
export const menu = signal(false);

// Binary frames (video/audio) go to whoever streams them, not through signals.
export let onMedia: ((buf: ArrayBuffer) => void) | undefined;
export const setMedia = (fn?: (buf: ArrayBuffer) => void) => (onMedia = fn);

let ws: WebSocket | undefined;

export function send(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

export const controlling = () => state.value?.role === "controller" && !!state.value.session;

export function flash(text: string) {
  toast.value = text;
  setTimeout(() => {
    if (toast.value === text) toast.value = "";
  }, 4000);
}

export function connect(auth: Auth) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/api/ws?token=${encodeURIComponent(auth.token)}`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => send({ t: "hello", guild_id: auth.guildId, channel_id: auth.channelId });
  ws.onmessage = (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      onMedia?.(ev.data);
      return;
    }
    const msg = JSON.parse(ev.data);
    if (msg.t === "state") {
      if (msg.session?.id !== state.value?.session?.id) menu.value = false;
      state.value = msg;
    } else if (msg.t === "error") flash(msg.message);
  };
  ws.onclose = () => {
    flash("Disconnected, reconnecting…");
    setTimeout(() => connect(auth), 2000);
  };
}
