import "./style.css";
import { authenticate, type Auth, type User } from "./auth";
import { Player } from "./player";

type Game = { id: string; name: string };
type Entry = { id: string; name: string; join_url?: string; games?: Game[] };
type SessionView = {
  id: string;
  key: string;
  name: string;
  host: User;
  controller: User;
  guild_id: string;
  channel_id: string;
  status: string;
  running: boolean;
  room_code: string;
  join_url: string;
};
type State = {
  me: User;
  session: SessionView | null;
  viewers: User[];
  role: "" | "controller" | "host" | "viewer";
  can_reclaim: boolean;
  privileged: boolean;
};

const app = document.getElementById("app")!;
let auth: Auth;
let ws: WebSocket | undefined;
let state: State | undefined;
let catalog: Entry[] = [];
let tab: "screen" | "play" = "screen";
let player: Player | undefined;
let sessionId = "";
let toast = "";

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
  if (sessionId !== prevId) player?.reset();
  render();
}

// Rendering keeps the canvas (and its Player) alive across state updates.
let canvas: HTMLCanvasElement | undefined;

function render() {
  if (!state) return;
  const s = state.session;
  const root = h("div", { class: "root" });
  root.append(header(state));
  if (!s) {
    root.append(picker());
  } else if (tab === "screen") {
    root.append(screen(state, s));
  } else {
    root.append(play(state, s));
  }
  if (toast) root.append(h("div", { class: "toast" }, toast));
  // keep a half-typed room code across re-renders
  const typing = document.activeElement instanceof HTMLInputElement && document.activeElement.classList.contains("code")
    ? document.activeElement.value
    : undefined;
  app.replaceChildren(root);
  if (typing !== undefined) {
    const input = root.querySelector<HTMLInputElement>("input.code");
    if (input) {
      input.value = typing;
      input.focus();
    }
  }
}

function header(st: State) {
  const s = st.session;
  const bar = h("header", {});
  if (s) {
    const tabs = h("nav", {},
      tabButton("screen", "Screen"),
      tabButton("play", "Play"),
    );
    bar.append(tabs, h("div", { class: "title" }, s.name, s.status && h("span", { class: "status" }, s.status)));
  } else {
    bar.append(h("div", { class: "title" }, "Pick a game"));
  }
  bar.append(h("div", { class: "viewers", title: st.viewers.map((v) => v.name).join(", ") }, `👀 ${st.viewers.length}`));
  return bar;
}

function tabButton(id: typeof tab, label: string) {
  return h("button", {
    class: tab === id ? "tab active" : "tab",
    onclick: () => {
      tab = id;
      render();
    },
  }, label);
}

function picker() {
  const wrap = h("main", { class: "picker" });
  if (catalog.length === 0) {
    wrap.append(h("p", { class: "muted" }, "No games installed."));
    return wrap;
  }
  const start = (key: string) => {
    send({ t: "start", key });
    tab = "screen";
  };
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

function screen(st: State, s: SessionView) {
  const main = h("main", { class: "screen" });
  if (!canvas) {
    canvas = h("canvas", { class: "stream", width: 1280, height: 720 });
    bindInput(canvas);
  }
  player ??= new Player(canvas);
  const stage = h("div", { class: "stage" }, canvas);
  if (!s.running) stage.append(h("div", { class: "overlay" }, s.status || "Starting…"));
  else if (player.audioBlocked) {
    stage.append(h("button", {
      class: "overlay clickable",
      onclick: async () => {
        await player!.unlock();
        render();
      },
    }, "🔊 Tap for sound"));
  }
  main.append(stage, controls(st, s));
  return main;
}

function controls(st: State, s: SessionView) {
  const bar = h("div", { class: "controls" });
  const who = st.role === "controller" ? "You have control" : `${s.controller.name} has control`;
  bar.append(h("span", { class: st.role === "controller" ? "badge on" : "badge" }, who));
  if (st.can_reclaim) bar.append(h("button", { onclick: () => send({ t: "take" }) }, "Take control"));
  if (st.privileged) {
    const others = st.viewers.filter((v) => v.id !== s.controller.id);
    if (others.length) {
      const sel = h("select", { onchange: () => sel.value && send({ t: "handoff", to: sel.value }) },
        h("option", { value: "" }, "Hand control to…"),
        ...others.map((v) => h("option", { value: v.id }, v.name)),
      );
      bar.append(sel);
    }
    bar.append(roomCodeForm(s));
    bar.append(h("button", {
      class: "danger",
      onclick: () => confirm(`Stop ${s.name}?`) && send({ t: "stop" }),
    }, "Stop"));
  }
  const vol = h("input", { type: "range", min: "0", max: "1", step: "0.05", value: "1", title: "Volume" });
  vol.oninput = () => player?.setVolume(Number(vol.value));
  bar.append(vol);
  return bar;
}

function roomCodeForm(s: SessionView) {
  const input = h("input", {
    class: "code",
    value: s.room_code,
    placeholder: "Room code",
    maxLength: 4,
    autocapitalize: "characters",
  });
  const form = h("form", {
    class: "inline",
    onsubmit: (ev: SubmitEvent) => {
      ev.preventDefault();
      send({ t: "room", code: input.value });
    },
  }, input, h("button", { type: "submit" }, "Set"));
  return form;
}

function play(st: State, s: SessionView) {
  const main = h("main", { class: "play" });
  if (s.room_code) {
    main.append(
      h("p", { class: "muted" }, "Room code"),
      h("div", { class: "bigcode" }, s.room_code),
      h("button", { class: "primary big", onclick: () => auth.openExternal(s.join_url) }, "Open jackbox.tv"),
      h("p", { class: "muted" }, "Opens in your browser. Enter the code and your name to join."),
    );
  } else {
    main.append(h("p", {}, st.privileged
      ? "Type the room code shown on screen so players can join:"
      : "Waiting for the host to share the room code…"));
    if (st.privileged) main.append(roomCodeForm(s));
    main.append(h("button", { class: "big", onclick: () => auth.openExternal(s.join_url) }, "Open jackbox.tv anyway"));
  }
  return main;
}

// DOM button 0/1/2 (left/middle/right) to X button 1/2/3.
const button = (ev: PointerEvent) => (ev.button === 1 ? 2 : ev.button === 2 ? 3 : 1);

// Input goes to the game only while this viewer controls it.
function bindInput(c: HTMLCanvasElement) {
  const controlling = () => state?.role === "controller" && tab === "screen";
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
  c.addEventListener("pointerdown", (ev) => {
    if (!controlling()) return;
    c.setPointerCapture(ev.pointerId);
    c.focus();
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
    if (!controlling() || ev.repeat || (ev.target as HTMLElement).closest("input,select,textarea")) return;
    send({ t: "k", code: ev.code, d });
    ev.preventDefault();
  };
  window.addEventListener("keydown", key(true));
  window.addEventListener("keyup", key(false));
}

main();
