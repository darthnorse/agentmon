import * as React from "react";
import { Button } from "@/components/ui/button";
import { usePrefs } from "@/store/prefs";
import type { ThemeName } from "@/lib/terminal-themes";

const FONT_MIN = 8;
const FONT_MAX = 24;
const clampFont = (n: number) => Math.max(FONT_MIN, Math.min(FONT_MAX, n));

const THEMES: ReadonlyArray<{ value: ThemeName; label: string }> = [
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" },
  { value: "highContrast", label: "High contrast" },
];

// A small settings popover (gear button) bound to the persisted prefs store:
// terminal font sizes (desktop + mobile), theme, and the done-alert toggle.
export function SettingsPanel() {
  const [open, setOpen] = React.useState(false);
  const ref = React.useRef<HTMLDivElement>(null);

  const fontSizeDesktop = usePrefs((s) => s.fontSizeDesktop);
  const fontSizeMobile = usePrefs((s) => s.fontSizeMobile);
  const terminalTheme = usePrefs((s) => s.terminalTheme);
  const alertOnDone = usePrefs((s) => s.alertOnDone);
  const setFontSizeDesktop = usePrefs((s) => s.setFontSizeDesktop);
  const setFontSizeMobile = usePrefs((s) => s.setFontSizeMobile);
  const setTerminalTheme = usePrefs((s) => s.setTerminalTheme);
  const setAlertOnDone = usePrefs((s) => s.setAlertOnDone);

  // Close on outside click / Escape so the popover behaves like a menu.
  React.useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <Button
        variant="outline"
        size="sm"
        aria-label="Settings"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        ⚙
      </Button>
      {open && (
        <div
          role="dialog"
          aria-label="Settings"
          className="absolute right-0 z-50 mt-1 w-64 rounded-md border border-border bg-card p-3 text-sm shadow-md"
        >
          <FontStepper
            label="Desktop font"
            value={fontSizeDesktop}
            onChange={(n) => setFontSizeDesktop(clampFont(n))}
          />
          <FontStepper
            label="Mobile font"
            value={fontSizeMobile}
            onChange={(n) => setFontSizeMobile(clampFont(n))}
          />
          <div className="mb-3">
            <label htmlFor="settings-theme" className="mb-1 block text-xs font-medium text-muted-foreground">
              Terminal theme
            </label>
            <select
              id="settings-theme"
              value={terminalTheme}
              onChange={(e) => setTerminalTheme(e.target.value as ThemeName)}
              className="h-8 w-full rounded-md border border-input bg-background px-2 text-sm"
            >
              {THEMES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </div>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={alertOnDone}
              onChange={(e) => setAlertOnDone(e.target.checked)}
            />
            <span>Alert when a session finishes (done)</span>
          </label>
        </div>
      )}
    </div>
  );
}

function FontStepper({
  label, value, onChange,
}: {
  label: string;
  value: number;
  onChange(n: number): void;
}) {
  // Stable aria-labels ("Desktop font" → "desktop font") for the stepper buttons.
  const noun = label.toLowerCase();
  return (
    <div className="mb-3 flex items-center justify-between">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <span className="flex items-center gap-1">
        <Button variant="outline" size="sm" aria-label={`Decrease ${noun}`} onClick={() => onChange(value - 1)}>
          −
        </Button>
        <span className="w-6 text-center tabular-nums" aria-label={`${label} size`}>{value}</span>
        <Button variant="outline" size="sm" aria-label={`Increase ${noun}`} onClick={() => onChange(value + 1)}>
          +
        </Button>
      </span>
    </div>
  );
}
