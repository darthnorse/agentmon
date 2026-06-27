import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev: proxy API + WS to a locally running hubd. Prod: this dev server is unused;
// `vite build` emits dist/ which hubd embeds and serves same-origin.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist" },
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: true },
      "/ws": { target: "ws://127.0.0.1:8080", ws: true },
    },
  },
});
