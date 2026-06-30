import * as React from "react";
import { Button } from "@/components/ui/button";
import { pushSupported, enablePush, getActiveRegistration } from "@/lib/push";
import { audioCue } from "@/lib/audio-cue";

// Opt-in control for attention alerts (M9). Renders nothing on browsers that can't
// do Web-Push (iOS not-installed, older Safari, jsdom) so those clients fall back
// silently to Tiers 1/2. On click — a real user gesture, required by both the
// Notification-permission and audio-autoplay policies — it primes the audio cue and
// runs the feature-detected push enrolment, reflecting the outcome to the user.

type EnableStatus = "idle" | "enabling" | "enabled" | "blocked";

export function EnableAlerts() {
  const [status, setStatus] = React.useState<EnableStatus>("idle");

  // Feature-detect at render: hidden entirely on unsupported clients.
  if (!pushSupported()) return null;

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

  if (status === "enabled") {
    return (
      <span className="text-xs text-muted-foreground" aria-live="polite">
        Alerts on
      </span>
    );
  }

  return (
    <Button
      variant="outline"
      size="sm"
      onClick={onEnable}
      disabled={status === "enabling"}
    >
      {status === "blocked" ? "Alerts blocked" : "Enable alerts"}
    </Button>
  );
}
