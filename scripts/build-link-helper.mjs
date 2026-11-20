#!/usr/bin/env node
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const [platform = process.platform, architecture = process.arch] = process.argv.slice(2);
const goos = { darwin: "darwin", linux: "linux", win32: "windows" }[platform];
const goarch = { arm64: "arm64", x64: "amd64" }[architecture];
if (!goos || !goarch || process.argv.length > 4) throw new Error("Choose darwin/linux/win32 and arm64/x64");
const directory = path.join(root, "apps/desktop/bin");
await fs.mkdir(directory, { recursive: true });
// The desktop ships the link-layer client (link-helper), a standalone module
// built without the workspace.
const helpers = [
  { name: "link-helper", cwd: "services/link", target: "./cmd/link-helper", env: { GOWORK: "off" } },
];
for (const helper of helpers) {
  const output = path.join(directory, `${helper.name}${goos === "windows" ? ".exe" : ""}`);
  const temporary = `${output}.${process.pid}.tmp`;
  try {
    const build = spawnSync("go", ["build", "-trimpath", "-o", temporary, helper.target], {
      cwd: path.join(root, helper.cwd), stdio: "inherit", timeout: 300000,
      env: { ...process.env, ...helper.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch },
    });
    if (build.error || build.status !== 0) throw new Error(`${helper.name} build failed`);
    await fs.rename(temporary, output);
    process.stdout.write(`Built ${helper.name} for ${platform}/${architecture}\n`);
  } finally { await fs.rm(temporary, { force: true }); }
}
