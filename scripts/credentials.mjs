#!/usr/bin/env node
import path from "node:path";
import { fileURLToPath } from "node:url";
import { claimCredential, parseCredentialArguments } from "./lib/credentials.mjs";

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    await claimCredential(parseCredentialArguments(process.argv.slice(2)));
  } catch (error) {
    process.stderr.write(`Credential retrieval stopped: ${error.message}\n`);
    process.exitCode = 1;
  }
}
