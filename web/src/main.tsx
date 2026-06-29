/// <reference types="vite/client" />
/// <reference types="vite-plugin-pwa/client" />
import "./index.css";
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { Toaster } from "sonner";
import { router } from "./router";
import { queryClient } from "@/lib/query-client";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      {/* Tier-1 attention toasts (M9). Mounted once at the root so toasts survive
          route changes; useAttentionAlerts (in AuthLayout) calls sonner's `toast`. */}
      <Toaster position="top-right" richColors closeButton />
    </QueryClientProvider>
  </React.StrictMode>,
);

// Register the service worker (PWA install + Web-Push display). Guarded to PROD so
// dev/test environments — where the virtual module is a no-op shim and there is no
// built SW — never attempt registration. `registerType: 'autoUpdate'` makes the
// plugin swap to a fresh SW automatically when a new build is deployed.
if (import.meta.env.PROD) {
  void import("virtual:pwa-register").then(({ registerSW }) => {
    registerSW({ immediate: true });
  });
}
