import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// API proxy hedefi: E2E varsayılan mock API; gerçek backend için
// API_PROXY_TARGET=http://127.0.0.1:8080 verilir (Makefile/CI yapar).
const apiProxyTarget = process.env.API_PROXY_TARGET ?? "http://127.0.0.1:8787";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  preview: {
    port: 4173,
    proxy: {
      "/api": { target: apiProxyTarget, changeOrigin: true },
      // E2E yardımcı uçları (/e2e/health, /e2e/reset) — yalnız mock.
      "/e2e": { target: apiProxyTarget, changeOrigin: true },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    globals: false,
    // Playwright E2E dosyaları vitest kapsam dışıdır (web/e2e).
    exclude: ["**/node_modules/**", "**/dist/**", "e2e/**"],
  },
});
