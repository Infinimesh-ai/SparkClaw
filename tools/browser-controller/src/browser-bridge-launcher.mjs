#!/usr/bin/env node

import net from "node:net";

import {
  assertExpectedReadyStatus,
  nativeSocketPath,
  parseLauncherArguments,
} from "./bridge-native-protocol.mjs";

const request = parseLauncherArguments(process.argv.slice(2));
const socket = net.createConnection(nativeSocketPath());
const timer = setTimeout(() => socket.destroy(new Error("Browser Bridge native host timeout")), 5000);
timer.unref?.();
let received = "";

socket.setEncoding("utf8");
socket.on("connect", () => {
  socket.write(`${JSON.stringify({ schema_version: 1, ...request })}\n`);
});
socket.on("data", (chunk) => {
  received += chunk;
  if (received.length > 4096) socket.destroy(new Error("Browser Bridge native host response is invalid"));
  const newline = received.indexOf("\n");
  if (newline < 0) return;
  const result = JSON.parse(received.slice(0, newline));
  try {
    if (request.operation === "status") assertExpectedReadyStatus(result);
    else if (result?.schema_version !== 1 || result?.state !== "opened") {
      throw new Error("Browser Bridge native host rejected the connection");
    }
    clearTimeout(timer);
    socket.end();
  } catch (error) {
    socket.destroy(error);
  }
});
socket.on("error", () => {
  clearTimeout(timer);
  process.exitCode = 1;
});
