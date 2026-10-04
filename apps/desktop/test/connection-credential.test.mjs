import assert from "node:assert/strict";
import test from "node:test";

import { createConnectionCredential, parseConnectionCredential } from "../src/main/connection-credential.mjs";

const descriptor = {
  schema_version: 2,
  origin: "https://sparkclaw.example:18790",
  deployment_id: "deployment",
  owner_id: "owner",
  tls_certificate_sha256: "a".repeat(64),
  tls_ca_pem: "-----BEGIN CERTIFICATE-----\npublic-ca\n-----END CERTIFICATE-----",
};
const token = "issued-device-token-" + "x".repeat(32);

test("connection credential round-trips the pinned public backend identity and secret token", () => {
  const encoded = createConnectionCredential(descriptor, token);
  assert.match(encoded, /^sparkclaw-connect-v1\.[A-Za-z0-9_-]+$/u);
  assert.deepEqual(parseConnectionCredential(encoded), { descriptor, token });
  assert.equal(encoded.includes(token), false, "the opaque copy value does not expose a separate raw token field");
});

test("connection credential rejects legacy, malformed, noncanonical and extended payloads", () => {
  assert.throws(() => createConnectionCredential({ schema_version: 1, origin: "http://127.0.0.1:18790", deployment_id: "deployment" }, token), /HTTPS LAN/u);
  assert.throws(() => createConnectionCredential(descriptor, "short"), /invalid/u);
  const valid = createConnectionCredential(descriptor, token);
  assert.throws(() => parseConnectionCredential(` ${valid}`), /invalid/u);
  assert.throws(() => parseConnectionCredential(`${valid}=`), /invalid/u);
  assert.throws(() => parseConnectionCredential(valid.replace(/.$/u, "!")), /invalid/u);
  const payload = Buffer.from(JSON.stringify({ schema_version: 1, backend: descriptor, token, extra: true })).toString("base64url");
  assert.throws(() => parseConnectionCredential(`sparkclaw-connect-v1.${payload}`), /invalid/u);
});
