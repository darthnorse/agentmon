import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { HomeRoute } from "./routes/index";
import { LoginRoute } from "./routes/login";

const rootRoute = createRootRoute({ component: () => <Outlet /> });
const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: HomeRoute });
const loginRoute = createRoute({ getParentRoute: () => rootRoute, path: "/login", component: LoginRoute });

const routeTree = rootRoute.addChildren([indexRoute, loginRoute]);
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router; }
}
