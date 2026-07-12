import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { TerminalView } from "@/components/TerminalView";
import { boardSessionsKey, listSessions } from "@/lib/api-client";
import type { EpicDTO, ProjectDTO, Session } from "@/lib/contracts";
import { themeOf } from "@/lib/terminal-themes";
import { usePrefs } from "@/store/prefs";

// Watch-only live preview (spec §8.3): the same TerminalView the grid uses,
// with an input-blocking overlay — keystrokes require Open full session, so a
// phone scroll can't type into a runner. active={false} keeps xterm from
// stealing focus while the drawer is open.
export function TerminalPreview({ project, epic, onOpenFull }: {
  project: ProjectDTO; epic: EpicDTO; onOpenFull(): void;
}) {
  const theme = usePrefs((s) => s.terminalTheme);
  // Query the project's TARGET, not just its server — the runner session lives
  // under that socket (Finding: non-default target would show "session ended").
  const q = useQuery({
    queryKey: boardSessionsKey(project.server_id, project.target),
    queryFn: () => listSessions(project.server_id, project.target || undefined),
  });
  const session: Session | undefined = q.data?.find(
    (s) => s.name === epic.session && (project.target === "" || s.target === project.target),
  );
  const pane = session?.windows[0]?.panes[0];

  return (
    <section className="flex flex-col gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        Live session{epic.session ? <span className="ml-1 font-mono normal-case">— {epic.session}</span> : null}
      </div>
      {q.isLoading ? (
        <div className="text-xs text-muted-foreground">Looking for the session…</div>
      ) : !session || !pane ? (
        <div className="text-xs text-muted-foreground">Session ended — nothing to preview.</div>
      ) : (
        <div className="relative h-56 overflow-hidden rounded-md border border-border">
          <TerminalView serverId={project.server_id} paneId={pane.id} target={session.target} active={false}
            fontSize={11} theme={themeOf(theme)} />
          <div className="absolute inset-0 z-10" aria-hidden onClick={onOpenFull} />
          <Button size="sm" className="absolute bottom-2 right-2 z-20" onClick={onOpenFull}>
            Open full session
          </Button>
        </div>
      )}
    </section>
  );
}
