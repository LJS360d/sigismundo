import { catalog, send, type Entry } from "../store";

export function Catalog() {
  if (catalog.value.length === 0) return <p class="center muted">No apps installed.</p>;
  return (
    <main class="catalog">
      {catalog.value.map((e) => <AppCard key={e.id} entry={e} />)}
    </main>
  );
}

function AppCard({ entry }: { entry: Entry }) {
  return (
    <section class="card">
      {entry.image && <img class="cover" src={entry.image} alt="" />}
      <h2>{entry.name}</h2>
      {entry.description && <p class="muted">{entry.description}</p>}
      <button class="launch" onClick={() => send({ t: "start", key: entry.id })}>Launch</button>
    </section>
  );
}
