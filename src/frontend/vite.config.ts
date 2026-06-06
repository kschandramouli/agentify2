import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev proxy: the app calls /api and /admin on its own origin; Vite forwards them
// to the Go backend on :8080 — so no backend CORS config is needed for local dev.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: true },
      "/admin": { target: "http://localhost:8080", changeOrigin: true },
    },
  },
});
