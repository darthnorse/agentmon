import * as React from "react";
import { toast } from "sonner";
import { changePassword, ApiError } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";

const MIN_LEN = 8;

// Change the logged-in user's password from the Settings menu. On success it clears
// the default-password nudge and resets the form.
export function ChangePasswordForm() {
  const clearMustChange = useAuth((s) => s.clearMustChangePassword);
  const [current, setCurrent] = React.useState("");
  const [next, setNext] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const mismatch = next.length > 0 && next !== confirm;
  const valid = current.length > 0 && next.length >= MIN_LEN && next === confirm;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      await changePassword(current, next);
      clearMustChange();
      setCurrent("");
      setNext("");
      setConfirm("");
      toast.success("Password changed");
    } catch (err) {
      const status = err instanceof ApiError ? err.status : undefined;
      setError(
        status === 401
          ? "Current password is incorrect."
          : status === 400
            ? `New password must be at least ${MIN_LEN} characters.`
            : "Could not change password.",
      );
    } finally {
      setBusy(false);
    }
  }

  const input = "h-8 w-full rounded-md border border-input bg-background px-2 text-sm";
  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <span className="text-xs font-medium text-muted-foreground">Change password</span>
      <input className={input} type="password" autoComplete="current-password" placeholder="Current password"
        value={current} onChange={(e) => { setCurrent(e.target.value); setError(null); }} aria-label="Current password" />
      <input className={input} type="password" autoComplete="new-password" placeholder={`New password (min ${MIN_LEN})`}
        value={next} onChange={(e) => { setNext(e.target.value); setError(null); }} aria-label="New password" />
      <input className={input} type="password" autoComplete="new-password" placeholder="Confirm new password"
        value={confirm} onChange={(e) => { setConfirm(e.target.value); setError(null); }} aria-label="Confirm new password" />
      {mismatch && <span className="text-xs text-muted-foreground">Passwords don’t match.</span>}
      {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
      <Button type="submit" variant="outline" size="sm" disabled={!valid || busy}>
        {busy ? "Changing…" : "Change password"}
      </Button>
    </form>
  );
}
