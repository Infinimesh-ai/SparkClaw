import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "local-mail-files-"));
  const store = new ClientStore(path.join(root, "workbench"));
  t.after(() => { store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  const conversation = store.create(scope, "Desktop files");
  const file = store.saveFile(scope, conversation.id, "report.txt", Buffer.from("approved desktop bytes"));
  return { root, store, file, filename: path.join(store.filesRoot, file.id) };
}

test("file inventory contains only current desktop scope metadata and rejects forged identifiers", t => {
  const { store, file } = fixture(t);
  assert.equal(store.listFiles(scope)[0].id, file.id);
  assert.equal(Object.hasOwn(store.listFiles(scope)[0], "path"), false);
  for (const key of ["owner_id", "client_id", "deployment_id"]) {
    const foreign = { ...scope, [key]: "other" };
    assert.deepEqual(store.listFiles(foreign), []);
    assert.throws(() => store.file(foreign, file.id), /not found/);
  }
  for (const id of ["../../client.sqlite", "/tmp/report.txt", "file:///tmp/report.txt", crypto.randomUUID()]) assert.throws(() => store.file(scope, id));
});

for (const attack of ["symlink", "hardlink", "fifo", "oversized", "root replacement", "root symlink"]) {
  test(`local attachment refuses ${attack} before reading any bytes`, t => {
    const { root, store, file, filename } = fixture(t);
    if (attack === "symlink") { fs.renameSync(filename, `${filename}.original`); fs.symlinkSync(`${filename}.original`, filename); }
    if (attack === "hardlink") fs.linkSync(filename, path.join(root, "external-link"));
    if (attack === "fifo") { fs.unlinkSync(filename); assert.equal(spawnSync("mkfifo", ["-m", "600", filename], { timeout: 1000 }).status, 0); }
    if (attack === "oversized") fs.appendFileSync(filename, "unreviewed");
    if (attack.startsWith("root")) {
      fs.renameSync(store.filesRoot, `${store.filesRoot}.old`);
      if (attack === "root symlink") fs.symlinkSync(`${store.filesRoot}.old`, store.filesRoot);
      else { fs.mkdirSync(store.filesRoot, { mode: 0o700 }); fs.copyFileSync(`${store.filesRoot}.old/${file.id}`, filename); }
    }
    const read = t.mock.method(fs, "readSync");
    assert.throws(() => store.file(scope, file.id));
    assert.equal(read.mock.callCount(), 0);
  });
}

test("swapping the parent between directory check and file open cannot expose another directory", t => {
  const { store, file } = fixture(t);
  const open = fs.openSync;
  let swapped = false;
  t.mock.method(fs, "openSync", (filename, ...args) => {
    if (!swapped && String(filename).endsWith(`/${file.id}`)) {
      swapped = true;
      fs.renameSync(store.filesRoot, `${store.filesRoot}.old`);
      fs.mkdirSync(store.filesRoot, { mode: 0o700 });
      fs.writeFileSync(path.join(store.filesRoot, file.id), "approved desktop bytes", { mode: 0o600 });
    }
    return open(filename, ...args);
  });
  const read = t.mock.method(fs, "readSync");
  assert.throws(() => store.file(scope, file.id), /directory changed/);
  assert.equal(read.mock.callCount(), 0);
});

test("same-length content changes and metadata corruption are never silently rehashed for review", t => {
  const { store, file, filename } = fixture(t);
  fs.writeFileSync(filename, "different desktop data");
  assert.throws(() => store.file(scope, file.id), /verification/);
  store.db.prepare("UPDATE files SET name='../secret' WHERE id=?").run(file.id);
  assert.throws(() => store.file(scope, file.id), /name/i);
});

test("mail's smaller read bound rejects a file before allocating or reading its bytes", t => {
  const { store, file } = fixture(t);
  const read = t.mock.method(fs, "readSync");
  assert.throws(() => store.file(scope, file.id, file.size - 1), /read limit/);
  assert.equal(read.mock.callCount(), 0);
});
