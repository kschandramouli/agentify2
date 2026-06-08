import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Switch target between local backend and the live AWS ALB:
//   Local dev:  http://localhost:8080
//   AWS (live): http://k8s-agentify-agentify-e5e5d95182-1161371101.ap-southeast-2.elb.amazonaws.com
const BACKEND =
  process.env.VITE_BACKEND_URL ||
  "http://k8s-agentify-agentify-e5e5d95182-1161371101.ap-southeast-2.elb.amazonaws.com";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api":    { target: BACKEND, changeOrigin: true },
      "/admin":  { target: BACKEND, changeOrigin: true },
      "/metrics":{ target: BACKEND, changeOrigin: true },
    },
  },
});
