import { useSignal } from "@preact/signals";
import { canLaunch, catalog, launching, returning, send, state, type Entry } from "../store";
import { ControllerBadge } from "./Control";
import { sfx } from "../sfx";

// view-transition-name shared by a card and the stage it zooms into
export const vtName = (key: string) => `game-${CSS.escape(key)}`;

export function Catalog() {
  const focused = useSignal("");
  return (
    <main class="relative h-full overflow-hidden">
      <Backdrop focused={focused.value} />
      <div class="relative mx-auto h-full max-w-5xl overflow-auto px-4 pt-6 pb-20">
        {catalog.value.length === 0 ? (
          <p class="py-12 text-center text-muted">No apps installed.</p>
        ) : (
          <div class="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-4">
            {catalog.value.map((e, i) => (
              <AppCard key={e.id} entry={e} index={i} onFocus={() => (focused.value = e.id)} />
            ))}
          </div>
        )}
      </div>
      <ControllerBadge />
    </main>
  );
}

// Aurora blobs, with the focused game's cover blurred on top.
export function Backdrop({ focused = "" }: { focused?: string }) {
  return (
    <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden>
      <div class="absolute -top-1/4 -left-1/4 size-[70vmax] animate-drift rounded-full bg-accent/25 blur-3xl" />
      <div class="absolute -right-1/4 -bottom-1/4 size-[60vmax] animate-drift rounded-full bg-sky-500/15 blur-3xl [animation-delay:-12s] [animation-direction:reverse]" />
      {catalog.value.map(
        (e) =>
          e.image && (
            <img
              key={e.id}
              src={e.image}
              alt=""
              class={`absolute inset-0 size-full scale-125 animate-pan object-cover blur-2xl transition-opacity duration-700 ${focused === e.id ? "opacity-45" : "opacity-0"}`}
            />
          ),
      )}
      <div class="absolute inset-0 bg-linear-to-b from-bg/30 via-bg/60 to-bg" />
    </div>
  );
}

function AppCard({ entry, index, onFocus }: { entry: Entry; index: number; onFocus: () => void }) {
  const pending = launching.value === entry.id;
  const enter = () => (onFocus(), sfx.hover());
  return (
    <button
      class={`group flex cursor-pointer flex-col overflow-hidden rounded-sm border border-white/10 bg-white/5 text-left shadow-lg backdrop-blur-md transition duration-300
        hover:-translate-y-1 hover:scale-[1.03] hover:border-accent/70 hover:bg-white/10 hover:shadow-accent/30 hover:shadow-2xl
        focus-visible:-translate-y-1 focus-visible:scale-[1.03] focus-visible:border-accent focus-visible:outline-none
        disabled:cursor-default ${pending ? "animate-pulse border-accent" : entry.id === returning ? "" : "animate-rise"}`}
      style={{ viewTransitionName: vtName(entry.id), animationDelay: `${index * 70}ms` }}
      disabled={!!launching.value || !canLaunch()}
      onMouseEnter={enter}
      onFocus={enter}
      onClick={() => {
        sfx.click();
        launching.value = entry.id;
        send({ t: "start", key: entry.id });
      }}
    >
      <div class="relative aspect-[2/1] overflow-hidden">
        {entry.image ? (
          <img class="size-full object-cover transition duration-500 group-hover:scale-110" src={entry.image} alt="" />
        ) : (
          <div class="grid size-full place-items-center bg-linear-135 from-accent/40 to-bg text-4xl font-bold">
            {entry.name[0]}
          </div>
        )}
        <span class="absolute right-2 bottom-2 rounded-full bg-accent px-3 py-1 text-xs font-bold opacity-0 shadow-lg transition duration-300 group-hover:opacity-100 group-focus-visible:opacity-100">
          {pending ? "Starting…" : canLaunch() ? "▶ Play" : `${state.value?.controller?.name} picks`}
        </span>
      </div>
      <div class="flex flex-1 flex-col gap-1 p-3">
        <h2 class="text-base leading-tight font-bold">{entry.name}</h2>
        {entry.description && <p class="text-xs leading-snug whitespace-pre-line text-muted">{entry.description}</p>}
      </div>
    </button>
  );
}
