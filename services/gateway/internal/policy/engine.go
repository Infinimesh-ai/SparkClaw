package policy

import (
	"slices"
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
	cfg config.Config
}

type ExposureContext struct {
	ActorRef string
	Workflow app.WorkflowID
	NodeID   app.WorkflowNodeID
}

func New(cfg config.Config) Engine {
	return Engine{cfg: cfg}
}

func (e Engine) MayExpose(def app.ToolDefinition, _ ExposureContext) Decision {
	if slices.Contains(e.cfg.Security.DeniedTools, def.Name) {
		return Decision{Allowed: false, Reason: "tool is denied by static exposure policy"}
	}
	return Decision{Allowed: true, Reason: "tool is statically exposable"}
}

func (e Engine) Decide(def app.ToolDefinition, args map[string]any) Decision {
	if slices.Contains(e.cfg.Security.DeniedTools, def.Name) {
		return Decision{Allowed: false, Reason: "tool is denied by policy"}
	}
	decision := Decision{Allowed: true, Reason: "allowed by default policy"}
	if def.RequiresApproval || slices.Contains(e.cfg.Security.ApprovalRequiredTools, def.Name) || def.Risk == app.RiskDangerous && e.cfg.Security.ApprovalRequiredForDangerousTools {
		decision.RequiresApproval = true
		decision.Reason = "approval required by risk policy"
	}
	if def.Sandbox == "required" || (def.Risk == app.RiskReversible || def.Risk == app.RiskDangerous) && e.cfg.Security.SandboxRequiredForMutatingTools {
		decision.RequiresSandbox = true
	}
	if def.Risk == app.RiskDangerous && e.cfg.Security.DangerousToolsRequireDeepVerification {
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
