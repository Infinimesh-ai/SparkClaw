import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const gatewayTarget = loadEnv(mode, ".", "").SPARKCLAW_WEBCHAT_PROXY_TARGET || "http://127.0.0.1:18789";

  return {
    plugins: [react()],
    server: {
      host: "0.0.0.0",
      port: 18790,
      proxy: {
        "/api": gatewayTarget,
        "/healthz": gatewayTarget,
        "/readyz": gatewayTarget
      }
    },
    preview: {
      host: "0.0.0.0",
      port: 18790
    }
  };
});
