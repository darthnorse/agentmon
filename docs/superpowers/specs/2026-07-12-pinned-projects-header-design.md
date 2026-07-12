# Pinned projects in the header — design

**Date:** 2026-07-12 · **Status:** approved (design) · **Size:** small (web-only)

## Goal

Cut the two-step "Projects → pick a project" navigation to one click for the
projects you're actively babysitting: pin a project and a chip appears in the
header that jumps straight to its board (`/projects/{id}?tab=board&epic=`).

## Decisions

- **Persistence: per-device (localStorage).** A new `prefs.ts` field, consistent
  with every other UI pref — pins on desktop need not match pins on the phone.
  (Cross-device sync was considered and deferred; it would need a hub prefs
  table + API and isn't worth it for a cheap-to-re-pin convenience.)
- **Pin control lives on the project's board header**, not the switcher. The
  switcher is a native `<select>` and can't hold clickable per-row icons without
  becoming a custom dropdown.
- **v1 renders chips in the home top-bar only** (the entry point). The `/projects`
  page already has the switcher, so chips there are a possible later extension.

## Units

1. **`prefs.ts`** — add `pinnedProjects: string[]` (project ids, pin order) +
   `togglePinnedProject(id: string)`. Pure store logic; persisted like the rest.
2. **`ProjectHeader`** — a star toggle (pin/unpin the project whose board is
   shown), beside the existing Run-issue / Plan-epics / Run-doctor controls.
3. **`PinnedProjects`** (new) — rendered in the home top-bar right after the
   "Projects" button. Resolves pinned ids → project name + per-project needs
   count from the cached projects query; each chip: name (truncated) + red
   needs-badge when that project has epics needing attention; click →
   `navigate({ to: "/projects/$projectId", search: { tab: "board", epic: "" } })`;
   an `×` unpins. Renders nothing when there are no pins or the orchestrator is
   dormant / the projects query is empty.

## Behavior & edges

- **Order / limit:** append on pin (pin order); soft cap ~6 to keep the bar
  clean. Chips wrap on desktop; horizontally-scrollable strip on mobile.
- **Deleted project:** a pinned id with no matching live project is filtered out
  on render and pruned from the store — never a dead chip.
- **Dormant orchestrator / no projects:** `PinnedProjects` renders nothing.

## Testing

- Unit: `togglePinnedProject` add/remove + idempotence; stale-id prune against a
  project list (pure).
- Component: a chip click navigates to `/projects/{id}?tab=board&epic=`; the `×`
  unpins; the star on `ProjectHeader` reflects and toggles pinned state.
- Follows the existing `prefs`/`board`/`ProjectHeader` test patterns.

## Out of scope (v1)

Cross-device sync; pins on the `/projects` header; manual reordering; a hard
pin cap with UI feedback.
