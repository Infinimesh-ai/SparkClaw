// Verify the installed Electron/ASAR's UDS parser with explicit synthetic child
// replies. This is not a claim that MCP/browser produced a 10 MiB result.
export async function probePackagedResponseBoundary(application) {
  return application.evaluate(async ({ app }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), fs = require("node:fs/promises"), path = require("node:path"), http = require("node:http");
    const { controllerRequest, MAX_CONTROLLER_RESPONSE_BYTES } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-cap-")), socket = path.join(root, "reply.sock");
    const length = MAX_CONTROLLER_RESPONSE_BYTES - Buffer.byteLength(JSON.stringify({ text: "" }));
    const text = "界".repeat(Math.floor(length / 3)) + "x".repeat(length % 3), bytes = Buffer.from(JSON.stringify({ text }));
    assert.equal(bytes.length, MAX_CONTROLLER_RESPONSE_BYTES);
    let overflow = false;
    const server = http.createServer((request, response) => {
      if (request.url === "/v1/health") return response.end('{"state":"ready"}');
      for (let offset = 0; offset < bytes.length; offset += 32768) response.write(bytes.subarray(offset, offset + 32768));
      response.end(overflow ? " " : undefined);
    });
    try {
      await new Promise(resolve => server.listen(socket, resolve)); await fs.chmod(socket, 0o600);
      const accepted = await controllerRequest(socket, "execute", {});
      assert.equal(Buffer.byteLength(JSON.stringify(accepted.payload)), MAX_CONTROLLER_RESPONSE_BYTES);
      assert.equal(accepted.payload.text.length, text.length); assert.equal(accepted.payload.text.slice(-8), text.slice(-8));
      overflow = true;
      await assert.rejects(controllerRequest(socket, "execute", {}), /response is too large/);
      assert.deepEqual(await controllerRequest(socket, "health"), { status: 200, payload: { state: "ready" } });
      return { scope: "packaged_Electron_UDS_synthetic_reply_not_MCP_or_wire", accepted_bytes: bytes.length,
        rejected_bytes: bytes.length + 1, UTF8_preserved: true, no_truncated_success: true, subsequent_read_works: true };
    } finally {
      server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await fs.rm(root, { recursive: true, force: true });
    }
  });
}
