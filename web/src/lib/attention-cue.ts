import { audioCue } from "@/lib/audio-cue";

// The shared attention cue for the two alert hooks (session `useAttentionAlerts`
// and board `useEpicAttentionAlerts`): an audio chirp + haptic buzz always, plus
// — only when the tab is hidden and the user already granted permission — a
// system Notification (Tier 2). Every Web-API touch is feature-checked/guarded so
// it never throws into a stream pump. The caller owns its own toast/navigate.
export function raiseAttentionCue(title: string, opts: { body?: string; tag: string }): void {
  audioCue.play();

  try {
    navigator.vibrate?.([120, 60, 120]);
  } catch {
    // some browsers throw if vibrate is gated; ignore.
  }

  if (
    typeof document !== "undefined" &&
    document.visibilityState === "hidden" &&
    typeof Notification !== "undefined" &&
    Notification.permission === "granted"
  ) {
    try {
      new Notification(title, { body: opts.body, tag: opts.tag });
    } catch {
      // constructing a Notification can throw on some platforms; ignore.
    }
  }
}
