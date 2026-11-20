import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { resolveDesktopUserData } from "../src/main/user-data-directory.mjs";

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkx-launch-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const defaultUserData = path.join(root, "default");
  const selected = path.join(root, "selected");
  for (const directory of [defaultUserData, selected]) await fs.mkdir(directory, { mode: 0o700 });
  const selector = path.join(defaultUserData, "iscp-launch-profile.json");
  const record = { schema_version: 2, mode: "node-managed", profile_path: path.join(root, "retired.json"), user_data_directory: selected };
  const write = (value = record) => fs.writeFile(selector, JSON.stringify(value), { mode: 0o600 });
  const resolve = (overrides = {}) => resolveDesktopUserData({ defaultUserData, env: {}, ...overrides });
  return { root, selected, defaultUserData, selector, record, write, resolve };
}

test("ordinary launches keep the existing selected data, even after the old connection files are gone", async t => {
  const f = await fixture(t);
  assert.equal(f.resolve(), undefined);
  await f.write();
  assert.equal(f.resolve(), f.selected);
  await f.write({ ...f.record, user_data_directory: f.defaultUserData });
  assert.equal(f.resolve(), f.defaultUserData);
  const { mode, ...legacy } = f.record;
  await f.write({ ...legacy, schema_version: 1, test_mode: true });
  assert.equal(f.resolve(), f.selected);
  await f.write({ ...legacy, schema_version: 1, test_mode: true, user_data_directory: f.defaultUserData });
  assert.throws(f.resolve, /dedicated/u);
});

test("qualification and explicit launches use their own directory", async t => {
  const f = await fixture(t);
  await f.write({ invalid: true });
  const env = { SPARKCLAW_DESKTOP_USER_DATA_DIR: f.selected };
  assert.equal(f.resolve({ env }), f.selected);
  assert.equal(f.resolve({ qualification: true, env }), f.selected);
  assert.throws(() => f.resolve({ qualification: true }), /separate/u);
  assert.throws(() => f.resolve({ qualification: true, env: { SPARKCLAW_DESKTOP_USER_DATA_DIR: f.defaultUserData } }), /separate/u);
  assert.throws(() => f.resolve({ env: { SPARKCLAW_DESKTOP_USER_DATA_DIR: "relative" } }), /absolute/u);
  const ownSelector = path.join(f.selected, path.basename(f.selector));
  await fs.writeFile(ownSelector, JSON.stringify({ ...f.record, user_data_directory: f.defaultUserData }), { mode: 0o600 });
  assert.equal(f.resolve({ env }), f.defaultUserData);
  assert.equal(f.resolve({ qualification: true, env }), f.selected);
});

test("malformed, duplicated, extra or missing selector data never opens a fresh fallback profile", async t => {
  const f = await fixture(t);
  await f.write();
  for (const value of [{ ...f.record, mode: "unknown" }, { ...f.record, secret: "unwanted" },
    { ...f.record, profile_path: "relative" }, { ...f.record, user_data_directory: "relative" },
    { ...f.record, user_data_directory: path.join(f.root, "missing") }]) {
    await f.write(value);
    assert.throws(f.resolve);
  }
  for (const raw of ["{", JSON.stringify(f.record).replace('"schema_version":2', '"schema_version":1,"schema_version":2'), " ".repeat(65537)]) {
    await fs.writeFile(f.selector, raw);
    assert.throws(f.resolve);
  }
});

test("the selector and both directories must be private, owned and free of symbolic links", async t => {
  const f = await fixture(t);
  await f.write();
  assert.throws(() => f.resolve({ uid: process.getuid() + 1 }), /owner-only/u);
  for (const [entry, mode] of [[f.selector, 0o600], [f.selected, 0o700], [f.defaultUserData, 0o700]]) {
    await fs.chmod(entry, mode | 0o044);
    assert.throws(f.resolve, /owner-only/u);
    await fs.chmod(entry, mode);
  }
  const alias = path.join(f.root, "alias");
  await fs.symlink(f.root, alias);
  assert.throws(() => f.resolve({ defaultUserData: path.join(alias, "default") }), /symbolic links/u);
  await f.write({ ...f.record, user_data_directory: path.join(alias, "selected") });
  assert.throws(f.resolve, /symbolic links/u);
  await f.write();
  await fs.rename(f.selector, f.selector + ".original");
  await fs.symlink(f.selector + ".original", f.selector);
  assert.throws(f.resolve);
});
