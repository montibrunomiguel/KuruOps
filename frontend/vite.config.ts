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
    // Vitest 5 defaults to the "forks" pool: one child PROCESS per test
    // file, each building its own jsdom. That is fine on a developer
    // machine and does not fit a 2-core CI runner -- every one of the 71
    // files died with "[vitest-pool]: Failed to start forks worker", so the
    // run reported "no tests" rather than a failure anyone could read.
    //
    // Threads share the process and are far cheaper to start. Isolation per
    // file is preserved, which is what the suite actually relies on; only
    // the process boundary is given up, and nothing here needs it. The cap
    // keeps the pool inside a small runner instead of scaling to a core
    // count that machine does not have.
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
