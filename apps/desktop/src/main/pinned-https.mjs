import crypto from "node:crypto";
import https from "node:https";
import tls from "node:tls";
import { Readable } from "node:stream";

export function pinnedHTTPSFetch(descriptor) {
  return (raw, init = {}) => new Promise((resolve, reject) => {
    const url = new URL(raw);
    if (url.origin !== descriptor.origin || url.protocol !== "https:") return reject(new Error("Backend origin differs from pinned identity"));
    const body = init.body === undefined ? undefined : Buffer.from(init.body);
    const method = (init.method || "GET").toUpperCase();
    const mailTransfer = method === "POST" && url.pathname === "/api/v1/mail/attachments";
    const mailMutation = ["POST", "PUT"].includes(method) && /^\/api\/email\/drafts(?:\/[^/]+(?:\/send)?)?$/u.test(url.pathname);
    const mailReconcile = method === "POST" && /^\/api\/email\/drafts\/[^/]+\/reconcile$/u.test(url.pathname);
    const mailLogin = method === "POST" && /^\/api\/email\/providers\/(outlook|qq_mail|gmail)\/(check|login-browser)$/u.test(url.pathname);
    const timeout = !url.search && (mailTransfer || mailMutation || mailReconcile || mailLogin) ? 180000 : 30000;
    const headers = new Headers(init.headers);
    if (body !== undefined) {
      // Node does not automatically frame DELETE bodies. Use the encoded byte
      // length for every method so the Gateway receives the complete JSON.
      headers.delete("transfer-encoding");
      headers.set("content-length", String(body.length));
    }
    const request = https.request(url, {
      method: init.method || "GET",
      headers: Object.fromEntries(headers.entries()),
      // A fresh socket validates the chain, hostname and leaf pin before it can
      // transmit any HTTP header, including the bearer. Never bypass PKI.
      agent: false,
      ca: descriptor.ca,
      signal: AbortSignal.any([AbortSignal.timeout(timeout), ...(init.signal ? [init.signal] : [])]),
      checkServerIdentity(hostname, certificate) {
        const error = tls.checkServerIdentity(hostname, certificate);
        if (error) return error;
        const digest = crypto.createHash("sha256").update(certificate.raw).digest("hex");
        if (digest !== descriptor.certificateSHA256) {
          const mismatch = new Error("Backend certificate fingerprint differs");
          mismatch.code = "SPARKCLAW_TLS_IDENTITY_CONFLICT";
          return mismatch;
        }
      },
    }, (response) => {
      const headers = new Headers();
      for (let index = 0; index < response.rawHeaders.length; index += 2) headers.append(response.rawHeaders[index], response.rawHeaders[index + 1]);
      const status = response.statusCode || 502;
      if ([204, 205, 304].includes(status) || init.method === "HEAD") {
        response.resume();
        resolve(new Response(null, { status, headers }));
      } else resolve(new Response(Readable.toWeb(response), { status, headers }));
    });
    request.on("error", reject);
    request.setTimeout(timeout, () => request.destroy(new Error("Backend request timed out")));
    if (body !== undefined) request.write(body);
    request.end();
  });
}

export function isTLSIdentityError(error) {
  return typeof error?.code === "string" && (error.code === "SPARKCLAW_TLS_IDENTITY_CONFLICT" ||
    error.code.startsWith("ERR_TLS_") || ["DEPTH_ZERO_SELF_SIGNED_CERT", "SELF_SIGNED_CERT_IN_CHAIN", "UNABLE_TO_VERIFY_LEAF_SIGNATURE", "CERT_HAS_EXPIRED", "UNABLE_TO_GET_ISSUER_CERT_LOCALLY"].includes(error.code));
}
