import * as React from "react";
import { StateStream, type StateStreamDeps } from "@/lib/sse-state";
import { useSessionState } from "@/store/session-state";
import { stateKey, normalizeState } from "@/lib/state";
import { isAttentionTransition } from "@/lib/alerts";
import type { StateEventFrame } from "@/lib/contracts";

// Mounts one SSE stream for the authed session and pumps it into the store.
// Optional `onAttention` is invoked (after the store applies the delta) when a
// delta is a pure attention-transition into `blocked` for a non-focused key
// (M9 Tier 1/2). The store reducer stays pure — detection (isAttentionTransition)
// runs here in the wiring layer, never inside applyDelta. The callback is held in
// a ref so changing it does not tear down / re-open the single EventSource.
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
          if (
            cb &&
            isAttentionTransition(prev, normalizeState(frame.state), store.focusedKey, key)
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
