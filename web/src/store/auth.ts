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
