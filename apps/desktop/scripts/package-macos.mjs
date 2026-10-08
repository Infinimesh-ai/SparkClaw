import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const argument = process.argv.slice(2);
if (process.platform !== "darwin") throw new Error("Mac packaging must run locally on macOS; Linux cross builds are unsupported");
if (argument.length !== 2 || argument[0] !== "--arch" || !["arm64", "x64"].includes(argument[1])) {
  throw new Error("Choose one Mac CPU: --arch arm64 or --arch x64");
}
if (process.arch !== argument[1]) throw new Error("Build with a Node.js installation matching the selected Mac CPU architecture");
if (!/^v26\./u.test(process.version)) throw new Error("Use the locked Node.js 26 toolchain");
// Vite otherwise inlines an operator's legacy WebChat token from the shell or
// .env files. Explicit empty values win over Vite's dotenv defaults.
const buildEnvironment = { ...process.env, VITE_SPARKCLAW_API_TOKEN: "", VITE_SPARKCLAW_API_BASE: "" };

for (const args of [
  ["run", "build:webchat"],
  ["run", "build:iscp-helper", "--", "darwin", argument[1]],
  ["run", "check:desktop-managed-scripts"],
  ["--workspace", "@sparkclaw/desktop", "exec", "--", "electron-builder", "--mac", "dmg", "zip", `--${argument[1]}`, "--publish", "never"],
]) {
  const result = spawnSync("npm", args, { cwd: root, stdio: "inherit", env: buildEnvironment });
  if (result.error || result.status !== 0) process.exit(result.status || 1);
}
