import { createRootRoute, createRoute, createRouter, redirect, Outlet } from "@tanstack/react-router";
import { useAuth } from "@/store/auth";
import { AuthLayout } from "@/components/AuthLayout";
import { LoginRoute } from "./routes/login";
import { ShellRoute } from "./routes/index";
import { MobileTerminalRoute, type TerminalSearch } from "./routes/terminal";
import { ProjectsIndexRoute, ProjectDetailRoute, validateProjectsSearch } from "./routes/projects";

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
  component: AuthLayout,
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

const projectsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/projects",
  validateSearch: validateProjectsSearch,
  component: ProjectsIndexRoute,
});

const projectRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/projects/$projectId",
  validateSearch: validateProjectsSearch,
  component: ProjectDetailRoute,
});

const routeTree = rootRoute.addChildren([loginRoute, authRoute.addChildren([indexRoute, terminalRoute, projectsRoute, projectRoute])]);
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router; }
}
