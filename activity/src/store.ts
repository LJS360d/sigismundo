import { signal } from "@preact/signals";
import { sfx } from "./sfx";

export type Entry = { id: string; name: string; description?: string; image?: string };
export type SessionView = { id: string; key: string; name: string; status: string; running: boolean };
export type User = { id: string; name: string; avatar?: string };
export type State = { session: SessionView | null; controller: User | null; me: User; owner: boolean };

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

// Binary frames (video/audio) go to whoever streams them, not through signals.
export let onMedia: ((buf: ArrayBuffer) => void) | undefined;
export const setMedia = (fn?: (buf: ArrayBuffer) => void) => (onMedia = fn);

let ws: WebSocket | undefined;

export function send(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
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

export function connect(token: string) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/api/ws?token=${encodeURIComponent(token)}`);
  ws.binaryType = "arraybuffer";
  ws.onmessage = (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      onMedia?.(ev.data);
      return;
    }
    const msg = JSON.parse(ev.data);
    if (msg.t === "state") {
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
