/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// API_BASE_URL points at cmd/api (see backend/.env.example HTTP_ADDR).
// Proxying /api and /auth in dev avoids CORS entirely instead of requiring
// the Go backend to grow CORS middleware just for local development.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
      "/auth": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    globals: true,
    css: false,
    // Headroom above setup.ts's asyncUtilTimeout (8000ms) -- otherwise
    // Vitest's own default 5000ms testTimeout can fire first and report a
    // less useful "Test timed out" instead of the actual findBy*/waitFor
    // failure.
    testTimeout: 15000,
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/main.tsx", "src/vite-env.d.ts", "src/test/**"],
    },
  },
});
