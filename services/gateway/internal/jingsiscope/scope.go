// Package jingsiscope owns the projection of a JingSi Runtime v1 authorization
// into an Agent run and its inverse. The provider (internal/gateway) projects
// the verified authorization envelope into MessageContext.Authorization.Scope
// with Grant.Scopes; the Agent Runtime reads it back with ForRun. Both sides
// share this package so the prefixes, the adapter identity and the fail-closed
// rules are defined exactly once.
//
// The package is a leaf: it imports only internal/app.
package jingsiscope

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// AdapterID identifies JingSi Runtime v1 ingress on MessageSourceContext.Adapter
// and on the hidden session that hosts the execution.
const AdapterID = "jingsi-runtime-v1"

// Scope prefixes. Every scope string in a JingSi run is one of these prefixes
// followed by the value; the projection is sorted so it is order-independent.
const (
	PrefixTool           = "sparkclaw.tool:"
	PrefixApproval       = "sparkclaw.approval:"
	PrefixMaxToolCalls   = "sparkclaw.budget.max_tool_calls:"
	PrefixMaxOutputBytes = "sparkclaw.budget.max_output_bytes:"
	PrefixData           = "sparkclaw.data:"
	PrefixNetwork        = "sparkclaw.network:"
	PrefixPurpose        = "sparkclaw.purpose:"
	PrefixGrant          = "sparkclaw.grant:"
)

// Approval policies frozen by the contract's authorization.approval_policy enum.
const (
	ApprovalDeny             = "deny"
	ApprovalAsk              = "ask"
	ApprovalAllowWithinScope = "allow_within_scope"
)

var approvalPolicies = []string{ApprovalDeny, ApprovalAsk, ApprovalAllowWithinScope}

// Grant is the typed authorization a JingSi Runtime v1 execution carries into
// the Agent Runtime. It is the minimal projection of the verified task
// authorization plus the submit budget; SparkClaw may only narrow it.
type Grant struct {
	Tools          []string
	ApprovalPolicy string
	MaxToolCalls   int
	MaxOutputBytes int
	DataScope      []string
	NetworkScope   []string
	Purpose        string
	GrantID        string
	GrantVersion   string
}

// Closed is the grant every consumer falls back to when a run's projection
// cannot be trusted: no tools, no tool calls, approval denied, no scopes.
func Closed() Grant {
	return Grant{ApprovalPolicy: ApprovalDeny}
}

// Scopes projects the grant into the sorted scope strings persisted on the run.
func (g Grant) Scopes() []string {
	scopes := make([]string, 0, len(g.Tools)+len(g.DataScope)+len(g.NetworkScope)+5)
	for _, value := range g.Tools {
		scopes = append(scopes, PrefixTool+value)
	}
	for _, value := range g.DataScope {
		scopes = append(scopes, PrefixData+value)
	}
	for _, value := range g.NetworkScope {
		scopes = append(scopes, PrefixNetwork+value)
	}
	scopes = append(scopes,
		PrefixApproval+g.ApprovalPolicy,
		PrefixMaxToolCalls+strconv.Itoa(g.MaxToolCalls),
		PrefixMaxOutputBytes+strconv.Itoa(g.MaxOutputBytes),
		PrefixPurpose+g.Purpose,
		PrefixGrant+g.GrantID+"@"+g.GrantVersion,
	)
	slices.Sort(scopes)
	return scopes
}

// Parse inverts Scopes. It fails on any scope that is not a known prefix, on a
// malformed or negative budget, on an unknown approval policy, on a grant
// without exactly one id@version separator, and on a missing or repeated
// singleton (approval, budgets, purpose, grant). Callers must treat an error
// as Closed; nothing in a malformed projection may widen authority.
func Parse(scopes []string) (Grant, error) {
	grant := Grant{}
	seen := map[string]bool{}
	singleton := func(prefix string) error {
		if seen[prefix] {
			return fmt.Errorf("jingsi scope %q is repeated", strings.TrimSuffix(prefix, ":"))
		}
		seen[prefix] = true
		return nil
	}
	for _, scope := range scopes {
		prefix, value, ok := splitScope(scope)
		if !ok {
			return Grant{}, fmt.Errorf("jingsi scope %q has an unknown prefix", scope)
		}
		if value == "" {
			return Grant{}, fmt.Errorf("jingsi scope %q has an empty value", scope)
		}
		switch prefix {
		case PrefixTool:
			grant.Tools = append(grant.Tools, value)
		case PrefixData:
			grant.DataScope = append(grant.DataScope, value)
		case PrefixNetwork:
			grant.NetworkScope = append(grant.NetworkScope, value)
		case PrefixApproval:
			if err := singleton(prefix); err != nil {
				return Grant{}, err
			}
			if !slices.Contains(approvalPolicies, value) {
				return Grant{}, fmt.Errorf("jingsi approval policy %q is unknown", value)
			}
			grant.ApprovalPolicy = value
		case PrefixMaxToolCalls:
			if err := singleton(prefix); err != nil {
				return Grant{}, err
			}
			count, err := parseBudget(value)
			if err != nil {
				return Grant{}, fmt.Errorf("jingsi max_tool_calls budget: %w", err)
			}
			grant.MaxToolCalls = count
		case PrefixMaxOutputBytes:
			if err := singleton(prefix); err != nil {
				return Grant{}, err
			}
			count, err := parseBudget(value)
			if err != nil {
				return Grant{}, fmt.Errorf("jingsi max_output_bytes budget: %w", err)
			}
			grant.MaxOutputBytes = count
		case PrefixPurpose:
			if err := singleton(prefix); err != nil {
				return Grant{}, err
			}
			grant.Purpose = value
		case PrefixGrant:
			if err := singleton(prefix); err != nil {
				return Grant{}, err
			}
			id, version, found := strings.Cut(value, "@")
			if !found || id == "" || version == "" || strings.Contains(version, "@") {
				return Grant{}, fmt.Errorf("jingsi grant %q is not id@version", value)
			}
			grant.GrantID, grant.GrantVersion = id, version
		}
	}
	for _, prefix := range []string{PrefixApproval, PrefixMaxToolCalls, PrefixMaxOutputBytes, PrefixPurpose, PrefixGrant} {
		if !seen[prefix] {
			return Grant{}, fmt.Errorf("jingsi scope %q is missing", strings.TrimSuffix(prefix, ":"))
		}
	}
	slices.Sort(grant.Tools)
	slices.Sort(grant.DataScope)
	slices.Sort(grant.NetworkScope)
	return grant, nil
}

// ForRun reports whether run is JingSi Runtime v1 ingress and, if so, its
// grant. A JingSi run whose projection does not parse yields Closed so a
// corrupted or foreign scope list can never expose a tool or a tool call.
func ForRun(run app.AgentRun) (Grant, bool) {
	if run.MessageContext == nil || run.MessageContext.Source.Adapter != AdapterID {
		return Grant{}, false
	}
	grant, err := Parse(run.MessageContext.Authorization.Scope)
	if err != nil {
		return Closed(), true
	}
	return grant, true
}

// AllowsTool reports whether the grant exposes one tool definition: the exact
// name must be in tool_scope, the tool-call budget must not be exhausted, and
// approval_policy=deny hides tools that require approval.
func (g Grant) AllowsTool(definition app.ToolDefinition) bool {
	if !slices.Contains(g.Tools, definition.Name) || g.MaxToolCalls == 0 {
		return false
	}
	return g.ApprovalPolicy != ApprovalDeny || !definition.RequiresApproval
}

func splitScope(scope string) (prefix, value string, ok bool) {
	for _, candidate := range []string{
		PrefixTool, PrefixApproval, PrefixMaxToolCalls, PrefixMaxOutputBytes,
		PrefixData, PrefixNetwork, PrefixPurpose, PrefixGrant,
	} {
		if strings.HasPrefix(scope, candidate) {
			return candidate, strings.TrimPrefix(scope, candidate), true
		}
	}
	return "", "", false
}

func parseBudget(value string) (int, error) {
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, errors.New("negative budget")
	}
	return count, nil
}
