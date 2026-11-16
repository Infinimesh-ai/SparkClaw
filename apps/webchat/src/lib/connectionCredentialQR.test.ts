import { describe, expect, it } from "vitest";
import { COMPACT_CONNECTION_CREDENTIAL_PREFIX, CONNECTION_CREDENTIAL_PREFIX, MAX_QR_BYTES, compactConnectionCredential, expandConnectionCredential } from "./connectionCredentialQR";

function canonical(value: unknown) {
  const bytes = new TextEncoder().encode(JSON.stringify(value));
  let binary = "";
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return CONNECTION_CREDENTIAL_PREFIX + btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

const pem = "-----BEGIN CERTIFICATE-----\\n" + "MIIBszCCAVmgAwIBAgIU".repeat(30) + "\\n-----END CERTIFICATE-----\\n";
const credential = canonical({ schema_version: 1, type: "sparkclaw.iscp.node_connection.v1", origin: "https://203.0.113.7:18443", tls_certificate_pem: pem, domain_id: "domain", relay_id: "relay", scopes: ["workbench.read", "events.read"], client_name: "手机", bootstrap_secret: "c2VjcmV0".repeat(6) });

describe("connection credential QR form", () => {
  it("round-trips losslessly and shrinks the payload", async () => {
    const compact = await compactConnectionCredential(credential);
    expect(compact?.startsWith(COMPACT_CONNECTION_CREDENTIAL_PREFIX)).toBe(true);
    expect(compact!.length).toBeLessThan(credential.length);
    expect(compact!.length).toBeLessThanOrEqual(MAX_QR_BYTES);
    expect(await expandConnectionCredential(compact!)).toBe(credential);
  });

  it("refuses other credential kinds and oversized payloads", async () => {
    expect(await compactConnectionCredential("sparkclaw-connect-v1.token-bundle")).toBeNull();
    expect(await compactConnectionCredential(CONNECTION_CREDENTIAL_PREFIX + "not base64!")).toBeNull();
    const random = crypto.getRandomValues(new Uint8Array(4000));
    let binary = "";
    random.forEach((byte) => { binary += String.fromCharCode(byte); });
    const noisy = canonical({ noise: btoa(binary) });
    expect(await compactConnectionCredential(noisy)).toBeNull();
  });
});
