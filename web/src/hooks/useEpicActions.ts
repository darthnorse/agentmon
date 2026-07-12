import * as React from "react";
import { toast } from "sonner";
import { ApiError, epicAction } from "@/lib/api-client";
import type { EpicActionRequest } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";

// One mutation helper for every board action (epic- and project-scoped —
// same hub endpoint). Success invalidates ["board"] immediately rather than
// waiting on the SSE delta; typed 409s carry human-readable hub messages.
export function useEpicActions(projectId: string) {
  const [busy, setBusy] = React.useState<string | null>(null);
  const act = React.useCallback(
    async (body: EpicActionRequest, success?: string): Promise<boolean> => {
      setBusy(body.action);
      try {
        await epicAction(projectId, body);
        if (success) toast(success);
        void queryClient.invalidateQueries({ queryKey: ["board"] });
        return true;
      } catch (err) {
        toast.error(err instanceof ApiError ? err.message : "Action failed — check hub logs");
        return false;
      } finally {
        setBusy(null);
      }
    },
    [projectId],
  );
  return { act, busy };
}
