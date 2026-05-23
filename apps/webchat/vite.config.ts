import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 18790,
    proxy: {
      "/api": "http://127.0.0.1:18789",
      "/healthz": "http://127.0.0.1:18789",
      "/readyz": "http://127.0.0.1:18789"
    }
  },
  preview: {
    host: "127.0.0.1",
    port: 18790
  }
});
