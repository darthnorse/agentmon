import { useEffect, useRef, useState } from "react";

function read(): { height: number | undefined; keyboardOpen: boolean } {
  if (typeof window === "undefined" || !window.visualViewport) {
    return { height: undefined, keyboardOpen: false };
  }
  const vv = window.visualViewport;
  // Treat a large viewport shrink as the soft keyboard — but only when NOT pinch-zoomed
  // (scale === 1), so zoom / Safari-chrome changes don't false-positive.
  const keyboardOpen = vv.scale === 1 && window.innerHeight - vv.height > 120;
  return { height: vv.height, keyboardOpen };
}

// iOS overlays the soft keyboard instead of shrinking the page, so a full-height terminal
// hides behind it. visualViewport reports the truly-visible area: size the terminal to
// `height` to keep the cursor above the keyboard, and use `keyboardOpen` to show the key
// bar (incl. the close-keyboard button) only while it's up. Falls back gracefully where the
// API is absent (older browsers / SSR / jsdom).
export function useVisualViewport(): { height: number | undefined; keyboardOpen: boolean } {
  const [state, setState] = useState(read);
  const raf = useRef(0);

  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    // Coalesce the resize/scroll bursts iOS fires during the keyboard animation into one
    // state update per frame, and skip the update entirely when nothing changed.
    const update = () => {
      cancelAnimationFrame(raf.current);
      raf.current = requestAnimationFrame(() =>
        setState((prev) => {
          const next = read();
          return prev.height === next.height && prev.keyboardOpen === next.keyboardOpen ? prev : next;
        }),
      );
    };
    update();
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    return () => {
      cancelAnimationFrame(raf.current);
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  }, []);

  return state;
}
