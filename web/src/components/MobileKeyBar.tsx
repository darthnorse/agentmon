import * as React from "react";
import type { TerminalController } from "@/hooks/useTerminalSession";
import type { BarKey } from "@/lib/keybar";
import { useVisualViewport } from "@/hooks/useVisualViewport";

// The scrollable bar, in order. `key` items send a byte; `ctrl` toggles sticky
// Ctrl; `copy`/`paste` hit the clipboard. Enter is intentionally absent — the soft
// keyboard's return key already submits; the bar provides Nl (a soft newline).
type BarItem =
  | { kind: "key"; key: BarKey; label: string }
  | { kind: "ctrl" }
  | { kind: "copy" }
  | { kind: "paste" };

const BAR: BarItem[] = [
  { kind: "key", key: "tab", label: "Tab" },
  { kind: "key", key: "nl", label: "⏎ Nl" },
  { kind: "key", key: "esc", label: "Esc" },
  { kind: "key", key: "up", label: "↑" },
  { kind: "key", key: "down", label: "↓" },
  { kind: "key", key: "left", label: "←" },
  { kind: "key", key: "right", label: "→" },
  { kind: "copy" },
  { kind: "paste" },
  { kind: "ctrl" },
  { kind: "key", key: "stab", label: "⇧Tab" },
];

const BTN =
  "flex-none h-8 rounded-md border border-border bg-accent px-3 text-xs text-accent-foreground active:bg-primary/30";

// §6.2 key bar, minus [Lock] (no read-only lock in M5). Single-row, horizontally
// scrollable. Every button preventDefaults mousedown so it never steals focus from
// xterm's hidden textarea, and every non-close action re-asserts terminal focus —
// so the soft keyboard stays up for the whole bar. Only ⌨▾ (Close keyboard) blurs.
export function MobileKeyBar({ controller }: { controller: TerminalController }) {
  const { keyboardOpen } = useVisualViewport();
  // Only show the key bar while the soft keyboard is up — when reading Claude's output the
  // terminal stays full-screen (and this avoids the bar being clipped at the bottom edge).
  if (!keyboardOpen) return null;

  // preventDefault on mousedown keeps the tap from moving focus off the terminal
  // (pointerdown.preventDefault is unreliable for this on iOS Safari).
  const keepFocus = (e: React.MouseEvent) => e.preventDefault();

  // Wrap every non-close action so focus returns to the terminal → keyboard stays up.
  const withFocus = (fn: () => void) => () => { fn(); controller.focusTerminal(); };

  return (
    <div className="flex items-stretch gap-1 border-t border-border bg-card p-1.5">
      {/* Single-press "close keyboard" — pinned left (outside the scroll). Blurs xterm's
          textarea → the keyboard drops (and this bar hides) for full-screen reading. It is
          the ONLY control that dismisses the keyboard; it does not re-focus. */}
      <button
        className={`${BTN} !bg-primary !text-primary-foreground`}
        aria-label="Close keyboard"
        onMouseDown={keepFocus}
        onClick={() => controller.dismissKeyboard()}
      >
        ⌨▾
      </button>
      <div className="flex flex-nowrap gap-1 overflow-x-auto" style={{ touchAction: "pan-x" }}>
        {BAR.map((item, i) => {
          if (item.kind === "ctrl") {
            return (
              <button
                key="ctrl"
                className={`${BTN} ${controller.ctrlArmed ? "!bg-primary !text-primary-foreground" : ""}`}
                aria-pressed={controller.ctrlArmed}
                onMouseDown={keepFocus}
                onClick={withFocus(() => controller.toggleCtrl())}
              >
                Ctrl
              </button>
            );
          }
          if (item.kind === "copy") {
            return (
              <button key="copy" className={BTN} onMouseDown={keepFocus} onClick={withFocus(() => void controller.copy())}>
                Copy
              </button>
            );
          }
          if (item.kind === "paste") {
            return (
              <button key="paste" className={BTN} onMouseDown={keepFocus} onClick={withFocus(() => void controller.paste())}>
                Paste
              </button>
            );
          }
          return (
            <button key={`${item.key}-${i}`} className={BTN} onMouseDown={keepFocus} onClick={withFocus(() => controller.sendKey(item.key))}>
              {item.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
