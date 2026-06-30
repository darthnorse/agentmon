import { useEffect, useState } from "react";

function read(): { height: number | undefined; keyboardOpen: boolean } {
  const vv = window.visualViewport;
  if (!vv) return { height: undefined, keyboardOpen: false };
  // The soft keyboard (and its accessory bar) occupies the gap between the layout
  // viewport (window.innerHeight, unchanged by the keyboard on iOS) and the visible
  // visual viewport. A gap > 120px ⇒ the keyboard is up.
  return { height: vv.height, keyboardOpen: window.innerHeight - vv.height > 120 };
}

// iOS overlays the soft keyboard instead of shrinking the page, so a full-height terminal
// hides behind it. visualViewport reports the truly-visible area: size the terminal to
// `height` to keep the cursor above the keyboard, and use `keyboardOpen` to show the
// close-keyboard button only while it's up. Falls back gracefully where the API is absent.
export function useVisualViewport(): { height: number | undefined; keyboardOpen: boolean } {
  const [state, setState] = useState(read);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => setState(read());
    update();
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update); // iOS scrolls the visual viewport as the keyboard animates
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  }, []);
  return state;
}
