import { Outlet } from "@tanstack/react-router";
import { useStateStream } from "@/hooks/useStateStream";

// Auth layout: one live SSE stream for the whole authed session, around the Outlet.
export function AuthLayout() {
  useStateStream();
  return <Outlet />;
}
