import * as React from "react";
import { Button } from "@/components/ui/button";
import { pushSupported, enablePush, disablePush, getActiveRegistration } from "@/lib/push";
import { audioCue } from "@/lib/audio-cue";

// On/off toggle for attention alerts (M9 Web-Push). Renders nothing on browsers that
// can't do Web-Push (iOS not-installed, older Safari, jsdom) so those clients fall
// back silently to Tiers 1/2. Enabling — a real user gesture, required by both the
// Notification-permission and audio-autoplay policies — primes the audio cue and runs
// the feature-detected push enrolment; disabling tears the subscription down (hub +
// local). On mount it reflects the real subscription state so the toggle is accurate
// across reloads.

type EnableStatus = "idle" | "enabling" | "enabled" | "disabling" | "blocked";

export function EnableAlerts() {
  const [status, setStatus] = React.useState<EnableStatus>("idle");
  const supported = pushSupported();

  // Reflect the actual subscription on mount: if push permission is granted and a
  // subscription already exists, show the toggle as "on" rather than "Enable".
  React.useEffect(() => {
    if (!supported) return;
    let cancelled = false;
    void (async () => {
      try {
        if (Notification.permission !== "granted") return;
        const reg = await getActiveRegistration();
        const sub = await reg?.pushManager.getSubscription();
        if (!cancelled && sub) setStatus("enabled");
      } catch {
        // best-effort: leave it at "idle" so the user can (re-)enable.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [supported]);

  if (!supported) return null;

  async function onEnable() {
    // Prime audio from the gesture so the Tier-1 cue can sound later.
    audioCue.prime();
    setStatus("enabling");
    try {
      // getActiveRegistration() never hangs (getRegistration(), not the `.ready`
      // trap); undefined → no active SW yet, so we can't enrol.
      const reg = await getActiveRegistration();
      if (!reg) {
        setStatus("blocked");
        return;
      }
      const ok = await enablePush(reg);
      setStatus(ok ? "enabled" : "blocked");
    } catch {
      setStatus("blocked");
    }
  }

  async function onDisable() {
    setStatus("disabling");
    try {
      const reg = await getActiveRegistration();
      if (reg) await disablePush(reg);
    } catch {
      // best-effort; fall through to idle regardless.
    }
    setStatus("idle");
  }

  if (status === "enabled" || status === "disabling") {
    return (
      <Button variant="outline" size="sm" onClick={onDisable} disabled={status === "disabling"}>
        {status === "disabling" ? "Disabling…" : "Disable alerts"}
      </Button>
    );
  }

  return (
    <Button variant="outline" size="sm" onClick={onEnable} disabled={status === "enabling"}>
      {status === "blocked" ? "Alerts blocked" : status === "enabling" ? "Enabling…" : "Enable alerts"}
    </Button>
  );
}
