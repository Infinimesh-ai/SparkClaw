import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { verifyControllerBundle } from "../src/browser/controller-bundle.mjs";
import { syntheticControllerBundle } from "./controller-bundle-fixture.mjs";

test("Controller integrity rejects changed bytes, extra files, permissions, symlinks and backend payloads", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-bundle-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const expected = await syntheticControllerBundle(root);
  assert.equal((await verifyControllerBundle(root)).bytes, expected.bytes);
  const entry = path.join(root, "src/client-main.mjs"), original = await fs.readFile(entry);
  await fs.appendFile(entry, "changed");
  await assert.rejects(verifyControllerBundle(root), /integrity/);
  await fs.writeFile(entry, original);
  await fs.chmod(entry, 0o700);
  await assert.rejects(verifyControllerBundle(root), /integrity/);
  await fs.chmod(entry, 0o644);
  const extra = path.join(root, "extra"); await fs.writeFile(extra, "extra");
  await assert.rejects(verifyControllerBundle(root), /integrity/);
  await fs.rm(extra);
  await fs.symlink(entry, extra);
  await assert.rejects(verifyControllerBundle(root), /unapproved/);
  await fs.rm(extra);
  await fs.writeFile(path.join(root, "src/mail-observer-feed.mjs"), "");
  await assert.rejects(verifyControllerBundle(root), /unapproved/);
});
