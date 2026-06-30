import { useAuth } from "@/store/auth";

// Shown right after login while the operator is still using the shipped default
// password (admin / changeme123). Dismisses once they change it (Settings → ⚙).
export function DefaultPasswordBanner() {
  const mustChange = useAuth((s) => s.session?.mustChangePassword);
  if (!mustChange) return null;
  return (
    <div
      role="region"
      aria-label="Default password warning"
      className="border-b border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm"
    >
      <span className="font-semibold text-amber-700 dark:text-amber-400">You’re using the default password.</span>{" "}
      <span className="text-muted-foreground">Change it in Settings (⚙) to secure this hub.</span>
    </div>
  );
}
