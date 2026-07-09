import type { Provider } from "@/lib/provider";
import { cn } from "@/lib/utils";

const LABELS: Record<Provider, string> = { claude: "Claude Code", codex: "Codex" };

// Muted inline tag naming the coding agent a session runs. Renders nothing when
// the provider is unknown, so callers never need to conditionalize. flex-none:
// the tag keeps its size — a long session name truncates first.
export function ProviderTag({ provider, className }: { provider?: Provider; className?: string }) {
  if (!provider) return null;
  return (
    <span
      title={LABELS[provider]}
      aria-label={LABELS[provider]}
      className={cn("flex-none text-xs text-muted-foreground", className)}
    >
      {provider}
    </span>
  );
}
