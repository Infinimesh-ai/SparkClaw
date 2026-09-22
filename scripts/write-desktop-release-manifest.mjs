import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = path.join(root, "apps", "desktop", "dist");
const entries = (await fs.readdir(output, { withFileTypes: true }))
  .filter((entry) => entry.isFile() && (/\.AppImage$/u.test(entry.name) || /\.deb$/u.test(entry.name)))
  .map((entry) => entry.name)
  .sort();
if (entries.length !== 2) throw new Error(`Expected one ARM64 AppImage and one deb, found ${entries.length}`);

const artifacts = [];
for (const name of entries) {
  const filename = path.join(output, name);
  const content = await fs.readFile(filename);
  artifacts.push({ name, bytes: content.length, sha256: crypto.createHash("sha256").update(content).digest("hex") });
}
const versions = JSON.parse(execFileSync(path.join(root, "node_modules", "electron", "dist", "electron"), [
  "-e", "console.log(JSON.stringify(process.versions))",
], { env: { ...process.env, ELECTRON_RUN_AS_NODE: "1" }, encoding: "utf8" }));
const lock = JSON.parse(await fs.readFile(path.join(root, "package-lock.json"), "utf8"));
const packageVersion = JSON.parse(await fs.readFile(path.join(root, "apps", "desktop", "package.json"), "utf8")).version;
const dependency = async (name, fallback) => {
  const locked = lock.packages?.[`node_modules/${name}`]?.version;
  if (locked) return locked;
  try {
    return JSON.parse(await fs.readFile(path.join(root, fallback, "package.json"), "utf8")).version;
  } catch {
    return "unavailable";
  }
};
const manifest = {
  schema_version: 1,
  product: "SparkClaw Desktop",
  version: packageVersion,
  platform: "linux",
  architecture: "arm64",
  runtime: { electron: versions.electron, chromium: versions.chrome, node: versions.node },
  control: {
    browser_bridge: await dependency("@sparkclaw/browser-bridge", "tools/browser-bridge"),
    playwright_mcp: await dependency("@playwright/mcp", "tools/browser-controller/node_modules/@playwright/mcp"),
    playwright_cli: await dependency("@playwright/cli", "tools/browser-controller/node_modules/@playwright/cli"),
    playwright: await dependency("playwright", "tools/browser-controller/node_modules/playwright"),
  },
  artifacts,
};
await fs.writeFile(path.join(output, "release-manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o644 });
await fs.writeFile(path.join(output, "SHA256SUMS"), `${artifacts.map((item) => `${item.sha256}  ${item.name}`).join("\n")}\n`, { mode: 0o644 });
process.stdout.write(`${JSON.stringify({ event: "sparkclaw_desktop_release_manifest", manifest })}\n`);
