import { announce, send, state, type User } from "../store";

function Avatar({ user, size }: { user: User; size: string }) {
  return user.avatar ? (
    <img class={`${size} shrink-0 rounded-full object-cover`} src={user.avatar} alt="" />
  ) : (
    <span class={`${size} grid shrink-0 place-items-center rounded-full bg-accent font-bold`}>{user.name[0]}</span>
  );
}

// Bottom-left pill on the games menu: who picks, and Seize for owners.
export function ControllerBadge() {
  const s = state.value!;
  const c = s.controller;
  if (!c) return null;
  const me = c.id === s.me.id;
  return (
    <div class="fixed bottom-4 left-4 z-10 flex animate-rise items-center gap-2 rounded-full border border-white/10 bg-panel/70 py-1.5 pr-1.5 pl-1.5 shadow-lg backdrop-blur-md">
      <Avatar user={c} size="size-7 text-sm" />
      <span class="pr-2 text-sm">
        <span class="font-semibold">{me ? "You" : c.name}</span>
        <span class="text-muted"> {me ? "are" : "is"} in control</span>
      </span>
      {s.owner && !me && (
        <button class="btn btn-accent rounded-full px-3 py-1 text-xs" onClick={() => send({ t: "seize" })}>
          Seize control
        </button>
      )}
    </div>
  );
}

// Center card announcing a new controller; fades out on its own.
export function Announcement() {
  const u = announce.value;
  if (!u) return null;
  const me = u.id === state.value?.me.id;
  const hint = state.value?.session
    ? me && "Your mouse and keyboard now drive the game."
    : me ? "Pick a game to start." : `${u.name} picks the next game.`;
  return (
    <div class="pointer-events-none fixed inset-0 z-20 grid place-items-center p-4" role="status">
      <div
        key={u.id}
        class="flex animate-announce flex-col items-center gap-3 rounded-2xl border border-accent/40 bg-panel/80 px-8 py-6 text-center shadow-2xl shadow-accent/20 backdrop-blur-xl"
      >
        <Avatar user={u} size="size-16 text-2xl" />
        <p class="text-lg font-bold">{me ? "You are" : `${u.name} is`} now in control</p>
        {hint && <p class="text-sm text-muted">{hint}</p>}
      </div>
    </div>
  );
}
