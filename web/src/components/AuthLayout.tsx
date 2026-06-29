import { Outlet } from "@tanstack/react-router";
import { useStateStream } from "@/hooks/useStateStream";
import { useAttentionAlerts } from "@/hooks/useAttentionAlerts";

// Auth layout: one live SSE stream for the whole authed session, around the Outlet.
// The single stream also drives M9 Tier 1/2 attention alerts (toast/sound/vibrate,
// tab-aware) via the onAttention handler — no second EventSource.
export function AuthLayout() {
  const onAttention = useAttentionAlerts();
  useStateStream(undefined, onAttention);
  return <Outlet />;
}
