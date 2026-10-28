import { Backdrop } from "./Catalog";

export type Step = { label: string; done: boolean };

// Boot screen until the first state arrives: animated wordmark, orbiting
// loader and the real boot steps ticking off.
export function Splash({ steps, error }: { steps: Step[]; error: string }) {
  return (
    <main class="relative grid h-full place-items-center overflow-hidden">
      <Backdrop />
      <div class="relative flex flex-col items-center gap-8 px-6 text-center">
        <Orbit failed={!!error} />
        {error ? (
          <div class="flex max-w-sm animate-rise flex-col items-center gap-4">
            <p class="text-sm text-danger">{error}</p>
            <button class="btn btn-accent" onClick={() => location.reload()}>
              Try again
            </button>
          </div>
        ) : (
          <ul class="flex flex-col gap-2 text-sm">
            {steps.map((s, i) => (
              <li
                key={s.label}
                class={`flex animate-rise items-center gap-2.5 transition-colors duration-500 ${s.done ? "text-text" : "text-muted"}`}
                style={{ animationDelay: `${700 + i * 120}ms` }}
              >
                <span
                  class={`grid size-4 place-items-center rounded-full border text-[10px] transition-all duration-500 ${
                    s.done ? "scale-110 border-accent bg-accent" : "animate-pulse border-muted"
                  }`}
                >
                  {s.done && "✓"}
                </span>
                {s.label}
              </li>
            ))}
          </ul>
        )}
      </div>
    </main>
  );
}

// Two counter-rotating arcs around a breathing core; turns red and stops on error.
function Orbit({ failed }: { failed: boolean }) {
  const arc = failed ? "border-danger" : "border-t-accent border-r-accent/40";
  return (
    <div class={`relative size-24 animate-rise ${failed ? "[&_*]:animate-none" : ""}`}>
      <div class={`absolute inset-0 animate-spin rounded-full border-4 border-transparent [animation-duration:1.4s] ${arc}`} />
      <div
        class={`absolute inset-3 animate-spin rounded-full border-4 border-transparent [animation-direction:reverse] [animation-duration:2.2s] ${
          failed ? "border-danger/50" : "border-b-sky-400 border-l-sky-400/40"
        }`}
      />
      <div class={`absolute inset-8 animate-breathe rounded-full ${failed ? "bg-danger" : "bg-accent"} shadow-[0_0_40px_var(--color-accent)]`} />
    </div>
  );
}
