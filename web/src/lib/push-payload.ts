// Board push payload (hubd/internal/orchestrator/push.go dispatchBoardPush) and
// its notification/URL derivations. Pure so sw.ts stays untestable-but-trivial.
export interface EpicPush {
  type: "epic";
  project: string;
  epic_id: string;
  issue: number;
  title: string;
  needs: string;
  stage: string;
}

export function isEpicPush(d: unknown): d is EpicPush {
  if (!d || typeof d !== "object") return false;
  const p = d as Record<string, unknown>;
  return p.type === "epic" &&
    typeof p.project === "string" && p.project !== "" &&
    typeof p.epic_id === "string" && p.epic_id !== "" &&
    typeof p.issue === "number" && Number.isSafeInteger(p.issue) && p.issue > 0 &&
    typeof p.title === "string" &&
    typeof p.needs === "string" && typeof p.stage === "string";
}

export function epicNotification(p: EpicPush): { title: string; options: NotificationOptions } {
  return {
    title: `Epic #${p.issue} needs you`,
    options: {
      body: p.needs ? `${p.title} — ${p.needs}` : p.title,
      tag: `epic:${p.epic_id}`,
      data: p,
    },
  };
}

export function epicUrl(p: EpicPush): string {
  return `/projects/${encodeURIComponent(p.project)}?tab=board&epic=${encodeURIComponent(p.epic_id)}`;
}
