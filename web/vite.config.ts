/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";
import path from "node:path";

// Dev: proxy API + terminal WS to a locally running hubd. Prod: this dev server is
// unused; `vite build` emits dist/ which hubd embeds and serves same-origin.
export default defineConfig({
  plugins: [
    react(),
    // injectManifest: we own the SW source (src/sw.ts); the plugin precaches the
    // build output into it and emits dist/sw.js + dist/manifest.webmanifest.
    VitePWA({
      strategies: "injectManifest",
      srcDir: "src",
      filename: "sw.ts",
      registerType: "autoUpdate",
      injectRegister: null,
      manifest: {
        name: "AgentMon",
        short_name: "AgentMon",
        description: "Monitor and supervise your AI coding agents",
        display: "standalone",
        start_url: "/",
        scope: "/",
        theme_color: "#0b0b0b",
        background_color: "#0b0b0b",
        icons: [
          { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
          { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
          {
            src: "/icon-maskable-512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "maskable",
          },
        ],
      },
      injectManifest: {
        globPatterns: ["**/*.{js,css,html,svg,png,ico,woff2,mp3}"],
      },
      devOptions: { enabled: false },
    }),
  ],
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
