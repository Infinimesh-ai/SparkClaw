// Request identity is chosen once for each explicit owner submission. Transport
// recovery only queries it; it never creates another POST or request identity.
export function newWorkbenchRequestID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export type WorkbenchRequestState = "accepted" | "running" | "approval_pending" | "browser_login_blocked" | "completed" | "blocked" | "delivered" | "delivery_failed" | "failed" | "canceled" | "unknown" | "delivery_expired";

export type WorkbenchRequestStatus = {
  schema_version: 1;
  request_id: string;
  input_digest: string;
  state: WorkbenchRequestState;
  run_id?: string;
  message_id?: string;
  draft_revision?: number;
  submitted_draft_revision?: number;
};

export function validateWorkbenchRequestStatus(value: WorkbenchRequestStatus, requestID: string): WorkbenchRequestStatus {
  if (value?.schema_version !== 1 || value.request_id !== requestID ||
      !/^[a-f0-9]{64}$/u.test(value.input_digest) ||
      !["accepted", "running", "approval_pending", "browser_login_blocked", "completed", "blocked", "delivered", "delivery_failed", "failed", "canceled", "unknown", "delivery_expired"].includes(value.state)) {
    throw new Error("Workbench execution identity is unavailable");
  }
  return value;
}
