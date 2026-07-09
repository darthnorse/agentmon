# Provider Tags + Sidebar Server-Dot Removal — Design

**Date:** 2026-07-09
**Scope:** web-only (no agent, hub, or wire-schema changes)

## Goal

1. Show which coding agent — Claude Code or Codex — a session is running, on every session surface: desktop sidebar rows, mobile home rows, mobile terminal tabs, desktop grid tile headers.
2. Remove the state dot next to the hostname in the desktop sidebar. Per-session dots stay everywhere; the server header keeps only its name. Mobile already shows per-session dots only.

## Detection: exact command match (fleet convention)

`session.command` already flows tmux → agent → hub → web (it is the active pane's
`pane_current_command`). A pure helper derives the provider:

```ts
// web/src/lib/provider.ts
export type Provider = "claude" | "codex";
export function providerOf(command: string | undefined): Provider | undefined {
  return command === "claude" || command === "codex" ? command : undefined;
}
```

`undefined` renders nothing — a shell, vim, or anything else gets no tag.

**Fleet convention this relies on (documented in README):** Claude Code and Codex are
installed as **native builds**, so the tmux foreground process is named `claude` /
`codex`. An npm/wrapper install reports `node` and gets *no* tag (never a wrong tag).
The owner converted the fleet to native installs on 2026-07-09. A running session
launched under the old binary keeps reporting the old name until restarted.

**Rejected alternatives:** hook-derived provider (an `X-AgentMon-Provider` header baked
into the generated hook command at install time, stamped agent-side onto the session) is
authoritative and survives wrappers, but costs an agent+shared+web change plus a hooks
re-merge on every host. It remains the documented upgrade path if wrapper installs ever
return — the UI layer below is identical either way, only `providerOf`'s data source
changes. Payload sniffing at intake was rejected as fragile.

## `ProviderTag` component

`web/src/components/ProviderTag.tsx`, styled like `StateDot`'s sibling:

- Renders `null` for `undefined` provider.
- Otherwise a muted, small, lowercase text tag: `claude` / `codex`
  (`text-xs text-muted-foreground`, `flex-none` so the tag itself never shrinks or
  wraps — a long session name truncates first, the tag stays legible,
  `title` + `aria-label` = "Claude Code" / "Codex" for accessibility/tests).

## Surfaces

| Surface | File | Placement |
|---|---|---|
| Desktop sidebar row | `Sidebar.tsx` | after the session name, inside the name row, before `⋯` |
| Mobile home row | `SessionList.tsx` | after `SessionNameEditor`, same line |
| Mobile tab | `MobileSessionTabs.tsx` | after the tab name; `SessionTab` gains `provider?: Provider`, filled by `buildTabs` from the resolved row's `session.command`; the synthetic first-paint tab has none (no tag until the list loads) |
| Desktop grid tile header | `GridView.tsx` | after "server · session"; `DesktopShell` builds a pane-identity → provider lookup from live rows and passes it down, so tags stay live as rows refetch |

## Sidebar server-dot removal

Remove the `StateDot` from the server header row in `Sidebar.tsx` (name only). The
`serverState` **rollup computation stays** — it still drives blocked-first ordering of
the server groups. Session-less servers show just their name.

## Testing

- `providerOf`: `claude`, `codex`, `node`, `""`, `undefined`.
- `ProviderTag`: renders the label for each provider; renders nothing for `undefined`.
- One assertion per surface: tag present for a codex row, absent for a shell (`bash`) row.
- `buildTabs`: provider threaded from row; synthetic tab has none.
- `Sidebar`: server header contains no dot; blocked-first group ordering unchanged.

## Out of scope

- Hook-derived provider (upgrade path, above).
- Duplicate tmux session names (tmux forbids them per socket; same-cwd sessions with
  distinct names are the supported way to run both agents on one project).
- Any replacement indicator on the server header (e.g. blocked counts) — YAGNI.
