#!/usr/bin/env node

import http from "node:http";

const socketPath = process.env.SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET?.trim() ||
  "/run/sparkclaw/browser-controller/controller.sock";
const response = await new Promise((resolve, reject) => {
  const request = http.request({ socketPath, path: "/v1/health", method: "GET", timeout: 5000 }, (incoming) => {
    let body = "";
    incoming.setEncoding("utf8");
    incoming.on("data", (chunk) => {
      body += chunk;
      if (body.length > 64 << 10) request.destroy(new Error("browser controller response is too large"));
    });
    incoming.on("end", () => resolve({ status: incoming.statusCode, body }));
  });
  request.on("timeout", () => request.destroy(new Error("browser controller timed out")));
  request.on("error", reject);
  request.end();
});

if (response.status !== 200) throw new Error("browser controller health failed");
const health = JSON.parse(response.body);
if (health.schema_version !== 1 || health.profile_id !== "default" || !["ready", "busy"].includes(health.state)) {
  throw new Error("browser controller health is invalid");
}
process.stdout.write("SparkClaw browser controller ready\n");
