const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const SHA = /^[a-f0-9]{64}$/u;
const STATES = new Set(['accepted', 'running', 'approval_pending', 'browser_login_blocked', 'completed', 'failed', 'canceled', 'unknown', 'delivery_expired', 'delivered']);
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const exact = (value, keys) => object(value) && Object.keys(value).sort().join(',') === [...keys].sort().join(',');

// HTTP/v1 and ISCP already have different closed response contracts. Validate
// the selected transport, plus the exact decision binding, before confirming it.
export function validateApprovalDecisionReceipt(value, { transport, requestID, approvalID, digest, decision, revision = 0, inputsDigest = "" }) {
  const invalid = () => { throw new Error('Invalid approval receipt'); };
  if (transport !== 'iscp') {
    if (!exact(value, ['resolved']) || value.resolved !== true) invalid();
    return;
  }
  if (!exact(value, ['resolved', 'request_id', 'revision', 'approval_receipts', 'state']) || value.resolved !== true ||
      value.request_id !== requestID || typeof value.revision !== 'string' || !/^[1-9][0-9]*$/u.test(value.revision) ||
      !STATES.has(value.state) || !Array.isArray(value.approval_receipts) || value.approval_receipts.length < 1 || value.approval_receipts.length > 32) invalid();
  const current = Number(value.revision);
  if (!Number.isSafeInteger(current) || !Number.isSafeInteger(revision) || current < revision) invalid();
  let matched = false;
  const seen = new Set();
  for (const receipt of value.approval_receipts) {
    const keys = ['approval_id', 'digest', 'revision', 'state', 'decision'];
    if (object(receipt) && Object.hasOwn(receipt, 'decided_at')) keys.push('decided_at');
    if (object(receipt) && Object.hasOwn(receipt, 'inputs_digest')) keys.push('inputs_digest');
    if (!exact(receipt, keys) || typeof receipt.approval_id !== 'string' || !ID.test(receipt.approval_id) || typeof receipt.digest !== 'string' || !SHA.test(receipt.digest) || seen.has(receipt.approval_id) ||
        !Number.isSafeInteger(receipt.revision) || receipt.revision < 1 || receipt.revision > current ||
        (Object.hasOwn(receipt, 'inputs_digest') && (typeof receipt.inputs_digest !== 'string' || !SHA.test(receipt.inputs_digest))) ||
        !['decided', 'decision_unknown'].includes(receipt.state) || !['approve', 'reject'].includes(receipt.decision) ||
        (Object.hasOwn(receipt, 'decided_at') && (typeof receipt.decided_at !== 'string' || !Number.isFinite(Date.parse(receipt.decided_at))))) invalid();
    seen.add(receipt.approval_id);
    if (receipt.approval_id === approvalID) {
      if (receipt.digest !== digest || receipt.decision !== decision || receipt.state !== 'decided' || (receipt.inputs_digest ?? '') !== inputsDigest) invalid();
      matched = true;
    }
  }
  if (!matched) invalid();
}
