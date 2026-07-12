import * as React from "react";
import { toast } from "sonner";
import { useNavigate } from "@tanstack/react-router";
import { audioCue } from "@/lib/audio-cue";
import type { BoardDeltaFrame } from "@/lib/contracts";

type Navigate = (opts: any) => unknown;

export function useEpicAttentionAlerts(): (f: BoardDeltaFrame) => void {
  const navigate = useNavigate() as Navigate;
  return React.useCallback(
    (f) => {
      const title = `Epic #${f.issue} needs you`;
      try {
        toast(title, {
          description: f.needs || f.title,
          action: {
            label: "View",
            onClick: () =>
              void navigate({
                to: "/projects/$projectId",
                params: { projectId: f.project_id },
                search: { tab: "board", epic: f.epic_id },
              }),
          },
        });
      } catch { /* toast failure must not break sound/notification */ }
      audioCue.play();
      try { navigator.vibrate?.([120, 60, 120]); } catch { /* unsupported */ }
      try {
        if (document.visibilityState === "hidden" && "Notification" in window && Notification.permission === "granted") {
          new Notification(title, { body: f.title, tag: `epic:${f.epic_id}` });
        }
      } catch { /* unsupported */ }
    },
    [navigate],
  );
}
