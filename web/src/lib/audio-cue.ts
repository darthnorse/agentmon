// A tiny WebAudio "beep" cue for attention alerts (M9 Tier-1). The AudioContext is
// constructed lazily on the first user gesture via prime() so we satisfy the browser
// autoplay policy, then reused for every play(). Every Web-Audio touch is guarded so
// unsupported environments (jsdom, older browsers) or a blocked/closed context simply
// no-op instead of throwing into the alert path.

type AudioContextCtor = new () => AudioContext;

function audioContextCtor(): AudioContextCtor | undefined {
  if (typeof window === "undefined") return undefined;
  const w = window as unknown as {
    AudioContext?: AudioContextCtor;
    webkitAudioContext?: AudioContextCtor;
  };
  return w.AudioContext ?? w.webkitAudioContext;
}

let ctx: AudioContext | null = null;

function ensureContext(): AudioContext | null {
  if (ctx) return ctx;
  const Ctor = audioContextCtor();
  if (!Ctor) return null;
  try {
    ctx = new Ctor();
  } catch {
    ctx = null;
  }
  return ctx;
}

export const audioCue = {
  /** Construct/resume the AudioContext from a user gesture so play() can sound later. */
  prime(): void {
    try {
      const c = ensureContext();
      if (c && c.state === "suspended" && typeof c.resume === "function") {
        void c.resume();
      }
    } catch {
      // best-effort: priming must never throw into the click handler.
    }
  },

  /** Play a short two-stage attention cue. No-throw if unsupported or blocked. */
  play(): void {
    try {
      const c = ensureContext();
      if (!c) return;
      if (c.state === "suspended" && typeof c.resume === "function") void c.resume();
      const now = c.currentTime;
      const osc = c.createOscillator();
      const gain = c.createGain();
      osc.connect(gain);
      gain.connect(c.destination);
      osc.type = "sine";
      osc.frequency.setValueAtTime(880, now);
      // Quick attack then exponential decay — a soft, non-jarring chirp.
      gain.gain.setValueAtTime(0.0001, now);
      gain.gain.exponentialRampToValueAtTime(0.2, now + 0.01);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.25);
      osc.start(now);
      osc.stop(now + 0.26);
    } catch {
      // best-effort: a failed cue must never break the alert.
    }
  },
};
