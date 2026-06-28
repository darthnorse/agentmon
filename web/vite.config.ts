/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Dev: proxy API + terminal WS to a locally running hubd. Prod: this dev server is
// unused; `vite build` emits dist/ which hubd embeds and serves same-origin.
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: { outDir: "dist" },
  server: {
    proxy: {
      // ws:true so the terminal WS (/api/v1/.../io) upgrades through the dev proxy.
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: true, ws: true },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
