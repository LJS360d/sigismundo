import { useEffect, useRef } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { Player } from "../player";
import { canLaunch, catalog, controlling, isMenuKey, MENU_KEY, menu, send, setMedia, type SessionView } from "../store";
import { vtName } from "./Catalog";
import { ControllerBadge } from "./Control";

// DOM button 0/1/2 (left/middle/right) to X button 1/2/3.
const button = (ev: PointerEvent) => (ev.button === 1 ? 2 : ev.button === 2 ? 3 : 1);

// Stage is the fullscreen stream of one session; keyed by session id, so a
// new session gets a fresh Player.
export function Stage({ session }: { session: SessionView }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const player = useRef<Player>(undefined);
  const audioBlocked = useSignal(false);

  useEffect(() => {
    const c = ref.current!;
    const p = (player.current = new Player(c));
    audioBlocked.value = p.audioBlocked;
    setMedia((buf) => p.push(buf));
    return () => {
      setMedia();
      p.destroy();
    };
  }, []);

  // Input goes to the game only while this viewer controls it.
  useEffect(() => {
    const c = ref.current!;
    const pos = (ev: PointerEvent | WheelEvent) => {
      const r = c.getBoundingClientRect();
      // the canvas is letterboxed with object-fit: contain
      const scale = Math.min(r.width / c.width, r.height / c.height);
      const w = c.width * scale, h = c.height * scale;
      const x = (ev.clientX - r.left - (r.width - w) / 2) / w;
      const y = (ev.clientY - r.top - (r.height - h) / 2) / h;
      return { x, y };
    };
    let pending: { x: number; y: number } | undefined;
    const flushMove = () => {
      if (pending) send({ t: "m", ...pending });
      pending = undefined;
    };
    const move = (ev: PointerEvent) => {
      if (!controlling()) return;
      if (!pending) requestAnimationFrame(flushMove);
      pending = pos(ev);
    };
    const down = (ev: PointerEvent) => {
      if (!controlling()) return;
      c.setPointerCapture(ev.pointerId);
      send({ t: "m", ...pos(ev) });
      send({ t: "b", b: button(ev), d: true });
      ev.preventDefault();
    };
    const up = (ev: PointerEvent) => {
      if (controlling()) send({ t: "b", b: button(ev), d: false });
    };
    const wheel = (ev: WheelEvent) => {
      if (!controlling()) return;
      const b = ev.deltaY < 0 ? 4 : 5;
      send({ t: "b", b, d: true });
      send({ t: "b", b, d: false });
      ev.preventDefault();
    };
    const noMenu = (ev: Event) => ev.preventDefault();
    // browsers only start audio after a user gesture
    const unlock = () => {
      const p = player.current;
      if (p?.audioBlocked) p.unlock().then(() => (audioBlocked.value = p.audioBlocked));
    };
    // The menu shortcut toggles the stop menu instead of reaching the game.
    const key = (d: boolean) => (ev: KeyboardEvent) => {
      if (d) unlock();
      if (d && isMenuKey(ev)) {
        if (!ev.repeat) menu.value = !menu.value;
        ev.preventDefault();
        return;
      }
      if (d && menu.value && ev.code === "Escape") {
        menu.value = false;
        ev.preventDefault();
        return;
      }
      // key-ups still pass while the menu is open, so the shortcut's modifiers don't stick
      if (!controlling() || ev.repeat || (d && menu.value)) return;
      send({ t: "k", code: ev.code, d });
      ev.preventDefault();
    };
    const keydown = key(true), keyup = key(false);
    c.addEventListener("pointermove", move);
    c.addEventListener("pointerdown", down);
    c.addEventListener("pointerup", up);
    c.addEventListener("wheel", wheel, { passive: false });
    c.addEventListener("contextmenu", noMenu);
    window.addEventListener("pointerdown", unlock);
    window.addEventListener("keydown", keydown);
    window.addEventListener("keyup", keyup);
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", keydown);
      window.removeEventListener("keyup", keyup);
    };
  }, []);

  return (
    <main class="fixed inset-0 bg-black" style={{ viewTransitionName: vtName(session.key) }}>
      <canvas
        ref={ref}
        class={`block h-full w-full touch-none object-contain outline-none transition-opacity duration-1000 ${session.running ? "" : "opacity-0"}`}
        width={1280}
        height={720}
      />
      {!session.running ? (
        <Loading session={session} />
      ) : (
        audioBlocked.value && <div class="pointer-events-none absolute inset-x-0 bottom-4 mx-auto w-fit rounded-lg bg-black/70 px-5 py-3">Click for sound</div>
      )}
      <ControllerBadge awayOnly />
      {menu.value && (
        <div class="absolute inset-0 grid place-items-center bg-black/55 p-4 backdrop-blur-sm" onClick={(e) => e.target === e.currentTarget && (menu.value = false)}>
          <div class="w-full max-w-90 rounded-xl border border-line bg-panel p-5 shadow-2xl" role="dialog" aria-label="Game menu">
            <h2 class="mb-1.5 text-lg font-bold">Game menu</h2>
            <p class="mb-4 text-sm leading-relaxed text-muted">The game keeps running for everyone. <kbd>Esc</kbd> or <kbd>{MENU_KEY}</kbd> to go back.</p>
            <div class="flex justify-end gap-2">
              <button class="btn" onClick={() => (menu.value = false)} autofocus>Back to game</button>
              {canLaunch() && (
                <button class="btn btn-danger" onClick={() => send({ t: "stop" })}>
                  Stop game
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </main>
  );
}

// Shown in place of the stream until the game is up: the card's cover, blown up.
function Loading({ session }: { session: SessionView }) {
  const image = catalog.value.find((e) => e.id === session.key)?.image;
  return (
    <div class="absolute inset-0 grid place-items-center overflow-hidden bg-bg">
      {image && <img class="absolute inset-0 size-full animate-pan object-cover opacity-40 blur-xl" src={image} alt="" />}
      <div class="absolute inset-0 bg-linear-to-t from-bg via-bg/40 to-transparent" />
      <div class="relative flex w-full max-w-md flex-col items-center gap-4 px-6 text-center">
        <h1 class="animate-rise text-3xl font-bold tracking-tight drop-shadow-lg">{session.name}</h1>
        <div class="h-1 w-full overflow-hidden rounded-full bg-white/10">
          <div class="h-full w-2/5 animate-shimmer rounded-full bg-linear-to-r from-transparent via-accent to-transparent" />
        </div>
        <p class="text-sm text-muted">{session.status || "Starting…"}</p>
        <p class="mt-6 text-xs text-muted/80">
          Press <kbd>{MENU_KEY}</kbd> in game for the menu.
        </p>
      </div>
    </div>
  );
}
