# AgentMon M5 — Web SPA (login + session list + xterm.js terminal) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `web/` stubs with the real SPA: a user logs in, sees their servers/sessions, and opens a live `xterm.js` terminal through the M4 relay — desktop as a live tiled grid (each tile its own WS) with click-to-expand, mobile as one full-screen terminal + key bar.

**Architecture:** A Vite + React + TS client-rendered SPA. All protocol logic lives in three framework-free `lib/` modules (`api-client`, `ws-terminal`, `keybar`) that are unit-tested hard; React components are thin wrappers. xterm.js is isolated in one `XTerm` DOM wrapper, composed by a shared `useTerminalSession` hook used by both the desktop grid tile and the mobile terminal. The SPA conforms to M4's already-live, already-tested wire contract and changes no hub code.

**Tech Stack:** Vite 5.4, React 18.3, TypeScript 5.6, TanStack Router 1.170 + TanStack Query 5, Zustand 5, xterm.js 5.5 (+ fit/web-links/webgl addons), Tailwind 3.4 + shadcn/ui (Radix), Vitest 2 + Testing Library (jsdom).

## Global Constraints

- **Conform to the frozen M3/M4 wire contract; change no hub Go code.** Endpoints, headers, and the transparent WS protocol are fixed (spec §2). The SPA satisfies them, never alters them.
- **CSRF:** send `X-CSRF-Token` (the `csrfToken` from `login`/`me`) **only on mutating methods** (`POST/PUT/PATCH/DELETE`) and only when the token is non-empty. The single M5 mutation is logout. Never send it on `GET`.
- **Credentials:** every `fetch` uses `credentials: "same-origin"` (sends the session cookie). The terminal WS carries the cookie automatically; the browser sets `Origin` automatically — the SPA neither can nor needs to set `Origin`.
- **Input fidelity is inherited verbatim from `spike-0.5/static/index.html`** (proven on tmux 3.5a + Claude Code v2.1.195): forward `xterm.onData` bytes verbatim (UTF-8); **soft newline = LF `0x0a`**, **Enter = CR `0x0d`**; **Esc = lone `0x1b`**; **Tab = `0x09`**, **⇧Tab = `ESC[Z`**; **arrows DECCKM-aware** (`ESC O d` in app-cursor mode, else `ESC [ d`; up=A down=B right=C left=D); **sticky Ctrl** (`a–z`→`c-96`, `A–Z`→`c-64`, else `c & 0x1f`); **paste** via `term.paste()` (xterm owns bracketed-paste framing) with a confirm when multiline or >200 chars; **copy** via `term.getSelection()`.
- **Terminal WS treats every binary frame identically** = `term.write(bytes)`. The first binary frame (scrollback snapshot) is **not** special-cased; on (re)connect call `term.reset()` and the fresh snapshot repaints.
- **No read-only lock in M5** (product decision): hub stays `rw`; the §6.2 key bar omits `[Lock]`; input is always forwarded.
- **No session state** (blocked/done dots, blocked-first sorting): `shared.Session` has no `State` field until Phase 3. The list is flat + searchable only.
- **Desktop grid liveness invariant:** every visible tile holds its own live WS; **expand is in-state, not a route change** — the non-focused tiles stay mounted (`display:none`) so their sockets + scrollback survive. **Esc is never bound to collapse** (Esc must reach the terminal); a visible "⊟ grid" control collapses.
- **`GRID_TILE_CAP = 6`** — the client-side soft cap on simultaneously-live tiles (a single constant; surfacing it as a user pref is Phase-4). Opening beyond it warns and declines.
- **Always use the latest compatible version of every dependency** (standing user rule); resolve "latest" live via npm, do not pin from memory. Versions below are floors known compatible with Vite 5 / React 18 / Node 20.
- **TDD throughout; commit after every green task.** Tests live next to source as `*.test.ts(x)`; component tests are prop-driven where possible so they need no router/query providers.

Spec: `docs/superpowers/specs/2026-06-28-agentmon-m5-web-spa-design.md`.

---

## File structure

| File | Create/Modify | Responsibility |
|---|---|---|
| `web/package.json` | Modify | Add deps (tailwind/shadcn, vitest/TL, xterm addons, radix) + `test`/`test:run` scripts. |
| `web/vite.config.ts` | Modify | `@`→`src` alias; `/api` proxy `ws: true`; Vitest `test` block (jsdom + setup). |
| `web/tsconfig.json` | Modify | `@/*` path mapping. |
| `web/postcss.config.js` | Create | Tailwind + autoprefixer. |
| `web/tailwind.config.js` | Create | Content globs, dark theme tokens, `tailwindcss-animate`. |
| `web/components.json` | Create | shadcn config (so the canonical primitives drop in consistently). |
| `web/src/index.css` | Create | Tailwind directives + shadcn dark CSS variables + base resets (terminal-friendly). |
| `web/src/main.tsx` | Modify | `import "./index.css"`. |
| `web/src/test/setup.ts` | Create | jest-dom matchers; jsdom shims (`matchMedia`, `ResizeObserver`). |
| `web/src/lib/utils.ts` | Create | `cn()` classnames helper (shadcn). |
| `web/src/components/ui/{button,input,label,card}.tsx` | Create | Canonical shadcn primitives. |
| `web/src/lib/keybar.ts` | Create | Pure key→bytes encodings + sticky-Ctrl transform. |
| `web/src/lib/ws-terminal.ts` | Create | `TerminalSocket` transport + URL builder + reconnect backoff. |
| `web/src/lib/contracts.ts` | Modify | Add `ServerSummary`, `SessionInfo`. |
| `web/src/lib/api-client.ts` | Create | Typed `/api/v1` client; CSRF/credentials; `ApiError`. |
| `web/src/store/auth.ts` | Create | Zustand auth store (principal + csrf + bootstrap). |
| `web/src/store/panes.ts` | Create | Zustand desktop grid store (open tiles, focused id, soft cap). |
| `web/src/lib/use-media-query.ts` | Create | `useMediaQuery` (desktop/mobile breakpoint). |
| `web/src/components/LoginForm.tsx` | Create | Username/password form → `api.login`. |
| `web/src/components/SessionList.tsx` | Create | Flat searchable server→session list (mobile + presentational). |
| `web/src/components/XTerm.tsx` | Create | xterm.js DOM wrapper; imperative handle (write/fit/focus/appCursor/paste/copy/scroll). |
| `web/src/components/MobileKeyBar.tsx` | Create | §6.2 key bar (no Lock); calls controller. |
| `web/src/hooks/useTerminalSession.ts` | Create | Integration: wires `TerminalSocket`↔`XTerm`↔sticky-Ctrl; reconnect/resize. |
| `web/src/components/TerminalView.tsx` | Create | Composes XTerm + (optional) MobileKeyBar + reconnect banner; the reusable terminal. |
| `web/src/components/Sidebar.tsx` | Create | Desktop servers→sessions tree; opens grid tiles. |
| `web/src/components/GridView.tsx` | Create | Live tiled grid + in-state expand + soft cap. |
| `web/src/routes/login.tsx` | Modify | Login route component. |
| `web/src/routes/index.tsx` | Modify | `/` responsive shell (desktop grid / mobile list). |
| `web/src/routes/terminal.tsx` | Create | `/t/$serverId/$paneId` mobile full-screen terminal + switcher. |
| `web/src/router.tsx` | Modify | Auth guard (pathless layout route), routes, redirects. |
| `.github/workflows/ci.yml` | Modify | Web job runs `npm run test:run` before `npm run build`. |

---

## Task 1: Foundation — tooling, Tailwind + shadcn, Vitest, dev proxy, CI

**Files:**
- Modify: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/src/main.tsx`, `.github/workflows/ci.yml`
- Create: `web/postcss.config.js`, `web/tailwind.config.js`, `web/components.json`, `web/src/index.css`, `web/src/test/setup.ts`, `web/src/lib/utils.ts`, `web/src/components/ui/{button,input,label,card}.tsx`, `web/src/lib/smoke.test.ts`

**Interfaces:**
- Produces: `cn(...inputs: ClassValue[]): string` from `@/lib/utils`; shadcn `Button`, `Input`, `Label`, `Card`/`CardHeader`/`CardContent`/`CardFooter`/`CardTitle`/`CardDescription` from `@/components/ui/*`; npm scripts `test` (watch) and `test:run` (`vitest run`); the `@` alias resolving to `web/src`.

- [ ] **Step 1: Install dependencies** (resolve latest compatible live; floors shown)

```bash
cd web
npm install \
  class-variance-authority@^0.7 clsx@^2.1 tailwind-merge@^2.6 \
  @radix-ui/react-slot@^1.1 @radix-ui/react-label@^2.1 \
  @xterm/addon-web-links@^0.11 @xterm/addon-webgl@^0.18
npm install -D \
  tailwindcss@^3.4 postcss@^8.4 autoprefixer@^10.4 tailwindcss-animate@^1.0 \
  vitest@^2.1 jsdom@^25 @testing-library/react@^16.1 @testing-library/user-event@^14.5 \
  @testing-library/jest-dom@^6.6 @testing-library/dom@^10.4
```

- [ ] **Step 2: Add scripts to `web/package.json`**

In the `"scripts"` block add:

```json
    "test": "vitest",
    "test:run": "vitest run",
```

(Leave `"build": "tsc --noEmit && vite build"` as-is so typecheck stays gated.)

- [ ] **Step 3: `web/postcss.config.js`**

```js
export default {
  plugins: { tailwindcss: {}, autoprefixer: {} },
};
```

- [ ] **Step 4: `web/tailwind.config.js`**

```js
import animate from "tailwindcss-animate";

/** @type {import('tailwindcss').Config} */
export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: { DEFAULT: "hsl(var(--primary))", foreground: "hsl(var(--primary-foreground))" },
        accent: { DEFAULT: "hsl(var(--accent))", foreground: "hsl(var(--accent-foreground))" },
        destructive: { DEFAULT: "hsl(var(--destructive))", foreground: "hsl(var(--destructive-foreground))" },
        muted: { DEFAULT: "hsl(var(--muted))", foreground: "hsl(var(--muted-foreground))" },
        card: { DEFAULT: "hsl(var(--card))", foreground: "hsl(var(--card-foreground))" },
      },
      borderRadius: { lg: "var(--radius)", md: "calc(var(--radius) - 2px)", sm: "calc(var(--radius) - 4px)" },
    },
  },
  plugins: [animate],
};
```

- [ ] **Step 5: `web/components.json`** (shadcn config, dark base)

```json
{
  "$schema": "https://ui.shadcn.com/schema.json",
  "style": "default",
  "rsc": false,
  "tsx": true,
  "tailwind": { "config": "tailwind.config.js", "css": "src/index.css", "baseColor": "slate", "cssVariables": true },
  "aliases": { "components": "@/components", "utils": "@/lib/utils", "ui": "@/components/ui" }
}
```

- [ ] **Step 6: `web/src/index.css`** (Tailwind + dark shadcn tokens + terminal-friendly base)

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --background: 215 28% 9%;
  --foreground: 213 20% 84%;
  --card: 217 24% 12%;
  --card-foreground: 213 20% 84%;
  --primary: 217 84% 55%;
  --primary-foreground: 210 40% 98%;
  --accent: 217 19% 20%;
  --accent-foreground: 210 40% 98%;
  --destructive: 4 74% 60%;
  --destructive-foreground: 210 40% 98%;
  --muted: 217 19% 20%;
  --muted-foreground: 215 16% 62%;
  --border: 216 18% 22%;
  --input: 216 18% 22%;
  --ring: 217 84% 55%;
  --radius: 0.5rem;
}

* { border-color: hsl(var(--border)); }
html, body, #root { height: 100%; margin: 0; }
body {
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  font-family: -apple-system, system-ui, sans-serif;
  /* prevent mobile rubber-banding around the terminal */
  overscroll-behavior: none;
}
```

- [ ] **Step 7: `web/src/main.tsx` — import the stylesheet**

Add as the first import:

```tsx
import "./index.css";
```

- [ ] **Step 8: `web/src/test/setup.ts`**

```ts
import "@testing-library/jest-dom/vitest";

// jsdom lacks matchMedia; default to "not matched" (mobile-first components decide).
if (!window.matchMedia) {
  // @ts-expect-error minimal shim
  window.matchMedia = (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener: () => {}, removeEventListener: () => {},
    addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false,
  });
}

// jsdom lacks ResizeObserver (used by the terminal fit logic).
if (!globalThis.ResizeObserver) {
  // @ts-expect-error minimal shim
  globalThis.ResizeObserver = class {
    observe() {} unobserve() {} disconnect() {}
  };
}
```

- [ ] **Step 9: `web/vite.config.ts` — alias, WS proxy, Vitest block**

```ts
/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Dev: proxy API + terminal WS to a locally running hubd. Prod: this dev server is
// unused; `vite build` emits dist/ which hubd embeds and serves same-origin.
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: { outDir: "dist" },
  server: {
    proxy: {
      // ws:true so the terminal WS (/api/v1/.../io) upgrades through the dev proxy.
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: true, ws: true },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
```

- [ ] **Step 10: `web/tsconfig.json` — path mapping**

Add inside `compilerOptions`:

```json
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] },
```

- [ ] **Step 11: `web/src/lib/utils.ts`**

```ts
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
```

- [ ] **Step 12: shadcn primitives**

`web/src/components/ui/button.tsx`:

```tsx
import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground shadow hover:bg-primary/90",
        outline: "border border-input bg-background shadow-sm hover:bg-accent hover:text-accent-foreground",
        ghost: "hover:bg-accent hover:text-accent-foreground",
        destructive: "bg-destructive text-destructive-foreground shadow-sm hover:bg-destructive/90",
      },
      size: { default: "h-9 px-4 py-2", sm: "h-8 rounded-md px-3 text-xs", icon: "h-9 w-9" },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />;
  },
);
Button.displayName = "Button";

export { Button, buttonVariants };
```

`web/src/components/ui/input.tsx`:

```tsx
import * as React from "react";
import { cn } from "@/lib/utils";

const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      ref={ref}
      className={cn(
        "flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = "Input";

export { Input };
```

`web/src/components/ui/label.tsx`:

```tsx
import * as React from "react";
import * as LabelPrimitive from "@radix-ui/react-label";
import { cn } from "@/lib/utils";

const Label = React.forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root
    ref={ref}
    className={cn("text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70", className)}
    {...props}
  />
));
Label.displayName = LabelPrimitive.Root.displayName;

export { Label };
```

`web/src/components/ui/card.tsx`:

```tsx
import * as React from "react";
import { cn } from "@/lib/utils";

const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("rounded-xl border bg-card text-card-foreground shadow", className)} {...props} />
  ),
);
Card.displayName = "Card";

const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("flex flex-col space-y-1.5 p-6", className)} {...props} />
  ),
);
CardHeader.displayName = "CardHeader";

const CardTitle = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("font-semibold leading-none tracking-tight", className)} {...props} />
  ),
);
CardTitle.displayName = "CardTitle";

const CardDescription = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("text-sm text-muted-foreground", className)} {...props} />
  ),
);
CardDescription.displayName = "CardDescription";

const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => <div ref={ref} className={cn("p-6 pt-0", className)} {...props} />,
);
CardContent.displayName = "CardContent";

const CardFooter = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("flex items-center p-6 pt-0", className)} {...props} />
  ),
);
CardFooter.displayName = "CardFooter";

export { Card, CardHeader, CardFooter, CardTitle, CardDescription, CardContent };
```

- [ ] **Step 13: Write the foundation smoke test** — `web/src/lib/smoke.test.ts`

```ts
import { describe, it, expect } from "vitest";
import { cn } from "@/lib/utils";

describe("foundation", () => {
  it("cn merges and dedupes tailwind classes", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
    expect(cn("text-sm", false && "hidden", "font-medium")).toBe("text-sm font-medium");
  });
});
```

- [ ] **Step 14: Run the test (verify the runner + alias work)**

Run: `cd web && npm run test:run`
Expected: 1 passed (smoke).

- [ ] **Step 15: Verify typecheck + production build still pass**

Run: `cd web && npm run build`
Expected: `tsc --noEmit` clean and `vite build` emits `dist/` (Tailwind compiles, shadcn primitives typecheck).

- [ ] **Step 16: `.github/workflows/ci.yml` — run web tests before build**

Replace the `web` job's steps with:

```yaml
  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: "22" }
      - run: cd web && npm ci
      - run: cd web && npm run test:run
      - run: cd web && npm run build
```

- [ ] **Step 17: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.ts web/tsconfig.json \
  web/postcss.config.js web/tailwind.config.js web/components.json web/src/index.css \
  web/src/main.tsx web/src/test/setup.ts web/src/lib/utils.ts web/src/components/ui \
  web/src/lib/smoke.test.ts .github/workflows/ci.yml
git commit -m "chore(web): M5 foundation — Tailwind + shadcn primitives, Vitest, dev WS proxy, CI test step"
```

---

## Task 2: `lib/keybar.ts` — pure key→bytes encodings

**Files:**
- Create: `web/src/lib/keybar.ts`, `web/src/lib/keybar.test.ts`

**Interfaces:**
- Produces:
  - `utf8(s: string): Uint8Array`
  - `ESC: Uint8Array` (`[0x1b]`), `TAB: Uint8Array` (`[0x09]`), `ENTER: Uint8Array` (`[0x0d]`), `SOFT_NEWLINE: Uint8Array` (`[0x0a]`), `SHIFT_TAB: Uint8Array` (`ESC [ Z`)
  - `type ArrowDir = "up" | "down" | "left" | "right"`
  - `arrow(dir: ArrowDir, appCursor: boolean): Uint8Array`
  - `encodeCtrl(data: string): Uint8Array` — first char → control byte, rest literal
  - `type BarKey = "esc" | "tab" | "stab" | "up" | "down" | "left" | "right" | "nl" | "enter"`

- [ ] **Step 1: Write the failing test** — `web/src/lib/keybar.test.ts`

```ts
import { describe, it, expect } from "vitest";
import { utf8, ESC, TAB, ENTER, SOFT_NEWLINE, SHIFT_TAB, arrow, encodeCtrl } from "@/lib/keybar";

const bytes = (a: Uint8Array) => Array.from(a);

describe("keybar constants", () => {
  it("encodes the fixed keys exactly", () => {
    expect(bytes(ESC)).toEqual([0x1b]);
    expect(bytes(TAB)).toEqual([0x09]);
    expect(bytes(ENTER)).toEqual([0x0d]); // CR submits
    expect(bytes(SOFT_NEWLINE)).toEqual([0x0a]); // LF inserts newline without submitting
    expect(bytes(SHIFT_TAB)).toEqual([0x1b, 0x5b, 0x5a]); // ESC [ Z
  });
});

describe("arrow (DECCKM-aware)", () => {
  it("uses ESC[ in normal mode", () => {
    expect(bytes(arrow("up", false))).toEqual([0x1b, 0x5b, 0x41]); // ESC [ A
    expect(bytes(arrow("down", false))).toEqual([0x1b, 0x5b, 0x42]);
    expect(bytes(arrow("right", false))).toEqual([0x1b, 0x5b, 0x43]);
    expect(bytes(arrow("left", false))).toEqual([0x1b, 0x5b, 0x44]);
  });
  it("uses ESCO in application-cursor mode", () => {
    expect(bytes(arrow("up", true))).toEqual([0x1b, 0x4f, 0x41]); // ESC O A
  });
});

describe("encodeCtrl (sticky Ctrl)", () => {
  it("maps lowercase and uppercase to the same control byte", () => {
    expect(bytes(encodeCtrl("c"))).toEqual([0x03]); // Ctrl-C
    expect(bytes(encodeCtrl("C"))).toEqual([0x03]);
    expect(bytes(encodeCtrl("d"))).toEqual([0x04]);
    expect(bytes(encodeCtrl("l"))).toEqual([0x0c]);
  });
  it("masks non-letters with 0x1f", () => {
    expect(bytes(encodeCtrl("["))).toEqual([0x1b]); // 0x5b & 0x1f
  });
  it("sends the control byte then any trailing chars literally", () => {
    expect(bytes(encodeCtrl("cX"))).toEqual([0x03, 0x58]);
  });
});

describe("utf8", () => {
  it("encodes multibyte text", () => {
    expect(bytes(utf8("é"))).toEqual([0xc3, 0xa9]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- keybar`
Expected: FAIL (cannot resolve `@/lib/keybar`).

- [ ] **Step 3: Write `web/src/lib/keybar.ts`**

```ts
// Pure key → byte encodings, inherited verbatim from the Phase 0.5 spike
// (spike-0.5/static/index.html). Single source of truth for the key bar and
// any keyboard shortcut path. No DOM, no xterm — fully unit-testable.

const encoder = new TextEncoder();

export function utf8(s: string): Uint8Array {
  return encoder.encode(s);
}

export const ESC = Uint8Array.of(0x1b); // lone escape, flushed immediately
export const TAB = Uint8Array.of(0x09);
export const ENTER = Uint8Array.of(0x0d); // CR submits
export const SOFT_NEWLINE = Uint8Array.of(0x0a); // LF inserts a newline WITHOUT submitting
export const SHIFT_TAB = utf8("\x1b[Z"); // ESC [ Z

export type ArrowDir = "up" | "down" | "left" | "right";
const ARROW_FINAL: Record<ArrowDir, string> = { up: "A", down: "B", right: "C", left: "D" };

// DECCKM application-cursor-keys mode swaps the CSI introducer ESC[ → ESCO.
export function arrow(dir: ArrowDir, appCursor: boolean): Uint8Array {
  return utf8("\x1b" + (appCursor ? "O" : "[") + ARROW_FINAL[dir]);
}

// Sticky Ctrl: the first char of `data` becomes a control byte; any remaining
// chars are appended literally (matches the spike's onData handler).
export function encodeCtrl(data: string): Uint8Array {
  const c = data.charCodeAt(0);
  let ctrl: number;
  if (c >= 97 && c <= 122) ctrl = c - 96; // a-z -> 0x01..0x1a
  else if (c >= 65 && c <= 90) ctrl = c - 64; // A-Z
  else ctrl = c & 0x1f;
  const rest = data.slice(1);
  if (!rest) return Uint8Array.of(ctrl);
  const tail = utf8(rest);
  const out = new Uint8Array(1 + tail.length);
  out[0] = ctrl;
  out.set(tail, 1);
  return out;
}

export type BarKey = "esc" | "tab" | "stab" | "up" | "down" | "left" | "right" | "nl" | "enter";
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm run test:run -- keybar`
Expected: PASS (all keybar tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/keybar.ts web/src/lib/keybar.test.ts
git commit -m "feat(web): keybar — pure key→byte encodings (sticky Ctrl, DECCKM arrows, LF soft-newline)"
```

---

## Task 3: `lib/ws-terminal.ts` — terminal WS transport + reconnect

**Files:**
- Create: `web/src/lib/ws-terminal.ts`, `web/src/lib/ws-terminal.test.ts`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `interface TerminalTarget { serverId: string; paneId: string; target: string }`
  - `interface Loc { protocol: string; host: string }`
  - `buildTerminalURL(t: TerminalTarget, loc: Loc): string`
  - `nextDelay(attempt: number): number` — bounded backoff (base 1200ms, cap 10000ms)
  - `interface TerminalSocketHandlers { onData(bytes: Uint8Array): void; onOpen?(): void; onClose?(): void; onError?(): void }`
  - `interface TerminalSocketDeps { WebSocketCtor?: typeof WebSocket; loc?: Loc }`
  - `class TerminalSocket` with `constructor(t: TerminalTarget, h: TerminalSocketHandlers, deps?: TerminalSocketDeps)`, `open(): void`, `send(bytes: Uint8Array): void`, `resize(cols: number, rows: number): void`, `dispose(): void`, `readonly connected: boolean`

- [ ] **Step 1: Write the failing test** — `web/src/lib/ws-terminal.test.ts`

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { buildTerminalURL, nextDelay, TerminalSocket } from "@/lib/ws-terminal";

describe("buildTerminalURL", () => {
  it("builds a ws:// URL for http and escapes the target", () => {
    const url = buildTerminalURL(
      { serverId: "srv1", paneId: "%0", target: "my target" },
      { protocol: "http:", host: "localhost:5173" },
    );
    expect(url).toBe("ws://localhost:5173/api/v1/servers/srv1/panes/%250/io?target=my%20target");
  });
  it("builds a wss:// URL for https", () => {
    const url = buildTerminalURL(
      { serverId: "s", paneId: "%1", target: "default" },
      { protocol: "https:", host: "host" },
    );
    expect(url).toBe("wss://host/api/v1/servers/s/panes/%251/io?target=default");
  });
});

describe("nextDelay", () => {
  it("grows then caps", () => {
    expect(nextDelay(0)).toBe(1200);
    expect(nextDelay(1)).toBe(2400);
    expect(nextDelay(10)).toBe(10000); // capped
  });
});

// Minimal fake WebSocket the tests drive directly.
class FakeWS {
  static OPEN = 1;
  static instances: FakeWS[] = [];
  url: string;
  binaryType = "";
  readyState = 0;
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: { data: any }) => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeWS.instances.push(this);
  }
  send(data: any) { this.sent.push(data); }
  close() { this.readyState = 3; this.onclose && this.onclose(); }
  // test helpers
  fireOpen() { this.readyState = 1; this.onopen && this.onopen(); }
  fireMessage(data: any) { this.onmessage && this.onmessage({ data }); }
}

const target = { serverId: "s", paneId: "%0", target: "default" };
const loc = { protocol: "http:", host: "h" };

describe("TerminalSocket", () => {
  beforeEach(() => { FakeWS.instances = []; vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it("sends binary input and JSON resize frames", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    const ws = FakeWS.instances[0];
    ws.fireOpen();
    sock.send(Uint8Array.of(1, 2, 3));
    sock.resize(80, 24);
    expect(ws.sent[0]).toEqual(Uint8Array.of(1, 2, 3));
    expect(ws.sent[1]).toBe(JSON.stringify({ type: "resize", cols: 80, rows: 24 }));
  });

  it("delivers inbound binary frames to onData", () => {
    const got: Uint8Array[] = [];
    const sock = new TerminalSocket(target, { onData: (b) => got.push(b) }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    const ws = FakeWS.instances[0];
    ws.fireOpen();
    ws.fireMessage(Uint8Array.of(9, 9).buffer);
    expect(Array.from(got[0])).toEqual([9, 9]);
  });

  it("reconnects after an unexpected close", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    FakeWS.instances[0].fireOpen();
    FakeWS.instances[0].close(); // unexpected
    expect(FakeWS.instances.length).toBe(1);
    vi.advanceTimersByTime(1200);
    expect(FakeWS.instances.length).toBe(2); // reconnected
  });

  it("does not reconnect after dispose()", () => {
    const sock = new TerminalSocket(target, { onData: () => {} }, { WebSocketCtor: FakeWS as any, loc });
    sock.open();
    FakeWS.instances[0].fireOpen();
    sock.dispose();
    vi.advanceTimersByTime(20000);
    expect(FakeWS.instances.length).toBe(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- ws-terminal`
Expected: FAIL (cannot resolve `@/lib/ws-terminal`).

- [ ] **Step 3: Write `web/src/lib/ws-terminal.ts`**

```ts
// Thin transport over one WebSocket to the M4 relay. Decoupled from xterm via
// callbacks; the WebSocket constructor + location are injectable for tests.
// Transparent protocol: binary frames = raw terminal bytes (every inbound binary
// frame, including the first scrollback snapshot, goes to onData); a JSON text
// frame {type:"resize",cols,rows} is the only control frame we send.

export interface TerminalTarget {
  serverId: string;
  paneId: string;
  target: string;
}

export interface Loc {
  protocol: string;
  host: string;
}

export function buildTerminalURL(t: TerminalTarget, loc: Loc): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  const path =
    `/api/v1/servers/${encodeURIComponent(t.serverId)}` +
    `/panes/${encodeURIComponent(t.paneId)}/io` +
    `?target=${encodeURIComponent(t.target)}`;
  return `${scheme}//${loc.host}${path}`;
}

const BACKOFF_BASE = 1200;
const BACKOFF_CAP = 10000;

export function nextDelay(attempt: number): number {
  return Math.min(BACKOFF_CAP, BACKOFF_BASE * 2 ** attempt);
}

export interface TerminalSocketHandlers {
  onData(bytes: Uint8Array): void;
  onOpen?(): void;
  onClose?(): void;
  onError?(): void;
}

export interface TerminalSocketDeps {
  WebSocketCtor?: typeof WebSocket;
  loc?: Loc;
}

export class TerminalSocket {
  private ws: WebSocket | null = null;
  private disposed = false;
  private attempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly WS: typeof WebSocket;
  private readonly loc: Loc;
  private readonly url: string;

  constructor(
    private readonly target: TerminalTarget,
    private readonly handlers: TerminalSocketHandlers,
    deps: TerminalSocketDeps = {},
  ) {
    this.WS = deps.WebSocketCtor ?? WebSocket;
    this.loc = deps.loc ?? { protocol: location.protocol, host: location.host };
    this.url = buildTerminalURL(target, this.loc);
    this.onVisibility = this.onVisibility.bind(this);
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", this.onVisibility);
    }
  }

  get connected(): boolean {
    return !!this.ws && this.ws.readyState === 1;
  }

  open(): void {
    if (this.disposed) return;
    const ws = new this.WS(this.url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.handlers.onOpen?.();
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === "string") return; // relay sends no client control today
      this.handlers.onData(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onerror = () => {
      this.handlers.onError?.();
    };
    ws.onclose = () => {
      this.ws = null;
      this.handlers.onClose?.();
      this.scheduleReconnect();
    };
  }

  send(bytes: Uint8Array): void {
    if (this.ws && this.ws.readyState === 1) this.ws.send(bytes);
  }

  resize(cols: number, rows: number): void {
    if (this.ws && this.ws.readyState === 1) {
      this.ws.send(JSON.stringify({ type: "resize", cols, rows }));
    }
  }

  dispose(): void {
    this.disposed = true;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.onVisibility);
    }
    if (this.ws) {
      this.ws.onclose = null; // suppress reconnect on our own close
      this.ws.close();
      this.ws = null;
    }
  }

  private scheduleReconnect(): void {
    if (this.disposed || this.reconnectTimer) return;
    const delay = nextDelay(this.attempt++);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  private onVisibility(): void {
    if (this.disposed) return;
    if (document.visibilityState === "visible" && !this.connected) {
      if (this.reconnectTimer) {
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
      }
      this.open(); // wake → reconnect immediately
    }
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm run test:run -- ws-terminal`
Expected: PASS (URL build, backoff, send/resize, inbound binary, reconnect, dispose).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/ws-terminal.ts web/src/lib/ws-terminal.test.ts
git commit -m "feat(web): ws-terminal — transparent WS transport, resize control frame, bounded reconnect"
```

---

## Task 4: `lib/api-client.ts` + contracts — typed `/api/v1` client

**Files:**
- Create: `web/src/lib/api-client.ts`, `web/src/lib/api-client.test.ts`
- Modify: `web/src/lib/contracts.ts`

**Interfaces:**
- Consumes: `Session` from `@/lib/contracts` (existing).
- Produces (from `@/lib/contracts`): `interface ServerSummary { id: string; name: string; labels: string[]; enabled: boolean }`, `interface SessionInfo { principalId: string; username: string; displayName: string; csrfToken: string }`.
- Produces (from `@/lib/api-client`):
  - `class ApiError extends Error { status: number }`
  - `setCsrfToken(t: string): void`, `getCsrfToken(): string`
  - `login(username: string, password: string): Promise<SessionInfo>`
  - `logout(): Promise<void>`
  - `me(): Promise<SessionInfo>`
  - `listServers(): Promise<ServerSummary[]>`
  - `listSessions(serverId: string, target?: string): Promise<Session[]>`

- [ ] **Step 1: Extend `web/src/lib/contracts.ts`**

Append:

```ts
// Mirrors hubd registry.ServerSummary (browser-safe; no secrets).
export interface ServerSummary { id: string; name: string; labels: string[]; enabled: boolean; }
// Mirrors the hub's login/me JSON body.
export interface SessionInfo { principalId: string; username: string; displayName: string; csrfToken: string; }
```

- [ ] **Step 2: Write the failing test** — `web/src/lib/api-client.test.ts`

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { login, logout, me, listServers, listSessions, setCsrfToken, ApiError } from "@/lib/api-client";

function mockFetch(status: number, body: unknown) {
  // A 204/null-body status cannot carry a body in undici → pass null when no body.
  const hasBody = body !== undefined;
  return vi.fn(
    async () =>
      new Response(hasBody ? JSON.stringify(body) : null, {
        status,
        headers: hasBody ? { "Content-Type": "application/json" } : {},
      }),
  );
}

describe("api-client", () => {
  beforeEach(() => { setCsrfToken(""); });

  it("login POSTs credentials with same-origin and no CSRF header (token empty)", async () => {
    const f = mockFetch(200, { principalId: "p", username: "u", displayName: "U", csrfToken: "tok" });
    vi.stubGlobal("fetch", f);
    const info = await login("u", "pw");
    expect(info.csrfToken).toBe("tok");
    const [url, init] = f.mock.calls[0];
    expect(url).toBe("/api/v1/auth/login");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(init.body).toBe(JSON.stringify({ username: "u", password: "pw" }));
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
  });

  it("GET requests never carry a CSRF header", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await listServers();
    const init = f.mock.calls[0][1];
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBeUndefined();
    expect(init.method).toBe("GET");
  });

  it("logout (mutation) sends X-CSRF-Token when a token is set", async () => {
    const f = mockFetch(204, undefined);
    vi.stubGlobal("fetch", f);
    setCsrfToken("tok");
    await logout();
    const [url, init] = f.mock.calls[0];
    expect(url).toBe("/api/v1/auth/logout");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
  });

  it("listSessions escapes the target query", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    await listSessions("srv 1", "t/x");
    expect(f.mock.calls[0][0]).toBe("/api/v1/servers/srv%201/sessions?target=t%2Fx");
  });

  it("listSessions omits the query when no target", async () => {
    const f = mockFetch(200, []);
    vi.stubGlobal("fetch", f);
    await listSessions("s");
    expect(f.mock.calls[0][0]).toBe("/api/v1/servers/s/sessions");
  });

  it("throws ApiError with the status and parsed message on non-2xx", async () => {
    vi.stubGlobal("fetch", mockFetch(401, { error: "invalid credentials" }));
    await expect(me()).rejects.toMatchObject({ status: 401, message: "invalid credentials" });
    expect((await me().catch((e) => e)) instanceof ApiError).toBe(true);
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd web && npm run test:run -- api-client`
Expected: FAIL (cannot resolve `@/lib/api-client`).

- [ ] **Step 4: Write `web/src/lib/api-client.ts`**

```ts
import type { ServerSummary, SessionInfo, Session } from "@/lib/contracts";

const BASE = "/api/v1";

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// The HttpOnly session cookie is unreadable to JS; the hub returns the CSRF token
// in the login/me body. We hold it here and attach it to mutating requests.
let csrfToken = "";
export function setCsrfToken(t: string): void { csrfToken = t; }
export function getCsrfToken(): string { return csrfToken; }

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const init: RequestInit = { method, credentials: "same-origin", headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  if (MUTATING.has(method) && csrfToken) headers["X-CSRF-Token"] = csrfToken;

  const res = await fetch(BASE + path, init);
  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!res.ok) {
    const msg = (data && typeof data.error === "string" && data.error) || res.statusText || "request failed";
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const login = (username: string, password: string) =>
  request<SessionInfo>("POST", "/auth/login", { username, password });

export const logout = () => request<void>("POST", "/auth/logout");

export const me = () => request<SessionInfo>("GET", "/me");

export const listServers = () => request<ServerSummary[]>("GET", "/servers");

export const listSessions = (serverId: string, target?: string) =>
  request<Session[]>(
    "GET",
    `/servers/${encodeURIComponent(serverId)}/sessions` +
      (target ? `?target=${encodeURIComponent(target)}` : ""),
  );
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web && npm run test:run -- api-client`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api-client.ts web/src/lib/api-client.test.ts web/src/lib/contracts.ts
git commit -m "feat(web): api-client — typed /api/v1 client with CSRF-on-mutations + same-origin creds"
```

---

## Task 5: `store/auth.ts` — Zustand auth store

**Files:**
- Create: `web/src/store/auth.ts`, `web/src/store/auth.test.ts`

**Interfaces:**
- Consumes: `login`, `logout`, `me`, `setCsrfToken` from `@/lib/api-client`; `SessionInfo` from `@/lib/contracts`.
- Produces:
  - `type AuthStatus = "unknown" | "authed" | "anon"`
  - `useAuth` (Zustand store) with state `{ session: SessionInfo | null; status: AuthStatus }` and actions `signIn(username, password): Promise<void>`, `signOut(): Promise<void>`, `bootstrap(): Promise<void>`, `setSession(s: SessionInfo): void`, `clear(): void`.

- [ ] **Step 1: Write the failing test** — `web/src/store/auth.test.ts`

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("@/lib/api-client", () => ({
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
  setCsrfToken: vi.fn(),
}));

import { useAuth } from "@/store/auth";
import * as api from "@/lib/api-client";

const info = { principalId: "p", username: "u", displayName: "U", csrfToken: "tok" };

describe("auth store", () => {
  beforeEach(() => {
    useAuth.getState().clear();
    vi.clearAllMocks();
  });

  it("signIn stores the session and pushes the csrf token to the api client", async () => {
    (api.login as any).mockResolvedValue(info);
    await useAuth.getState().signIn("u", "pw");
    expect(useAuth.getState().status).toBe("authed");
    expect(useAuth.getState().session?.username).toBe("u");
    expect(api.setCsrfToken).toHaveBeenCalledWith("tok");
  });

  it("bootstrap → authed when me() resolves", async () => {
    (api.me as any).mockResolvedValue(info);
    await useAuth.getState().bootstrap();
    expect(useAuth.getState().status).toBe("authed");
  });

  it("bootstrap → anon when me() rejects", async () => {
    (api.me as any).mockRejectedValue(new Error("401"));
    await useAuth.getState().bootstrap();
    expect(useAuth.getState().status).toBe("anon");
    expect(useAuth.getState().session).toBeNull();
  });

  it("signOut clears and resets the csrf token", async () => {
    (api.logout as any).mockResolvedValue(undefined);
    useAuth.getState().setSession(info);
    await useAuth.getState().signOut();
    expect(useAuth.getState().status).toBe("anon");
    expect(api.setCsrfToken).toHaveBeenLastCalledWith("");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- auth`
Expected: FAIL (cannot resolve `@/store/auth`).

- [ ] **Step 3: Write `web/src/store/auth.ts`**

```ts
import { create } from "zustand";
import type { SessionInfo } from "@/lib/contracts";
import * as api from "@/lib/api-client";

export type AuthStatus = "unknown" | "authed" | "anon";

interface AuthState {
  session: SessionInfo | null;
  status: AuthStatus;
  setSession(s: SessionInfo): void;
  clear(): void;
  signIn(username: string, password: string): Promise<void>;
  signOut(): Promise<void>;
  bootstrap(): Promise<void>;
}

export const useAuth = create<AuthState>((set) => ({
  session: null,
  status: "unknown",
  setSession(s) {
    api.setCsrfToken(s.csrfToken);
    set({ session: s, status: "authed" });
  },
  clear() {
    api.setCsrfToken("");
    set({ session: null, status: "anon" });
  },
  async signIn(username, password) {
    const info = await api.login(username, password);
    api.setCsrfToken(info.csrfToken);
    set({ session: info, status: "authed" });
  },
  async signOut() {
    try {
      await api.logout();
    } finally {
      api.setCsrfToken("");
      set({ session: null, status: "anon" });
    }
  },
  async bootstrap() {
    try {
      const info = await api.me();
      api.setCsrfToken(info.csrfToken);
      set({ session: info, status: "authed" });
    } catch {
      api.setCsrfToken("");
      set({ session: null, status: "anon" });
    }
  },
}));
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm run test:run -- auth`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/store/auth.ts web/src/store/auth.test.ts
git commit -m "feat(web): auth store — session + csrf bootstrap via /me, sign in/out"
```

---

## Task 6: Auth routes + guard + LoginForm + SessionList

**Files:**
- Create: `web/src/components/LoginForm.tsx`, `web/src/components/LoginForm.test.tsx`, `web/src/components/SessionList.tsx`, `web/src/components/SessionList.test.tsx`
- Modify: `web/src/router.tsx`, `web/src/routes/login.tsx`, `web/src/routes/index.tsx`

**Interfaces:**
- Consumes: `useAuth` from `@/store/auth`; `ApiError` from `@/lib/api-client`; `Session`, `ServerSummary` from `@/lib/contracts`.
- Produces:
  - `LoginForm({ onSuccess }: { onSuccess: () => void })`
  - `interface SessionRow { server: ServerSummary; session: Session; pane: { id: string; command: string; cwd: string }; window: { id: string; index: string; name: string } }`
  - `flattenSessions(servers: ServerSummary[], byServer: Record<string, Session[]>): SessionRow[]`
  - `matchesQuery(row: SessionRow, q: string): boolean`
  - `SessionList({ rows, query, onQueryChange, onOpen }: { rows: SessionRow[]; query: string; onQueryChange(q: string): void; onOpen(row: SessionRow): void })`
  - Router: a pathless `auth` layout route running `bootstrap()` + redirecting to `/login` when not authed; `/login` redirects to `/` when authed.

- [ ] **Step 1: Write the failing LoginForm test** — `web/src/components/LoginForm.test.tsx`

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const signIn = vi.fn();
vi.mock("@/store/auth", () => ({
  useAuth: (sel: any) => sel({ signIn }),
}));

import { LoginForm } from "@/components/LoginForm";

describe("LoginForm", () => {
  beforeEach(() => { signIn.mockReset(); });

  it("submits credentials and calls onSuccess", async () => {
    signIn.mockResolvedValue(undefined);
    const onSuccess = vi.fn();
    render(<LoginForm onSuccess={onSuccess} />);
    await userEvent.type(screen.getByLabelText(/username/i), "patrik");
    await userEvent.type(screen.getByLabelText(/password/i), "secret");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(signIn).toHaveBeenCalledWith("patrik", "secret"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("shows an error message when sign in fails", async () => {
    signIn.mockRejectedValue(Object.assign(new Error("invalid credentials"), { status: 401 }));
    render(<LoginForm onSuccess={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/username/i), "x");
    await userEvent.type(screen.getByLabelText(/password/i), "y");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(await screen.findByText(/invalid credentials/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- LoginForm`
Expected: FAIL (cannot resolve `@/components/LoginForm`).

- [ ] **Step 3: Write `web/src/components/LoginForm.tsx`**

```tsx
import * as React from "react";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function LoginForm({ onSuccess }: { onSuccess: () => void }) {
  const signIn = useAuth((s) => s.signIn);
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await signIn(username, password);
      onSuccess();
    } catch (err) {
      const status = (err as { status?: number }).status;
      const msg = (err as Error).message || "sign in failed";
      // A 403 here almost always means a dev external_origin mismatch.
      setError(status === 403 ? `${msg} (check the hub's external_origin)` : msg);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>AgentMon</CardTitle>
          <CardDescription>Sign in to your hub</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">Username</Label>
              <Input id="username" autoComplete="username" value={username}
                onChange={(e) => setUsername(e.target.value)} autoFocus />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">Password</Label>
              <Input id="password" type="password" autoComplete="current-password" value={password}
                onChange={(e) => setPassword(e.target.value)} />
            </div>
            {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
```

- [ ] **Step 4: Run LoginForm test → pass**

Run: `cd web && npm run test:run -- LoginForm`
Expected: PASS.

- [ ] **Step 5: Write the failing SessionList test** — `web/src/components/SessionList.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionList, flattenSessions, matchesQuery, type SessionRow } from "@/components/SessionList";

const servers = [{ id: "s1", name: "aigallery", labels: [], enabled: true }];
const byServer = {
  s1: [{
    name: "demo-web", server: "s1", target: "default", cwd: "/home/dev/web", command: "claude",
    windows: [{ id: "@0", index: "0", name: "main", panes: [{ id: "%0", command: "claude", cwd: "/home/dev/web" }] }],
  }],
};

describe("flatten + filter", () => {
  it("flattens servers→sessions→first pane into rows", () => {
    const rows = flattenSessions(servers, byServer);
    expect(rows).toHaveLength(1);
    expect(rows[0].pane.id).toBe("%0");
    expect(rows[0].session.name).toBe("demo-web");
  });
  it("matchesQuery checks server, session name and cwd", () => {
    const row = flattenSessions(servers, byServer)[0];
    expect(matchesQuery(row, "aigall")).toBe(true);
    expect(matchesQuery(row, "demo")).toBe(true);
    expect(matchesQuery(row, "/web")).toBe(true);
    expect(matchesQuery(row, "nope")).toBe(false);
  });
});

describe("SessionList", () => {
  it("renders rows and fires onOpen", async () => {
    const rows = flattenSessions(servers, byServer);
    const onOpen = vi.fn();
    render(<SessionList rows={rows} query="" onQueryChange={() => {}} onOpen={onOpen} />);
    await userEvent.click(screen.getByText("demo-web"));
    expect(onOpen).toHaveBeenCalledWith(rows[0]);
  });
});
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd web && npm run test:run -- SessionList`
Expected: FAIL (cannot resolve `@/components/SessionList`).

- [ ] **Step 7: Write `web/src/components/SessionList.tsx`**

```tsx
import * as React from "react";
import type { Session, ServerSummary, Window, Pane } from "@/lib/contracts";
import { Input } from "@/components/ui/input";

export interface SessionRow {
  server: ServerSummary;
  session: Session;
  window: Pick<Window, "id" | "index" | "name">;
  pane: Pane;
}

// Each session is shown by its first pane (the session's primary terminal).
export function flattenSessions(
  servers: ServerSummary[],
  byServer: Record<string, Session[]>,
): SessionRow[] {
  const rows: SessionRow[] = [];
  for (const server of servers) {
    for (const session of byServer[server.id] ?? []) {
      const win = session.windows[0];
      const pane = win?.panes[0];
      if (!win || !pane) continue;
      rows.push({ server, session, window: { id: win.id, index: win.index, name: win.name }, pane });
    }
  }
  return rows;
}

export function matchesQuery(row: SessionRow, q: string): boolean {
  if (!q) return true;
  const hay = `${row.server.name} ${row.session.name} ${row.session.cwd} ${row.session.command}`.toLowerCase();
  return hay.includes(q.toLowerCase());
}

export function SessionList({
  rows, query, onQueryChange, onOpen,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  return (
    <div className="flex h-full flex-col">
      <div className="p-3">
        <Input placeholder="Search server, session, path…" value={query}
          onChange={(e) => onQueryChange(e.target.value)} aria-label="Search sessions" />
      </div>
      <ul className="flex-1 overflow-y-auto">
        {filtered.map((row) => (
          <li key={`${row.server.id}:${row.session.target}:${row.session.name}:${row.pane.id}`}>
            <button
              className="w-full border-b border-border px-4 py-3 text-left hover:bg-accent"
              onClick={() => onOpen(row)}
            >
              <div className="font-medium">{row.session.name}</div>
              <div className="text-xs text-muted-foreground">
                {row.server.name} · {row.session.cwd || "—"}
              </div>
            </button>
          </li>
        ))}
        {filtered.length === 0 && (
          <li className="px-4 py-6 text-center text-sm text-muted-foreground">No sessions</li>
        )}
      </ul>
    </div>
  );
}
```

- [ ] **Step 8: Run SessionList test → pass**

Run: `cd web && npm run test:run -- SessionList`
Expected: PASS.

- [ ] **Step 9: Rewrite `web/src/router.tsx` with the auth guard**

```tsx
import { createRootRoute, createRoute, createRouter, redirect, Outlet } from "@tanstack/react-router";
import { useAuth } from "@/store/auth";
import { LoginRoute } from "./routes/login";
import { ShellRoute } from "./routes/index";

const rootRoute = createRootRoute({ component: () => <Outlet /> });

async function ensureStatus(): Promise<void> {
  if (useAuth.getState().status === "unknown") await useAuth.getState().bootstrap();
}

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  beforeLoad: async () => {
    await ensureStatus();
    if (useAuth.getState().status === "authed") throw redirect({ to: "/" });
  },
  component: LoginRoute,
});

// Pathless layout route: everything under it requires auth.
const authRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "auth",
  beforeLoad: async () => {
    await ensureStatus();
    if (useAuth.getState().status !== "authed") throw redirect({ to: "/login" });
  },
  component: () => <Outlet />,
});

const indexRoute = createRoute({ getParentRoute: () => authRoute, path: "/", component: ShellRoute });

const routeTree = rootRoute.addChildren([loginRoute, authRoute.addChildren([indexRoute])]);
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router; }
}
```

- [ ] **Step 10: Rewrite `web/src/routes/login.tsx`**

```tsx
import { useNavigate } from "@tanstack/react-router";
import { LoginForm } from "@/components/LoginForm";

export function LoginRoute() {
  const navigate = useNavigate();
  return <LoginForm onSuccess={() => navigate({ to: "/" })} />;
}
```

- [ ] **Step 11: Rewrite `web/src/routes/index.tsx`** (interim shell: SessionList + logout; made responsive in Task 10)

```tsx
import * as React from "react";
import { useQuery, useQueries } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { listServers, listSessions } from "@/lib/api-client";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { SessionList, flattenSessions, type SessionRow } from "@/components/SessionList";
import type { Session } from "@/lib/contracts";

export function ShellRoute() {
  const navigate = useNavigate();
  const signOut = useAuth((s) => s.signOut);
  const [query, setQuery] = React.useState("");

  const serversQ = useQuery({ queryKey: ["servers"], queryFn: listServers });
  const servers = serversQ.data ?? [];

  const sessionQs = useQueries({
    queries: servers.map((s) => ({
      queryKey: ["sessions", s.id],
      queryFn: () => listSessions(s.id),
    })),
  });

  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);

  function open(row: SessionRow) {
    navigate({
      to: "/t/$serverId/$paneId",
      params: { serverId: row.server.id, paneId: row.pane.id },
      search: { target: row.session.target, session: row.session.name },
    });
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-border px-4 py-2">
        <span className="font-semibold">AgentMon</span>
        <Button variant="ghost" size="sm" onClick={() => signOut().then(() => navigate({ to: "/login" }))}>
          Sign out
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        <SessionList rows={rows} query={query} onQueryChange={setQuery} onOpen={open} />
      </div>
    </div>
  );
}
```

> Note: `navigate({ to: "/t/..." })` targets the route added in Task 8; until then the link 404s at runtime (the component still typechecks once Task 8 registers the route — if executing strictly in order, temporarily navigate to `"/"` and restore in Task 8).

- [ ] **Step 12: Typecheck + run all tests**

Run: `cd web && npm run test:run && npm run build`
Expected: tests green; `tsc --noEmit` clean (if the `/t` route is not yet registered, temporarily point `open()` at `{ to: "/" }`, then restore in Task 8 Step 8).

- [ ] **Step 13: Commit**

```bash
git add web/src/components/LoginForm.tsx web/src/components/LoginForm.test.tsx \
  web/src/components/SessionList.tsx web/src/components/SessionList.test.tsx \
  web/src/router.tsx web/src/routes/login.tsx web/src/routes/index.tsx
git commit -m "feat(web): auth routes + guard, LoginForm, searchable SessionList"
```

---

## Task 7: Terminal surface — XTerm wrapper, MobileKeyBar, useTerminalSession, TerminalView

**Files:**
- Create: `web/src/components/XTerm.tsx`, `web/src/components/MobileKeyBar.tsx`, `web/src/components/MobileKeyBar.test.tsx`, `web/src/hooks/useTerminalSession.ts`, `web/src/components/TerminalView.tsx`, `web/src/components/TerminalView.test.tsx`

**Interfaces:**
- Consumes: `TerminalSocket` from `@/lib/ws-terminal`; `keybar` (`utf8`, `arrow`, `encodeCtrl`, `ESC`, `TAB`, `SHIFT_TAB`, `ENTER`, `SOFT_NEWLINE`, `BarKey`).
- Produces:
  - `interface XTermHandle { write(b: Uint8Array): void; fit(): { cols: number; rows: number } | null; focus(): void; appCursor(): boolean; getSelection(): string; paste(text: string): void; scrollLines(n: number): void; reset(): void }`
  - `XTerm = React.forwardRef<XTermHandle, { onData(d: string): void; onResize(cols: number, rows: number): void }>`
  - `interface TerminalController { sendKey(k: BarKey): void; toggleCtrl(): void; ctrlArmed: boolean; paste(): Promise<void>; copy(): Promise<void> }`
  - `useTerminalSession(target: TerminalTarget): { xtermRef; controller: TerminalController; connected: boolean }`
  - `MobileKeyBar({ controller }: { controller: TerminalController })`
  - `TerminalView({ serverId, paneId, target, showKeyBar }: { serverId: string; paneId: string; target: string; showKeyBar?: boolean })`

- [ ] **Step 1: Write `web/src/components/XTerm.tsx`** (DOM wrapper; thin, no protocol logic)

```tsx
import * as React from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

export interface XTermHandle {
  write(b: Uint8Array): void;
  fit(): { cols: number; rows: number } | null;
  focus(): void;
  appCursor(): boolean;
  getSelection(): string;
  paste(text: string): void;
  scrollLines(n: number): void;
  reset(): void;
}

export const XTerm = React.forwardRef<
  XTermHandle,
  { onData(d: string): void; onResize(cols: number, rows: number): void }
>(function XTerm({ onData, onResize }, ref) {
  const hostRef = React.useRef<HTMLDivElement>(null);
  const termRef = React.useRef<Terminal | null>(null);
  const fitRef = React.useRef<FitAddon | null>(null);

  // keep the latest callbacks without re-creating the terminal
  const onDataRef = React.useRef(onData);
  const onResizeRef = React.useRef(onResize);
  onDataRef.current = onData;
  onResizeRef.current = onResize;

  React.useImperativeHandle(ref, (): XTermHandle => ({
    write: (b) => termRef.current?.write(b),
    fit: () => {
      fitRef.current?.fit();
      const t = termRef.current;
      return t ? { cols: t.cols, rows: t.rows } : null;
    },
    focus: () => termRef.current?.focus(),
    appCursor: () => !!termRef.current?.modes.applicationCursorKeysMode,
    getSelection: () => termRef.current?.getSelection() ?? "",
    paste: (text) => termRef.current?.paste(text),
    scrollLines: (n) => termRef.current?.scrollLines(n),
    reset: () => termRef.current?.reset(),
  }), []);

  React.useEffect(() => {
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      scrollback: 5000,
      fontFamily: "Menlo, Consolas, monospace",
      theme: { background: "#111418", foreground: "#cdd6e0" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    // WebGL is optional; load lazily with a fallback to the default renderer.
    void import("@xterm/addon-webgl")
      .then(({ WebglAddon }) => {
        try {
          const addon = new WebglAddon();
          addon.onContextLoss(() => addon.dispose());
          term.loadAddon(addon);
        } catch { /* fall back to the default renderer */ }
      })
      .catch(() => {});
    term.open(hostRef.current!);
    fit.fit();
    term.onData((d) => onDataRef.current(d));
    term.onResize(({ cols, rows }) => onResizeRef.current(cols, rows));
    termRef.current = term;
    fitRef.current = fit;

    const ro = new ResizeObserver(() => { try { fit.fit(); } catch { /* detached */ } });
    ro.observe(hostRef.current!);

    // touch swipe = scroll the scrollback (do not let the page scroll)
    const host = hostRef.current!;
    let startY: number | null = null;
    const onStart = (e: TouchEvent) => { if (e.touches.length === 1) startY = e.touches[0].clientY; };
    const onMove = (e: TouchEvent) => {
      if (startY === null || e.touches.length !== 1) return;
      const y = e.touches[0].clientY;
      const dy = startY - y;
      const cell = 13 * 1.2;
      if (Math.abs(dy) > 6) {
        const lines = Math.trunc(dy / cell);
        if (lines !== 0) { term.scrollLines(lines); startY = y; }
        e.preventDefault();
      }
    };
    const onEnd = () => { startY = null; };
    host.addEventListener("touchstart", onStart, { passive: true });
    host.addEventListener("touchmove", onMove, { passive: false });
    host.addEventListener("touchend", onEnd, { passive: true });

    return () => {
      ro.disconnect();
      host.removeEventListener("touchstart", onStart);
      host.removeEventListener("touchmove", onMove);
      host.removeEventListener("touchend", onEnd);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, []);

  return <div ref={hostRef} className="h-full w-full" />;
});
```

- [ ] **Step 2: Write `web/src/hooks/useTerminalSession.ts`** (integration: socket ↔ xterm ↔ sticky Ctrl)

```ts
import * as React from "react";
import { TerminalSocket, type TerminalTarget } from "@/lib/ws-terminal";
import type { XTermHandle } from "@/components/XTerm";
import * as keys from "@/lib/keybar";

export interface TerminalController {
  sendKey(k: keys.BarKey): void;
  toggleCtrl(): void;
  ctrlArmed: boolean;
  paste(): Promise<void>;
  copy(): Promise<void>;
}

export function useTerminalSession(target: TerminalTarget) {
  const xtermRef = React.useRef<XTermHandle>(null);
  const sockRef = React.useRef<TerminalSocket | null>(null);
  const ctrlArmedRef = React.useRef(false);
  const [ctrlArmed, setCtrlArmed] = React.useState(false);
  const [connected, setConnected] = React.useState(false);

  const send = React.useCallback((b: Uint8Array) => sockRef.current?.send(b), []);

  // typed/pasted text from xterm → apply sticky Ctrl → socket
  const handleData = React.useCallback((d: string) => {
    if (ctrlArmedRef.current) {
      send(keys.encodeCtrl(d));
      ctrlArmedRef.current = false;
      setCtrlArmed(false);
      return;
    }
    send(keys.utf8(d));
  }, [send]);

  const handleResize = React.useCallback((cols: number, rows: number) => {
    sockRef.current?.resize(cols, rows);
  }, []);

  React.useEffect(() => {
    const sock = new TerminalSocket(target, {
      onData: (b) => xtermRef.current?.write(b),
      onOpen: () => {
        setConnected(true);
        xtermRef.current?.reset();           // fresh paint; snapshot arrives next as binary
        const size = xtermRef.current?.fit();
        if (size) sock.resize(size.cols, size.rows);
        xtermRef.current?.focus();
      },
      onClose: () => setConnected(false),
    });
    sockRef.current = sock;
    sock.open();
    return () => { sock.dispose(); sockRef.current = null; };
    // re-create only when the pane target changes
  }, [target.serverId, target.paneId, target.target]);

  const controller: TerminalController = {
    ctrlArmed,
    sendKey(k) {
      switch (k) {
        case "esc": return send(keys.ESC);
        case "tab": return send(keys.TAB);
        case "stab": return send(keys.SHIFT_TAB);
        case "enter": return send(keys.ENTER);
        case "nl": return send(keys.SOFT_NEWLINE);
        case "up": case "down": case "left": case "right":
          return send(keys.arrow(k, xtermRef.current?.appCursor() ?? false));
      }
    },
    toggleCtrl() {
      ctrlArmedRef.current = !ctrlArmedRef.current;
      setCtrlArmed(ctrlArmedRef.current);
    },
    async paste() {
      try {
        const text = await navigator.clipboard.readText();
        if (!text) return;
        if (text.includes("\n") || text.length > 200) {
          const lines = text.split("\n").length;
          if (!confirm(`Paste ${text.length} chars / ${lines} lines?`)) return;
        }
        xtermRef.current?.paste(text); // xterm owns bracketed-paste framing
      } catch { /* clipboard needs a secure context + permission */ }
    },
    async copy() {
      const sel = xtermRef.current?.getSelection() ?? "";
      if (!sel) return;
      try { await navigator.clipboard.writeText(sel); } catch { /* secure context */ }
    },
  };

  return { xtermRef, controller, connected, handleData, handleResize };
}
```

- [ ] **Step 3: Write the failing MobileKeyBar test** — `web/src/components/MobileKeyBar.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import type { TerminalController } from "@/hooks/useTerminalSession";

function makeController(over: Partial<TerminalController> = {}): TerminalController {
  return {
    sendKey: vi.fn(), toggleCtrl: vi.fn(), ctrlArmed: false,
    paste: vi.fn().mockResolvedValue(undefined), copy: vi.fn().mockResolvedValue(undefined),
    ...over,
  };
}

describe("MobileKeyBar", () => {
  it("routes each bar key to controller.sendKey", async () => {
    const c = makeController();
    render(<MobileKeyBar controller={c} />);
    await userEvent.click(screen.getByRole("button", { name: "Esc" }));
    await userEvent.click(screen.getByRole("button", { name: "Enter" }));
    expect(c.sendKey).toHaveBeenCalledWith("esc");
    expect(c.sendKey).toHaveBeenCalledWith("enter");
  });

  it("Ctrl toggles and reflects the armed state", async () => {
    const c = makeController({ ctrlArmed: true });
    render(<MobileKeyBar controller={c} />);
    const ctrl = screen.getByRole("button", { name: "Ctrl" });
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(ctrl);
    expect(c.toggleCtrl).toHaveBeenCalled();
  });

  it("omits a Lock button (no read-only lock in M5)", () => {
    render(<MobileKeyBar controller={makeController()} />);
    expect(screen.queryByRole("button", { name: /lock/i })).toBeNull();
  });
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd web && npm run test:run -- MobileKeyBar`
Expected: FAIL (cannot resolve `@/components/MobileKeyBar`).

- [ ] **Step 5: Write `web/src/components/MobileKeyBar.tsx`**

```tsx
import * as React from "react";
import type { TerminalController } from "@/hooks/useTerminalSession";
import type { BarKey } from "@/lib/keybar";

const KEYS: { key: BarKey; label: string }[] = [
  { key: "esc", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "stab", label: "⇧Tab" },
  { key: "up", label: "↑" },
  { key: "down", label: "↓" },
  { key: "left", label: "←" },
  { key: "right", label: "→" },
  { key: "nl", label: "⏎ nl" },
  { key: "enter", label: "Enter" },
];

// §6.2 key bar, minus [Lock] (no read-only lock in M5). Single-row, horizontally
// scrollable. pointerDown.preventDefault keeps xterm's hidden textarea focused so
// the soft keyboard stays up.
export function MobileKeyBar({ controller }: { controller: TerminalController }) {
  const btn =
    "flex-none h-8 rounded-md border border-border bg-accent px-3 text-xs text-accent-foreground active:bg-primary/30";
  return (
    <div
      className="flex flex-nowrap gap-1 overflow-x-auto border-t border-border bg-card p-1.5"
      style={{ touchAction: "pan-x", paddingBottom: "max(0.375rem, env(safe-area-inset-bottom))" }}
      onPointerDown={(e) => { if ((e.target as HTMLElement).tagName === "BUTTON") e.preventDefault(); }}
    >
      <button
        className={`${btn} ${controller.ctrlArmed ? "!bg-primary !text-primary-foreground" : ""}`}
        aria-pressed={controller.ctrlArmed}
        onClick={() => controller.toggleCtrl()}
      >
        Ctrl
      </button>
      {KEYS.map(({ key, label }) => (
        <button key={key} className={btn} onClick={() => controller.sendKey(key)}>
          {label}
        </button>
      ))}
      <button className={btn} onClick={() => void controller.paste()}>Paste</button>
      <button className={btn} onClick={() => void controller.copy()}>Copy</button>
    </div>
  );
}
```

- [ ] **Step 6: Run MobileKeyBar test → pass**

Run: `cd web && npm run test:run -- MobileKeyBar`
Expected: PASS.

- [ ] **Step 7: Write `web/src/components/TerminalView.tsx`** (composes XTerm + key bar + reconnect banner)

```tsx
import * as React from "react";
import { XTerm } from "@/components/XTerm";
import { MobileKeyBar } from "@/components/MobileKeyBar";
import { useTerminalSession } from "@/hooks/useTerminalSession";

export function TerminalView({
  serverId, paneId, target, showKeyBar = false,
}: {
  serverId: string;
  paneId: string;
  target: string;
  showKeyBar?: boolean;
}) {
  const targetObj = React.useMemo(() => ({ serverId, paneId, target }), [serverId, paneId, target]);
  const { xtermRef, controller, connected, handleData, handleResize } = useTerminalSession(targetObj);

  return (
    <div className="relative flex h-full w-full flex-col">
      {!connected && (
        <div className="absolute left-0 right-0 top-0 z-10 bg-destructive px-2 py-1 text-center text-xs font-semibold text-destructive-foreground">
          disconnected — reconnecting…
        </div>
      )}
      <div className="min-h-0 flex-1">
        <XTerm ref={xtermRef} onData={handleData} onResize={handleResize} />
      </div>
      {showKeyBar && <MobileKeyBar controller={controller} />}
    </div>
  );
}
```

- [ ] **Step 8: Write the TerminalView smoke test** — `web/src/components/TerminalView.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";

// xterm.js needs a real canvas/WebGL; mock the DOM wrapper to a smoke double.
vi.mock("@/components/XTerm", () => ({
  XTerm: () => <div data-testid="xterm" />,
}));
// Avoid opening a real socket in jsdom.
const open = vi.fn();
const dispose = vi.fn();
vi.mock("@/lib/ws-terminal", async (orig) => {
  const mod = await (orig as any)();
  return { ...mod, TerminalSocket: class { constructor() {} open = open; dispose = dispose; send() {} resize() {} } };
});

import { TerminalView } from "@/components/TerminalView";

describe("TerminalView", () => {
  it("mounts the terminal and opens a socket; shows the key bar when asked", () => {
    const { getByTestId, getByText, unmount } = render(
      <TerminalView serverId="s" paneId="%0" target="default" showKeyBar />,
    );
    expect(getByTestId("xterm")).toBeInTheDocument();
    expect(open).toHaveBeenCalled();
    expect(getByText("Esc")).toBeInTheDocument(); // key bar present
    unmount();
    expect(dispose).toHaveBeenCalled(); // cleans up the socket
  });
});
```

- [ ] **Step 9: Run the smoke test → pass**

Run: `cd web && npm run test:run -- TerminalView`
Expected: PASS (mounts, opens, key bar shown, disposes on unmount).

- [ ] **Step 10: Typecheck + full test run**

Run: `cd web && npm run test:run && npm run build`
Expected: all green; `tsc --noEmit` clean.

- [ ] **Step 11: Commit**

```bash
git add web/src/components/XTerm.tsx web/src/hooks/useTerminalSession.ts \
  web/src/components/MobileKeyBar.tsx web/src/components/MobileKeyBar.test.tsx \
  web/src/components/TerminalView.tsx web/src/components/TerminalView.test.tsx
git commit -m "feat(web): terminal surface — XTerm wrapper, useTerminalSession, MobileKeyBar, TerminalView"
```

---

## Task 8: Mobile terminal route + session switching

**Files:**
- Create: `web/src/routes/terminal.tsx`, `web/src/routes/terminal.test.tsx`
- Modify: `web/src/router.tsx`

**Interfaces:**
- Consumes: `TerminalView` from `@/components/TerminalView`; TanStack Router params/search.
- Produces:
  - `interface TerminalSearch { target: string; session: string }`
  - `MobileTerminalRoute()` — full-screen `TerminalView showKeyBar` with a header (`server / session / pane`, a "‹ Back" to `/`) reading `serverId`/`paneId` from params and `target`/`session` from search.
  - Router: `terminalRoute` at `/t/$serverId/$paneId` under the `auth` layout, with `validateSearch`.

- [ ] **Step 1: Write the failing route test** — `web/src/routes/terminal.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid="tv">{`${p.serverId}:${p.paneId}:${p.target}:${String(p.showKeyBar)}`}</div>,
}));
vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ serverId: "s1", paneId: "%0" }),
  useSearch: () => ({ target: "default", session: "demo-web" }),
  useNavigate: () => vi.fn(),
}));

import { MobileTerminalRoute } from "@/routes/terminal";

describe("MobileTerminalRoute", () => {
  it("passes params/search into a key-bar TerminalView and shows the session header", () => {
    render(<MobileTerminalRoute />);
    expect(screen.getByTestId("tv")).toHaveTextContent("s1:%0:default:true");
    expect(screen.getByText("demo-web")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- routes/terminal`
Expected: FAIL (cannot resolve `@/routes/terminal`).

- [ ] **Step 3: Write `web/src/routes/terminal.tsx`**

```tsx
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";

export interface TerminalSearch { target: string; session: string; }

export function MobileTerminalRoute() {
  const { serverId, paneId } = useParams({ strict: false }) as { serverId: string; paneId: string };
  const { target, session } = useSearch({ strict: false }) as TerminalSearch;
  const navigate = useNavigate();

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b border-border px-2 py-2">
        <Button variant="ghost" size="sm" onClick={() => navigate({ to: "/" })}>‹ Back</Button>
        <div className="min-w-0">
          <div className="truncate font-medium">{session}</div>
          <div className="truncate text-xs text-muted-foreground">{serverId} · {paneId}</div>
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <TerminalView serverId={serverId} paneId={paneId} target={target} showKeyBar />
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run route test → pass**

Run: `cd web && npm run test:run -- routes/terminal`
Expected: PASS.

- [ ] **Step 5: Register the route in `web/src/router.tsx`**

Add the import:

```tsx
import { MobileTerminalRoute, type TerminalSearch } from "./routes/terminal";
```

Add the route (after `indexRoute`):

```tsx
const terminalRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/t/$serverId/$paneId",
  validateSearch: (s: Record<string, unknown>): TerminalSearch => ({
    target: typeof s.target === "string" ? s.target : "default",
    session: typeof s.session === "string" ? s.session : "",
  }),
  component: MobileTerminalRoute,
});
```

Update the tree:

```tsx
const routeTree = rootRoute.addChildren([
  loginRoute,
  authRoute.addChildren([indexRoute, terminalRoute]),
]);
```

- [ ] **Step 6: Restore the real navigation in `web/src/routes/index.tsx`** (if it was stubbed to `"/"` in Task 6 Step 11)

Ensure `open()` navigates to:

```tsx
navigate({
  to: "/t/$serverId/$paneId",
  params: { serverId: row.server.id, paneId: row.pane.id },
  search: { target: row.session.target, session: row.session.name },
});
```

- [ ] **Step 7: Typecheck + full test run**

Run: `cd web && npm run test:run && npm run build`
Expected: all green; `tsc --noEmit` clean (the typed `/t` route now exists).

- [ ] **Step 8: Commit**

```bash
git add web/src/routes/terminal.tsx web/src/routes/terminal.test.tsx web/src/router.tsx web/src/routes/index.tsx
git commit -m "feat(web): mobile terminal route /t with full-screen TerminalView + back navigation"
```

---

## Task 9: Desktop — panes store, Sidebar, GridView with live tiles + in-state expand

**Files:**
- Create: `web/src/store/panes.ts`, `web/src/store/panes.test.ts`, `web/src/components/Sidebar.tsx`, `web/src/components/GridView.tsx`, `web/src/components/GridView.test.tsx`

**Interfaces:**
- Consumes: `TerminalView` from `@/components/TerminalView`; `SessionRow`/`SessionList` plumbing from Task 6; `useQuery`/`useQueries`.
- Produces:
  - `interface OpenPane { id: string; serverId: string; paneId: string; target: string; session: string; serverName: string }` (`id = serverId:target:session:paneId`)
  - `GRID_TILE_CAP = 6`
  - `usePanes` (Zustand) state `{ panes: OpenPane[]; focusedId: string | null }` + actions `openPane(p: Omit<OpenPane,"id">): { ok: boolean; reason?: "cap" }`, `closePane(id: string): void`, `focus(id: string): void`, `collapse(): void`
  - `Sidebar({ rows, query, onQueryChange, onOpen }: { rows: SessionRow[]; query: string; onQueryChange(q): void; onOpen(row: SessionRow): void })`
  - `GridView()` — renders every open pane as a mounted `TerminalView`; non-focused tiles are hidden (`display:none`) but stay mounted; focused tile renders full-area with a "⊟ grid" control.

- [ ] **Step 1: Write the failing panes-store test** — `web/src/store/panes.test.ts`

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { usePanes, GRID_TILE_CAP } from "@/store/panes";

const mk = (n: number) => ({
  serverId: "s", paneId: `%${n}`, target: "default", session: `sess${n}`, serverName: "aigallery",
});

describe("panes store", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("opens a pane and focuses it", () => {
    const r = usePanes.getState().openPane(mk(0));
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(usePanes.getState().focusedId).toBe("s:default:sess0:%0");
  });

  it("is idempotent on the same pane id (re-focuses, no duplicate)", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().collapse();
    const r = usePanes.getState().openPane(mk(0));
    expect(r.ok).toBe(true);
    expect(usePanes.getState().panes).toHaveLength(1);
    expect(usePanes.getState().focusedId).toBe("s:default:sess0:%0");
  });

  it("rejects opening beyond the soft cap", () => {
    for (let i = 0; i < GRID_TILE_CAP; i++) expect(usePanes.getState().openPane(mk(i)).ok).toBe(true);
    const r = usePanes.getState().openPane(mk(GRID_TILE_CAP));
    expect(r.ok).toBe(false);
    expect(r.reason).toBe("cap");
    expect(usePanes.getState().panes).toHaveLength(GRID_TILE_CAP);
  });

  it("closePane removes it and clears focus if it was focused", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().closePane("s:default:sess0:%0");
    expect(usePanes.getState().panes).toHaveLength(0);
    expect(usePanes.getState().focusedId).toBeNull();
  });

  it("collapse clears focus but keeps panes", () => {
    usePanes.getState().openPane(mk(0));
    usePanes.getState().collapse();
    expect(usePanes.getState().focusedId).toBeNull();
    expect(usePanes.getState().panes).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm run test:run -- panes`
Expected: FAIL (cannot resolve `@/store/panes`).

- [ ] **Step 3: Write `web/src/store/panes.ts`**

```ts
import { create } from "zustand";

export const GRID_TILE_CAP = 6; // client-side soft cap on simultaneously-live tiles

export interface OpenPane {
  id: string; // serverId:target:session:paneId
  serverId: string;
  paneId: string;
  target: string;
  session: string;
  serverName: string;
}

const idOf = (p: Omit<OpenPane, "id">) => `${p.serverId}:${p.target}:${p.session}:${p.paneId}`;

interface PanesState {
  panes: OpenPane[];
  focusedId: string | null;
  openPane(p: Omit<OpenPane, "id">): { ok: boolean; reason?: "cap" };
  closePane(id: string): void;
  focus(id: string): void;
  collapse(): void;
}

export const usePanes = create<PanesState>((set, get) => ({
  panes: [],
  focusedId: null,
  openPane(p) {
    const id = idOf(p);
    const existing = get().panes.find((x) => x.id === id);
    if (existing) {
      set({ focusedId: id }); // already open → just focus/expand
      return { ok: true };
    }
    if (get().panes.length >= GRID_TILE_CAP) return { ok: false, reason: "cap" };
    set((s) => ({ panes: [...s.panes, { ...p, id }], focusedId: id }));
    return { ok: true };
  },
  closePane(id) {
    set((s) => ({
      panes: s.panes.filter((x) => x.id !== id),
      focusedId: s.focusedId === id ? null : s.focusedId,
    }));
  },
  focus(id) { set({ focusedId: id }); },
  collapse() { set({ focusedId: null }); },
}));
```

- [ ] **Step 4: Run panes test → pass**

Run: `cd web && npm run test:run -- panes`
Expected: PASS.

- [ ] **Step 5: Write `web/src/components/Sidebar.tsx`**

```tsx
import type { SessionRow } from "@/components/SessionList";
import { Input } from "@/components/ui/input";
import { matchesQuery } from "@/components/SessionList";

// Desktop servers→sessions tree. Clicking a session opens it as a live grid tile.
export function Sidebar({
  rows, query, onQueryChange, onOpen,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
  onOpen(row: SessionRow): void;
}) {
  const filtered = rows.filter((r) => matchesQuery(r, query));
  const byServer = new Map<string, SessionRow[]>();
  for (const r of filtered) {
    const list = byServer.get(r.server.name) ?? [];
    list.push(r);
    byServer.set(r.server.name, list);
  }
  return (
    <aside className="flex h-full w-72 flex-none flex-col border-r border-border">
      <div className="p-3">
        <Input placeholder="Search…" value={query} onChange={(e) => onQueryChange(e.target.value)}
          aria-label="Search sessions" />
      </div>
      <div className="flex-1 overflow-y-auto">
        {[...byServer.entries()].map(([serverName, list]) => (
          <div key={serverName}>
            <div className="px-3 py-1 text-xs font-semibold uppercase text-muted-foreground">{serverName}</div>
            {list.map((row) => (
              <button
                key={`${row.session.target}:${row.session.name}:${row.pane.id}`}
                className="block w-full px-4 py-2 text-left text-sm hover:bg-accent"
                onClick={() => onOpen(row)}
              >
                <div className="truncate">{row.session.name}</div>
                <div className="truncate text-xs text-muted-foreground">{row.session.cwd || "—"}</div>
              </button>
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}
```

- [ ] **Step 6: Write the failing GridView test** — `web/src/components/GridView.test.tsx`

```tsx
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/components/TerminalView", () => ({
  TerminalView: (p: any) => <div data-testid={`tv-${p.paneId}`} />,
}));

import { GridView } from "@/components/GridView";
import { usePanes } from "@/store/panes";

describe("GridView", () => {
  beforeEach(() => usePanes.setState({ panes: [], focusedId: null }));

  it("renders a live TerminalView per open pane", () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().openPane({ serverId: "s", paneId: "%1", target: "default", session: "b", serverName: "h" });
    usePanes.getState().collapse();
    render(<GridView />);
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
  });

  it("expanding one tile keeps the others MOUNTED (liveness invariant)", async () => {
    usePanes.getState().openPane({ serverId: "s", paneId: "%0", target: "default", session: "a", serverName: "h" });
    usePanes.getState().openPane({ serverId: "s", paneId: "%1", target: "default", session: "b", serverName: "h" });
    usePanes.getState().focus("s:default:a:%0"); // expand %0
    render(<GridView />);
    // both terminals still in the DOM (the non-focused one is hidden, not unmounted)
    expect(screen.getByTestId("tv-%0")).toBeInTheDocument();
    expect(screen.getByTestId("tv-%1")).toBeInTheDocument();
    // a collapse control is present while expanded
    expect(screen.getByRole("button", { name: /grid/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 7: Run test to verify it fails**

Run: `cd web && npm run test:run -- GridView`
Expected: FAIL (cannot resolve `@/components/GridView`).

- [ ] **Step 8: Write `web/src/components/GridView.tsx`**

```tsx
import { usePanes } from "@/store/panes";
import { TerminalView } from "@/components/TerminalView";
import { Button } from "@/components/ui/button";

// Live tiled grid. EVERY tile stays mounted (its own WS); expand is in-state, so
// the non-focused tiles are hidden with display:none — sockets + scrollback survive.
export function GridView() {
  const { panes, focusedId, focus, collapse, closePane } = usePanes();

  if (panes.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Open a session from the sidebar to start a terminal.
      </div>
    );
  }

  return (
    <div className="relative h-full w-full">
      <div
        className="grid h-full w-full gap-2 p-2"
        style={{
          gridTemplateColumns: focusedId ? "1fr" : "repeat(auto-fit, minmax(360px, 1fr))",
          // when expanded, the grid collapses to one cell; hidden tiles take no space
        }}
      >
        {panes.map((p) => {
          const expanded = focusedId === p.id;
          const hidden = focusedId !== null && !expanded;
          return (
            <div
              key={p.id}
              className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border"
              style={{ display: hidden ? "none" : "flex" }}
            >
              <div className="flex items-center justify-between border-b border-border bg-card px-2 py-1 text-xs">
                <button className="min-w-0 truncate text-left hover:underline"
                  onClick={() => (expanded ? collapse() : focus(p.id))}
                  title={expanded ? "Back to grid" : "Expand"}>
                  {p.serverName} · {p.session} · {p.paneId}
                </button>
                <span className="flex flex-none items-center gap-1">
                  {expanded ? (
                    <Button variant="ghost" size="sm" onClick={() => collapse()}>⊟ grid</Button>
                  ) : (
                    <Button variant="ghost" size="sm" onClick={() => focus(p.id)}>⤢</Button>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => closePane(p.id)} aria-label="Close">✕</Button>
                </span>
              </div>
              <div className="min-h-0 flex-1">
                <TerminalView serverId={p.serverId} paneId={p.paneId} target={p.target} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
```

- [ ] **Step 9: Run GridView test → pass**

Run: `cd web && npm run test:run -- GridView`
Expected: PASS (both tiles mounted while one is expanded; ⊟ grid control present).

- [ ] **Step 10: Typecheck + full test run**

Run: `cd web && npm run test:run && npm run build`
Expected: all green.

- [ ] **Step 11: Commit**

```bash
git add web/src/store/panes.ts web/src/store/panes.test.ts \
  web/src/components/Sidebar.tsx web/src/components/GridView.tsx web/src/components/GridView.test.tsx
git commit -m "feat(web): desktop grid — panes store (soft cap 6), Sidebar, live tiles + in-state expand"
```

---

## Task 10: Responsive shell + final integration + live-acceptance doc

**Files:**
- Create: `web/src/lib/use-media-query.ts`, `web/src/lib/use-media-query.test.ts`, `web/src/components/DesktopShell.tsx`
- Modify: `web/src/routes/index.tsx`
- Create: `docs/superpowers/m5-live-acceptance.md`

**Interfaces:**
- Consumes: everything above (`Sidebar`, `GridView`, `usePanes`, `SessionList`, queries).
- Produces:
  - `useMediaQuery(query: string): boolean`
  - `DesktopShell({ rows, query, onQueryChange }: { rows: SessionRow[]; query: string; onQueryChange(q): void })` — `Sidebar` + `GridView`; the sidebar's `onOpen` calls `usePanes().openPane(...)` and surfaces the cap rejection.
  - `ShellRoute` chooses `DesktopShell` (≥1024px) or the mobile `SessionList` (navigates to `/t`).

- [ ] **Step 1: Write the failing media-query test** — `web/src/lib/use-media-query.test.ts`

```ts
import { describe, it, expect, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useMediaQuery } from "@/lib/use-media-query";

describe("useMediaQuery", () => {
  it("returns the initial match state", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: true, media: q, addEventListener: () => {}, removeEventListener: () => {},
    }));
    const { result } = renderHook(() => useMediaQuery("(min-width: 1024px)"));
    expect(result.current).toBe(true);
  });
});
```

- [ ] **Step 2: Run test → fail**

Run: `cd web && npm run test:run -- use-media-query`
Expected: FAIL (cannot resolve `@/lib/use-media-query`).

- [ ] **Step 3: Write `web/src/lib/use-media-query.ts`**

```ts
import * as React from "react";

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = React.useState(() =>
    typeof window !== "undefined" && window.matchMedia ? window.matchMedia(query).matches : false,
  );
  React.useEffect(() => {
    const mql = window.matchMedia(query);
    const onChange = () => setMatches(mql.matches);
    onChange();
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}
```

- [ ] **Step 4: Run test → pass**

Run: `cd web && npm run test:run -- use-media-query`
Expected: PASS.

- [ ] **Step 5: Write `web/src/components/DesktopShell.tsx`**

```tsx
import * as React from "react";
import { Sidebar } from "@/components/Sidebar";
import { GridView } from "@/components/GridView";
import { usePanes, GRID_TILE_CAP } from "@/store/panes";
import type { SessionRow } from "@/components/SessionList";

export function DesktopShell({
  rows, query, onQueryChange,
}: {
  rows: SessionRow[];
  query: string;
  onQueryChange(q: string): void;
}) {
  const openPane = usePanes((s) => s.openPane);
  const [notice, setNotice] = React.useState<string | null>(null);

  function onOpen(row: SessionRow) {
    const r = openPane({
      serverId: row.server.id, paneId: row.pane.id, target: row.session.target,
      session: row.session.name, serverName: row.server.name,
    });
    if (!r.ok && r.reason === "cap") {
      setNotice(`Tile limit reached (${GRID_TILE_CAP}). Close a terminal to open another.`);
      setTimeout(() => setNotice(null), 4000);
    }
  }

  return (
    <div className="flex h-full">
      <Sidebar rows={rows} query={query} onQueryChange={onQueryChange} onOpen={onOpen} />
      <main className="min-w-0 flex-1">
        {notice && (
          <div className="bg-destructive px-3 py-1 text-center text-xs font-semibold text-destructive-foreground">
            {notice}
          </div>
        )}
        <GridView />
      </main>
    </div>
  );
}
```

- [ ] **Step 6: Update `web/src/routes/index.tsx` to choose desktop vs mobile**

Replace the render branch (keep the data-fetching `serversQ`/`sessionQs`/`rows`/`open` logic) so the body is:

```tsx
import { useMediaQuery } from "@/lib/use-media-query";
import { DesktopShell } from "@/components/DesktopShell";
// …existing imports (useQuery, useQueries, useNavigate, listServers, listSessions,
//   useAuth, Button, SessionList, flattenSessions, types) …

export function ShellRoute() {
  const navigate = useNavigate();
  const signOut = useAuth((s) => s.signOut);
  const [query, setQuery] = React.useState("");
  const isDesktop = useMediaQuery("(min-width: 1024px)");

  const serversQ = useQuery({ queryKey: ["servers"], queryFn: listServers });
  const servers = serversQ.data ?? [];
  const sessionQs = useQueries({
    queries: servers.map((s) => ({ queryKey: ["sessions", s.id], queryFn: () => listSessions(s.id) })),
  });
  const byServer: Record<string, Session[]> = {};
  servers.forEach((s, i) => { byServer[s.id] = (sessionQs[i]?.data as Session[]) ?? []; });
  const rows = flattenSessions(servers, byServer);

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-border px-4 py-2">
        <span className="font-semibold">AgentMon</span>
        <Button variant="ghost" size="sm" onClick={() => signOut().then(() => navigate({ to: "/login" }))}>
          Sign out
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        {isDesktop ? (
          <DesktopShell rows={rows} query={query} onQueryChange={setQuery} />
        ) : (
          <SessionList
            rows={rows}
            query={query}
            onQueryChange={setQuery}
            onOpen={(row) =>
              navigate({
                to: "/t/$serverId/$paneId",
                params: { serverId: row.server.id, paneId: row.pane.id },
                search: { target: row.session.target, session: row.session.name },
              })
            }
          />
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 7: Full test + build + lint-equivalent**

Run: `cd web && npm run test:run && npm run build`
Expected: all tests green; `tsc --noEmit` clean; `vite build` emits `dist/`.

- [ ] **Step 8: Write `docs/superpowers/m5-live-acceptance.md`** (the safe live-test runbook)

```markdown
# M5 live acceptance runbook (safety-critical)

SAFETY (memory dev-host-runs-hub-and-claude): this host runs the hub AND Claude's
own tmux on the DEFAULT socket. The relay is LIVE. Test ONLY against the `aigallery`
agent's `agentmon`-socket demo panes (demo-web=%0, demo-db=%1). NEVER the default socket.

1. Build the SPA into the hub:
   - `make embed` (copies web/dist → hubd/internal/webui/dist), then build the hub
     binary (`make build-hub`) OR run the hub from source with the embedded dist.
2. Run the hub on a LOOPBACK TEST PORT against a COPY of the live DB:
   - copy `deploy/data` to a throwaway dir; point a test config at it.
   - set the test hub `external_origin` to the loopback origin you browse to
     (e.g. `http://127.0.0.1:8388`); `trust_forwarded_proto: false`.
3. Browse to the loopback origin; log in as `patrik`.
4. Verify: servers list shows `aigallery`.
5. Desktop grid: open BOTH demo panes (%0, %1) as two live tiles; confirm both stream.
6. Expand demo-web; run `echo AGENTMON_M5_OK`; confirm it runs; collapse → the other tile is still live.
7. Mobile path: narrow the viewport (or device-emulate < 1024px) → list → open demo-web →
   key bar drives it (Esc, Ctrl-C, arrows, ⏎ nl vs Enter); force a WS drop → it reconnects
   with a fresh snapshot; the tmux session survives.
8. Tear down: stop the test hub; delete the DB copy + the embedded dist
   (`make clean` restores the tracked placeholder). Confirm Claude's default-socket
   session and the demo panes are intact.

Full on-device iOS Safari / Android Chrome §6.4 checklist is run separately as Phase-1 acceptance.
```

- [ ] **Step 9: Commit**

```bash
git add web/src/lib/use-media-query.ts web/src/lib/use-media-query.test.ts \
  web/src/components/DesktopShell.tsx web/src/routes/index.tsx docs/superpowers/m5-live-acceptance.md
git commit -m "feat(web): responsive shell (desktop grid / mobile list) + M5 live-acceptance runbook"
```

- [ ] **Step 10: Final verification (whole milestone)**

Run:
```bash
cd web && npm run test:run && npm run build
```
Expected: all unit + component tests green; typecheck clean; `dist/` built. Then run the live-acceptance runbook (`docs/superpowers/m5-live-acceptance.md`) against the demo panes before declaring M5 done.

---

## Self-review notes (coverage map)

- **Login / CSRF / Origin** (spec §2.1): Task 4 (api-client), Task 5 (auth store), Task 6 (LoginForm + guard). Origin is automatic; dev `external_origin` is documented in Task 1 (vite proxy) and the runbook (Task 10).
- **Server→session list** (§2.2): Task 6 (SessionList + flatten/filter), data-fetching in Task 6/10.
- **Terminal WS transparent protocol** (§2.3): Task 3 (ws-terminal), Task 7 (XTerm + useTerminalSession + TerminalView).
- **Input fidelity** (§2.4): Task 2 (keybar), Task 7 (sticky Ctrl wiring, paste/copy, touch scroll), Task 7 MobileKeyBar.
- **Desktop grid + expand + soft cap** (§5.3): Task 9 (panes store, GridView liveness invariant), Task 10 (DesktopShell cap notice).
- **Mobile** (§5.4): Task 7 (key bar), Task 8 (terminal route + switching), Task 10 (mobile branch).
- **Reconnect/resize** (§7): Task 3 (reconnect/backoff/visibility), Task 7 (onOpen reset+resize, ResizeObserver, banner).
- **Tooling/CI/dev proxy** (§6): Task 1.
- **Live acceptance + safety** (§9): Task 10 runbook.
- **Deferred (no task, intentional):** read-only lock, server-side concurrency cap, state/inbox, PWA/push, saved layouts (spec §1.2/§10).
```
