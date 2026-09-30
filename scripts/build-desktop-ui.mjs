import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Desktop requests are authenticated by main. Vite must not embed an
// operator's ordinary WebChat credentials or bypass the desktop API proxy.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const result = spawnSync("npm", ["run", "build:webchat"], {
  cwd: root, stdio: "inherit",
  env: { ...process.env, VITE_SPARKCLAW_API_TOKEN: "", VITE_SPARKCLAW_API_BASE: "" },
});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
