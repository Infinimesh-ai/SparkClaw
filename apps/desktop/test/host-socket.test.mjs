import assert from "node:assert/strict";
import test from "node:test";
import https from "node:https";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { EventEmitter, once } from "node:events";
import { connectPinnedHostSocket, HostSocket } from "../src/browser/host-socket.mjs";

function frame(text, final = true, opcode = 1) { const raw = Buffer.from(text); assert.ok(raw.length < 126); return Buffer.concat([Buffer.from([(final ? 128 : 0) | opcode, raw.length]), raw]); }
test("outbound WSS validates chain, hostname and certificate pin before credentials and rejects redirects", async (t) => {
 const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-host-tls-"));t.after(() => fs.rm(root, { recursive: true, force: true }));
 await promisify(execFile)("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", path.join(root, "key.pem"), "-out", path.join(root, "cert.pem")]);
 const [key, cert] = await Promise.all([fs.readFile(path.join(root, "key.pem")), fs.readFile(path.join(root, "cert.pem"))]);
 let requests = 0; let redirect = false; const sockets = new Set();
 const server = https.createServer({ key, cert });
 server.on("upgrade", (request, socket) => {
   requests++; assert.equal(request.url, "/api/v1/browser/hosts/connect");assert.equal(request.headers.authorization, "Bearer synthetic");
   sockets.add(socket);socket.on("close", () => sockets.delete(socket));
   if (redirect) { socket.end("HTTP/1.1 302 Found\r\nLocation: https://elsewhere.test\r\nContent-Length: 0\r\n\r\n");return; }
   const accept = crypto.createHash("sha1").update(request.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
   socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
   setTimeout(() => socket.write(frame('{"type":"welcome"}')), 10);
 });
 await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));t.after(() => { for (const socket of sockets) socket.destroy(); server.close(); });
 const descriptor = { schemaVersion: 2, origin: `https://127.0.0.1:${server.address().port}`, ca: cert.toString(), certificateSHA256: crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex") };
 await assert.rejects(connectPinnedHostSocket({ ...descriptor, certificateSHA256: "0".repeat(64) }, { Authorization: "Bearer synthetic" }), /fingerprint/);assert.equal(requests, 0);
 await assert.rejects(connectPinnedHostSocket({ ...descriptor, ca: undefined }, { Authorization: "Bearer synthetic" }), /self-signed/);assert.equal(requests, 0);
 await assert.rejects(connectPinnedHostSocket({ ...descriptor, schemaVersion: 1 }, {}), /pinned HTTPS/);
 const transport = await connectPinnedHostSocket(descriptor, { Authorization: "Bearer synthetic" });assert.deepEqual((await once(transport, "message"))[0], { type: "welcome" });transport.close();
 redirect = true; await assert.rejects(connectPinnedHostSocket(descriptor, { Authorization: "Bearer synthetic" }), /access rejected/);assert.equal(requests, 2);
});
test("closed WSS codec handles fragmentation and masked writes with bounded malformed input", () => {
 const socket = new EventEmitter();socket.writes = [];socket.write = (bytes) => socket.writes.push(bytes);socket.setTimeout = () => {};socket.destroy = () => {};
 const transport = new HostSocket(socket);const messages = [];transport.on("message", (value) => messages.push(value));
 socket.emit("data", frame('{"value":', false));socket.emit("data", frame('17}', true, 0));assert.deepEqual(messages, [{ value: 17 }]);
 transport.send({ type: "heartbeat" });const sent = socket.writes[0];assert.equal(sent[1] & 128, 128);const size = sent[1] & 127;const mask = sent.subarray(2, 6);const decoded = Buffer.alloc(size);for (let i = 0; i < size; i++) decoded[i] = sent[6+i] ^ mask[i%4];assert.deepEqual(JSON.parse(decoded), { type: "heartbeat" });
 socket.emit("data", Buffer.from([129, 127, 0, 0, 0, 0, 0, 3, 0, 0]));assert.equal(transport.closed, true);
});
