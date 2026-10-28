import "./style.css";
import { render } from "preact";
import { signal } from "@preact/signals";
import { authenticate } from "./auth";
import { catalog, connect, state, toast } from "./store";
import { Catalog } from "./components/Catalog";
import { Stage } from "./components/Stage";

const error = signal("");

function App() {
  if (error.value) return <div class="center error">{error.value}</div>;
  const s = state.value;
  if (!s) return <div class="center">Connecting…</div>;
  return (
    <div class="root">
      {s.session ? <Stage key={s.session.id} session={s.session} /> : <Catalog />}
      {toast.value && <div class="toast">{toast.value}</div>}
    </div>
  );
}

render(<App />, document.getElementById("app")!);

try {
  const auth = await authenticate();
  catalog.value = await (await fetch("/api/catalog")).json();
  connect(auth);
} catch (e) {
  console.error(e);
  error.value = e instanceof Error ? e.message : JSON.stringify(e);
}
