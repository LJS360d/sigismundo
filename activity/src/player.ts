// Decodes the server's frames (see internal/stream/hub.go for the format)
// with WebCodecs and keeps video in step with audio.

const KIND_VIDEO = 1;
const KIND_AUDIO = 2;
const FLAG_KEY = 1;
// how far ahead of the audio clock packets are scheduled, absorbs jitter
const JITTER = 0.12;
// a backlog past this (bursty delivery) restarts at JITTER; video follows the
// audio clock, so this bounds the added delay for both
const MAX_AHEAD = 0.25;
const MAX_QUEUED_FRAMES = 30;

type Decoded = { ts: number; frame: VideoFrame };

type OpusFallback = {
  decodeFrame(data: Uint8Array): { channelData: Float32Array[]; samplesDecoded: number; sampleRate: number };
  free(): void;
};

export class Player {
  private ctx = new AudioContext({ sampleRate: 48000, latencyHint: "interactive" });
  private gain = this.ctx.createGain();
  private video?: VideoDecoder;
  private codec = "";
  private needKey = true;
  private queue: Decoded[] = [];
  private audio?: AudioDecoder;
  private fallback?: OpusFallback;
  // seconds to add to a server timestamp to get AudioContext time; set by audio packets
  private base?: number;
  private nextAudio = 0;
  private raf = 0;
  private g: CanvasRenderingContext2D;

  constructor(private canvas: HTMLCanvasElement) {
    this.g = canvas.getContext("2d", { alpha: false })!;
    this.gain.connect(this.ctx.destination);
    this.setupAudio();
    this.raf = requestAnimationFrame(this.render);
  }

  get audioBlocked() {
    return this.ctx.state !== "running";
  }

  // Browsers only start audio after a user gesture.
  async unlock() {
    await this.ctx.resume();
  }

  push(buf: ArrayBuffer) {
    const view = new DataView(buf);
    const kind = view.getUint8(0);
    const key = (view.getUint8(1) & FLAG_KEY) !== 0;
    const ts = Number(view.getBigUint64(2));
    const data = new Uint8Array(buf, 10);
    if (kind === KIND_VIDEO) this.pushVideo(data, ts, key);
    else if (kind === KIND_AUDIO) this.pushAudio(data, ts);
  }

  private pushVideo(data: Uint8Array, ts: number, key: boolean) {
    if (this.needKey && !key) return;
    if (key) {
      const codec = codecFromSPS(data);
      if (codec && codec !== this.codec) this.configureVideo(codec);
    }
    if (!this.video || this.video.state !== "configured") return;
    if (this.video.decodeQueueSize > 10 && !key) {
      // decoder can't keep up, skip to the next keyframe
      this.needKey = true;
      return;
    }
    this.needKey = false;
    this.video.decode(new EncodedVideoChunk({ type: key ? "key" : "delta", timestamp: ts, data }));
  }

  private configureVideo(codec: string) {
    this.video?.close();
    this.codec = codec;
    this.video = new VideoDecoder({
      output: (frame) => {
        this.queue.push({ ts: frame.timestamp, frame });
        while (this.queue.length > MAX_QUEUED_FRAMES) this.queue.shift()!.frame.close();
      },
      error: (e) => {
        console.error("video decoder", e);
        this.codec = "";
        this.needKey = true;
      },
    });
    // no description: the decoder takes Annex B with in-band SPS/PPS
    this.video.configure({ codec, optimizeForLatency: true, hardwareAcceleration: "prefer-hardware" });
  }

  private async setupAudio() {
    const config: AudioDecoderConfig = { codec: "opus", sampleRate: 48000, numberOfChannels: 2 };
    if ("AudioDecoder" in window && (await AudioDecoder.isConfigSupported(config).catch(() => ({ supported: false }))).supported) {
      this.audio = new AudioDecoder({
        output: (d) => {
          const chans = Array.from({ length: d.numberOfChannels }, (_, i) => {
            const c = new Float32Array(d.numberOfFrames);
            d.copyTo(c, { planeIndex: i, format: "f32-planar" });
            return c;
          });
          this.schedule(chans, d.sampleRate, d.timestamp);
          d.close();
        },
        error: (e) => console.error("audio decoder", e),
      });
      this.audio.configure(config);
      return;
    }
    const { OpusDecoder } = await import("opus-decoder");
    const dec = new OpusDecoder({ channels: 2 });
    await dec.ready;
    this.fallback = dec as unknown as OpusFallback;
  }

  private pushAudio(data: Uint8Array, ts: number) {
    if (this.audio?.state === "configured") {
      this.audio.decode(new EncodedAudioChunk({ type: "key", timestamp: ts, data }));
    } else if (this.fallback) {
      const out = this.fallback.decodeFrame(data);
      if (out.samplesDecoded > 0) this.schedule(out.channelData, out.sampleRate, ts);
    }
  }

  // Audio plays back to back; each packet re-anchors the server-to-audio
  // clock mapping that video frames are timed against.
  private schedule(chans: Float32Array[], rate: number, ts: number) {
    if (this.audioBlocked) return;
    const now = this.ctx.currentTime;
    let at = this.nextAudio;
    if (at < now || at > now + MAX_AHEAD) at = now + JITTER;
    const buf = this.ctx.createBuffer(chans.length, chans[0].length, rate);
    chans.forEach((c, i) => buf.copyToChannel(c as Float32Array<ArrayBuffer>, i));
    const src = this.ctx.createBufferSource();
    src.buffer = buf;
    src.connect(this.gain);
    src.start(at);
    this.nextAudio = at + buf.duration;
    this.base = at - ts / 1e6;
  }

  private render = () => {
    this.raf = requestAnimationFrame(this.render);
    if (this.queue.length === 0) return;
    // with a running audio clock, show the newest frame that is due; otherwise the newest frame
    let pick = this.queue.length - 1;
    if (this.base !== undefined && !this.audioBlocked) {
      const now = this.ctx.currentTime;
      pick = -1;
      for (let i = 0; i < this.queue.length; i++) {
        if (this.queue[i].ts / 1e6 + this.base <= now) pick = i;
      }
      if (pick < 0) return;
    }
    const { frame } = this.queue[pick];
    for (let i = 0; i < pick; i++) this.queue[i].frame.close();
    this.queue.splice(0, pick + 1);
    if (this.canvas.width !== frame.displayWidth || this.canvas.height !== frame.displayHeight) {
      this.canvas.width = frame.displayWidth;
      this.canvas.height = frame.displayHeight;
    }
    this.g.drawImage(frame, 0, 0);
    frame.close();
  };

  destroy() {
    cancelAnimationFrame(this.raf);
    for (const d of this.queue) d.frame.close();
    this.video?.close();
    this.audio?.close();
    this.fallback?.free();
    this.ctx.close();
  }
}

// codecFromSPS builds "avc1.PPCCLL" from the SPS in an Annex B access unit.
function codecFromSPS(au: Uint8Array): string | undefined {
  for (let i = 0; i + 7 < au.length; i++) {
    if (au[i] === 0 && au[i + 1] === 0 && au[i + 2] === 1 && (au[i + 3] & 0x1f) === 7) {
      const hex = (b: number) => b.toString(16).padStart(2, "0");
      return `avc1.${hex(au[i + 4])}${hex(au[i + 5])}${hex(au[i + 6])}`;
    }
  }
  return undefined;
}
