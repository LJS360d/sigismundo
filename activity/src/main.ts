import "./style.css";
import { authenticate, type Auth } from "./auth";
import { Player } from "./player";

type Game = { id: string; name: string };
type Entry = { id: string; name: string; games?: Game[] };
type SessionView = { id: string; status: string; running: boolean };
type State = { session: SessionView | null; role: "" | "controller" | "host" | "viewer" };

const app = document.getElementById("app")!;
let auth: Auth;
let ws: WebSocket | undefined;
let state: State | undefined;
let catalog: Entry[] = [];
let player: Player | undefined;
let sessionId = "";
let toast = "";
let menu = false;

function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  props: Partial<HTMLElementTagNameMap[K]> & { class?: string } = {},
  ...children: (Node | string | false | undefined)[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  const { class: cls, ...rest } = props;
  if (cls) el.className = cls;
  Object.assign(el, rest);
  for (const c of children) if (c) el.append(c);
  return el;
}

function send(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

async function main() {
  app.append(h("div", { class: "center" }, "Connecting…"));
  try {
    auth = await authenticate();
    catalog = await (await fetch("/api/catalog")).json();
  } catch (e) {
    const msg = e instanceof Error ? e.message : JSON.stringify(e);
    console.error(e);
    app.replaceChildren(h("div", { class: "center error" }, msg));
    return;
  }
  connect();
}

function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/api/ws?token=${encodeURIComponent(auth.token)}`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => send({ t: "hello", guild_id: auth.guildId, channel_id: auth.channelId });
  ws.onmessage = (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      player?.push(ev.data);
      return;
    }
    const msg = JSON.parse(ev.data);
    if (msg.t === "state") onState(msg);
    else if (msg.t === "error") flash(msg.message);
  };
  ws.onclose = () => {
    flash("Disconnected, reconnecting…");
    setTimeout(connect, 2000);
  };
}

function flash(text: string) {
  toast = text;
  render();
  setTimeout(() => {
    if (toast === text) {
      toast = "";
      render();
    }
  }, 4000);
}

function onState(s: State) {
  const prevId = sessionId;
  state = s;
  sessionId = s.session?.id ?? "";
  if (sessionId !== prevId) {
    player?.reset();
    menu = false;
  }
  render();
}

// Rendering keeps the canvas (and its Player) alive across state updates.
let canvas: HTMLCanvasElement | undefined;

function render() {
  if (!state) return;
  const s = state.session;
  const root = h("div", { class: "root" }, s ? screen(s) : picker());
  if (toast) root.append(h("div", { class: "toast" }, toast));
  app.replaceChildren(root);
}

function picker() {
  const wrap = h("main", { class: "picker" });
  if (catalog.length === 0) {
    wrap.append(h("p", { class: "muted" }, "No games installed."));
    return wrap;
  }
  const start = (key: string) => send({ t: "start", key });
  for (const e of catalog) {
    const card = h("section", { class: "card" }, h("h2", {}, e.name));
    const games = h("div", { class: "games" });
    for (const g of e.games ?? []) {
      games.append(h("button", { class: "game", onclick: () => start(`${e.id}/${g.id}`) }, g.name));
    }
    games.append(h("button", { class: "game secondary", onclick: () => start(e.id) }, e.games?.length ? "Pack menu" : "Launch"));
    card.append(games);
    wrap.append(card);
  }
  return wrap;
}

function screen(s: SessionView) {
  if (!canvas) {
    canvas = h("canvas", { class: "stream", width: 1280, height: 720 });
    bindInput(canvas);
  }
  player ??= new Player(canvas);
  const stage = h("main", { class: "stage" }, canvas);
  if (!s.running) stage.append(h("div", { class: "overlay" }, s.status || "Starting…"));
  else if (player.audioBlocked) stage.append(h("div", { class: "overlay hint" }, "Click for sound"));
  if (menu) {
    stage.append(h("div", { class: "overlay" },
      h("button", { class: "danger", onclick: () => send({ t: "stop" }) }, "Stop game"),
      h("button", { onclick: toggleMenu }, "Back"),
    ));
  }
  return stage;
}

// Esc opens the stop menu instead of reaching the game.
function toggleMenu() {
  menu = !menu;
  render();
}

// DOM button 0/1/2 (left/middle/right) to X button 1/2/3.
const button = (ev: PointerEvent) => (ev.button === 1 ? 2 : ev.button === 2 ? 3 : 1);

// Input goes to the game only while this viewer controls it.
function bindInput(c: HTMLCanvasElement) {
  const controlling = () => state?.role === "controller" && !!state.session;
  // browsers only start audio after a user gesture
  const unlock = () => {
    if (!player?.audioBlocked) return;
    player.unlock().then(render);
  };
  const pos = (ev: PointerEvent | WheelEvent) => {
    const r = c.getBoundingClientRect();
    // the canvas is letterboxed with object-fit: contain
    const scale = Math.min(r.width / c.width, r.height / c.height);
    const w = c.width * scale, hgt = c.height * scale;
    const x = (ev.clientX - r.left - (r.width - w) / 2) / w;
    const y = (ev.clientY - r.top - (r.height - hgt) / 2) / hgt;
    return { x, y };
  };
  let pending: { x: number; y: number } | undefined;
  const flushMove = () => {
    if (pending) send({ t: "m", ...pending });
    pending = undefined;
  };
  c.addEventListener("pointermove", (ev) => {
    if (!controlling()) return;
    if (!pending) requestAnimationFrame(flushMove);
    pending = pos(ev);
  });
  window.addEventListener("pointerdown", unlock);
  c.addEventListener("pointerdown", (ev) => {
    if (!controlling()) return;
    c.setPointerCapture(ev.pointerId);
    send({ t: "m", ...pos(ev) });
    send({ t: "b", b: button(ev), d: true });
    ev.preventDefault();
  });
  c.addEventListener("pointerup", (ev) => {
    if (!controlling()) return;
    send({ t: "b", b: button(ev), d: false });
  });
  c.addEventListener("wheel", (ev) => {
    if (!controlling()) return;
    const b = ev.deltaY < 0 ? 4 : 5;
    send({ t: "b", b, d: true });
    send({ t: "b", b, d: false });
    ev.preventDefault();
  }, { passive: false });
  c.addEventListener("contextmenu", (ev) => ev.preventDefault());
  const key = (d: boolean) => (ev: KeyboardEvent) => {
    if (d) unlock();
    if (ev.code === "Escape" && state?.session) {
      if (d && !ev.repeat) toggleMenu();
      ev.preventDefault();
      return;
    }
    if (!controlling() || ev.repeat || menu) return;
    send({ t: "k", code: ev.code, d });
    ev.preventDefault();
  };
  window.addEventListener("keydown", key(true));
  window.addEventListener("keyup", key(false));
}

main();
