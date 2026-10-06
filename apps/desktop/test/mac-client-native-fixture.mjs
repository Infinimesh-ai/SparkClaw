import { app, safeStorage } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { ClientStore, CLIENT_SCHEMA_VERSION } from "../src/main/client-store.mjs";
import { ScheduleClient } from "../src/main/schedule-client.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { SecureCredentialStore } from "../src/main/secure-credential-store.mjs";

const root = process.env.SPARKCLAW_MAC_QUALIFICATION_ROOT;
const token = process.env.SPARKCLAW_MAC_QUALIFICATION_TOKEN;
const phase = process.argv[2];
app.setName("SparkClaw R3 Qualification");
app.setPath("userData", path.join(root, "profile"));
let store, schedules, execution;
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
    const draftContent = "  Mac 重启后继续编辑的草稿 🐾\n";
    const welcomeContent = "未选择对话的本机草稿";
    const intervalMS = 60 * 60 * 1000;
    execution = new ExecutionClient({ auth, store, getIdentity: () => auth.connection ? scope : null });
    schedules = new ScheduleClient({ auth, store, execution, getIdentity: () => auth.connection ? scope : null });
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
      store.saveDraft(scope, conversation.id, draftContent, [file.id], 0);
      store.saveDraft(scope, "", welcomeContent, [], 0);
      const dueAt = new Date(Date.now() + 1000).toISOString();
      const schedule = await schedules.create(scope, conversation.id, "只在未来在线轮次执行", dueAt, intervalMS);
      assert.equal(schedule.state, "saved");
      assert.equal(schedule.interval_ms, intervalMS);
      assert.equal(store.read(scope, conversation.id).schedules.length, 1);
      await fs.writeFile(expectedPath, JSON.stringify({ installation: store.installationID, conversation: conversation.id,
        request: task.request_id, file: file.id, schedule_id: schedule.schedule_id,
        occurrence_id: schedule.request_id, due_at: dueAt }), { mode: 0o600 });
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
      assert.deepEqual(store.draft(scope, expected.conversation), { content: draftContent, local_file_ids: [expected.file], revision: 1 });
      assert.deepEqual(store.draft(scope, ""), { content: welcomeContent, local_file_ids: [], revision: 1 });
      const occurrence = store.scheduledRequest(scope, expected.occurrence_id);
      assert.equal(occurrence.schedule_id, expected.schedule_id);
      assert.equal(occurrence.interval_ms, intervalMS);
      assert.equal(occurrence.due_at, expected.due_at);
      assert.equal(occurrence.definition_state, "active");
      if (phase === "restore") {
        assert.equal(occurrence.state, "saved", "unclaimed occurrence persisted from the earlier Electron process");
        assert.ok(Date.parse(expected.due_at) <= Date.now(), "restore begins after the offline occurrence was due");
        schedules.start();
        await eventually(() => store.scheduledRequest(scope, expected.occurrence_id).state === "missed");
        const [missed, future] = store.read(scope, expected.conversation).schedules;
        assert.equal(missed.request_id, expected.occurrence_id);
        assert.equal(missed.missed_count, 1);
        assert.equal(missed.missed_until, expected.due_at);
        assert.equal(store.request(scope, missed.request_id).explicitly_submitted, 0);
        assert.equal(future.state, "saved");
        assert.equal(future.schedule_id, expected.schedule_id);
        assert.notEqual(future.request_id, expected.occurrence_id);
        assert.equal(Date.parse(future.due_at), Date.parse(expected.due_at) + intervalMS);
        assert.ok(Date.parse(future.due_at) > Date.now());
        expected.future_occurrence_id = future.request_id;
        await fs.writeFile(expectedPath, JSON.stringify(expected), { mode: 0o600 });
        schedules.close();
        execution.close();
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
        assert.equal(occurrence.state, "missed");
        assert.equal(occurrence.missed_count, 1);
        const future = store.scheduledRequest(scope, expected.future_occurrence_id);
        assert.equal(future.state, "saved");
        assert.equal(future.schedule_id, expected.schedule_id);
        assert.ok(Date.parse(future.due_at) > Date.now());
        schedules.start();
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(store.scheduledRequest(scope, expected.occurrence_id).state, "missed");
        assert.equal(store.scheduledRequest(scope, expected.future_occurrence_id).state, "saved");
      } else throw new Error("Unknown Mac qualification phase");
    }
    if (phase !== "revoke") {
      const encrypted = await fs.readFile(vault.filename);
      assert.equal(encrypted.includes(Buffer.from(token)), false);
      assert.equal(JSON.stringify(auth.status).includes(token), false);
      assert.equal((await fs.stat(vault.filename)).mode & 0o777, 0o600);
    }
    assert.equal(store.db.prepare("PRAGMA user_version").get().user_version, CLIENT_SCHEMA_VERSION);
    assert.equal(store.db.prepare("PRAGMA journal_mode").get().journal_mode, "wal");
    assert.equal(store.db.prepare("PRAGMA synchronous").get().synchronous, 2);
    console.log(JSON.stringify({ event: "sparkclaw_mac_client_phase", passed: true, phase,
      electron: process.versions.electron, node: process.versions.node, schema: CLIENT_SCHEMA_VERSION,
      draft_and_selected_files_preserved: true, recurring_definition_preserved: true,
      ...(phase !== "prepare" ? { offline_occurrence_missed: true, future_occurrence_preserved: true } : {}) }));
    schedules.close(); execution.close();
    store.close(); store = undefined;
    app.exit(0);
  } catch (error) {
    // No credential, decrypted vault or server request is printed.
    console.error(error.stack); schedules?.close(); execution?.close(); store?.close(); app.exit(1);
  }
});

async function eventually(predicate) {
  const deadline = Date.now() + 5000;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error("Local scheduler did not record the missed occurrence");
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}
