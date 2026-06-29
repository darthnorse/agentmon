import * as React from "react";
import { useSessionState } from "@/store/session-state";
import { stateKey } from "@/lib/state";
import { postSeen } from "@/lib/api-client";
import type { SeenRequest } from "@/lib/contracts";

// Marks the actively-viewed session focused (continuous done-suppression) + seen,
// and persists via POST /seen (best-effort). Passing null clears the focus.
export function useFocusedSeen(req: SeenRequest | null): void {
  const key = req ? stateKey(req.serverId, req.target, req.sessionName) : null;
  const reqRef = React.useRef(req);
  reqRef.current = req;
  // useLayoutEffect (not useEffect): apply the focus/seen mask BEFORE paint so the
  // newly-focused tile never paints a one-frame `done` dot before suppression lands.
  // The POST stays an async fire-and-forget side effect.
  React.useLayoutEffect(() => {
    const store = useSessionState.getState();
    if (!key) { store.setFocusedKey(null); return; }
    store.setFocusedKey(key);
    store.markSeen(key);
    void postSeen(reqRef.current!).catch(() => {});
    return () => { useSessionState.getState().setFocusedKey(null); };
  }, [key]);
}
