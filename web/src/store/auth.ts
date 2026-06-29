import { create } from "zustand";
import type { SessionInfo } from "@/lib/contracts";
import * as api from "@/lib/api-client";
import { disablePush } from "@/lib/push";
import { usePanes } from "@/store/panes";
import { useSessionState } from "@/store/session-state";

/** Best-effort Web-Push teardown on sign-out (M9). Runs before logout so the
 *  unsubscribe POST still carries a valid CSRF token. Never throws — a failed
 *  push teardown must never block the user from signing out. No-ops where the
 *  service worker is unavailable (unsupported browser / dev / test). */
async function unsubscribePushBestEffort(): Promise<void> {
  try {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    // Use getRegistration(), NOT `.ready`: `.ready` only resolves once a worker
    // becomes ACTIVE and otherwise stays pending forever — a try/catch can't
    // guard a never-resolving promise, so awaiting it would hang sign-out when
    // the SW failed to activate. getRegistration() resolves promptly to the
    // registration (active or not) or undefined.
    const reg = await navigator.serviceWorker.getRegistration();
    if (!reg) return;
    await disablePush(reg);
  } catch {
    /* best-effort; ignore */
  }
}

/** Reset panes + query cache without a static import cycle.
 *  query-client.ts imports auth.ts (for useAuth), so auth.ts must not
 *  statically import query-client.ts — use a lazy dynamic import instead. */
function resetGridAndCache() {
  usePanes.setState({ panes: [], focusedId: null });
  useSessionState.getState().reset();
  void import("@/lib/query-client").then((m) => m.queryClient.clear());
}

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

export const useAuth = create<AuthState>((set, get) => ({
  session: null,
  status: "unknown",
  setSession(s) {
    api.setCsrfToken(s.csrfToken);
    set({ session: s, status: "authed" });
  },
  clear() {
    api.setCsrfToken("");
    set({ session: null, status: "anon" });
    resetGridAndCache();
  },
  async signIn(username, password) {
    const info = await api.login(username, password);
    get().setSession(info);
  },
  async signOut() {
    await unsubscribePushBestEffort();
    try { await api.logout(); } catch { /* best-effort; clear locally regardless */ }
    finally { get().clear(); }
  },
  async bootstrap() {
    try {
      const info = await api.me();
      get().setSession(info);
    } catch {
      // bootstrap catch does not call resetGridAndCache (no grid to clear on startup)
      api.setCsrfToken("");
      set({ session: null, status: "anon" });
    }
  },
}));
