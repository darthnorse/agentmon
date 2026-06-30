import * as React from "react";
import type { TerminalController } from "@/hooks/useTerminalSession";
import type { BarKey } from "@/lib/keybar";
import { useVisualViewport } from "@/hooks/useVisualViewport";

const KEYS: { key: BarKey; label: string }[] = [
  { key: "esc", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "stab", label: "⇧Tab" },
  { key: "up", label: "↑" },
  { key: "down", label: "↓" },
  { key: "left", label: "←" },
  { key: "right", label: "→" },
  { key: "nl", label: "⏎ nl" },
  { key: "enter", label: "Enter" },
];

// §6.2 key bar, minus [Lock] (no read-only lock in M5). Single-row, horizontally
// scrollable. pointerDown.preventDefault keeps xterm's hidden textarea focused so
// the soft keyboard stays up.
export function MobileKeyBar({ controller }: { controller: TerminalController }) {
  const { keyboardOpen } = useVisualViewport();
  // Only show the key bar while the soft keyboard is up — when reading Claude's output the
  // terminal stays full-screen (and this avoids the bar being clipped at the bottom edge).
  if (!keyboardOpen) return null;

  const btn =
    "flex-none h-8 rounded-md border border-border bg-accent px-3 text-xs text-accent-foreground active:bg-primary/30";
  return (
    <div
      // No env(safe-area-inset-bottom) padding here: the bar only renders while the keyboard
      // is up (it floats above the keyboard), so the home-indicator reservation is dead space
      // that just opens a gap between this bar and iOS's keyboard accessory bar.
      className="flex items-stretch gap-1 border-t border-border bg-card p-1.5"
      onPointerDown={(e) => { if ((e.target as HTMLElement).tagName === "BUTTON") e.preventDefault(); }}
    >
      {/* Single-press "close keyboard" — pinned left (outside the scroll). Blurs xterm's
          textarea → the keyboard drops (and this bar hides) for full-screen reading. */}
      <button
        className={`${btn} !bg-primary !text-primary-foreground`}
        aria-label="Close keyboard"
        onClick={() => controller.dismissKeyboard()}
      >
        ⌨▾
      </button>
      <div className="flex flex-nowrap gap-1 overflow-x-auto" style={{ touchAction: "pan-x" }}>
        <button
          className={`${btn} ${controller.ctrlArmed ? "!bg-primary !text-primary-foreground" : ""}`}
          aria-pressed={controller.ctrlArmed}
          onClick={() => controller.toggleCtrl()}
        >
          Ctrl
        </button>
        {KEYS.map(({ key, label }) => (
          <button key={key} className={btn} onClick={() => controller.sendKey(key)}>
            {label}
          </button>
        ))}
        <button className={btn} onClick={() => void controller.paste()}>Paste</button>
        <button className={btn} onClick={() => void controller.copy()}>Copy</button>
      </div>
    </div>
  );
}
