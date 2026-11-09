import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

export const CONTROLLER_PROTOCOL = "browser.controller.v1";
export const CONTROLLER_REQUIRED_FILES = ["package.json", "package-lock.json", "src/client-main.mjs", "src/client-http-server.mjs",
  "src/controller-core.mjs", "src/mcp-client-core.mjs", "src/generic-operations.json",
  "bin/sparkclaw-electron-browser.mjs", "src/browser/protocol.mjs",
  "node_modules/@playwright/mcp/cli.js", "node_modules/playwright-core/lib/coreBundle.js"];
export const CONTROLLER_VERSIONS = Object.freeze({ "@playwright/mcp": "0.0.80", playwright: "1.63.0-alpha-2026-08-31", "playwright-core": "1.63.0-alpha-2026-08-31" });
const forbidden = /(?:^|\/)(?:app-cli[^/]*|ai-chat-export\.mjs|ai-platform-login\.mjs|mail-observer-feed\.mjs|application-hook\.cjs|provider-scripts|userscripts|\.git|\.env|\.cache)(?:\/|$)/u;

export async function controllerBundleFiles(root) {
  const result = {};
  let total = 0;
  async function visit(directory, prefix = "") {
    const entries = await fs.readdir(directory, { withFileTypes: true });
    for (const entry of entries.sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)) {
      const name = `${prefix}${entry.name}`, filename = path.join(directory, entry.name);
      if (name === "manifest.json") continue;
      if (entry.isSymbolicLink() || forbidden.test(name)) throw new Error("Controller bundle contains an unapproved entry");
      if (entry.isDirectory()) await visit(filename, `${name}/`);
      else if (entry.isFile()) {
        const stat = await fs.lstat(filename);
        total += stat.size;
        if (Object.keys(result).length >= 10000 || total > 150 << 20) throw new Error("Controller bundle exceeds limits");
        result[name] = { bytes: stat.size, sha256: crypto.createHash("sha256").update(await fs.readFile(filename)).digest("hex"), executable: Boolean(stat.mode & 0o111) };
      } else throw new Error("Controller bundle entry is not a regular file");
    }
  }
  const stat = await fs.lstat(root);
  if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error("Controller bundle root is invalid");
  await visit(root);
  return { files: result, bytes: total };
}

export async function verifyControllerBundle(root) {
  const manifestStat = await fs.lstat(path.join(root, "manifest.json"));
  if (!manifestStat.isFile() || manifestStat.isSymbolicLink() || manifestStat.size > 4 << 20) throw new Error("Controller manifest is invalid");
  const manifest = JSON.parse(await fs.readFile(path.join(root, "manifest.json"), "utf8"));
  if (manifest.schema_version !== 1 || manifest.protocol !== CONTROLLER_PROTOCOL || !manifest.files ||
      CONTROLLER_REQUIRED_FILES.some(name => !Object.hasOwn(manifest.files, name))) throw new Error("Controller manifest is incomplete");
  const actual = await controllerBundleFiles(root);
  if (actual.bytes !== manifest.bytes || JSON.stringify(actual.files) !== JSON.stringify(manifest.files)) throw new Error("Controller bundle integrity check failed");
  for (const [name, version] of Object.entries(CONTROLLER_VERSIONS)) {
    const pkg = JSON.parse(await fs.readFile(path.join(root, "node_modules", name, "package.json"), "utf8"));
    if (pkg.version !== version || manifest.versions?.[name] !== version) throw new Error("Controller dependency version differs");
  }
  return Object.freeze(manifest);
}
