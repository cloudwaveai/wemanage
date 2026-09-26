import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Build output goes into the Go embed target.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "../internal/webui/dist", emptyOutDir: true },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/v1": "http://localhost:8080",
    },
  },
});
