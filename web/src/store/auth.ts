import { create } from "zustand";
import type { SessionInfo } from "@/lib/contracts";
import * as api from "@/lib/api-client";
import { disablePush, getActiveRegistration } from "@/lib/push";
import { usePanes } from "@/store/panes";
import { useSessionState } from "@/store/session-state";

/** Best-effort Web-Push teardown on sign-out (M9). Runs before logout so the
 *  unsubscribe POST still carries a valid CSRF token. Never throws — a failed
 *  push teardown must never block the user from signing out. No-ops where the
 *  service worker is unavailable (unsupported browser / dev / test). */
async function unsubscribePushBestEffort(): Promise<void> {
  // getActiveRegistration() never throws and never hangs (it uses getRegistration(),
  // not the `.ready` trap). disablePush() is best-effort, but guard it anyway so a
  // teardown failure can never block sign-out.
  const reg = await getActiveRegistration();
  if (!reg) return;
  try {
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
  clearMustChangePassword(): void;
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
  // Dismiss the default-password nudge after a successful change (the flag is
  // login-scoped, so clearing it locally keeps the banner from lingering).
  clearMustChangePassword() {
    const s = get().session;
    if (s?.mustChangePassword) set({ session: { ...s, mustChangePassword: false } });
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
