import type { ReactNode } from "react";

// Full-width amber attention banner — shared chrome for the pending-agents and
// default-password banners (a region with a label; children supply the content).
export function WarningBanner({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div
      role="region"
      aria-label={label}
      className="border-b border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm"
    >
      {children}
    </div>
  );
}
