import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";
import { SessionNameEditor } from "@/components/SessionNameEditor";
import { useFocusedSeen } from "@/hooks/useFocusedSeen";
import { usePrefs } from "@/store/prefs";
import { themeOf } from "@/lib/terminal-themes";

export interface TerminalSearch { target: string; session: string; }

export function MobileTerminalRoute() {
  const { serverId, paneId } = useParams({ strict: false }) as { serverId: string; paneId: string };
  const { target, session } = useSearch({ strict: false }) as TerminalSearch;
  const navigate = useNavigate();
  const fontSize = usePrefs((s) => s.fontSizeMobile);
  const theme = themeOf(usePrefs((s) => s.terminalTheme));

  useFocusedSeen({ serverId, target, sessionName: session });

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b border-border px-2 py-2">
        <Button variant="ghost" size="sm" onClick={() => navigate({ to: "/" })}>‹ Back</Button>
        <div className="min-w-0">
          <SessionNameEditor
            className="font-medium"
            serverId={serverId}
            target={target}
            name={session}
            paneId={paneId}
            onRenamed={(to) =>
              navigate({ to: ".", search: (s) => ({ ...s, session: to }), replace: true })
            }
          />
          <div className="truncate text-xs text-muted-foreground">{serverId} · {paneId}</div>
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <TerminalView serverId={serverId} paneId={paneId} target={target} showKeyBar fontSize={fontSize} theme={theme} />
      </div>
    </div>
  );
}
