#!/usr/bin/env node
// Writes or checks the Gateway-embedded provider script contract so the Go
// email registry is generated from, never restated alongside, this registry.
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { PROVIDER_SCRIPT_CONTRACT_PATH, renderProviderScriptContract } from "./provider-scripts.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const target = path.join(REPOSITORY_ROOT, PROVIDER_SCRIPT_CONTRACT_PATH);
const rendered = renderProviderScriptContract();

if (process.argv.includes("--write")) {
  await fs.writeFile(target, rendered, "utf8");
} else if (process.argv.includes("--check")) {
  const current = await fs.readFile(target, "utf8").catch(() => "");
  if (current !== rendered) {
    console.error(`${PROVIDER_SCRIPT_CONTRACT_PATH} is stale; run: npm run sync:provider-contract --prefix tools/browser-controller`);
    process.exit(1);
  }
} else {
  process.stdout.write(rendered);
}
