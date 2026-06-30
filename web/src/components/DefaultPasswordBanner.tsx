import { useAuth } from "@/store/auth";
import { WarningBanner } from "@/components/WarningBanner";

// Shown while the operator is still using the shipped default password
// (admin / changeme123). Dismisses once they change it (Settings → ⚙).
export function DefaultPasswordBanner() {
  const mustChange = useAuth((s) => s.session?.mustChangePassword);
  if (!mustChange) return null;
  return (
    <WarningBanner label="Default password warning">
      <span className="font-semibold text-amber-700 dark:text-amber-400">You’re using the default password.</span>{" "}
      <span className="text-muted-foreground">Change it in Settings (⚙) to secure this hub.</span>
    </WarningBanner>
  );
}
