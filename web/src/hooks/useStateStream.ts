import * as React from "react";
import { StateStream, type StateStreamDeps } from "@/lib/sse-state";
import { useSessionState } from "@/store/session-state";

// Mounts one SSE stream for the authed session and pumps it into the store.
export function useStateStream(deps?: StateStreamDeps): void {
  React.useEffect(() => {
    const s = useSessionState.getState();
    const stream = new StateStream(
      {
        onSnapshot: s.applySnapshot,
        onDelta: s.applyDelta,
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
