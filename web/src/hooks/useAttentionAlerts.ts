import * as React from "react";
import { toast } from "sonner";
import { useNavigate } from "@tanstack/react-router";
import type { StateEventFrame } from "@/lib/contracts";
import { stateKey } from "@/lib/state";
import { blockedTitle, doneTitle } from "@/lib/alerts";
import { raiseAttentionCue } from "@/lib/attention-cue";

// M9 Tier 1/2 in-app attention driver. Returns the `onAttention` handler that
// `useStateStream` invokes when a *different* session transitions into `blocked`
// (the pure gate lives in lib/alerts via useStateStream). The handler raises the
// foreground cue (sonner toast + audio chirp + haptic) and, when the tab is
// hidden but the user already granted Notification permission, also fires a
// system Notification (Tier 2) so a backgrounded-but-alive tab still surfaces it.
// Every Web-API touch is feature-checked/guarded so it never throws into the
// stream pump.
export function useAttentionAlerts(): (frame: StateEventFrame) => void {
  const navigate = useNavigate();
  return React.useCallback(
    (frame: StateEventFrame) => {
      // The gate (useStateStream) only calls us for an alerting transition into
      // `blocked` or `done` — pick the matching copy by the frame's state.
      const title = frame.state === "done" ? doneTitle(frame.session) : blockedTitle(frame.session);

      try {
        toast(title, {
          description: frame.server,
          action: { label: "Open", onClick: () => void navigate({ to: "/" }) },
        });
      } catch {
        // best-effort: a toast failure must not break sound/haptic/notification.
      }

      raiseAttentionCue(title, {
        body: frame.server,
        tag: stateKey(frame.server, frame.target, frame.session),
      });
    },
    [navigate],
  );
}
