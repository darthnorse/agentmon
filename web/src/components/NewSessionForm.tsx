import * as React from "react";
import { createSession, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Session, CreateSessionRequest } from "@/lib/contracts";

// Client-side mirror of shared.ValidateSessionName (^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$).
// The hub + agent re-validate; this only gates the submit button for fast feedback.
const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
export const isValidSessionName = (name: string): boolean => NAME_RE.test(name);

// The last path segment of a directory, used to suggest a default name (§9.5).
const baseName = (p: string): string => {
  const parts = p.trim().replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] ?? "";
};

interface Props {
  serverId: string;
  target: string;
  onCreated: (session: Session) => void;
}

export function NewSessionForm({ serverId, target, onCreated }: Props) {
  const [name, setName] = React.useState("");
  const [nameEdited, setNameEdited] = React.useState(false);
  const [cwd, setCwd] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  void target; // accepted for symmetry with the open path; the hub derives the agent target separately

  const nameOk = isValidSessionName(name);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!nameOk || busy) return;
    setError(null);
    setBusy(true);
    const body: CreateSessionRequest = { name };
    if (cwd.trim()) body.cwd = cwd.trim();
    try {
      const session = await createSession(serverId, body);
      setName("");
      setNameEdited(false);
      setCwd("");
      onCreated(session);
    } catch (err) {
      const status = err instanceof ApiError ? err.status : (err as { status?: number }).status;
      if (status === 409) {
        setError("A session with that name already exists.");
      } else {
        setError((err as Error).message || "Failed to create session.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor="new-session-name">Name</Label>
        <Input
          id="new-session-name"
          value={name}
          autoComplete="off"
          spellCheck={false}
          placeholder="dockmon"
          onChange={(e) => { setName(e.target.value); setNameEdited(true); setError(null); }}
        />
        {name && !nameOk && (
          <p className="text-xs text-muted-foreground">
            Letters, digits, “_” and “-” only; must start with a letter or digit (max 64).
          </p>
        )}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="new-session-cwd">Directory (optional)</Label>
        <Input
          id="new-session-cwd"
          value={cwd}
          autoComplete="off"
          spellCheck={false}
          placeholder="defaults to the agent's home"
          onChange={(e) => {
            const v = e.target.value;
            setCwd(v);
            setError(null);
            // Suggest the directory basename as the name until the user edits it (§9.5).
            if (!nameEdited) setName(baseName(v));
          }}
        />
      </div>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <Button type="submit" size="sm" disabled={!nameOk || busy}>
        {busy ? "Creating…" : "Create session"}
      </Button>
    </form>
  );
}
