import { describe, it, expect } from "vitest";
import { providerByIdent, providerOf } from "@/lib/provider";
import { paneIdentity } from "@/lib/pane-identity";

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

describe("providerByIdent", () => {
  const row = (paneId: string, paneCmd: string, sessCmd: string) => ({
    server: { id: "s1" },
    session: { target: "default", command: sessCmd },
    pane: { id: paneId, command: paneCmd },
  });
  it("keys by pane identity and derives from the row's OWN pane, not session.command", () => {
    // Split-pane regression: session.command is tmux's ACTIVE pane and may name a
    // different pane's process — the displayed pane's command must win.
    const m = providerByIdent([row("%0", "claude", "codex"), row("%1", "bash", "claude")]);
    expect(m.get(paneIdentity("s1", "default", "%0"))).toBe("claude");
    expect(m.has(paneIdentity("s1", "default", "%1"))).toBe(false); // no wrong tag from session.command
  });
  it("returns an empty map for no rows", () => {
    expect(providerByIdent([]).size).toBe(0);
  });
});
