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
const output = path.join(directory, `iscp-workbench${goos === "windows" ? ".exe" : ""}`);
const temporary = `${output}.${process.pid}.tmp`;
try {
  const build = spawnSync("go", ["build", "-trimpath", "-o", temporary, "./cmd/iscp-workbench"], {
    cwd: path.join(root, "services/gateway"), stdio: "inherit", timeout: 120000,
    env: { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch },
  });
  if (build.error || build.status !== 0) throw new Error("ISCP helper build failed");
  await fs.rename(temporary, output);
  process.stdout.write(`Built ISCP helper for ${platform}/${architecture}\n`);
} finally { await fs.rm(temporary, { force: true }); }
