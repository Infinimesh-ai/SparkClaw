// Compact QR form of a node connection credential. The canonical credential
// (sparkclaw-iscp-connect-v1. + base64url JSON) is too large for one QR code,
// so the QR carries raw-deflate of the same JSON bytes. The phone library
// inflates it back to the identical canonical credential and verifies the
// signature as usual; nothing is re-signed or rewritten.
export const CONNECTION_CREDENTIAL_PREFIX = "sparkclaw-iscp-connect-v1.";
export const COMPACT_CONNECTION_CREDENTIAL_PREFIX = "sparkclaw-iscp-connect-v1z.";
// QR version 40, byte mode, low error correction.
export const MAX_QR_BYTES = 2953;
// Above this length medium error correction no longer fits version 40.
export const MEDIUM_QR_BYTES = 2331;

function base64urlDecode(value: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) return null;
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (value.length % 4)) % 4);
  try {
    const binary = atob(padded);
    const out = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) out[index] = binary.charCodeAt(index);
    return out;
  } catch {
    return null;
  }
}

function base64urlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (let index = 0; index < bytes.length; index += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function transform(bytes: Uint8Array, stream: CompressionStream | DecompressionStream): Promise<Uint8Array> {
  const writer = stream.writable.getWriter();
  const written = writer.write(bytes as unknown as BufferSource).then(() => writer.close());
  const reader = stream.readable.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    total += value.length;
  }
  await written;
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) { out.set(chunk, offset); offset += chunk.length; }
  return out;
}

/** Returns the compact QR payload, or null when it cannot be produced or does not fit one QR code. */
export async function compactConnectionCredential(credential: string): Promise<string | null> {
  if (typeof CompressionStream === "undefined" || !credential.startsWith(CONNECTION_CREDENTIAL_PREFIX)) return null;
  const raw = base64urlDecode(credential.slice(CONNECTION_CREDENTIAL_PREFIX.length));
  if (!raw || raw.length === 0) return null;
  try {
    const compact = COMPACT_CONNECTION_CREDENTIAL_PREFIX + base64urlEncode(await transform(raw, new CompressionStream("deflate-raw")));
    return compact.length <= MAX_QR_BYTES ? compact : null;
  } catch {
    return null;
  }
}

/** Inverse of compactConnectionCredential; used by tests and diagnostics. */
export async function expandConnectionCredential(compact: string): Promise<string | null> {
  if (typeof DecompressionStream === "undefined" || !compact.startsWith(COMPACT_CONNECTION_CREDENTIAL_PREFIX)) return null;
  const raw = base64urlDecode(compact.slice(COMPACT_CONNECTION_CREDENTIAL_PREFIX.length));
  if (!raw) return null;
  try {
    return CONNECTION_CREDENTIAL_PREFIX + base64urlEncode(await transform(raw, new DecompressionStream("deflate-raw")));
  } catch {
    return null;
  }
}
