#!/usr/bin/env node

import {
  OutlookCliError,
  PROBE_EXPRESSION,
  classifyProbeEvidence,
  hasExactKeys,
  parseProbeEvidence,
  readCommandTimeout,
  readJsonStdin,
  validateInvocationId,
  withOwnedOutlookTab,
  writeFailure,
  writeSuccess,
} from "./outlook-host-cdp.mjs";

const INPUT_KEYS = [
  "account",
  "invocation_id",
  "operation",
  "provider",
  "schema_version",
];

async function main() {
  try {
    if (process.argv.length !== 2) throw new OutlookCliError("invalid_request");
    const input = validateInput(await readJsonStdin());
    const timeoutMs = readCommandTimeout("OUTLOOK_LOGIN_PROBE_TIMEOUT_MS");
    const result = await withOwnedOutlookTab({
      invocationId: input.invocation_id,
      operation: "probe",
      timeoutMs,
    }, async (tab) => {
      const evidence = parseProbeEvidence(await tab.inspect(PROBE_EXPRESSION));
      return classifyProbeEvidence(evidence);
    });
    writeSuccess(result);
  } catch (error) {
    writeFailure(error);
  }
}

function validateInput(input) {
  if (!hasExactKeys(input, INPUT_KEYS)) throw new OutlookCliError("invalid_request");
  if (
    input.schema_version !== 1 || input.operation !== "probe" ||
    input.provider !== "outlook" || input.account !== "default"
  ) {
    throw new OutlookCliError("invalid_request");
  }
  validateInvocationId(input.invocation_id);
  return input;
}

await main();
