import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    host: true,
    port: 5173,
    watch: {
      usePolling: process.env.VITE_USE_POLLING === "1",
    },
    proxy: {
      // Compose publishes the API on host port 8001. Override for a local
      // `jobscout api` process that listens on API_PORT directly.
      "/api": process.env.VITE_API_PROXY_TARGET ?? "http://localhost:8001",
    },
  },
});
