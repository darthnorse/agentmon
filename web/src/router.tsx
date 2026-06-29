import { createRootRoute, createRoute, createRouter, redirect, Outlet } from "@tanstack/react-router";
import { useAuth } from "@/store/auth";
import { LoginRoute } from "./routes/login";
import { ShellRoute } from "./routes/index";
import { MobileTerminalRoute, type TerminalSearch } from "./routes/terminal";

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

const terminalRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/t/$serverId/$paneId",
  validateSearch: (s: Record<string, unknown>): TerminalSearch => ({
    target: typeof s.target === "string" ? s.target : "default",
    session: typeof s.session === "string" ? s.session : "",
  }),
  component: MobileTerminalRoute,
});

const routeTree = rootRoute.addChildren([loginRoute, authRoute.addChildren([indexRoute, terminalRoute])]);
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router; }
}
