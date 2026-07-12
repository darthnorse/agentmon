import * as React from "react";
import { toast } from "sonner";
import { useNavigate } from "@tanstack/react-router";
import { raiseAttentionCue } from "@/lib/attention-cue";
import { epicTag, epicTitle } from "@/lib/push-payload";
import type { Navigate } from "@/components/board/open-session";
import type { BoardDeltaFrame } from "@/lib/contracts";

export function useEpicAttentionAlerts(): (f: BoardDeltaFrame) => void {
  const navigate = useNavigate() as Navigate;
  return React.useCallback(
    (f) => {
      const title = epicTitle(f.issue);
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
      raiseAttentionCue(title, { body: f.title, tag: epicTag(f.epic_id) });
    },
    [navigate],
  );
}
