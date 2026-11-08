import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The Go server (cmd/qpi) serves the API; in development Vite proxies to it.
const api = process.env.QPI_API ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": api, "/metrics": api } },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    css: false,
  },
});
