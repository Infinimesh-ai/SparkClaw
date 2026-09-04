#!/usr/bin/env node

import {
  GmailCliError,
  createOwnedGmailTab,
  inspectGmailLogin,
  readStrictJson,
  requireExactObject,
  requireInvocationId,
  requireSafeHostCdpEnvironment,
  writeFailure,
  writeSuccess
} from "./gmail-host-cdp.mjs";

const KNOWN_ERROR_CODES = new Set([
  "email_login_required",
  "email_probe_invalid_input",
  "email_probe_configuration_error",
  "email_provider_origin_invalid",
  "email_login_evidence_conflict",
  "email_page_contract_changed",
  "email_probe_timeout",
  "email_agent_browser_invalid_output",
  "email_agent_browser_failed",
  "email_tab_cleanup_failed"
]);

async function main() {
  let tab;
  let response;
  let failure;

  try {
    const request = validateRequest(await readStrictJson("email_probe_invalid_input"));
    const cdpEndpoint = requireSafeHostCdpEnvironment("email_probe_configuration_error");
    tab = await createOwnedGmailTab({
      kind: "probe",
      cdpEndpoint,
      configurationErrorCode: "email_probe_configuration_error",
      timeoutErrorCode: "email_probe_timeout",
      invalidOutputErrorCode: "email_agent_browser_invalid_output",
      browserErrorCode: "email_agent_browser_failed",
      originErrorCode: "email_provider_origin_invalid",
      originConflictErrorCode: "email_login_evidence_conflict"
    });

    await tab.open();
    const { accountHint } = await inspectGmailLogin(tab, { includeAccountHint: true });
    response = {
      schema_version: request.schema_version,
      status: "ready",
      provider: "gmail"
    };
    if (accountHint !== undefined) {
      response.account_hint = accountHint;
    }
  } catch (caught) {
    failure = normalizeFailure(caught);
  } finally {
    if (tab !== undefined) {
      try {
        await tab.closeOwnedTab();
      } catch {
        failure ??= new GmailCliError("email_tab_cleanup_failed");
      }
      await tab.dispose();
    }
  }

  if (failure !== undefined) {
    writeFailure(failure.code);
    process.exitCode = failure.code === "email_login_required" ? 3 : 2;
    return;
  }
  writeSuccess(response);
}

function validateRequest(request) {
  requireExactObject(
    request,
    ["schema_version", "operation", "invocation_id", "provider", "account"],
    [],
    "email_probe_invalid_input"
  );
  if (request.schema_version !== 1 || request.operation !== "probe" ||
      request.provider !== "gmail" || request.account !== "default") {
    throw new GmailCliError("email_probe_invalid_input");
  }
  requireInvocationId(request.invocation_id, "email_probe_invalid_input");
  return request;
}

function normalizeFailure(caught) {
  if (caught instanceof GmailCliError && KNOWN_ERROR_CODES.has(caught.code)) {
    return caught;
  }
  return new GmailCliError("email_agent_browser_failed");
}

await main();
