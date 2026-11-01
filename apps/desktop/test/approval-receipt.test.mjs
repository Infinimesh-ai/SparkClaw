import assert from 'node:assert/strict';
import test from 'node:test';
import { validateApprovalDecisionReceipt } from '../src/main/approval-receipt.mjs';
const binding = { transport: 'iscp', requestID: 'request', approvalID: 'approval', digest: 'a'.repeat(64), decision: 'approve', revision: 4 };
const receipt = () => ({ resolved: true, request_id: 'request', revision: '6', state: 'completed', approval_receipts: [
  { approval_id: 'approval', digest: binding.digest, revision: 5, state: 'decided', decision: 'approve', decided_at: '2026-10-30T07:00:00Z' },
] });
test('ISCP completed-race response retains the bound approval decision', () => {
  validateApprovalDecisionReceipt(receipt(), binding);
  validateApprovalDecisionReceipt({ resolved: true }, { ...binding, transport: 'http' });
  assert.throws(() => validateApprovalDecisionReceipt(receipt(), { ...binding, transport: 'http' }), /Invalid/);
  assert.throws(() => validateApprovalDecisionReceipt({ resolved: true }, binding), /Invalid/);
});
test('closed ISCP receipt rejects mismatches, rollback and unconfirmed decisions', () => {
  const changes = [v => { v.extra = true; }, v => { v.request_id = 'other'; }, v => { v.revision = '3'; },
    v => { v.revision = '06'; }, v => { v.state = 'invented'; }, v => { v.approval_receipts = []; },
    v => { v.approval_receipts.push(v.approval_receipts[0]); }, v => { v.approval_receipts[0].digest = 'b'.repeat(64); },
    v => { v.approval_receipts[0].decision = 'reject'; }, v => { v.approval_receipts[0].state = 'decision_unknown'; },
    v => { v.approval_receipts[0].revision = 7; }, v => { v.approval_receipts[0].extra = true; },
    v => { v.approval_receipts[0].decided_at = null; }, v => { v.approval_receipts[0].approval_id = 'different'; }];
  for (const change of changes) { const value = receipt(); change(value); assert.throws(() => validateApprovalDecisionReceipt(value, binding), /Invalid/); }
});
