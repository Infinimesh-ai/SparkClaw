package contracttest

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// This file holds the concrete proof behind each central assertion name; the
// assertionChecks registry in jingsi_runtime_fixtures_test.go binds them.

func checkAcceptedHandle(s *step) {
	t := s.h.t
	t.Helper()
	operation := s.h.contract.operation(t, "execution.submit")
	if s.result.status != operation.SuccessStatus || s.result.body["kind"] != operation.SuccessKind {
		s.fatalf("submit = %d %s", s.result.status, s.result.raw)
	}
	sentKey := nestedString(t, s.state.lastRequest[operation.RequestKind].body, "payload", "request_key")
	if got := nestedString(t, s.result.body, "payload", "request_key"); got != sentKey {
		s.fatalf("accepted request_key = %q, want %q", got, sentKey)
	}
	executionID, acceptedAt := s.requireHandle()
	if s.state.executionID == "" {
		s.state.executionID, s.state.acceptedAt = executionID, acceptedAt
	} else if s.state.executionID != executionID || s.state.acceptedAt != acceptedAt {
		s.fatalf("handle drifted: %s@%s -> %s@%s", s.state.executionID, s.state.acceptedAt, executionID, acceptedAt)
	}
}

func (s *step) requireHandle() (string, string) {
	t := s.h.t
	t.Helper()
	execution := nestedObject(t, s.result.body, "payload", "execution")
	executionID := valueString(t, execution, "execution_id")
	if !s.h.contract.isToken(executionID) {
		s.fatalf("execution_id %q is not a token", executionID)
	}
	if state := valueString(t, execution, "state"); !slices.Contains(s.h.contract.enum(t, "$defs", "executionState"), state) {
		s.fatalf("state %q is not a contract state", state)
	}
	acceptedAt := valueString(t, execution, "accepted_at")
	if _, err := time.Parse(time.RFC3339Nano, acceptedAt); err != nil {
		s.fatalf("accepted_at %q: %v", acceptedAt, err)
	}
	if trace, ok := execution["trace_ref"]; ok {
		s.requireOpaqueRef(valueObject(t, trace))
	}
	return executionID, acceptedAt
}

func checkExactReplay(s *step) {
	t := s.h.t
	t.Helper()
	operation := s.h.contract.operation(t, "execution.submit")
	if s.result.status != operation.SuccessStatus {
		s.fatalf("replay = %d %s", s.result.status, s.result.raw)
	}
	executionID, acceptedAt := s.requireHandle()
	if executionID != s.state.executionID || acceptedAt != s.state.acceptedAt {
		s.fatalf("replay handle %s@%s, want %s@%s", executionID, acceptedAt, s.state.executionID, s.state.acceptedAt)
	}
	if s.result.before.records != s.result.after.records {
		s.fatalf("replay created a record")
	}
	s.h.settle(s.state)
	replay := s.h.must(*s.sent)
	if nestedString(t, replay.body, "payload", "execution", "execution_id") != s.state.executionID || replay.after.records != s.result.after.records {
		s.fatalf("post-terminal replay drifted: %s", replay.raw)
	}
	if calls := s.h.executor.callCount(); calls != 1 {
		s.fatalf("executor ran %d times for one request key", calls)
	}
}

func checkSemanticDrift(s *step) {
	t := s.h.t
	t.Helper()
	requireProblem(s, s.result, http.StatusConflict, "idempotency_conflict")
	if nestedBool(t, s.result.body, "payload", "retryable") {
		s.fatalf("idempotency_conflict is retryable")
	}
	lookup := s.h.must(s.h.lookupRequest(s.state, "contracttest-lookup-after-drift", ""))
	if nestedString(t, lookup.body, "payload", "outcome") != "bound" || nestedString(t, lookup.body, "payload", "execution", "execution_id") != s.state.executionID {
		s.fatalf("drift rewrote the bound execution: %s", lookup.raw)
	}
	if calls := s.h.executor.callCount(); calls != 1 {
		s.fatalf("executor ran %d times after drift", calls)
	}
}

func checkProblemNoSideEffects(s *step) {
	s.h.t.Helper()
	payload := s.payload()
	if s.result.body["kind"] != "problem" || payload["side_effects"] != "none" {
		s.fatalf("not a zero-side-effect problem: %s", s.result.raw)
	}
	if _, handle := payload["execution"]; handle {
		s.fatalf("problem carries a handle: %s", s.result.raw)
	}
	if s.result.before != s.result.after {
		s.fatalf("problem changed side effects: %+v -> %+v", s.result.before, s.result.after)
	}
}

// checkRequestKeyHeader pins that the header and body key must be identical:
// the fixture binds them, and any divergence is an invalid_request with no
// side effects.
func checkRequestKeyHeader(s *step) {
	t := s.h.t
	t.Helper()
	if s.sent == nil {
		s.fatalf("no submit request to vary")
	}
	header := s.h.contract.requestKeyHeader
	bodyKey := nestedString(t, s.sent.body, "payload", "request_key")
	if s.message.Headers[header] != bodyKey {
		s.fatalf("fixture header %s=%q differs from body key %q", header, s.message.Headers[header], bodyKey)
	}
	s.h.settle(s.state)
	drifted := *s.sent
	drifted.headers = map[string]string{header: bodyKey + "-drift"}
	requireProblem(s, s.h.must(drifted), http.StatusBadRequest, "invalid_request")
	unbound := *s.sent
	unbound.headers = map[string]string{}
	requireProblem(s, s.h.must(unbound), http.StatusBadRequest, "invalid_request")
}

// checkTransportNotNegative: a bound lookup after a transport failure proves
// the lost request did create work; an unresolved lookup must not be dressed
// as a negative fence.
func checkTransportNotNegative(s *step) {
	t := s.h.t
	t.Helper()
	payload := s.payload()
	switch payload["outcome"] {
	case "bound":
		if s.state.transportErr == nil {
			s.fatalf("scenario recorded no transport failure")
		}
		if _, ok := payload["execution"]; !ok || s.result.after.records < 1 {
			s.fatalf("lost submit left no durable execution: %s", s.result.raw)
		}
	case "unresolved":
		if hasKey(payload, "negative_fence") || hasKey(payload, "execution") {
			s.fatalf("unresolved lookup carries proof: %s", s.result.raw)
		}
		nestedNumber(t, s.result.body, "payload", "retry_after_ms")
		if s.result.before.records != s.result.after.records {
			s.fatalf("unresolved lookup committed a record")
		}
	default:
		s.fatalf("outcome %v is not a transport-failure reconciliation", payload["outcome"])
	}
}

func checkLostResponseRecovers(s *step) {
	t := s.h.t
	t.Helper()
	if s.result.status != http.StatusOK || nestedString(t, s.result.body, "payload", "outcome") != "bound" {
		s.fatalf("lookup = %d %s", s.result.status, s.result.raw)
	}
	executionID, acceptedAt := s.requireHandle()
	if s.state.executionID == "" {
		s.state.executionID, s.state.acceptedAt = executionID, acceptedAt
	}
	status := s.h.must(s.h.executionRequest(s.state, "execution.status", "contracttest-recovered-status", nil))
	if status.status != http.StatusOK || nestedString(t, status.body, "payload", "execution", "execution_id") != executionID {
		s.fatalf("recovered handle is not addressable: %d %s", status.status, status.raw)
	}
	replay := s.h.must(s.state.lastRequest[s.h.contract.operation(t, "execution.submit").RequestKind])
	if nestedString(t, replay.body, "payload", "execution", "execution_id") != executionID {
		s.fatalf("exact replay bound another handle: %s", replay.raw)
	}
	s.h.settle(s.state)
	if calls := s.h.executor.callCount(); calls != 1 {
		s.fatalf("executor ran %d times", calls)
	}
}

func (s *step) requireOpaqueRef(ref map[string]any) {
	t := s.h.t
	t.Helper()
	required := s.h.contract.requiredNames(t, "$defs", "opaqueRef")
	if len(ref) != len(required) {
		s.fatalf("opaque reference has extra fields: %#v", ref)
	}
	for _, name := range required {
		if !s.h.contract.isToken(valueString(t, ref, name)) {
			s.fatalf("opaque reference %s is not a token: %#v", name, ref)
		}
	}
}

func (s *step) requireNoInternals(raw string) {
	t := s.h.t
	t.Helper()
	leaks := []string{s.h.stateDir, testBearer, nestedString(t, s.state.seed.Body, "payload", "goal")}
	if summary, ok := jsonPath(s.state.seed.Body, "payload", "memory_context", "summary"); ok {
		leaks = append(leaks, summary.(string))
	}
	for _, leak := range leaks {
		if strings.Contains(raw, leak) {
			s.fatalf("response exposes internal or owner data %q", leak)
		}
	}
}

func checkTraceOpaque(s *step) {
	s.h.t.Helper()
	refs := collectRefs(s.payload(), "trace_ref", "")
	if len(refs) == 0 {
		s.fatalf("no trace_ref in %s", s.result.raw)
	}
	for _, ref := range refs {
		s.requireOpaqueRef(ref)
	}
	s.requireNoInternals(s.result.raw)
}

func checkArtifactsOpaque(s *step) {
	t := s.h.t
	t.Helper()
	allowed := s.h.contract.propertyNames(t, "$defs", "artifactRef")
	required := s.h.contract.requiredNames(t, "$defs", "artifactRef")
	refs := collectRefs(s.payload(), "artifact_ref", "artifact_refs")
	if s.result.body["kind"] == s.h.contract.operation(t, "execution.status").SuccessKind {
		list := nestedSlice(t, s.result.body, "payload", "result", "artifact_refs")
		if len(list) == 0 || float64(len(list)) > s.h.contract.schemaNumber(t, "$defs", "executionResult", "properties", "artifact_refs", "maxItems") {
			s.fatalf("result artifact_refs = %d entries", len(list))
		}
	}
	for _, ref := range refs {
		for name := range ref {
			if _, ok := allowed[name]; !ok {
				s.fatalf("artifact reference has undeclared field %q", name)
			}
		}
		for _, name := range required {
			if !s.h.contract.isToken(valueString(t, ref, name)) {
				s.fatalf("artifact reference %s is not a token: %#v", name, ref)
			}
		}
	}
	s.requireNoInternals(s.result.raw)
}

// checkUnresolved: an unresolved lookup commits nothing and no submit under
// that key can start work while the durable dependency is unavailable.
func checkUnresolved(s *step) {
	t := s.h.t
	t.Helper()
	payload := s.payload()
	if payload["outcome"] != "unresolved" || hasKey(payload, "execution") || hasKey(payload, "negative_fence") {
		s.fatalf("unresolved lookup = %s", s.result.raw)
	}
	if s.result.after.records != 0 || s.result.after.calls != 0 {
		s.fatalf("unresolved lookup left side effects: %+v", s.result.after)
	}
	key := valueString(t, payload, "request_key")
	submit := s.h.must(s.h.submitForKey(s.state, "contracttest-submit-unresolved", key))
	requireProblem(s, submit, http.StatusServiceUnavailable, "runtime_unavailable")
	if !nestedBool(t, submit.body, "payload", "retryable") || submit.after.records != 0 {
		s.fatalf("submit during unresolved window = %s", submit.raw)
	}
}

func checkNegativeFence(s *step) {
	t := s.h.t
	t.Helper()
	payload := s.payload()
	if payload["outcome"] != "not_started" || hasKey(payload, "execution") {
		s.fatalf("lookup = %s", s.result.raw)
	}
	fence := nestedObject(t, payload, "negative_fence")
	for _, name := range s.h.contract.requiredNames(t, "$defs", "negativeFence") {
		valueString(t, fence, name)
	}
	if s.result.after.records != s.result.before.records+1 {
		s.fatalf("negative fence was not committed durably: %+v -> %+v", s.result.before, s.result.after)
	}
	replay := s.h.must(*s.sent)
	if replay.raw != s.result.raw {
		s.fatalf("fence replay drifted:\n%s\n%s", s.result.raw, replay.raw)
	}
	s.h.restart()
	restarted := s.h.must(*s.sent)
	if restarted.raw != s.result.raw || restarted.after.records != s.result.after.records {
		s.fatalf("fence did not survive restart:\n%s\n%s", s.result.raw, restarted.raw)
	}
}

// checkNoHandleFence: once fenced, the key can never bind an execution; the
// only no-handle proof is the fence itself.
func checkNoHandleFence(s *step) {
	t := s.h.t
	t.Helper()
	key := nestedString(t, s.result.body, "payload", "request_key")
	submit := s.h.must(s.h.submitForKey(s.state, "contracttest-submit-fenced", key))
	requireProblem(s, submit, http.StatusConflict, "idempotency_conflict")
	if submit.after.calls != 0 {
		s.fatalf("fenced key started work")
	}
	lookup := s.h.must(*s.sent)
	if lookup.raw != s.result.raw {
		s.fatalf("fence changed after submit attempt:\n%s\n%s", s.result.raw, lookup.raw)
	}
}

func checkStatusCoarse(s *step) {
	t := s.h.t
	t.Helper()
	operation := s.h.contract.operation(t, "execution.status")
	if s.result.status != operation.SuccessStatus || s.result.body["kind"] != operation.SuccessKind {
		s.fatalf("status = %d %s", s.result.status, s.result.raw)
	}
	payload := s.payload()
	s.requireDeclaredFields(payload, "$defs", "statusResult", "allOf", "1", "properties", "payload")
	execution := nestedObject(t, payload, "execution")
	s.requireDeclaredFields(execution, "$defs", "executionRef")
	if valueString(t, execution, "execution_id") != s.state.executionID {
		s.fatalf("status addresses another execution: %s", s.result.raw)
	}
	state := valueString(t, execution, "state")
	terminal := slices.Contains(s.h.terminalStates(), state)
	result, hasResult := payload["result"]
	if terminal != hasResult {
		s.fatalf("state %q with result=%v", state, hasResult)
	}
	if hasResult {
		object := valueObject(t, result)
		s.requireDeclaredFields(object, "$defs", "executionResult")
		if valueString(t, object, "outcome") != state {
			s.fatalf("result outcome %q != state %q", object["outcome"], state)
		}
	}
	if parseTime(t, valueString(t, payload, "updated_at")).Before(parseTime(t, valueString(t, execution, "accepted_at"))) {
		s.fatalf("updated_at precedes accepted_at: %s", s.result.raw)
	}
	s.requireNoInternals(s.result.raw)
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("timestamp %q: %v", value, err)
	}
	return parsed
}

func (s *step) requireDeclaredFields(object map[string]any, path ...string) {
	s.h.t.Helper()
	allowed := s.h.contract.propertyNames(s.h.t, path...)
	for name := range object {
		if _, ok := allowed[name]; !ok {
			s.fatalf("%s exposes undeclared field %q", path[len(path)-1], name)
		}
	}
}

func checkEventsMonotonic(s *step) {
	t := s.h.t
	t.Helper()
	operation := s.h.contract.operation(t, "execution.events")
	if s.result.status != operation.SuccessStatus || s.result.body["kind"] != operation.SuccessKind {
		s.fatalf("events = %d %s", s.result.status, s.result.raw)
	}
	request := s.state.lastRequest[operation.RequestKind]
	after := uint64(nestedNumber(t, request.body, "payload", "after_sequence"))
	s.requireEventPage(s.result, after, int(nestedNumber(t, request.body, "payload", "limit")))
	if !nestedBool(t, s.result.body, "payload", "terminal") {
		return
	}
	// Page by one, replay byte-for-byte, and run the cursor off the end.
	page := func(afterSequence uint64, limit int) wireResult {
		return s.h.must(s.h.executionRequest(s.state, "execution.events", "contracttest-events-page", map[string]any{"after_sequence": afterSequence, "limit": limit}))
	}
	single := page(1, 1)
	events := s.requireEventPage(single, 1, 1)
	if len(events) != 1 || nestedNumber(t, events[0], "sequence") != 2 {
		s.fatalf("page after 1 limit 1 = %s", single.raw)
	}
	if replay := page(1, 1); replay.raw != single.raw {
		s.fatalf("page replay is not byte-identical:\n%s\n%s", single.raw, replay.raw)
	}
	last := uint64(nestedNumber(t, s.result.body, "payload", "next_sequence"))
	tail := page(last, 100)
	if len(s.requireEventPage(tail, last, 100)) != 0 || uint64(nestedNumber(t, tail.body, "payload", "next_sequence")) != last {
		s.fatalf("cursor past the end = %s", tail.raw)
	}
}

func (s *step) requireEventPage(result wireResult, after uint64, limit int) []map[string]any {
	t := s.h.t
	t.Helper()
	if nestedString(t, result.body, "payload", "execution_id") != s.state.executionID {
		s.fatalf("page addresses another execution: %s", result.raw)
	}
	events := make([]map[string]any, 0)
	next := after
	for _, raw := range nestedSlice(t, result.body, "payload", "events") {
		event := valueObject(t, raw)
		s.requireDeclaredFields(event, "$defs", "executionEvent")
		for _, name := range s.h.contract.requiredNames(t, "$defs", "executionEvent") {
			if _, ok := event[name]; !ok {
				s.fatalf("event lacks %s: %#v", name, event)
			}
		}
		if sequence := uint64(nestedNumber(t, event, "sequence")); sequence != next+1 {
			s.fatalf("event sequence %d follows %d", sequence, next)
		} else {
			next = sequence
		}
		if !s.h.contract.isToken(valueString(t, event, "event_id")) {
			s.fatalf("event_id is not a token: %#v", event)
		}
		if !slices.Contains(s.h.contract.enum(t, "$defs", "executionEvent", "properties", "type"), valueString(t, event, "type")) {
			s.fatalf("event type is not a contract type: %#v", event)
		}
		events = append(events, event)
	}
	if len(events) > limit {
		s.fatalf("page holds %d events over limit %d", len(events), limit)
	}
	if uint64(nestedNumber(t, result.body, "payload", "next_sequence")) != next {
		s.fatalf("next_sequence != final sequence: %s", result.raw)
	}
	state := s.h.executionState(s.state)
	terminal := slices.Contains(s.h.terminalStates(), state)
	if nestedBool(t, result.body, "payload", "terminal") != terminal {
		s.fatalf("terminal flag disagrees with state %q: %s", state, result.raw)
	}
	if terminal && len(events) > 0 && limit >= len(events) && after == 0 {
		final := events[len(events)-1]
		if final["type"] != "execution."+state || final["state"] != state {
			s.fatalf("final event does not project terminal state %q: %#v", state, final)
		}
	}
	return events
}

func checkCancelIdempotent(s *step) {
	t := s.h.t
	t.Helper()
	operation := s.h.contract.operation(t, "execution.cancel")
	if s.result.status != operation.SuccessStatus || s.result.body["kind"] != operation.SuccessKind {
		s.fatalf("cancel = %d %s", s.result.status, s.result.raw)
	}
	payload := s.payload()
	if valueString(t, payload, "execution_id") != s.state.executionID || !nestedBool(t, payload, "idempotent") {
		s.fatalf("cancel result = %s", s.result.raw)
	}
	states := s.h.contract.enum(t, "$defs", "cancelResult", "allOf", "1", "properties", "payload", "properties", "state")
	state := valueString(t, payload, "state")
	if !slices.Contains(states, state) {
		s.fatalf("cancel state %q is not a contract state", state)
	}
	replay := s.h.must(*s.sent)
	if slices.Contains(s.h.terminalStates(), state) {
		if replay.raw != s.result.raw {
			s.fatalf("terminal cancel replay drifted:\n%s\n%s", s.result.raw, replay.raw)
		}
		return
	}
	replayState := nestedString(t, replay.body, "payload", "state")
	if !slices.Contains(states, replayState) || !nestedBool(t, replay.body, "payload", "idempotent") || replay.after.records != s.result.after.records {
		s.fatalf("non-terminal cancel replay = %s", replay.raw)
	}
}

func checkCancelTerminalStable(s *step) {
	t := s.h.t
	t.Helper()
	state := nestedString(t, s.result.body, "payload", "state")
	if !slices.Contains(s.h.terminalStates(), state) {
		s.fatalf("state %q is not terminal", state)
	}
	first := s.h.must(s.h.executionRequest(s.state, "execution.status", "contracttest-terminal-status", nil))
	if nestedString(t, first.body, "payload", "execution", "state") != state || nestedString(t, first.body, "payload", "result", "outcome") != state {
		s.fatalf("status after terminal cancel = %s", first.raw)
	}
	for range 2 {
		if replay := s.h.must(*s.sent); replay.raw != s.result.raw {
			s.fatalf("terminal replay drifted:\n%s\n%s", s.result.raw, replay.raw)
		}
	}
	if second := s.h.must(s.h.executionRequest(s.state, "execution.status", "contracttest-terminal-status", nil)); second.raw != first.raw {
		s.fatalf("terminal status drifted:\n%s\n%s", first.raw, second.raw)
	}
	events := s.h.must(s.h.executionRequest(s.state, "execution.events", "contracttest-terminal-events", map[string]any{"after_sequence": 0, "limit": 100}))
	if !nestedBool(t, events.body, "payload", "terminal") {
		s.fatalf("events not terminal: %s", events.raw)
	}
	if calls := s.h.executor.callCount(); calls != 1 {
		s.fatalf("executor ran %d times", calls)
	}
}

// checkAuthorizationRequired: a request without the authorization block is
// invalid, and a request without the service bearer is unauthenticated; both
// are handle-free Problems with no side effects.
func checkAuthorizationRequired(s *step) {
	t := s.h.t
	t.Helper()
	requireProblem(s, s.result, http.StatusBadRequest, "invalid_request")
	if _, has := s.sent.body["authorization"]; has {
		s.fatalf("fixture request carries authorization")
	}
	seed := s.h.requestFor(s.state.seed, s.state)
	seed.noBearer = true
	requireProblem(s, s.h.must(seed), http.StatusUnauthorized, "unauthenticated")
	seed.noBearer, seed.bearer = false, testBearer+"-other"
	requireProblem(s, s.h.must(seed), http.StatusUnauthorized, "unauthenticated")
	if s.h.recordCount() != 0 {
		s.fatalf("unauthorized requests left records")
	}
}

// checkUnknownField: the unknown-field request is rejected, the same request
// stripped to the envelope's declared members is accepted, and the same
// unknown name is rejected again inside authorization and payload.
func checkUnknownField(s *step) {
	t := s.h.t
	t.Helper()
	requireProblem(s, s.result, http.StatusBadRequest, "invalid_request")
	declared := s.h.contract.requiredNames(t, "$defs", "requestEnvelope")
	stripped := *s.sent
	stripped.body = deepCopy(t, s.sent.body)
	unknown := make([]string, 0)
	for name := range stripped.body {
		if !slices.Contains(declared, name) {
			unknown = append(unknown, name)
			delete(stripped.body, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) == 0 {
		s.fatalf("fixture has no unknown envelope field")
	}
	operation := s.h.contract.operation(t, "execution.submit")
	accepted := s.h.must(stripped)
	if accepted.status != operation.SuccessStatus || accepted.body["kind"] != operation.SuccessKind {
		s.fatalf("stripped request = %d %s", accepted.status, accepted.raw)
	}
	s.state.remember(operation.RequestKind, stripped, valueString(t, stripped.body, "request_id"), accepted)
	s.state.executionID = nestedString(t, accepted.body, "payload", "execution", "execution_id")
	s.state.acceptedAt = nestedString(t, accepted.body, "payload", "execution", "accepted_at")
	s.h.settle(s.state)
	for _, member := range []string{"authorization", "payload"} {
		poisoned := stripped
		poisoned.body = deepCopy(t, stripped.body)
		valueObject(t, poisoned.body[member])[unknown[0]] = true
		requireProblem(s, s.h.must(poisoned), http.StatusBadRequest, "invalid_request")
	}
}

func checkAuthorizationDenial(s *step) {
	t := s.h.t
	t.Helper()
	requireProblem(s, s.result, http.StatusForbidden, "permission_denied")
	if nestedBool(t, s.result.body, "payload", "retryable") || s.result.after.records != 0 || s.result.after.calls != 0 {
		s.fatalf("denied submit left side effects: %s %+v", s.result.raw, s.result.after)
	}
}

// checkOptionalIgnored is a consumer rule; on the provider side it pins that
// the accepted response satisfies the fixture once its same-major extension
// fields are set aside, so adding optional fields stays compatible while
// dropping a core field fails.
func checkOptionalIgnored(s *step) {
	s.h.t.Helper()
	if len(s.missing) == 0 {
		s.fatalf("fixture exercises no optional extension field")
	}
	for _, path := range s.missing {
		if hasKey(s.result.body, strings.Split(path, ".")...) {
			s.fatalf("extension field %s unexpectedly present", path)
		}
	}
	s.requireHandle()
}
