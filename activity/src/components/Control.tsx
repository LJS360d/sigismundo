import { announce, send, state, type Away, type User } from "../store";

function Avatar({ user, size }: { user: User; size: string }) {
  return user.avatar ? (
    <img class={`${size} shrink-0 rounded-full object-cover`} src={user.avatar} alt="" />
  ) : (
    <span class={`${size} grid shrink-0 place-items-center rounded-full bg-accent font-bold`}>{user.name[0]}</span>
  );
}

// The leaver's avatar, dimmed, inside a ring that runs out when control passes on.
// Keyed by the caller per state message, so the ring resyncs to the server.
function AwayAvatar({ user, away }: { user: User; away: Away }) {
  return (
    <span class="relative grid size-9 shrink-0 place-items-center">
      <svg class="absolute inset-0 size-full -rotate-90" viewBox="0 0 36 36" aria-hidden="true">
        <circle cx="18" cy="18" r="16.5" fill="none" stroke-width="2.5" class="stroke-white/10" />
        <circle
          cx="18" cy="18" r="16.5" fill="none" stroke-width="2.5" stroke-linecap="round" pathLength={100}
          class="stroke-accent" stroke-dasharray="100"
          style={{ animation: `deplete ${away.grace}ms linear ${away.in - away.grace}ms both` }}
        />
      </svg>
      <span class="opacity-50 grayscale">
        <Avatar user={user} size="size-7 text-sm" />
      </span>
    </span>
  );
}

// Bottom-left pill: who is in control, and Seize for owners. On the games
// menu always; over the stream (awayOnly) only while the controller is gone.
export function ControllerBadge({ awayOnly }: { awayOnly?: boolean }) {
  const s = state.value!;
  const c = s.controller;
  if (!c || (awayOnly && !s.away)) return null;
  const me = c.id === s.me.id;
  return (
    <div class="fixed bottom-4 left-4 z-10 flex animate-rise items-center gap-2 rounded-full border border-white/10 bg-panel/70 py-1.5 pr-1.5 pl-1.5 shadow-lg backdrop-blur-md">
      {s.away ? <AwayAvatar key={s.away.in} user={c} away={s.away} /> : <Avatar user={c} size="size-7 text-sm" />}
      <span class="pr-2 text-sm">
        <span class="font-semibold">{me ? "You" : c.name}</span>
        <span class="text-muted"> {s.away ? "left" : me ? "are in control" : "is in control"}</span>
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
