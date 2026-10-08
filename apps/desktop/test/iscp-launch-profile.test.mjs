import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { resolveISCPLaunchProfile, ISCP_LAUNCH_PROFILE_NAME } from "../src/main/iscp-launch-profile.mjs";

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-iscp-launch-"));
  await fs.chmod(directory, 0o700);
  t.after(() => fs.rm(directory, { force: true, recursive: true }));
  const defaultUserData = path.join(directory, "default"), userData = path.join(directory, "selected");
  await fs.mkdir(defaultUserData, { mode: 0o700 }); await fs.mkdir(userData, { mode: 0o700 });
  const helper = path.join(directory, "helper.json"), profile = path.join(directory, "profile.json");
  const binding = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  await fs.writeFile(helper, JSON.stringify({ schema_version: 1, mode: "local-test", role: "initiator", relay_profile: "local-lab", binding }), { mode: 0o600 });
  const wrapper = { schema_version: 1, transport: "iscp", test_mode: true, helper_config: helper,
    backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", ...binding, domain_id: "domain", initiator_device_id: "desktop", responder_device_id: "gateway",
      responder_key_thumbprint: "a".repeat(64), relay_url: "http://127.0.0.1:18792", relay_profile: "local-lab", test_mode: true } };
  await fs.writeFile(profile, JSON.stringify(wrapper), { mode: 0o600 });
  const filename = path.join(defaultUserData, ISCP_LAUNCH_PROFILE_NAME);
  const selection = { schema_version: 1, test_mode: true, profile_path: profile, user_data_directory: userData };
  const writeSelection = (value = selection) => fs.writeFile(filename, JSON.stringify(value), { mode: 0o600 });
  return { directory, defaultUserData, userData, profile, helper, wrapper, filename, selection, writeSelection,
    resolve: (options = {}) => resolveISCPLaunchProfile({ defaultUserData, env: {}, ...options }) };
}

test("Finder launch preserves HTTPS defaults when no explicit selection exists and restores them when the selection is removed", async (t) => {
  const f = await fixture(t);
  assert.deepEqual(f.resolve(), { source: "default", allowLocalISCPTest: false });
  await f.writeSelection();
  assert.deepEqual(f.resolve(), { source: "persisted", configuredUserData: f.userData, iscpProfilePath: f.profile, allowLocalISCPTest: true });
  await fs.rm(f.filename);
  assert.deepEqual(f.resolve(), { source: "default", allowLocalISCPTest: false });
});

test("explicit environment wins over persisted selection, while qualification never reads that selection", async (t) => {
  const f = await fixture(t); await fs.writeFile(f.filename, "invalid private selection", { mode: 0o600 });
  const env = { SPARKCLAW_DESKTOP_ISCP_CONFIG: f.profile, SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_USER_DATA_DIR: f.userData };
  assert.equal(f.resolve({ env }).source, "environment");
  assert.deepEqual(f.resolve({ env: { SPARKCLAW_DESKTOP_USER_DATA_DIR: f.userData } }), { source: "environment", configuredUserData: f.userData, allowLocalISCPTest: false });
  assert.deepEqual(f.resolve({ qualification: true }), { source: "environment", configuredUserData: undefined, allowLocalISCPTest: false });
  assert.throws(() => f.resolve({ env, qualification: true }), /qualification/u);
  assert.throws(() => f.resolve(), /invalid JSON/u);
});

test("persistent selection fails closed for incomplete, extra-secret, non-test or default-directory choices", async (t) => {
  const f = await fixture(t);
  for (const selection of [{ ...f.selection, schema_version: 2 }, { ...f.selection, test_mode: false }, { ...f.selection, token: "must-not-be-here" },
    { ...f.selection, profile_path: "relative" }, { ...f.selection, user_data_directory: "relative" }, { ...f.selection, user_data_directory: f.defaultUserData }]) {
    await f.writeSelection(selection); assert.throws(() => f.resolve(), /invalid|absolute|dedicated/u);
  }
  await f.writeSelection(); await fs.writeFile(f.profile, JSON.stringify({ ...f.wrapper, test_mode: false }));
  assert.throws(() => f.resolve(), /selected profile is invalid/u);
});

test("duplicate selection keys are rejected rather than silently replacing the explicit choice", async (t) => {
  const f = await fixture(t);
  await fs.writeFile(f.filename, JSON.stringify(f.selection).replace('"test_mode":true', '"test_mode":false,"test_mode":true'), { mode: 0o600 });
  assert.throws(() => f.resolve(), /invalid JSON/u);
});

test("selection, profile, helper and dedicated user-data must stay owner-only", async (t) => {
  const f = await fixture(t); await f.writeSelection();
  for (const filename of [f.filename, f.profile, f.helper]) {
    await fs.chmod(filename, 0o644); assert.throws(() => f.resolve(), /owner-only/u); await fs.chmod(filename, 0o600);
  }
  for (const directory of [f.defaultUserData, f.userData, f.directory]) {
    await fs.chmod(directory, 0o755); assert.throws(() => f.resolve(), /owner-only/u); await fs.chmod(directory, 0o700);
  }
  assert.throws(() => f.resolve({ uid: process.getuid() + 1 }), /owner-only/u);
});

test("symlink files, directory aliases and missing selected profiles cannot silently restore HTTPS", async (t) => {
  const f = await fixture(t); await f.writeSelection();
  const alias = path.join(f.directory, "alias"); await fs.symlink(f.userData, alias);
  await f.writeSelection({ ...f.selection, user_data_directory: alias }); assert.throws(() => f.resolve(), /symbolic links/u);
  await f.writeSelection();
  const profileAlias = path.join(f.directory, "profile-alias.json"); await fs.symlink(f.profile, profileAlias);
  await f.writeSelection({ ...f.selection, profile_path: profileAlias }); assert.throws(() => f.resolve());
  await f.writeSelection({ ...f.selection, profile_path: path.join(f.directory, "missing-profile.json") }); assert.throws(() => f.resolve(), { code: "ENOENT" });
  await f.writeSelection(); await fs.rename(f.filename, `${f.filename}.real`); await fs.symlink(`${f.filename}.real`, f.filename);
  assert.throws(() => f.resolve());
});

test("environmental ISCP still requires explicit test opt-in, absolute paths and a private separate directory", async (t) => {
  const f = await fixture(t);
  const env = { SPARKCLAW_DESKTOP_ISCP_CONFIG: f.profile, SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_USER_DATA_DIR: f.userData };
  for (const override of [{ SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "0" }, { SPARKCLAW_DESKTOP_USER_DATA_DIR: undefined },
    { SPARKCLAW_DESKTOP_USER_DATA_DIR: f.defaultUserData }, { SPARKCLAW_DESKTOP_ISCP_CONFIG: "" }, { SPARKCLAW_DESKTOP_ISCP_CONFIG: ` ${f.profile}` }]) {
    assert.throws(() => f.resolve({ env: { ...env, ...override } }));
  }
});
