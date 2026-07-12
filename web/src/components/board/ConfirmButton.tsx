import * as React from "react";
import { Button, type ButtonProps } from "@/components/ui/button";

// Two-step inline confirm: first click arms for 3s ("Merge?"), second fires.
// Deliberate friction before merging/pausing from a phone (spec §6) without a
// modal round-trip. stopPropagation so cards don't open their drawer.
export function ConfirmButton({
  label, confirmLabel = "Confirm?", onConfirm, variant = "outline", size = "sm", disabled, className,
}: {
  label: string; confirmLabel?: string; onConfirm(): void;
  variant?: ButtonProps["variant"]; size?: ButtonProps["size"]; disabled?: boolean; className?: string;
}) {
  const [armed, setArmed] = React.useState(false);
  React.useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <Button
      variant={armed ? "destructive" : variant}
      size={size}
      disabled={disabled}
      className={className}
      onClick={(e) => {
        e.stopPropagation();
        if (armed) { setArmed(false); onConfirm(); } else { setArmed(true); }
      }}
    >
      {armed ? confirmLabel : label}
    </Button>
  );
}
