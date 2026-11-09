import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { invalidRequest } from "./errors.mjs";
import { MAX_REQUEST_BYTES } from "./protocol.mjs";
import { listenOwnerOnlyUnixSocket } from "./unix-socket.mjs";

export async function startControllerUnixServer({ socketPath, controller, requestHandler }) {
  if (!path.isAbsolute(socketPath)) throw new TypeError("socketPath must be absolute");
  const parent = path.dirname(socketPath);
  await fs.mkdir(parent, { recursive: true, mode: 0o700 });
  await fs.chmod(parent, 0o700);
  await removeStaleSocket(socketPath);

  const server = http.createServer(requestHandler);
  await listenOwnerOnlyUnixSocket(server, socketPath);

  return {
    server,
    socketPath,
    async close() {
      let failure;
      try {await controller.shutdown();} catch (error) {failure=error;}
      await new Promise((resolve, reject) => {
        server.close((error) => error ? reject(error) : resolve());
        // A failed owned-page cleanup must not leave the listener alive until
        // systemd's stop timeout. All request work was already retired above.
        server.closeAllConnections();
      });
      await fs.rm(socketPath, { force: true });
      if (failure) throw failure;
    },
  };
}

async function readJSON(request, maximumBytes = MAX_REQUEST_BYTES, decoder = JSON.parse) {
  const contentType = String(request.headers["content-type"] ?? "").split(";", 1)[0].trim().toLowerCase();
  if (contentType !== "application/json") throw invalidRequest();
  let size = 0;
  const chunks = [];
  let body;
  try {
    for await (const chunk of request) {
      const buffered = Buffer.from(chunk);
      size += buffered.length;
      if (size > maximumBytes) throw invalidRequest();
      chunks.push(buffered);
    }
    if (size === 0) throw invalidRequest();
    body = Buffer.concat(chunks);
    return decoder(body.toString("utf8"));
  } catch {
    throw invalidRequest();
  } finally {
    body?.fill(0);
    for (const chunk of chunks) chunk.fill(0);
  }
}

function rejectQuery(url) {
  if (url.search) throw invalidRequest();
}

function writeJSON(response, status, body) {
  if (response.destroyed) return;
  const encoded = Buffer.from(`${JSON.stringify(body)}\n`);
  response.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "content-length": encoded.length,
    "cache-control": "no-store",
    "x-content-type-options": "nosniff",
  });
  response.end(encoded);
}

async function removeStaleSocket(socketPath) {
  try {
    const stat = await fs.lstat(socketPath);
    if (!stat.isSocket()) throw new Error("browser controller endpoint exists and is not a socket");
    await fs.rm(socketPath);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
}

export { readJSON, rejectQuery, writeJSON };
