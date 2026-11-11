import { useEffect } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { catalog, state } from "../store";
import { Backdrop } from "./Catalog";

// how long each cover holds before crossfading to the next
const CYCLE = 4_000;

// Picture in picture between games: the catalog's covers crossfade behind the
// wordmark, and who is choosing the next game.
export function Attract() {
  const covers = catalog.value.filter((e) => e.image);
  const at = useSignal(0);
  useEffect(() => {
    const t = setInterval(() => at.value++, CYCLE);
    return () => clearInterval(t);
  }, []);
  const shown = covers.length ? covers[at.value % covers.length] : undefined;
  const s = state.value!;
  const c = s.controller;
  const picker = !c ? "Waiting for players" : c.id === s.me.id ? "You are picking a game" : `${c.name} is picking a game`;
  return (
    <main class="relative grid h-full place-items-center overflow-hidden">
      <Backdrop />
      {covers.map((e) => (
        <img
          key={e.id}
          src={e.image}
          alt=""
          class={`absolute inset-0 size-full scale-110 animate-pan object-cover blur-md transition-opacity duration-1000 ${shown === e ? "opacity-50" : "opacity-0"}`}
        />
      ))}
      <div class="absolute inset-0 bg-linear-to-t from-bg via-bg/40 to-bg/10" />
      <div class="relative flex animate-rise flex-col items-center gap-2 px-4 text-center">
        <h1 class="text-4xl font-bold tracking-tight drop-shadow-lg">Sigismundo</h1>
        <p class="flex items-center gap-2 text-sm text-muted">
          {c?.avatar && <img class="size-5 rounded-full" src={c.avatar} alt="" />}
          {picker}
        </p>
      </div>
    </main>
  );
}
