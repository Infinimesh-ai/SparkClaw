import fs from "node:fs/promises";
import path from "node:path";

const DESCRIPTOR_KEYS = new Set(["schema_version", "origin", "deployment_id"]);
const LAN_DESCRIPTOR_KEYS = new Set(["schema_version", "origin", "deployment_id", "owner_id", "tls_certificate_sha256", "tls_ca_pem"]);
const CREDENTIAL_KEYS = new Set([
  "schema_version", "deployment_id", "client_id", "owner_id", "actor_id", "client_name", "token",
]);

export async function loadLocalBackendConnection({ descriptorPath, credentialPath, uid = process.getuid?.() }) {
	const descriptor = await loadLocalBackendDescriptor({ descriptorPath, uid });
	const credentialFile = await readControlledJSON(credentialPath, {
		label: "desktop Client credential",
		uid,
		privateFile: true,
		privateParent: true,
	});
	assertExactKeys(credentialFile.value, CREDENTIAL_KEYS, "desktop Client credential", ["actor_id"]);

	const credential = credentialFile.value;
	if (credential.schema_version !== 1) {
		throw new Error("Local workbench configuration version is unsupported");
	}
	const credentialDeploymentID = requiredString(credential.deployment_id, "credential deployment identity", 160);
	const clientID = requiredString(credential.client_id, "desktop Client identity", 160);
	const ownerID = requiredString(credential.owner_id, "desktop Owner identity", 160);
	const actorID = optionalString(credential.actor_id, 160) || ownerID;
	const clientName = requiredString(credential.client_name, "desktop Client name", 160);
	const token = requiredString(credential.token, "desktop Client token", 512);
	if (token.length < 32) throw new Error("Desktop Client token is incomplete");
	if (descriptor.deploymentID !== credentialDeploymentID) {
		throw new Error("Local workbench description and desktop credential do not identify the same deployment");
	}
	return Object.freeze({
		origin: descriptor.origin, deploymentID: descriptor.deploymentID, clientID, ownerID, actorID, clientName,
		authorization: `Bearer ${token}`,
	});
}

export async function loadLocalBackendDescriptor({ descriptorPath, uid = process.getuid?.() }) {
  const descriptorFile = await readControlledJSON(descriptorPath, {
    label: "local workbench description",
    uid,
    privateFile: false,
    privateParent: false,
  });
  return parseBackendDescriptor(descriptorFile.value);
}

export function parseBackendDescriptor(descriptor) {
  if (descriptor?.schema_version === 3) {
    const keys = ["schema_version", "transport", "origin", "deployment_id", "owner_id", "client_id", "domain_id", "initiator_device_id", "responder_device_id", "responder_key_thumbprint", "relay_url", "relay_profile", "test_mode"];
    assertExactKeys(descriptor, new Set(keys), "ISCP workbench description", ["relay_profile"]);
    if (descriptor.transport !== "iscp" || descriptor.test_mode !== true || descriptor.origin !== "https://iscp.invalid" ||
        typeof descriptor.responder_key_thumbprint !== "string" || !/^[A-Za-z0-9_-]{20,128}$/u.test(descriptor.responder_key_thumbprint)) throw new Error("ISCP workbench description is invalid");
    const relayURL = requiredString(descriptor.relay_url, "ISCP Relay address", 512);
    const relay = new URL(relayURL);
    const relayProfile = descriptor.relay_profile ?? "production";
    if (!["production", "local-lab"].includes(relayProfile)) throw new Error("ISCP Relay profile is invalid");
    if (relay.username || relay.password || relay.search || relay.hash) throw new Error("ISCP Relay address is invalid");
    if (relayProfile === "local-lab") {
      const loopback = relay.hostname === "localhost" || relay.hostname === "[::1]" || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/u.test(relay.hostname);
      if (!["http:", "ws:"].includes(relay.protocol) || !loopback || relayURL !== relay.origin) throw new Error("Local ISCP Relay must use a canonical HTTP/WS loopback origin");
    } else if (!["https:", "wss:"].includes(relay.protocol)) throw new Error("Production ISCP Relay requires HTTPS/WSS");
    return Object.freeze({ schemaVersion: 3, transport: "iscp", origin: descriptor.origin,
      deploymentID: requiredString(descriptor.deployment_id, "deployment identity", 160),
      ownerID: requiredString(descriptor.owner_id, "Owner identity", 160), clientID: requiredString(descriptor.client_id, "Client identity", 160),
      domainID: requiredString(descriptor.domain_id, "ISCP domain identity", 160),
      initiatorDeviceID: requiredString(descriptor.initiator_device_id, "ISCP initiator identity", 160),
      responderDeviceID: requiredString(descriptor.responder_device_id, "ISCP responder identity", 160),
      responderKeyThumbprint: descriptor.responder_key_thumbprint, relayURL, relayProfile, testMode: true });
  }
	if (descriptor?.schema_version === 2) {
		assertExactKeys(descriptor, LAN_DESCRIPTOR_KEYS, "LAN workbench description", ["tls_ca_pem"]);
		const origin = canonicalHTTPSOrigin(descriptor.origin);
		const deploymentID = requiredString(descriptor.deployment_id, "deployment identity", 160);
		const ownerID = requiredString(descriptor.owner_id, "Owner identity", 160);
		if (typeof descriptor.tls_certificate_sha256 !== "string" || !/^[a-f0-9]{64}$/u.test(descriptor.tls_certificate_sha256)) {
			throw new Error("LAN certificate fingerprint must be a lowercase SHA-256 digest");
		}
		if (descriptor.tls_ca_pem !== undefined && (typeof descriptor.tls_ca_pem !== "string" || descriptor.tls_ca_pem.length > 32768)) {
			throw new Error("LAN CA certificate is invalid");
		}
		const ca = descriptor.tls_ca_pem === undefined ? undefined : requiredString(descriptor.tls_ca_pem.trim(), "LAN CA certificate", 32768);
		if (ca && (!ca.startsWith("-----BEGIN CERTIFICATE-----") || !ca.endsWith("-----END CERTIFICATE-----"))) {
			throw new Error("LAN CA certificate must be a public PEM certificate");
		}
		return Object.freeze({ schemaVersion: 2, origin, deploymentID, ownerID, certificateSHA256: descriptor.tls_certificate_sha256, ...(ca ? { ca } : {}) });
	}
  assertExactKeys(descriptor, DESCRIPTOR_KEYS, "local workbench description");
	if (descriptor.schema_version !== 1) {
    throw new Error("Local workbench configuration version is unsupported");
  }
  const origin = canonicalLoopbackOrigin(descriptor.origin);
  const deploymentID = requiredString(descriptor.deployment_id, "deployment identity", 160);
	return Object.freeze({ origin, deploymentID });
}

function canonicalHTTPSOrigin(value) {
  const raw = requiredString(value, "LAN workbench origin", 512);
  let url;
  try { url = new URL(raw); } catch { throw new Error("LAN workbench origin is invalid"); }
  if (url.protocol !== "https:" || url.origin !== raw || url.username || url.password || url.pathname !== "/" || url.search || url.hash) {
    throw new Error("LAN workbench origin must be a canonical HTTPS origin");
  }
  return url.origin;
}

export async function verifyLocalBackend(connection, fetcher) {
  try {
    const response = await fetcher(`${connection.origin}/api/workbench/identity`, {
      method: "GET",
      headers: { Authorization: connection.authorization, Accept: "application/json" },
      redirect: "manual",
      signal: AbortSignal.timeout(5000),
    });
    if (response.status === 401 || response.status === 403) return connectionStatus("invalid_authentication");
    if (response.status >= 300 && response.status < 400) return connectionStatus("identity_conflict");
    if (!response.ok) return connectionStatus("service_unavailable");
    const identity = await response.json();
    if (identity?.deployment_id !== connection.deploymentID || identity?.owner_id !== connection.ownerID ||
        identity?.client_id !== connection.clientID) {
      return connectionStatus("identity_conflict");
    }
    return connectionStatus("connected");
  } catch {
    return connectionStatus("service_unavailable");
  }
}

export function connectionStatus(state) {
  return Object.freeze({ schema_version: 1, state });
}

function canonicalLoopbackOrigin(value) {
  const raw = requiredString(value, "local workbench origin", 512);
  let url;
  try {
    url = new URL(raw);
  } catch {
    throw new Error("Local workbench origin is invalid");
  }
  if (url.protocol !== "http:" || url.origin !== raw || url.username || url.password ||
      url.pathname !== "/" || url.search || url.hash || !["127.0.0.1", "[::1]"].includes(url.hostname)) {
    throw new Error("Local workbench origin must be a canonical HTTP loopback origin");
  }
  return url.origin;
}

async function readControlledJSON(filename, { label, uid, privateFile, privateParent }) {
  if (!path.isAbsolute(filename || "")) throw new Error(`${label} path must be absolute`);
  const [info, real] = await Promise.all([fs.lstat(filename), fs.realpath(filename)]);
  if (!info.isFile() || info.isSymbolicLink() || real !== path.resolve(filename)) {
    throw new Error(`${label} must be a regular file reached without symbolic links`);
  }
  if (Number.isInteger(uid) && info.uid !== uid) throw new Error(`${label} must be owned by the desktop user`);
  if (privateFile && (info.mode & 0o777) !== 0o600) throw new Error(`${label} must have mode 0600`);
  if (privateParent) {
    const parent = await fs.stat(path.dirname(filename));
    if (!parent.isDirectory() || (parent.mode & 0o777) !== 0o700 || (Number.isInteger(uid) && parent.uid !== uid)) {
      throw new Error(`${label} parent directory must be owned by the desktop user with mode 0700`);
    }
  }
  const raw = await fs.readFile(filename, "utf8");
  if (Buffer.byteLength(raw) > 64 << 10) throw new Error(`${label} is too large`);
  try {
    return { info, value: JSON.parse(raw) };
  } catch {
    throw new Error(`${label} is not valid JSON`);
  }
}

function assertExactKeys(value, allowed, label, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} must be a JSON object`);
  const optionalSet = new Set(optional);
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new Error(`${label} contains an unsupported field`);
  }
  for (const key of allowed) {
    if (!optionalSet.has(key) && !(key in value)) throw new Error(`${label} is incomplete`);
  }
}

function requiredString(value, label, maxLength) {
  const normalized = optionalString(value, maxLength);
  if (!normalized) throw new Error(`${label} is required`);
  return normalized;
}

function optionalString(value, maxLength) {
  if (value === undefined) return "";
  if (typeof value !== "string" || value !== value.trim() || value.length > maxLength) return "";
  return value;
}
