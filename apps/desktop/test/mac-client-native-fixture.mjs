import { app, safeStorage } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { ClientStore } from "../src/main/client-store.mjs";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { SecureCredentialStore } from "../src/main/secure-credential-store.mjs";

const root = process.env.SPARKCLAW_MAC_QUALIFICATION_ROOT;
const token = process.env.SPARKCLAW_MAC_QUALIFICATION_TOKEN;
const phase = process.argv[2];
app.setName("SparkClaw R3 Qualification");
app.setPath("userData", path.join(root, "profile"));
let store;
app.whenReady().then(async () => {
  try {
    assert.equal(process.platform, "darwin");
    assert.ok(safeStorage.isEncryptionAvailable(), "Mac Keychain encryption must be available");
    store = new ClientStore(path.join(root, "profile", "client-r3"));
    const scope = { deployment_id: "qualification-deployment", owner_id: "qualification-owner", client_id: "qualification-client" };
    const vault = new SecureCredentialStore({ directory: path.join(root, "profile", "authentication"), safeStorage });
    const auth = new DesktopAuth({ vault, descriptorPath: path.join(root, "profile", "backend.json"), installationID: store.installationID, requireLAN: true });
    const fileContent = Buffer.from("Mac 本地文件 🐾");
    const input = "Mac 本地中文对话 🐾";
    const expectedPath = path.join(root, "expected.json");
    if (phase === "prepare") {
      assert.deepEqual(store.list(scope), []);
      assert.equal((await auth.initialize()).state, "incomplete_setup");
      const descriptor = JSON.parse(await fs.readFile(path.join(root, "connection.json"), "utf8"));
      assert.equal((await auth.configure(descriptor)).state, "locked");
      assert.equal((await auth.login("invalid-" + "x".repeat(40))).state, "invalid_authentication");
      assert.equal(await vault.load(), undefined);
      assert.equal((await auth.login(token)).state, "connected");
      const conversation = store.create(scope, "Mac 原生验收");
      const task = store.enqueue(scope, conversation.id, input);
      const file = store.saveFile(scope, conversation.id, "中文文件.txt", fileContent);
      await fs.writeFile(expectedPath, JSON.stringify({ installation: store.installationID, conversation: conversation.id, request: task.request_id, file: file.id }), { mode: 0o600 });
    } else {
      const expected = JSON.parse(await fs.readFile(expectedPath, "utf8"));
      assert.equal(store.installationID, expected.installation);
      const state = (await auth.initialize()).state;
      assert.equal(state, phase === "revoke" ? "invalid_authentication" : "connected");
      const local = store.read(scope, expected.conversation);
      assert.equal(local.messages[0].content, input);
      assert.equal(local.tasks[0].request_id, expected.request);
      assert.equal(local.tasks[0].status, "awaiting_runtime");
      assert.deepEqual(store.file(scope, expected.file).content, fileContent);
      if (phase === "restore") {
        const response = await auth.authorizedFetch(`${auth.descriptor.origin}/api/fixture-stream`);
        const reader = response.body.getReader();
        assert.equal((await reader.read()).done, false);
        let stopped = 0;
        auth.onLock = () => { stopped++; };
        await auth.logout();
        assert.equal(stopped, 1);
        await assert.rejects(reader.read());
        assert.equal(await vault.load(), undefined);
        assert.equal(store.list(scope).length, 1);
        assert.equal((await auth.login(token)).state, "connected");
      } else if (phase === "revoke") {
        assert.equal(await vault.load(), undefined);
        assert.equal(auth.connection, undefined);
        assert.equal((await auth.authorizedFetch(`${auth.descriptor.origin}/api/fixture-stream`)).status, 401);
      } else throw new Error("Unknown Mac qualification phase");
    }
    if (phase !== "revoke") {
      const encrypted = await fs.readFile(vault.filename);
      assert.equal(encrypted.includes(Buffer.from(token)), false);
      assert.equal(JSON.stringify(auth.status).includes(token), false);
      assert.equal((await fs.stat(vault.filename)).mode & 0o777, 0o600);
    }
    assert.equal(store.db.prepare("PRAGMA user_version").get().user_version, 4);
    assert.equal(store.db.prepare("PRAGMA journal_mode").get().journal_mode, "wal");
    assert.equal(store.db.prepare("PRAGMA synchronous").get().synchronous, 2);
    console.log(JSON.stringify({ event: "sparkclaw_mac_client_phase", passed: true, phase, electron: process.versions.electron, node: process.versions.node }));
    store.close(); store = undefined;
    app.exit(0);
  } catch (error) {
    // No credential, decrypted vault or server request is printed.
    console.error(error.stack); store?.close(); app.exit(1);
  }
});
