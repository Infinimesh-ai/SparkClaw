import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";

import { asControllerError, invalidRequest, publicError } from "./errors.mjs";
import { MAX_REQUEST_BYTES } from "./protocol.mjs";

export function createRequestHandler(controller) {
  return async function requestHandler(request, response) {
    try {
      const url = new URL(request.url ?? "/", "http://browser-controller.local");
      if (request.method === "GET" && url.pathname === "/v1/health") {
        rejectQuery(url);
        return writeJSON(response, 200, controller.health());
      }
      if (request.method === "POST" && url.pathname === "/v1/validate-token") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.validateToken(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/acquire") {
        rejectQuery(url);
        return writeJSON(response, 201, await controller.acquire(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/release") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.release(await readJSON(request)));
      }
      return writeJSON(response, 404, {
        error: "browser controller route was not found",
        code: "not_found",
        retryable: false,
      });
    } catch (error) {
      const safe = asControllerError(error);
      return writeJSON(response, safe.status, publicError(safe));
    }
  };
}

export async function startUnixServer({ socketPath, controller }) {
  if (!path.isAbsolute(socketPath)) throw new TypeError("socketPath must be absolute");
  const parent = path.dirname(socketPath);
  await fs.mkdir(parent, { recursive: true, mode: 0o700 });
  await fs.chmod(parent, 0o700);
  await removeStaleSocket(socketPath);

  const server = http.createServer(createRequestHandler(controller));
  await new Promise((resolve, reject) => {
    const onError = (error) => reject(error);
    server.once("error", onError);
    server.listen(socketPath, () => {
      server.off("error", onError);
      resolve();
    });
  });
  await fs.chmod(socketPath, 0o600);

  return {
    server,
    socketPath,
    async close() {
      await controller.shutdown();
      await new Promise((resolve, reject) => {
        server.close((error) => error ? reject(error) : resolve());
      });
      await fs.rm(socketPath, { force: true });
    },
  };
}

async function readJSON(request) {
  const contentType = String(request.headers["content-type"] ?? "").split(";", 1)[0].trim().toLowerCase();
  if (contentType !== "application/json") throw invalidRequest();
  let size = 0;
  const chunks = [];
  for await (const chunk of request) {
    size += chunk.length;
    if (size > MAX_REQUEST_BYTES) throw invalidRequest();
    chunks.push(chunk);
  }
  if (size === 0) throw invalidRequest();
  try {
    return JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    throw invalidRequest();
  }
}

function rejectQuery(url) {
  if (url.search) throw invalidRequest();
}

function writeJSON(response, status, body) {
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
