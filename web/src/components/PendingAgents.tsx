import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { listPending, approveServer, rejectServer } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import { Button } from "@/components/ui/button";
import type { PendingServer } from "@/lib/contracts";

// Admit UI. A full-width banner (its OWN row below the header, so it wraps cleanly
// on mobile rather than crowding the header bar) listing agents awaiting admission.
// Polls so a freshly-installed agent appears without a manual refresh; renders
// nothing when none are pending. The admit step is the trust gate — each row shows
// the hostname + dial URL + os/arch so approval is an informed decision.
export function PendingAgents() {
  const { data } = useQuery({
    queryKey: ["pending"],
    queryFn: listPending,
    refetchInterval: 20_000,
  });
  const pending = data ?? [];
  if (pending.length === 0) return null;

  return (
    <div
      className="border-b border-amber-500/40 bg-amber-500/10 px-4 py-2"
      role="region"
      aria-label="Agents pending approval"
    >
      <div className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400">
        {pending.length} agent{pending.length === 1 ? "" : "s"} pending approval
      </div>
      <ul className="flex flex-col gap-2">
        {pending.map((a) => (
          <PendingRow key={a.id} agent={a} />
        ))}
      </ul>
    </div>
  );
}

function PendingRow({ agent }: { agent: PendingServer }) {
  const [busy, setBusy] = React.useState(false);

  async function act(verb: "approve" | "reject", fn: () => Promise<void>) {
    setBusy(true);
    try {
      await fn();
      queryClient.invalidateQueries({ queryKey: ["pending"] });
      queryClient.invalidateQueries({ queryKey: ["servers"] }); // a newly-active server appears
      toast.success(`${agent.hostname} ${verb === "approve" ? "approved" : "rejected"}`);
    } catch {
      toast.error(`Could not ${verb} ${agent.hostname}`);
      setBusy(false); // leave the row in place to retry (success unmounts it via the refetch)
    }
  }

  const meta = [agent.url, agent.arch ? `${agent.os || "linux"}/${agent.arch}` : ""].filter(Boolean).join(" · ");
  return (
    <li className="flex flex-wrap items-center gap-2">
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">{agent.hostname}</div>
        <div className="truncate text-xs text-muted-foreground">{meta}</div>
      </div>
      <div className="flex flex-none gap-2">
        <Button size="sm" disabled={busy} onClick={() => act("approve", () => approveServer(agent.id))}>
          Approve
        </Button>
        <Button variant="outline" size="sm" disabled={busy} onClick={() => act("reject", () => rejectServer(agent.id))}>
          Reject
        </Button>
      </div>
    </li>
  );
}
