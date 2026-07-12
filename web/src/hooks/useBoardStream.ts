import * as React from "react";
import { BoardStream, type BoardStreamDeps } from "@/lib/board-stream";
import type { BoardDeltaFrame } from "@/lib/contracts";
import { queryClient } from "@/lib/query-client";
import { needsAttention, useBoardAttention } from "@/store/board";

const INVALIDATE_DEBOUNCE_MS = 300;

// One app-wide subscription (mounted in AuthLayout, like useStateStream).
// Approach A (spec D7): the stream never patches board state — the snapshot
// seeds the attention store, and deltas just invalidate the ["board"] queries.
export function useBoardStream(deps?: BoardStreamDeps, onAttention?: (f: BoardDeltaFrame) => void): void {
  const onAttentionRef = React.useRef(onAttention);
  onAttentionRef.current = onAttention;

  React.useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const invalidateSoon = () => {
      if (timer) return;
      timer = setTimeout(() => {
        timer = null;
        void queryClient.invalidateQueries({ queryKey: ["board"] });
      }, INVALIDATE_DEBOUNCE_MS);
    };
    const stream = new BoardStream(
      {
        onSnapshot: (s) => {
          useBoardAttention.getState().applySnapshot(s.epics);
          // Reconnect catch-up: deltas may have been missed while down.
          void queryClient.invalidateQueries({ queryKey: ["board"] });
        },
        onDelta: (f) => {
          useBoardAttention.getState().applyDelta(f);
          invalidateSoon();
          if (needsAttention(f.stage)) onAttentionRef.current?.(f);
        },
        onOpen: () => useBoardAttention.getState().setConnected(true),
        onError: () => useBoardAttention.getState().setConnected(false),
      },
      deps,
    );
    stream.open();
    // Visibility-resume (parity with ws-terminal.ts:72): a backgrounded PWA may
    // have missed deltas even if the EventSource never fully dropped. On resume,
    // refetch the board so the UI can't sit on stale state. (When the browser
    // DID drop the connection, native reconnect also replays the snapshot.)
    const onVisible = () => {
      if (document.visibilityState === "visible") {
        void queryClient.invalidateQueries({ queryKey: ["board"] });
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisible);
      stream.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
