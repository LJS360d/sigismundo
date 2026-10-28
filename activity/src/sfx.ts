// Synthesized UI sounds. Browsers keep the context suspended until a user
// gesture, so sounds before the first click are silently dropped.
let ctx: AudioContext | undefined;

function out() {
  ctx ??= new AudioContext();
  if (ctx.state === "suspended") ctx.resume();
  return ctx;
}

function tone(freq: number, to: number, dur: number, gain: number, type: OscillatorType = "sine") {
  const c = out(), t = c.currentTime;
  const o = c.createOscillator(), g = c.createGain();
  o.type = type;
  o.frequency.setValueAtTime(freq, t);
  o.frequency.exponentialRampToValueAtTime(to, t + dur);
  g.gain.setValueAtTime(gain, t);
  g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
  o.connect(g).connect(c.destination);
  o.start(t);
  o.stop(t + dur);
}

function whoosh(from: number, to: number, dur: number) {
  const c = out(), t = c.currentTime;
  const buf = c.createBuffer(1, c.sampleRate * dur, c.sampleRate);
  const d = buf.getChannelData(0);
  for (let i = 0; i < d.length; i++) d[i] = Math.random() * 2 - 1;
  const src = c.createBufferSource(), f = c.createBiquadFilter(), g = c.createGain();
  src.buffer = buf;
  f.type = "bandpass";
  f.Q.value = 1.5;
  f.frequency.setValueAtTime(from, t);
  f.frequency.exponentialRampToValueAtTime(to, t + dur);
  g.gain.setValueAtTime(0.0001, t);
  g.gain.exponentialRampToValueAtTime(0.25, t + dur * 0.4);
  g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
  src.connect(f).connect(g).connect(c.destination);
  src.start(t);
}

export const sfx = {
  hover: () => tone(1800, 1400, 0.05, 0.03),
  click: () => tone(600, 900, 0.08, 0.08, "triangle"),
  launch: () => (whoosh(300, 4000, 0.6), tone(220, 880, 0.5, 0.06, "triangle")),
  exit: () => whoosh(3000, 250, 0.5),
  chime: () => (tone(660, 660, 0.25, 0.05), setTimeout(() => tone(990, 990, 0.35, 0.05), 120)),
};
