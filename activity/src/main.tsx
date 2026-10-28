import "./style.css";
import { render } from "preact";
import { signal } from "@preact/signals";
import { authenticate, getJSON } from "./auth";
import { catalog, connect, state, toast, type Entry } from "./store";
import { Announcement } from "./components/Control";
import { Catalog } from "./components/Catalog";
import { Stage } from "./components/Stage";
import { Splash } from "./components/Splash";

const error = signal("");
const signedIn = signal(false);
const gamesLoaded = signal(false);
// flips after the first state, but never before the splash had time to play
const ready = signal(false);

function App() {
  const s = state.value;
  if (!ready.value || !s)
    return (
      <Splash
        error={error.value}
        steps={[
          { label: "Signing in with Discord", done: signedIn.value },
          { label: "Loading games", done: gamesLoaded.value },
          { label: "Joining the room", done: !!s },
        ]}
      />
    );
  return (
    <div class="h-full">
      {s.session ? <Stage key={s.session.id} session={s.session} /> : <Catalog />}
      <Announcement />
      {toast.value && <div class="fixed bottom-4 left-1/2 -translate-x-1/2 rounded-lg border border-line bg-panel px-4 py-2">{toast.value}</div>}
    </div>
  );
}

render(<App />, document.getElementById("app")!);

try {
  const [token] = await Promise.all([
    authenticate().then((t) => ((signedIn.value = true), t)),
    getJSON<Entry[]>("/api/catalog").then((e) => ((catalog.value = e), (gamesLoaded.value = true))),
  ]);
  connect(token);
  const joined = new Promise<void>((r) => state.subscribe((v) => v && r()));
  await Promise.all([joined, new Promise((r) => setTimeout(r, 1600))]);
  // crossfade into the app; the timeout lets Preact render first (as in store.ts)
  const show = () => {
    ready.value = true;
    return new Promise<void>((r) => setTimeout(r));
  };
  document.startViewTransition ? document.startViewTransition(show) : show();
} catch (e) {
  console.error(e);
  error.value = e instanceof Error ? e.message : JSON.stringify(e);
}
