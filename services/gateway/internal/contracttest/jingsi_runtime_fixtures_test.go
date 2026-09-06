package contracttest

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// TestJingSiRuntimeProviderPassesCentralFixtures drives every message of every
// central conformance fixture through a real jingsiruntime.Provider over HTTP:
// request messages are sent as-is under the binding's method, path, media type
// and request-key header; response messages are matched against what the
// provider actually returned; and each assertion name a message carries is
// proven by a concrete check. The manifest self-consistency gate runs first.
func TestJingSiRuntimeProviderPassesCentralFixtures(t *testing.T) {
	t.Parallel()
	contract := loadCentralContract(t)
	for _, entry := range contract.cases {
		scenario, ok := scenarios[entry.Category]
		if !ok {
			t.Fatalf("central case %q has category %q without a provider scenario", entry.Name, entry.Category)
		}
		t.Run(entry.Name, func(t *testing.T) {
			t.Parallel()
			scenario(t, contract, entry)
		})
	}
}

var scenarios = map[string]func(*testing.T, *centralContract, centralCase){
	"submit_idempotency":          runSubmitIdempotency,
	"submit_conflict":             runSubmitConflict,
	"lookup_recovery":             runLookupRecovery,
	"lookup_negative_fence":       runLookupNegativeFence,
	"status_events_result":        runStatusEventsResult,
	"cancel":                      runCancel,
	"authorization_compatibility": runAuthorizationCompatibility,
}

// assertionChecks maps every central assertion name to the concrete proof the
// provider gate runs for a message carrying it. A fixture assertion without an
// entry fails the precondition gate rather than being ignored.
var assertionChecks = map[string]func(*step){
	"artifacts.opaque_references":                         checkArtifactsOpaque,
	"authorization.denial_no_side_effects":                checkAuthorizationDenial,
	"authorization.required":                              checkAuthorizationRequired,
	"cancel.idempotent":                                   checkCancelIdempotent,
	"cancel.terminal_stable":                              checkCancelTerminalStable,
	"events.monotonic_cursor":                             checkEventsMonotonic,
	"idempotency.exact_replay_same_handle":                checkExactReplay,
	"idempotency.semantic_drift_conflict":                 checkSemanticDrift,
	"lookup.lost_response_recovers_handle":                checkLostResponseRecovers,
	"lookup.not_started_negative_fence":                   checkNegativeFence,
	"lookup.unresolved_blocks_replay":                     checkUnresolved,
	"no_handle.zero_side_effects_requires_negative_fence": checkNoHandleFence,
	"problem.no_new_side_effects":                         checkProblemNoSideEffects,
	"request.unknown_field_rejected":                      checkUnknownField,
	"request_key.header_body_exact":                       checkRequestKeyHeader,
	"response.optional_field_ignored":                     checkOptionalIgnored,
	"status.coarse_only":                                  checkStatusCoarse,
	"submit.accepted_handle":                              checkAcceptedHandle,
	"trace.opaque_reference":                              checkTraceOpaque,
	"transport.failure_not_negative_proof":                checkTransportNotNegative,
}

type step struct {
	h       *harness
	state   *scenarioState
	message fixtureMessage
	sent    *wireRequest
	result  wireResult
	missing []string
}

func (s *step) fatalf(format string, args ...any) {
	s.h.t.Helper()
	s.h.t.Fatalf(s.message.Label+": "+format, args...)
}

func (s *step) payload() map[string]any {
	s.h.t.Helper()
	return nestedObject(s.h.t, s.result.body, "payload")
}

type driveHooks struct {
	before  func(fixtureMessage)
	problem func(fixtureMessage) wireResult
}

// drive walks a fixture's messages in order. A request message is sent and
// its response kept; a response message reuses the response of the request
// sharing its request id, or is produced by replaying the last request of its
// operation under the fixture's request id. Before a request whose paired
// response (or a replayed response itself) expects a terminal state, the
// scenario's execution is allowed to reach it.
func (h *harness) drive(entry centralCase, state *scenarioState, hooks driveHooks) {
	t := h.t
	t.Helper()
	for index, message := range entry.Messages {
		if hooks.before != nil {
			hooks.before(message)
		}
		kind := valueString(t, message.Body, "kind")
		requestID := valueString(t, message.Body, "request_id")
		current := &step{h: h, state: state, message: message}
		if operation, isRequest := h.contract.byRequestKind[kind]; isRequest {
			if target := h.pairedTerminalState(entry.Messages[index+1:], requestID); target != "" {
				h.waitForState(state, func(value string) bool { return value == target }, target)
			}
			if !message.ExpectedValid {
				h.settle(state)
			}
			request := h.requestFor(message, state)
			result := h.must(request)
			state.remember(operation.RequestKind, request, requestID, result)
			current.sent, current.result = &request, result
			if message.ExpectedValid {
				h.requireEnvelope(current, operation)
				h.adoptHandle(state, result)
			} else {
				h.requireInvalidRequest(current)
			}
		} else {
			switch {
			case state.lastResult != nil && state.lastRequestID == requestID:
				current.result = *state.lastResult
				if request, ok := state.lastRequest[h.contract.bySuccessKind[kind].RequestKind]; ok {
					current.sent = &request
				}
			case kind == "problem":
				if hooks.problem == nil {
					t.Fatalf("%s: no request produces this problem in case %q", message.Label, entry.Name)
				}
				current.result = hooks.problem(message)
			default:
				if target := h.expectedTerminalState(message.Body); target != "" {
					h.waitForState(state, func(value string) bool { return value == target }, target)
				}
				request := h.replayFor(message, state)
				result := h.must(request)
				state.remember(h.contract.bySuccessKind[kind].RequestKind, request, requestID, result)
				current.sent, current.result = &request, result
			}
			current.missing = h.match(message, current.result, slices.Contains(message.Assertions, "response.optional_field_ignored"))
		}
		for _, name := range message.Assertions {
			check, ok := assertionChecks[name]
			if !ok {
				t.Fatalf("%s: assertion %q has no provider check", message.Label, name)
			}
			check(current)
		}
	}
}

// adoptHandle records the execution an accepted submit bound, so checks that
// follow on the same message can settle the background execution first.
func (h *harness) adoptHandle(state *scenarioState, result wireResult) {
	if state.executionID != "" || result.body["kind"] != h.contract.operation(h.t, "execution.submit").SuccessKind {
		return
	}
	state.executionID = nestedString(h.t, result.body, "payload", "execution", "execution_id")
	state.acceptedAt = nestedString(h.t, result.body, "payload", "execution", "accepted_at")
}

func (h *harness) pairedTerminalState(rest []fixtureMessage, requestID string) string {
	for _, message := range rest {
		kind := valueString(h.t, message.Body, "kind")
		if _, isRequest := h.contract.byRequestKind[kind]; isRequest || message.Body["request_id"] != requestID {
			continue
		}
		return h.expectedTerminalState(message.Body)
	}
	return ""
}

func (h *harness) expectedTerminalState(body map[string]any) string {
	terminal := h.terminalStates()
	found := ""
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if state, ok := typed["state"].(string); ok && slices.Contains(terminal, state) && found == "" {
				found = state
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(body["payload"])
	return found
}

func (h *harness) requireEnvelope(s *step, operation bindingOperation) {
	h.t.Helper()
	if got := valueString(h.t, s.result.body, "protocol"); got != h.contract.protocol {
		s.fatalf("response protocol = %q", got)
	}
	if got := valueString(h.t, s.result.body, "request_id"); got != s.message.Body["request_id"] {
		s.fatalf("response request_id = %q", got)
	}
	switch kind := valueString(h.t, s.result.body, "kind"); kind {
	case operation.SuccessKind:
		if s.result.status != operation.SuccessStatus {
			s.fatalf("status = %d, want %d: %s", s.result.status, operation.SuccessStatus, s.result.raw)
		}
	case "problem":
		if s.result.status < 400 {
			s.fatalf("problem status = %d", s.result.status)
		}
	default:
		s.fatalf("response kind = %q", kind)
	}
}

// requireInvalidRequest is the contract's answer to a schema-invalid request:
// a complete application Problem, code invalid_request, and no side effects.
func (h *harness) requireInvalidRequest(s *step) {
	h.t.Helper()
	requireProblem(s, s.result, http.StatusBadRequest, "invalid_request")
}

func requireProblem(s *step, result wireResult, status int, code string) {
	s.h.t.Helper()
	if result.status != status || result.body["kind"] != "problem" {
		s.fatalf("response = %d %s, want %d problem %s", result.status, result.raw, status, code)
	}
	payload := nestedObject(s.h.t, result.body, "payload")
	if payload["code"] != code || payload["side_effects"] != "none" {
		s.fatalf("problem = %s, want code %s with side_effects none", result.raw, code)
	}
	if _, handle := payload["execution"]; handle {
		s.fatalf("problem carries an execution handle: %s", result.raw)
	}
	if result.before != result.after {
		s.fatalf("problem %s changed side effects: %+v -> %+v", code, result.before, result.after)
	}
}

func clockBefore(t *testing.T, state *scenarioState) time.Time {
	t.Helper()
	return state.seedDeadline(t).Add(-30 * time.Minute)
}

// --- scenarios -------------------------------------------------------------

func runSubmitIdempotency(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	h.drive(entry, state, driveHooks{})
}

func runSubmitConflict(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	h.seed(state)
	h.drive(entry, state, driveHooks{})
}

// runLookupRecovery submits the seed through a connection the server aborts
// after the provider handled it, so the client holds a transport error and no
// handle while the execution exists.
func runLookupRecovery(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	request := h.requestFor(state.seed, state)
	h.lose.Store(true)
	if _, err := h.do(request); err == nil {
		t.Fatal("lost-response submit returned a response")
	} else {
		state.transportErr = err
	}
	state.lastRequest[contract.operation(t, "execution.submit").RequestKind] = request
	h.drive(entry, state, driveHooks{})
}

// runLookupNegativeFence takes the durable state away before the unresolved
// lookup and restores it before the not_started one.
func runLookupNegativeFence(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	durable := true
	t.Cleanup(func() {
		if !durable {
			h.setDurable(true)
		}
	})
	h.drive(entry, state, driveHooks{before: func(message fixtureMessage) {
		outcome, _ := nestedObject(t, message.Body, "payload")["outcome"].(string)
		switch outcome {
		case "unresolved":
			h.setDurable(false)
			durable = false
		case "not_started":
			if !durable {
				h.setDurable(true)
				durable = true
			}
		}
	}})
}

func runStatusEventsResult(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	h.seed(state)
	h.drive(entry, state, driveHooks{})
}

func runCancel(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(true))
	h.seed(state)
	h.drive(entry, state, driveHooks{})
}

// runAuthorizationCompatibility produces the fixture's permission_denied
// Problem from a provider whose clock is past the authorization deadline.
func runAuthorizationCompatibility(t *testing.T, contract *centralContract, entry centralCase) {
	state := newScenarioState(t, contract)
	h := newHarness(t, contract, clockBefore(t, state), newFakeExecutor(false))
	h.drive(entry, state, driveHooks{problem: func(message fixtureMessage) wireResult {
		if code := nestedObject(t, message.Body, "payload")["code"]; code != "permission_denied" {
			t.Fatalf("%s: no request produces problem %v", message.Label, code)
		}
		expired := newHarness(t, contract, state.seedDeadline(t).Add(time.Minute), newFakeExecutor(false))
		request := expired.requestFor(state.seed, &scenarioState{})
		request.body["request_id"] = message.Body["request_id"]
		return expired.must(request)
	}})
}
