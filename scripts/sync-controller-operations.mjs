import fs from "node:fs/promises";

// Generic Controller is the source of truth; desktop admission and Gateway
// mutation fences consume the same closed operation/argument table.
const source = new URL("../tools/browser-controller/src/generic-operations.json", import.meta.url);
const raw = await fs.readFile(source, "utf8");
for (const destination of ["../apps/desktop/src/browser/controller-operations.json", "../services/gateway/internal/browsercontrol/controller_operations.json"]) {
  const target = new URL(destination, import.meta.url);
  if (process.argv.includes("--check")) {
    if (await fs.readFile(target, "utf8") !== raw) throw new Error(`Controller operation projection is stale: ${destination}`);
  } else await fs.writeFile(target, raw);
}
