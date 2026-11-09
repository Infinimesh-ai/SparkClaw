import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import electronExecutable from "electron";
import { verifyControllerBundle } from "../apps/desktop/src/browser/controller-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = path.join(root, "apps", "desktop", "dist");
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== "--arch" || !["arm64", "x64"].includes(args[1]))) throw new Error("Choose --arch arm64 or --arch x64");
const architecture = args[1] || "arm64";
const appImageArchitecture = architecture === "x64" ? "x86_64" : architecture;
const debArchitecture = architecture === "x64" ? "amd64" : architecture;
const entries = (await fs.readdir(output, { withFileTypes: true }))
  .filter((entry) => entry.isFile() && (entry.name.endsWith(`-linux-${appImageArchitecture}.AppImage`) || entry.name.endsWith(`-linux-${debArchitecture}.deb`)))
  .map((entry) => entry.name)
  .sort();
if (entries.length !== 2) throw new Error(`Expected one ${architecture} AppImage and one deb, found ${entries.length}`);

const artifacts = [];
for (const name of entries) {
  const filename = path.join(output, name);
  const content = await fs.readFile(filename);
  artifacts.push({ name, bytes: content.length, sha256: crypto.createHash("sha256").update(content).digest("hex") });
}
// electron-builder can produce Linux artifacts on macOS; resolve the locked
// build-host runtime through Electron's package instead of a Linux-only path.
const versions = JSON.parse(execFileSync(electronExecutable, [
  "-e", "console.log(JSON.stringify(process.versions))",
], { env: { ...process.env, ELECTRON_RUN_AS_NODE: "1" }, encoding: "utf8" }));
const lock = JSON.parse(await fs.readFile(path.join(root, "package-lock.json"), "utf8"));
if (versions.electron !== lock.packages["node_modules/electron"].version) throw new Error("Release runtime differs from the locked Electron version");
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
const controllerRoot = path.join(output, architecture === "x64" ? "linux-unpacked" : "linux-arm64-unpacked", "resources/browser-controller");
const controller = await verifyControllerBundle(controllerRoot);
const manifest = {
  schema_version: 1,
  product: "SparkX Desktop",
  version: packageVersion,
  platform: "linux",
  architecture,
  runtime: { electron: versions.electron, chromium: versions.chrome, node: versions.node },
  control: {
    browser_bridge: await dependency("@sparkclaw/browser-bridge", "tools/browser-bridge"),
    controller_protocol: controller.protocol,
    controller_manifest_sha256: crypto.createHash("sha256").update(await fs.readFile(path.join(controllerRoot, "manifest.json"))).digest("hex"),
    playwright_mcp: controller.versions["@playwright/mcp"],
    playwright: controller.versions.playwright,
    playwright_core: controller.versions["playwright-core"],
  },
  artifacts,
};
const manifestName = architecture === "arm64" ? "release-manifest.json" : `release-manifest-linux-${architecture}.json`;
const sumsName = architecture === "arm64" ? "SHA256SUMS" : `SHA256SUMS-linux-${architecture}`;
await fs.writeFile(path.join(output, manifestName), `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o644 });
await fs.writeFile(path.join(output, sumsName), `${artifacts.map((item) => `${item.sha256}  ${item.name}`).join("\n")}\n`, { mode: 0o644 });
process.stdout.write(`${JSON.stringify({ event: "sparkclaw_desktop_release_manifest", manifest })}\n`);
