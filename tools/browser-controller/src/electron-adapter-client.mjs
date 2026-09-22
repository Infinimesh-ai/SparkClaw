import crypto from "node:crypto";
import fs from "node:fs/promises";
import net from "node:net";
import path from "node:path";

const ADAPTER_PROTOCOL_VERSION = 1;
const GENERATION = /^[0-9a-f]{32}$/u;
const CREDENTIAL = /^[A-Za-z0-9_-]{32,128}$/u;

export function electronAdapterConfig(env = process.env) {
  const socketPath = env.SPARKCLAW_ELECTRON_ADAPTER_SOCKET?.trim() || "";
  const secretPath = env.SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE?.trim() || "";
  if (!socketPath && !secretPath) return null;
  if (!path.isAbsolute(socketPath) || path.basename(socketPath) !== "electron-adapter.sock" ||
      !path.isAbsolute(secretPath) || path.basename(secretPath) !== "adapter-secret") {
    throw new Error("Electron adapter configuration is invalid");
  }
  return Object.freeze({ socketPath, secretPath });
}

export async function registerElectronConnection({ adapter, token, binding }) {
  if (!adapter) return null;
  const runtimeSecret = await readPrivateSecret(adapter.secretPath);
  const response = await request(adapter.socketPath, {
    schema_version: 1,
    operation: "registerConnection",
    runtime_kind: "electron",
    protocol_version: ADAPTER_PROTOCOL_VERSION,
    runtime_secret: runtimeSecret,
    token_sha256: crypto.createHash("sha256").update(token).digest("hex"),
    binding: normalizeBinding(binding),
  });
  if (response?.schema_version !== 1 || response?.state !== "registered" ||
      response?.runtime_kind !== "electron" || response?.protocol_version !== ADAPTER_PROTOCOL_VERSION ||
      !CREDENTIAL.test(response?.connection_credential ?? "") ||
      !GENERATION.test(response?.runtime_generation ?? "")) {
    throw new Error("Electron adapter registration failed");
  }
  return Object.freeze({
    socketPath: adapter.socketPath,
    credential: response.connection_credential,
    binding: normalizeBinding(binding),
    runtimeGeneration: response.runtime_generation,
  });
}

export function electronConnectionEnvironment(connection) {
  if (!connection) return {};
  return {
    SPARKCLAW_ELECTRON_ADAPTER_SOCKET: connection.socketPath,
    SPARKCLAW_ELECTRON_CONNECTION_CREDENTIAL: connection.credential,
    SPARKCLAW_ELECTRON_TASK_ID: connection.binding.task_id,
    SPARKCLAW_ELECTRON_SESSION_ID: connection.binding.session_id,
    SPARKCLAW_ELECTRON_CONTROLLER_GENERATION: String(connection.binding.controller_generation),
    SPARKCLAW_ELECTRON_SESSION_GENERATION: String(connection.binding.session_generation),
    SPARKCLAW_ELECTRON_PAGE_GENERATION: String(connection.binding.page_generation),
  };
}

async function readPrivateSecret(secretPath) {
  const stat = await fs.lstat(secretPath);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.uid !== process.getuid() ||
      (stat.mode & 0o077) !== 0 || stat.size < 32 || stat.size > 256) {
    throw new Error("Electron adapter secret is unavailable");
  }
  const value = (await fs.readFile(secretPath, "utf8")).trim();
  if (!CREDENTIAL.test(value)) throw new Error("Electron adapter secret is unavailable");
  return value;
}

function normalizeBinding(binding) {
  if (!binding || typeof binding !== "object" ||
      typeof binding.task_id !== "string" || typeof binding.session_id !== "string" ||
      !Number.isSafeInteger(binding.controller_generation) || binding.controller_generation < 1 ||
      !Number.isSafeInteger(binding.session_generation) || binding.session_generation < 1 ||
      !Number.isSafeInteger(binding.page_generation) || binding.page_generation < 1) {
    throw new Error("Electron adapter binding is invalid");
  }
  return {
    task_id: binding.task_id,
    session_id: binding.session_id,
    controller_generation: binding.controller_generation,
    session_generation: binding.session_generation,
    page_generation: binding.page_generation,
  };
}

async function request(socketPath, payload) {
  return await new Promise((resolve, reject) => {
    const socket = net.createConnection(socketPath);
    const timer = setTimeout(() => socket.destroy(new Error("Electron adapter timeout")), 6000);
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
