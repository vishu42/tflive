import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/healthz": "http://localhost:8081",
      "/v1": "http://localhost:8081"
    }
  },
  test: {
    environment: "node"
  }
});
