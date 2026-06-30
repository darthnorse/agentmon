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
  const btn =
    "flex-none h-8 rounded-md border border-border bg-accent px-3 text-xs text-accent-foreground active:bg-primary/30";
  return (
    <div
      className="flex items-stretch gap-1 border-t border-border bg-card p-1.5"
      style={{ paddingBottom: "max(0.375rem, env(safe-area-inset-bottom))" }}
      onPointerDown={(e) => { if ((e.target as HTMLElement).tagName === "BUTTON") e.preventDefault(); }}
    >
      {/* Single-press "close keyboard" — pinned left (outside the scroll), only while the
          soft keyboard is up. Blurs xterm's textarea → the keyboard drops for full-screen reading. */}
      {keyboardOpen && (
        <button
          className={`${btn} !bg-primary !text-primary-foreground`}
          aria-label="Close keyboard"
          onClick={() => controller.dismissKeyboard()}
        >
          ⌨▾
        </button>
      )}
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
