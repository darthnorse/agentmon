import * as React from "react";
import { StateStream, type StateStreamDeps } from "@/lib/sse-state";
import { useSessionState } from "@/store/session-state";
import { stateKey, normalizeState } from "@/lib/state";
import { isAlertTransition } from "@/lib/alerts";
import { usePrefs } from "@/store/prefs";
import type { StateEventFrame } from "@/lib/contracts";

// Mounts one SSE stream for the authed session and pumps it into the store.
// Optional `onAttention` is invoked (after the store applies the delta) when a
// delta is an alerting transition for a non-focused key — into `blocked` always,
// and into `done` when `prefs.alertOnDone` is on (M9 Tier 1/2 + M11 done-too).
// The store reducer stays pure — detection (isAlertTransition) runs here in the
// wiring layer, never inside applyDelta. The callback is held in a ref so changing
// it does not tear down / re-open the single EventSource.
export function useStateStream(
  deps?: StateStreamDeps,
  onAttention?: (frame: StateEventFrame) => void,
): void {
  const onAttentionRef = React.useRef(onAttention);
  onAttentionRef.current = onAttention;

  React.useEffect(() => {
    const s = useSessionState.getState();
    const stream = new StateStream(
      {
        onSnapshot: s.applySnapshot,
        onDelta: (frame) => {
          const key = stateKey(frame.server, frame.target, frame.session);
          // Capture the store once: read prev BEFORE applyDelta, then reuse the same
          // snapshot for focusedKey (applyDelta never mutates focusedKey).
          const store = useSessionState.getState();
          const prev = store.live.get(key);
          store.applyDelta(frame);
          const cb = onAttentionRef.current;
          // Read the done-too toggle at delta time (not at mount) so flipping the
          // pref in settings takes effect on the next delta without re-opening SSE.
          const alertOnDone = usePrefs.getState().alertOnDone;
          if (
            cb &&
            isAlertTransition(prev, normalizeState(frame.state), store.focusedKey, key, alertOnDone)
          ) {
            cb(frame);
          }
        },
        onOpen: () => useSessionState.getState().setConnected(true),
        onError: () => useSessionState.getState().setConnected(false),
      },
      deps,
    );
    stream.open();
    return () => stream.dispose();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
