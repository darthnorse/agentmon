import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Each test imports the module fresh (vi.resetModules) because audioCue holds a
// lazily-constructed AudioContext singleton; a fresh module = a fresh, unprimed cue.
beforeEach(() => {
  vi.resetModules();
  vi.unstubAllGlobals();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

/** A minimal spyable AudioContext stand-in. */
function fakeCtxClass(opts?: { state?: string; throwOnOsc?: boolean }) {
  const osc = {
    type: "",
    frequency: { setValueAtTime: vi.fn() },
    connect: vi.fn(),
    start: vi.fn(),
    stop: vi.fn(),
  };
  const gain = {
    gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
    connect: vi.fn(),
  };
  const createOscillator = vi.fn(() => {
    if (opts?.throwOnOsc) throw new Error("boom");
    return osc;
  });
  const createGain = vi.fn(() => gain);
  const resume = vi.fn(() => Promise.resolve());
  const ctorCalls = { n: 0 };
  class FakeCtx {
    state = opts?.state ?? "running";
    currentTime = 0;
    destination = {};
    createOscillator = createOscillator;
    createGain = createGain;
    resume = resume;
    constructor() {
      ctorCalls.n++;
    }
  }
  return { FakeCtx, osc, gain, createOscillator, createGain, resume, ctorCalls };
}

describe("audioCue", () => {
  it("prime() and play() never throw when AudioContext is unsupported", async () => {
    // jsdom has no AudioContext; assert both no-op silently.
    vi.stubGlobal("AudioContext", undefined);
    vi.stubGlobal("webkitAudioContext", undefined);
    const { audioCue } = await import("@/lib/audio-cue");
    expect(() => audioCue.prime()).not.toThrow();
    expect(() => audioCue.play()).not.toThrow();
  });

  it("play() builds and starts a short oscillator cue on a supported context", async () => {
    const f = fakeCtxClass();
    vi.stubGlobal("AudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    audioCue.play();
    expect(f.createOscillator).toHaveBeenCalledOnce();
    expect(f.createGain).toHaveBeenCalledOnce();
    expect(f.osc.connect).toHaveBeenCalled();
    expect(f.gain.connect).toHaveBeenCalled();
    expect(f.osc.start).toHaveBeenCalledOnce();
    expect(f.osc.stop).toHaveBeenCalledOnce();
  });

  it("prime() resumes a suspended context (autoplay unlock)", async () => {
    const f = fakeCtxClass({ state: "suspended" });
    vi.stubGlobal("AudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    audioCue.prime();
    expect(f.resume).toHaveBeenCalledOnce();
  });

  it("prime() does not resume an already-running context", async () => {
    const f = fakeCtxClass({ state: "running" });
    vi.stubGlobal("AudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    audioCue.prime();
    expect(f.resume).not.toHaveBeenCalled();
  });

  it("reuses a single AudioContext across prime() + repeated play()", async () => {
    const f = fakeCtxClass();
    vi.stubGlobal("AudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    audioCue.prime();
    audioCue.play();
    audioCue.play();
    expect(f.ctorCalls.n).toBe(1);
    expect(f.createOscillator).toHaveBeenCalledTimes(2);
  });

  it("play() swallows errors thrown while building the cue", async () => {
    const f = fakeCtxClass({ throwOnOsc: true });
    vi.stubGlobal("AudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    expect(() => audioCue.play()).not.toThrow();
  });

  it("falls back to webkitAudioContext when AudioContext is absent", async () => {
    const f = fakeCtxClass();
    vi.stubGlobal("AudioContext", undefined);
    vi.stubGlobal("webkitAudioContext", f.FakeCtx);
    const { audioCue } = await import("@/lib/audio-cue");
    audioCue.play();
    expect(f.createOscillator).toHaveBeenCalledOnce();
  });
});
