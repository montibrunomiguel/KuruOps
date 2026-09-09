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
    // Kept for speed, not for correctness -- the suite runs in roughly half
    // the time on threads (46s vs 86s locally) because a thread is far
    // cheaper to start than the child process the default "forks" pool
    // spawns per test file, each building its own jsdom.
    //
    // Recorded because the comment here first said otherwise: the CI
    // failures that prompted this were NOT a pool problem. Every test file
    // failed to start a worker because the workflow was still on Node 20
    // while Vitest 5 requires ^22.12.0 || ^24 || >=26 -- switching pools
    // changed the message from "forks worker" to "threads worker" and
    // nothing else. Per-file isolation is unchanged either way; only the
    // process boundary is given up, and nothing here needs it.
    pool: "threads",
    poolOptions: {
      threads: { maxThreads: 4, minThreads: 1 },
    },
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/main.tsx", "src/vite-env.d.ts", "src/test/**"],
    },
  },
});
