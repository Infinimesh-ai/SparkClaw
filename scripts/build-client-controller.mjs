import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { installDownloads } from "../tools/browser-controller/src/install-playwright-downloads.mjs";
import { controllerBundleFiles, verifyControllerBundle, CONTROLLER_PROTOCOL } from "../apps/desktop/src/browser/controller-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const destination = path.join(root, "apps/desktop/bin/browser-controller");
const source = path.join(root, "tools/browser-controller");
const sources = ["client-main.mjs", "client-http-server.mjs", "controller-core.mjs", "generic-operations.json",
  "controller-error-codes.json", "errors.mjs", "protocol.mjs", "mcp-client-core.mjs", "playwright-output.mjs",
  "dom-actions.mjs", "mcp-errors.mjs", "electron-adapter-client.mjs", "mcp-stdio-rpc.mjs", "mcp-session-output.mjs",
  "mcp-arguments.mjs", "mcp-tabs.mjs", "bridge-native-protocol.mjs", "http-primitives.mjs", "unix-socket.mjs", "extension-downloads.cjs"];
if (process.argv.includes("--check")) {
  const manifest = await verifyControllerBundle(destination);
  process.stdout.write(`${JSON.stringify({ event: "client_controller_verified", bytes: manifest.bytes, versions: manifest.versions })}\n`);
} else {
  const staging = `${destination}.staging-${process.pid}`;
  await fs.mkdir(path.dirname(destination), { recursive: true });
  try {
    await fs.mkdir(path.join(staging, "src/browser"), { recursive: true });
    await fs.mkdir(path.join(staging, "bin"));
    for (const name of ["package.json", "package-lock.json"]) await fs.copyFile(path.join(source, "client", name), path.join(staging, name));
    for (const name of sources) await fs.copyFile(path.join(source, "src", name), path.join(staging, "src", name));
    await fs.copyFile(path.join(root, "apps/desktop/src/browser/protocol.mjs"), path.join(staging, "src/browser/protocol.mjs"));
    await fs.copyFile(path.join(root, "apps/desktop/bin/sparkclaw-electron-browser.mjs"), path.join(staging, "bin/sparkclaw-electron-browser.mjs"));
    execFileSync("npm", ["ci", "--omit=dev", "--omit=optional", "--ignore-scripts", "--no-audit", "--no-fund", "--fetch-timeout=60000", "--fetch-retries=1"], {
      cwd: staging, timeout: 180000, stdio: "inherit", env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1" },
    });
    // We invoke pinned module paths with Electron, never npm's shebang launchers.
    await fs.rm(path.join(staging, "node_modules/.bin"), { recursive: true, force: true });
    await installDownloads({ packageDirectory: staging, genericOnly: true });
    await installDownloads({ packageDirectory: staging, genericOnly: true, check: true });
    const inventory = await controllerBundleFiles(staging);
    const pkg = JSON.parse(await fs.readFile(path.join(staging, "package.json"), "utf8"));
    const manifest = { schema_version: 1, protocol: CONTROLLER_PROTOCOL,
      source_revision: execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, timeout: 5000, encoding: "utf8" }).trim(),
      versions: pkg.dependencies, ...inventory };
    await fs.writeFile(path.join(staging, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
    await verifyControllerBundle(staging);
    await fs.rm(destination, { recursive: true, force: true });
    await fs.rename(staging, destination);
    process.stdout.write(`${JSON.stringify({ event: "client_controller_built", files: Object.keys(inventory.files).length, bytes: inventory.bytes })}\n`);
  } finally {
    await fs.rm(staging, { recursive: true, force: true });
  }
}
