import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import fs from 'node:fs';
import os from 'node:os';

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

if (argument[1] === 'arm64') {
  const result=spawnSync('npm',['run','build:ezmcd-runtime'],{cwd:root,stdio:'inherit',env:buildEnvironment});
  if(result.error||result.status!==0)process.exit(result.status||1);
}

const temporary=fs.mkdtempSync(path.join(os.tmpdir(),'sparkx-mac-package-'));
const configPath=path.join(temporary,'builder.json');
const packageConfig=JSON.parse(fs.readFileSync(path.join(root,'apps/desktop/package.json'),'utf8')).build;
if(argument[1]==='arm64')packageConfig.mac.extraResources=[{from:'bin/ezmcd',to:'ezmcd',filter:['**/*']}];
fs.writeFileSync(configPath,JSON.stringify(packageConfig));
try { for (const args of [
  ["run", "build:desktop-ui"],
  ["run", "build:link-helper", "--", "darwin", argument[1]],
  ["run", "build:client-controller"],
  ["run", "check:desktop-managed-scripts"],
  ["--workspace", "@sparkclaw/desktop", "exec", "--", "electron-builder", "--config",configPath,"--mac", "dmg", "zip", `--${argument[1]}`, "--publish", "never"],
]) {
  const result = spawnSync("npm", args, { cwd: root, stdio: "inherit", env: buildEnvironment });
  if (result.error || result.status !== 0) throw new Error("Mac packaging step failed");
} } finally {fs.rmSync(temporary,{recursive:true,force:true});}
