#!/usr/bin/env node

import fs from "node:fs/promises";
import net from "node:net";

import {
  ELECTRON_ADAPTER_PROTOCOL_VERSION,
  adapterSecretPath,
  adapterSocketPath,
  exactStatusResponse,
  parseConnectionBinding,
  parsePlaywrightConnectionURL,
} from "../src/browser/protocol.mjs";

const args = process.argv.slice(2).filter((argument) =>
  argument !== "--no-sandbox" && !argument.startsWith("--user-data-dir=") &&
  !argument.startsWith("--profile-directory="));

try {
  if (args.length === 1 && args[0] === "--check") {
    const response = await request({ schema_version: 1, operation: "status" });
    if (!exactStatusResponse(response)) throw new Error("Electron adapter is unavailable");
  } else if (args.length === 1 && args[0].startsWith("https://")) {
    const runtimeSecret = (await fs.readFile(adapterSecretPath(), "utf8")).trim();
    const response = await request({
      schema_version: 1,
      operation: "openPersonalPage",
      runtime_kind: "electron",
      runtime_secret: runtimeSecret,
      url: args[0],
    });
    if (response?.schema_version !== 1 || response?.state !== "opened" ||
        response?.runtime_kind !== "electron" || typeof response?.page_ref !== "string") {
      throw new Error("Electron personal page was rejected");
    }
  } else if (args.length === 1) {
    parsePlaywrightConnectionURL(args[0]);
    const binding = parseConnectionBinding({
      task_id: process.env.SPARKCLAW_ELECTRON_TASK_ID,
      session_id: process.env.SPARKCLAW_ELECTRON_SESSION_ID,
      controller_generation: Number(process.env.SPARKCLAW_ELECTRON_CONTROLLER_GENERATION),
      session_generation: Number(process.env.SPARKCLAW_ELECTRON_SESSION_GENERATION),
      page_generation: Number(process.env.SPARKCLAW_ELECTRON_PAGE_GENERATION),
    });
    const response = await request({
      schema_version: 1,
      operation: "openConnection",
      runtime_kind: "electron",
      protocol_version: ELECTRON_ADAPTER_PROTOCOL_VERSION,
      connection_credential: process.env.SPARKCLAW_ELECTRON_CONNECTION_CREDENTIAL,
      connection_url: args[0],
      binding,
    });
    if (response?.schema_version !== 1 || response?.state !== "opened" ||
        response?.runtime_kind !== "electron" || typeof response?.page_ref !== "string") {
      throw new Error("Electron adapter rejected the connection");
    }
  } else {
    throw new Error("Electron browser launcher arguments are invalid");
  }
} catch {
  process.exitCode = 1;
}

async function request(payload) {
  return await new Promise((resolve, reject) => {
    const socket = net.createConnection(adapterSocketPath());
    const timer = setTimeout(() => socket.destroy(new Error("Electron adapter timeout")), 8000);
    timer.unref?.();
    let input = "";
    socket.setEncoding("utf8");
    socket.on("connect", () => socket.write(`${JSON.stringify(payload)}\n`));
    socket.on("data", (chunk) => {
      input += chunk;
      if (input.length > 24 << 10) return socket.destroy(new Error("Electron adapter response is invalid"));
      const newline = input.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      try {
        const response = JSON.parse(input.slice(0, newline));
        socket.end();
        resolve(response);
      } catch (error) {
        socket.destroy(error);
      }
    });
    socket.on("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
  });
}
