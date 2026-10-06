package policy

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Decision struct {
	Allowed          bool     `json:"allowed"`
	RequiresApproval bool     `json:"requires_approval"`
	RequiresSandbox  bool     `json:"requires_sandbox"`
	RequiresDeep     bool     `json:"requires_deep"`
	Reason           string   `json:"reason"`
	Resources        []string `json:"resources,omitempty"`
}

type Engine struct {
	current *atomic.Pointer[config.Config]
}

func New(cfg config.Config) Engine {
	e := Engine{current: &atomic.Pointer[config.Config]{}}
	e.Update(cfg)
	return e
}

// Update publishes one immutable policy snapshot to every runtime scope.
// Copy mutable policy inputs before publishing the snapshot.
func (e Engine) Update(cfg config.Config) {
	cfg.Security.DeniedTools = slices.Clone(cfg.Security.DeniedTools)
	cfg.Security.ApprovalRequiredTools = slices.Clone(cfg.Security.ApprovalRequiredTools)
	controls := &cfg.Security.OperatorControls
	controls.WebAccess = clonePolicyBool(controls.WebAccess)
	controls.WorkspaceFiles = clonePolicyBool(controls.WorkspaceFiles)
	controls.ShellCommands = clonePolicyBool(controls.ShellCommands)
	e.current.Store(&cfg)
}

func clonePolicyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (e Engine) snapshot() config.Config {
	if e.current == nil {
		return config.Config{}
	}
	return *e.current.Load()
}

func (e Engine) MayExpose(def app.ToolDefinition) Decision {
	cfg := e.snapshot()
	if slices.Contains(cfg.Security.DeniedTools, def.Name) || !operatorAllows(def, cfg.Security.OperatorControls) {
		return Decision{Allowed: false, Reason: "tool is denied by static exposure policy"}
	}
	return Decision{Allowed: true, Reason: "tool is statically exposable"}
}

// Decide requires the execution context at every call site so a caller
// cannot silently skip the external-MCP workspace gate; pass a zero
// PolicyExecutionContext only where the invocation is genuinely
// owner-principal (manual invocation, diagnostics).
func (e Engine) Decide(def app.ToolDefinition, args map[string]any, execution app.PolicyExecutionContext) Decision {
	cfg := e.snapshot()
	if slices.Contains(cfg.Security.DeniedTools, def.Name) || !operatorAllows(def, cfg.Security.OperatorControls) {
		return Decision{Allowed: false, Reason: "tool is denied by policy"}
	}
	decision := Decision{Allowed: true, Reason: "allowed by default policy"}
	if def.RequiresApproval || slices.Contains(cfg.Security.ApprovalRequiredTools, def.Name) || def.Risk == app.RiskDangerous && cfg.Security.ApprovalRequiredForDangerousTools || operatorNeedsApproval(def, cfg.Security.OperatorControls) {
		decision.RequiresApproval = true
		decision.Reason = "approval required by risk policy"
	}
	if execution.PrincipalClass == app.PolicyPrincipalExternalMCPAI &&
		execution.ResourceClass == app.PolicyResourceSparkClawWorkspaceData && execution.AccessClass != "" {
		decision.RequiresApproval = true
		decision.Reason = "external MCP AI workspace data access requires owner approval"
	}
	if def.Sandbox == "required" || def.Sandbox != "remote" && (def.Risk == app.RiskReversible || def.Risk == app.RiskDangerous) && cfg.Security.SandboxRequiredForMutatingTools {
		decision.RequiresSandbox = true
	}
	if def.Risk == app.RiskDangerous && cfg.Security.DangerousToolsRequireDeepVerification {
		decision.RequiresDeep = true
	}
	decision.Resources = resourcesFromArgs(args)
	return decision
}

func VerifierDecision(def app.ToolDefinition, decision Decision, now time.Time) (app.VerifierDecision, bool) {
	if !decision.RequiresApproval || (!decision.RequiresDeep && def.Risk != app.RiskReversible) {
		return app.VerifierDecision{}, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return app.VerifierDecision{
		Verdict:                  "ask_user",
		RiskLevel:                verifierRiskLevel(def.Risk),
		Lane:                     "deep",
		Reason:                   "Policy requires owner confirmation before this action can execute.",
		RequiredUserConfirmation: true,
		SafeNextAction:           "Queue approval and wait for the owner.",
		CreatedAt:                now.UTC(),
	}, true
}

func AttachVerifier(args map[string]any, decision app.VerifierDecision) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	out["_verifier"] = decision
	return out
}

func verifierRiskLevel(risk app.RiskLevel) string {
	switch risk {
	case app.RiskDangerous:
		return "high"
	case app.RiskReversible:
		return "medium"
	default:
		return "low"
	}
}

func resourcesFromArgs(args map[string]any) []string {
	resources := []string{}
	for _, key := range []string{"path", "root", "url", "command", "recipient", "subject", "title"} {
		if v, ok := args[key].(string); ok && v != "" {
			resources = append(resources, key+":"+v)
		}
	}
	if values, ok := args["to"].([]string); ok {
		for _, value := range values {
			if value != "" {
				resources = append(resources, "to:"+value)
			}
		}
	}
	if values, ok := args["to"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" {
				resources = append(resources, "to:"+text)
			}
		}
	}
	return resources
}
