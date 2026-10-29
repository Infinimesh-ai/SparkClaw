import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Desktop requests are authenticated by main. Vite must not embed an
// operator's ordinary WebChat credentials or bypass the desktop API proxy.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
for (const task of ["build:webchat", "build:local-preview"]) {
const result = spawnSync("npm", ["run", task], {
  cwd: root, stdio: "inherit",
  env: { ...process.env, VITE_SPARKCLAW_API_TOKEN: "", VITE_SPARKCLAW_API_BASE: "" },
});
if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
}
