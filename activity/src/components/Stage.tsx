import { useEffect, useRef } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { Player } from "../player";
import { controlling, menu, send, setMedia, type SessionView } from "../store";

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
    // Esc opens the stop menu instead of reaching the game.
    const key = (d: boolean) => (ev: KeyboardEvent) => {
      if (d) unlock();
      if (ev.code === "Escape") {
        if (d && !ev.repeat) menu.value = !menu.value;
        ev.preventDefault();
        return;
      }
      if (!controlling() || ev.repeat || menu.value) return;
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
    <main class="stage">
      <canvas ref={ref} class="stream" width={1280} height={720} />
      {!session.running ? (
        <div class="overlay">{session.status || "Starting…"}</div>
      ) : (
        audioBlocked.value && <div class="overlay hint">Click for sound</div>
      )}
      {menu.value && (
        <div class="overlay">
          <button class="danger" onClick={() => send({ t: "stop" })}>Stop game</button>
          <button onClick={() => (menu.value = false)}>Back</button>
        </div>
      )}
    </main>
  );
}
