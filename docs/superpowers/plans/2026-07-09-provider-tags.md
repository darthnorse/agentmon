# Provider Tags + Sidebar Server-Dot Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tag every session surface with the coding agent it runs (`claude` / `codex`) and remove the state dot next to hostnames in the desktop sidebar.

**Architecture:** Web-only. A pure `providerOf(command)` helper maps the session's already-flowing `pane_current_command` to a provider; a tiny `ProviderTag` component renders it on four surfaces (sidebar rows, mobile home rows, mobile tabs, grid tile headers). The sidebar's server-header `StateDot` is deleted while its rollup computation stays (it drives blocked-first ordering).

**Tech Stack:** React 18 + TypeScript, Vite, vitest + @testing-library/react. Spec: `docs/superpowers/specs/2026-07-09-provider-tags-design.md`.

## Global Constraints

- Web-only: no changes under `agent/`, `hubd/`, or `shared/`.
- Detection is EXACT string match: `command === "claude" || command === "codex"`; anything else (incl. `node`) renders NO tag — never a wrong tag.
- Tag copy is lowercase `claude` / `codex`; tooltip/aria-label is `Claude Code` / `Codex`.
- `ProviderTag` renders `null` for `undefined` provider — callers never conditionalize.
- The sidebar `serverState` rollup and `sortBlockedFirst(visibleGroups, …)` MUST keep working after the header dot is removed.
- All test commands run from the repo root: `npm --prefix web test -- --run <path relative to web/>`.
- Commit after every task; message prefix `feat(web):` (docs-only edits inside a task ride the same commit). NO Co-Authored-By / AI-attribution trailers.

---

### Task 1: `providerOf` helper + `ProviderTag` component + README convention note

**Files:**
- Create: `web/src/lib/provider.ts`
- Create: `web/src/lib/provider.test.ts`
- Create: `web/src/components/ProviderTag.tsx`
- Create: `web/src/components/ProviderTag.test.tsx`
- Modify: `README.md` (one sentence, hooks section — see Step 7)

**Interfaces:**
- Consumes: nothing.
- Produces: `type Provider = "claude" | "codex"`, `providerOf(command: string | undefined): Provider | undefined` (from `@/lib/provider`); `ProviderTag({ provider?: Provider; className?: string })` React component (from `@/components/ProviderTag`). Tasks 2–5 use exactly these.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/provider.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { providerOf } from "@/lib/provider";

describe("providerOf", () => {
  it("maps the exact native process names", () => {
    expect(providerOf("claude")).toBe("claude");
    expect(providerOf("codex")).toBe("codex");
  });
  it("returns undefined for anything else (wrapper installs, shells, empty)", () => {
    expect(providerOf("node")).toBeUndefined();   // npm-wrapped install
    expect(providerOf("bash")).toBeUndefined();
    expect(providerOf("")).toBeUndefined();
    expect(providerOf(undefined)).toBeUndefined();
    expect(providerOf("claude-wrapper")).toBeUndefined(); // exact match only
  });
});
```

`web/src/components/ProviderTag.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ProviderTag } from "@/components/ProviderTag";

describe("ProviderTag", () => {
  it("renders the lowercase tag with a full-name label", () => {
    render(<ProviderTag provider="codex" />);
    const tag = screen.getByText("codex");
    expect(tag).toHaveAttribute("title", "Codex");
    expect(tag).toHaveAttribute("aria-label", "Codex");
  });
  it("renders claude with its full-name label", () => {
    render(<ProviderTag provider="claude" />);
    expect(screen.getByText("claude")).toHaveAttribute("title", "Claude Code");
  });
  it("renders nothing without a provider", () => {
    const { container } = render(<ProviderTag provider={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- --run src/lib/provider.test.ts src/components/ProviderTag.test.tsx`
Expected: FAIL — cannot resolve `@/lib/provider` / `@/components/ProviderTag`.

- [ ] **Step 3: Implement `web/src/lib/provider.ts`**

```ts
export type Provider = "claude" | "codex";

// pane_current_command is the pane's foreground process NAME. Fleet convention
// (README, hooks section): Claude Code and Codex are native installs, so the
// process is literally `claude` / `codex`. A wrapper install (npm → `node`)
// gets no tag — never a wrong one. Exact match, deliberately no heuristics.
export function providerOf(command: string | undefined): Provider | undefined {
  return command === "claude" || command === "codex" ? command : undefined;
}
```

- [ ] **Step 4: Implement `web/src/components/ProviderTag.tsx`**

```tsx
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `npm --prefix web test -- --run src/lib/provider.test.ts src/components/ProviderTag.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 6: Add the fleet-convention sentence to README.md**

In the hooks section (after the paragraph ending "Hook behavior is independent of the selected Codex model, including Sol."), add:

```markdown
The web UI tags each session `claude` / `codex` from the pane's foreground process
name. This expects the **native builds** — an npm-wrapped install runs as `node`
and shows no tag (the tag reappears once the session restarts under a native binary).
```

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/provider.ts web/src/lib/provider.test.ts web/src/components/ProviderTag.tsx web/src/components/ProviderTag.test.tsx README.md
git commit -m "feat(web): providerOf helper + ProviderTag component"
```

---

### Task 2: Sidebar — remove the server-header dot, tag session rows

**Files:**
- Modify: `web/src/components/Sidebar.tsx` (header at ~lines 59–62; row at ~lines 69–80)
- Modify: `web/src/components/SessionActionsMenu.tsx` (Props + idle-mode JSX)
- Modify: `web/src/components/Sidebar.test.tsx`

**Interfaces:**
- Consumes: `providerOf`, `Provider`, `ProviderTag` from Task 1.
- Produces: `SessionActionsMenu` gains optional prop `provider?: Provider` (rendered after the name). Nothing else changes shape.

- [ ] **Step 1: Update the existing header-dot tests and add the row-tag test (failing first)**

In `web/src/components/Sidebar.test.tsx`:

(a) In the fixture `byServer`, change `s2`'s session to a codex session so the new tag test has a target — replace its `command: "c"` (both the session-level and pane-level fields) with `command: "codex"`.

(b) Replace the body of the test `"rolls up server dots and sorts the blocked server first"` (rename it to `"sorts the server holding a blocked session first, without a header dot"`):

```tsx
it("sorts the server holding a blocked session first, without a header dot", () => {
  const rows = flattenSessions(servers, byServer);
  const stateOf = (r: SessionRow): SessionState => (r.server.id === "s2" ? "blocked" : "idle");
  render(<Sidebar servers={servers} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={stateOf} />);
  const headers = screen.getAllByText(/alpha|bravo/).map((n) => n.textContent);
  expect(headers[0]).toBe("bravo"); // blocked-first ordering still driven by the rollup
  // exactly the two SESSION dots remain — no server-header dots
  expect(screen.getAllByRole("img")).toHaveLength(2);
  expect(screen.getAllByRole("img", { name: "blocked" })).toHaveLength(1);
});
```

(c) Replace the two session-less tests' dot assertions:

```tsx
it("renders a session-less server without any dot", () => {
  const withIdle: ServerSummary[] = [
    { id: "s1", name: "alpha", labels: [], enabled: true },
    { id: "s3", name: "charlie", labels: [], enabled: true, state: "blocked" },
  ];
  const onlyS1 = { s1: byServer.s1 };
  const rows = flattenSessions(withIdle, onlyS1);
  render(<Sidebar servers={withIdle} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
  expect(screen.getByText("charlie")).toBeInTheDocument();
  // its REST state no longer paints a dot anywhere
  expect(screen.queryByRole("img", { name: "blocked" })).toBeNull();
});

it("renders a server with no sessions and no state with just its name", () => {
  const oneServer: ServerSummary[] = [{ id: "s9", name: "empty", labels: [], enabled: true }];
  render(<Sidebar servers={oneServer} rows={[]} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
  expect(screen.getByText("empty")).toBeInTheDocument();
  expect(screen.queryByRole("img")).toBeNull();
});
```

(d) Add the row-tag test:

```tsx
it("tags a codex session row and leaves non-agent rows untagged", () => {
  const rows = flattenSessions(servers, byServer); // s1 command "c", s2 command "codex"
  render(<Sidebar servers={servers} rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
  expect(screen.getByText("codex")).toHaveAttribute("title", "Codex");
  expect(screen.queryByText("claude")).toBeNull();
});
```

- [ ] **Step 2: Run to verify the new/changed tests fail**

Run: `npm --prefix web test -- --run src/components/Sidebar.test.tsx`
Expected: FAIL — header dots still render (img counts too high) and no `codex` text found.

- [ ] **Step 3: Implement**

In `web/src/components/SessionActionsMenu.tsx`: add to imports `import { ProviderTag } from "@/components/ProviderTag";` and `import type { Provider } from "@/lib/provider";`; add `provider?: Provider;` to `Props`; update the signature to

```tsx
export function SessionActionsMenu({ serverId, serverName, target, name, paneId, state, provider }: Props) {
```

then in the idle-mode JSX insert the tag directly after the name span:

```tsx
<span className="truncate">{name}</span>
<ProviderTag provider={provider} />
```

In `web/src/components/Sidebar.tsx`:

(a) Server header — remove the `StateDot`, keeping everything else:

```tsx
<div className="flex items-center gap-2 px-3 py-1">
  <span className="text-xs font-semibold uppercase text-muted-foreground">{serverName}</span>
</div>
```

(b) Session row — pass the provider through (add `import { providerOf } from "@/lib/provider";`):

```tsx
<SessionActionsMenu
  serverId={row.server.id}
  serverName={serverName}
  target={row.session.target}
  name={row.session.name}
  paneId={row.pane.id}
  state={stateOf(row)}
  provider={providerOf(row.session.command)}
/>
```

`StateDot` stays imported (session rows still use it). `serverState` stays computed (ordering).

- [ ] **Step 4: Run to verify pass**

Run: `npm --prefix web test -- --run src/components/Sidebar.test.tsx src/components/SessionActionsMenu.test.tsx`
Expected: PASS (SessionActionsMenu's existing tests must not regress — the new prop is optional).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/Sidebar.tsx web/src/components/SessionActionsMenu.tsx web/src/components/Sidebar.test.tsx
git commit -m "feat(web): tag sidebar session rows; drop server-header state dot"
```

---

### Task 3: Mobile home rows (SessionList)

**Files:**
- Modify: `web/src/components/SessionList.tsx` (row JSX at ~lines 84–93)
- Modify: `web/src/components/SessionList.test.tsx`

**Interfaces:**
- Consumes: `providerOf`, `ProviderTag` from Task 1.
- Produces: no interface changes.

- [ ] **Step 1: Write the failing test**

Append to `web/src/components/SessionList.test.tsx` (reuse the file's existing `servers`/`byServer`/render pattern; give one fixture session `command: "claude"` at the session level):

```tsx
it("tags a claude session row on the mobile list", () => {
  const claudeServer = {
    s1: [{ name: "tagged", server: "s1", target: "default", cwd: "/a", command: "claude",
      windows: [{ id: "@9", index: "0", name: "m", panes: [{ id: "%9", command: "claude", cwd: "/a" }] }] }],
  };
  const rows = flattenSessions(servers, claudeServer);
  render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={() => {}} stateOf={() => "idle"} />);
  expect(screen.getByText("claude")).toHaveAttribute("title", "Claude Code");
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `npm --prefix web test -- --run src/components/SessionList.test.tsx`
Expected: FAIL — no element with text `claude`.

- [ ] **Step 3: Implement**

In `web/src/components/SessionList.tsx`, add imports `import { ProviderTag } from "@/components/ProviderTag";` and `import { providerOf } from "@/lib/provider";`, then wrap the name line:

```tsx
<div className="min-w-0">
  <span className="flex items-center gap-1.5">
    <SessionNameEditor
      className="font-medium"
      serverId={row.server.id}
      target={row.session.target}
      name={row.session.name}
      paneId={row.pane.id}
    />
    <ProviderTag provider={providerOf(row.session.command)} />
  </span>
  <div className="text-xs text-muted-foreground">{row.server.name} · {row.session.cwd || "—"}</div>
</div>
```

- [ ] **Step 4: Run to verify pass**

Run: `npm --prefix web test -- --run src/components/SessionList.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SessionList.tsx web/src/components/SessionList.test.tsx
git commit -m "feat(web): provider tag on mobile home session rows"
```

---

### Task 4: Mobile tabs (SessionTab.provider + buildTabs + render)

**Files:**
- Modify: `web/src/components/MobileSessionTabs.tsx`
- Modify: `web/src/components/MobileSessionTabs.test.tsx`

**Interfaces:**
- Consumes: `providerOf`, `Provider`, `ProviderTag` from Task 1.
- Produces: `SessionTab` gains `provider?: Provider`; `buildTabs` fills it from `row.session.command`. The terminal route builds tabs via `buildTabs`, so it needs no change.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/components/MobileSessionTabs.test.tsx` (the file's `mkSession` already sets session-level `command: "claude"`):

```tsx
it("threads the provider from the resolved row into each tab", () => {
  const tabs = buildTabs(openAll, rows, current, idle);
  expect(tabs.map((t) => t.provider)).toEqual(["claude", "claude", "claude"]);
});

it("gives the synthetic first-paint tab no provider", () => {
  const tabs = buildTabs([], [], current, idle);
  expect(tabs[0].provider).toBeUndefined();
});

it("renders the tag inside a tab", () => {
  const tabs = buildTabs([open("%0")], rows, current, idle);
  render(<MobileSessionTabs tabs={tabs} onSwitch={() => {}} onClose={() => {}} />);
  expect(screen.getAllByText("claude").length).toBeGreaterThanOrEqual(1);
});
```

- [ ] **Step 2: Run to verify they fail**

Run: `npm --prefix web test -- --run src/components/MobileSessionTabs.test.tsx`
Expected: FAIL — `provider` is undefined on tabs where "claude" is expected, and no tag text renders.

- [ ] **Step 3: Implement**

In `web/src/components/MobileSessionTabs.tsx`:

(a) Imports: `import { ProviderTag } from "@/components/ProviderTag";` and `import { providerOf, type Provider } from "@/lib/provider";`

(b) `SessionTab` gains a field:

```ts
provider?: Provider;
```

(c) In `buildTabs`, add to the pushed tab object (the resolved-row branch only):

```ts
provider: providerOf(row.session.command),
```

(the synthetic `tabs.unshift({...})` fallback gets no `provider` — leave it as is).

(d) In the render, insert after the name span in BOTH branches (active span and inactive button):

```tsx
<span className="max-w-[8rem] truncate">{tab.name}</span>
<ProviderTag provider={tab.provider} />
```

- [ ] **Step 4: Run to verify pass**

Run: `npm --prefix web test -- --run src/components/MobileSessionTabs.test.tsx`
Expected: PASS (all pre-existing tab tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/MobileSessionTabs.tsx web/src/components/MobileSessionTabs.test.tsx
git commit -m "feat(web): provider tag on mobile session tabs"
```

---

### Task 5: Desktop grid tile headers + full verification

**Files:**
- Modify: `web/src/components/GridView.tsx` (props + header JSX at ~line 108)
- Modify: `web/src/components/DesktopShell.tsx`
- Modify: `web/src/components/GridView.test.tsx`

**Interfaces:**
- Consumes: `providerOf`, `Provider`, `ProviderTag` from Task 1; `paneIdentity` from `@/lib/pane-identity` (already imported by both files… GridView yes, DesktopShell add it).
- Produces: `GridView` gains optional prop `providers?: ReadonlyMap<string, Provider>` keyed by `paneIdentity(serverId, target, paneId)`; absent key = no tag. `DesktopShell` builds it from live rows.

- [ ] **Step 1: Write the failing test**

Append to `web/src/components/GridView.test.tsx` (follow the file's `usePanes` fixture pattern):

```tsx
it("tags a tile whose pane identity maps to a provider", () => {
  usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
  usePanes.getState().collapse();
  const providers = new Map([["s:default:%0", "codex" as const]]);
  render(<GridView providers={providers} />);
  expect(screen.getByText("codex")).toHaveAttribute("title", "Codex");
});
```

(Note: `paneIdentity(serverId, target, paneId)` formats as `s:default:%0` — mirror the existing GridView test keys; if the literal differs, build the key with `paneIdentity("s", "default", "%0")` instead of a string literal.)

- [ ] **Step 2: Run to verify it fails**

Run: `npm --prefix web test -- --run src/components/GridView.test.tsx`
Expected: FAIL — `providers` prop unknown / no `codex` text.

- [ ] **Step 3: Implement GridView**

(a) Imports: `import { ProviderTag } from "@/components/ProviderTag";` and `import type { Provider } from "@/lib/provider";`

(b) Props:

```tsx
export function GridView({ livePaneIds, readyServers, providers }: {
  livePaneIds?: Set<string>;
  readyServers?: Set<string>;
  providers?: ReadonlyMap<string, Provider>;
} = {}) {
```

(c) Header — after the `SessionNameEditor` (line ~108):

```tsx
<SessionNameEditor className="min-w-0" serverId={p.serverId} target={p.target} name={p.session} paneId={p.paneId} />
<ProviderTag provider={providers?.get(paneIdentity(p.serverId, p.target, p.paneId))} />
```

- [ ] **Step 4: Implement DesktopShell**

Imports: `import { paneIdentity } from "@/lib/pane-identity";`, `import { providerOf, type Provider } from "@/lib/provider";` Then:

```tsx
const providers = React.useMemo(() => {
  const m = new Map<string, Provider>();
  for (const row of rows) {
    const p = providerOf(row.session.command);
    if (p) m.set(paneIdentity(row.server.id, row.session.target, row.pane.id), p);
  }
  return m;
}, [rows]);
```

and pass it down:

```tsx
<GridView livePaneIds={livePaneIds} readyServers={readyServers} providers={providers} />
```

- [ ] **Step 5: Run the grid tests, then the FULL web suite**

Run: `npm --prefix web test -- --run src/components/GridView.test.tsx`
Expected: PASS.

Run: `npm --prefix web test -- --run`
Expected: PASS — all suites (≥333 pre-existing + the new ones), zero failures.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/GridView.tsx web/src/components/DesktopShell.tsx web/src/components/GridView.test.tsx
git commit -m "feat(web): provider tag on desktop grid tile headers"
```

---

## Self-review notes

- Spec coverage: detection helper (T1), ProviderTag (T1), README convention (T1), sidebar row + header-dot removal incl. ordering kept (T2), mobile home rows (T3), mobile tabs incl. synthetic-tab case (T4), grid tiles via DesktopShell lookup (T5), full-suite gate (T5). Out-of-scope items untouched.
- Types: `Provider`, `providerOf`, `ProviderTag`, `SessionTab.provider?`, `GridView.providers?: ReadonlyMap<string, Provider>` — names consistent across tasks.
