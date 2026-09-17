import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  PROVIDER_SCRIPT_CONTRACT_PATH,
  ProviderScriptRegistry,
  providerScriptContract,
  renderProviderScriptContract,
} from "../src/provider-scripts.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");

test("the Gateway-embedded provider script contract matches the Controller registry", async () => {
  const current = await fs.readFile(path.join(REPOSITORY_ROOT, PROVIDER_SCRIPT_CONTRACT_PATH), "utf8");
  assert.equal(
    current,
    renderProviderScriptContract(),
    `${PROVIDER_SCRIPT_CONTRACT_PATH} is stale; run: npm run sync:provider-contract --prefix tools/browser-controller`,
  );
});

test("every contract entry resolves to a prepared registration with the same budget", async () => {
  const registry = new ProviderScriptRegistry();
  await registry.prepare();
  const { schema_version, scripts } = providerScriptContract();
  assert.equal(schema_version, 1);
  assert.equal(scripts.length, 24);
  assert.deepEqual(scripts.filter(entry => entry.operation === "collect_page").map(entry => entry.timeout_ms), [1_800_000,1_800_000,1_800_000]);
  for (const entry of scripts) {
    const registration = registry.resolve({
      provider: entry.provider,
      operation: entry.operation,
      scriptID: entry.script_id,
      revision: entry.revision,
    });
    assert.equal(registration.timeoutMS, entry.timeout_ms, entry.script_id);
    assert.ok(Number.isInteger(entry.timeout_ms) && entry.timeout_ms > 0, entry.script_id);
    if (registration.sourceFiles.includes('scripts/email/lib/read-capture.mjs')) {
      assert.ok(registration.sourceFiles.includes('scripts/email/lib/receipt-time.mjs'), `${entry.script_id}: exact timestamp parser must be in the checksum closure`);
    }
  }
  const keys = scripts.map((entry) => `${entry.provider}:${entry.operation}`);
  assert.deepEqual(keys, [...keys].sort(), "contract entries are sorted");
});
