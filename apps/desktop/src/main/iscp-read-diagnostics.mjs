const PATHS = new Set(['/api/config', '/readyz', '/api/owner', '/api/notifications', '/api/clients', '/api/workbench/identity', '/api/email/sync-status']);
const PHASES = new Set(['request', 'response', 'failure', 'proxy-gate', 'proxy-error']);
const STATES = new Set(['closed', 'verifying_relay', 'discovery_failed', 'connecting', 'relay_ready', 'handshaking', 'negotiating', 'capability_negotiation_failed', 'transport_ready', 'disconnected', 'authorization_expired', 'authorization_revoked', 'connected', 'reconnecting', 'service_unavailable', 'identity_conflict', 'invalid_authentication', 'incomplete_setup', 'locked', 'secure_storage_unavailable']);
const NAMES = new Set(['Error', 'TypeError', 'AbortError', 'TimeoutError', 'ISCPRequestNotSentError']);
const MESSAGES = new Set(['Backend request is unavailable', 'ISCP transport is unavailable', 'ISCP backend path is invalid', 'ISCP request was canceled', 'ISCP startup read was canceled', 'ISCP request deadline exceeded', 'ISCP authentication changed', 'ISCP request concurrency limit reached', 'ISCP read admission deadline exceeded', 'ISCP mail read queue limit reached', 'A change to this resource is already in progress', 'This capability is unavailable through ISCP', 'ISCP request exceeds the test profile limit', 'ISCP capacity class is unavailable', 'Body is unusable: Body has already been read', 'Response body object should not be disturbed or locked', 'Invalid URL', 'The operation was aborted', 'The operation was aborted due to timeout', 'fetch failed']);
const REASONS = new Set(['capacity', 'unavailable', 'validation', 'canceled']);
const OPERATIONS = new Set(ISCP_REGISTRY.map(row => row.name));
const CLASSES = new Set(['v1', 'business', 'control', 'bulk', 'events', 'audio']);
const count = value => Number.isSafeInteger(value) && value >= 0 && value <= 1_000_000 ? value : null;

// Private developer opt-in only. No object, URL, stack, header or response body
// is serialized; unknown errors are reduced to a fixed label. Diagnostics must
// never alter request/authentication behavior, including when stderr fails.
export function traceISCPRead({ phase, path, auth, error, generation = auth?.generation, transport = auth?.transport, signal, callerSignal, deadlineSignal, controllerSignal, responseStatus, startedMS }) {
  if (process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS !== '1' || auth?.descriptor?.transport !== 'iscp' || !PATHS.has(path)) return;
  try {
    console.error(JSON.stringify({ type: 'iscp.safe_read.diagnostic.v1', time_ms: Date.now(),
      phase: PHASES.has(phase) ? phase : 'unknown', path,
      elapsed_ms: count(Number.isFinite(startedMS) ? Date.now() - startedMS : undefined),
      auth_state: STATES.has(auth.status?.state) ? auth.status.state : 'unknown',
      transport_state: STATES.has(transport?.state) ? transport.state : 'unknown',
      current_transport_state: STATES.has(auth.transport?.state) ? auth.transport.state : 'unknown',
      generation: count(generation), current_generation: count(auth.generation), same_transport: transport === auth.transport,
      pending_calls: count(transport?.pending?.size), queued_mail_reads: count(transport?.mailReadQueue?.size),
      pending_operations: [...(transport?.pending?.values() || [])].slice(0, 16).map(call => ({
        operation: OPERATIONS.has(call.request?.operation) ? call.request.operation : 'unknown',
        capacity_class: CLASSES.has(call.capacityClass) ? call.capacityClass : 'unknown',
        age_ms: count(Number.isFinite(call.diagnosticStartedMS) ? Date.now() - call.diagnosticStartedMS : undefined),
      })),
      signal_aborted: Boolean(signal?.aborted), caller_aborted: Boolean(callerSignal?.aborted),
      deadline_aborted: Boolean(deadlineSignal?.aborted), controller_aborted: Boolean(controllerSignal?.aborted),
      response_status: Number.isInteger(responseStatus) && responseStatus >= 100 && responseStatus <= 599 ? responseStatus : null,
      error_name: error ? NAMES.has(error.name) ? error.name : 'unclassified' : null,
      error_message: error ? MESSAGES.has(error.message) ? error.message : 'unclassified' : null,
      not_sent_reason: REASONS.has(error?.reason) ? error.reason : null,
    }));
  } catch { /* Logging cannot interfere with connection recovery. */ }
}
import { ISCP_REGISTRY } from './iscp-routes.mjs';

