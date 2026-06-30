import type { KeyboardEvent } from "react";

// Props for a click-to-open row that may contain nested interactive controls
// (e.g. the inline rename editor). It activates only on the row's OWN Enter/Space
// keydown — `e.target === e.currentTarget` — so a keydown bubbling up from a child
// button or input (pencil / ✓ / ✕ / the name field) does NOT also fire onOpen.
export function rowActivation(onOpen: () => void) {
  return {
    role: "button" as const,
    tabIndex: 0,
    onClick: onOpen,
    onKeyDown: (e: KeyboardEvent) => {
      if (e.target !== e.currentTarget) return;
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onOpen();
      }
    },
  };
}
