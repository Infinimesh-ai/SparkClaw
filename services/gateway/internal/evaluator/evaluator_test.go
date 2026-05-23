package evaluator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRunnerProducesPassingReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			writeTestJSON(w, map[string]any{"ok": true})
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			writeTestJSON(w, map[string]any{"ok": true, "workspace_root": t.TempDir()})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tools":
			writeTestJSON(w, map[string]any{"tools": requiredToolDefinitions()})
		case r.Method == http.MethodGet && r.URL.Path == "/api/skills":
			writeTestJSON(w, map[string]any{"skills": []map[string]string{{"name": "local_files"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/evals/run":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/evals/eval_1":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := New(Config{GatewayURL: server.URL, EvalConfigPath: writeEvalProfileFixture(t, true)}).Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" || len(report.Cases) != 6 || report.GatewayEval == nil {
		t.Fatalf("unexpected report: %#v", report)
	}
	names := make([]string, 0, len(report.Cases))
	for _, evalCase := range report.Cases {
		names = append(names, evalCase.Name)
	}
	if !slices.Contains(names, "mtp_ab_eval_profile") {
		t.Fatalf("report missing mtp_ab_eval_profile: %#v", report.Cases)
	}
}

func TestRunnerFailsWhenToolMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			writeTestJSON(w, map[string]any{"ok": true})
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			writeTestJSON(w, map[string]any{"ok": true, "workspace_root": t.TempDir()})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tools":
			writeTestJSON(w, map[string]any{"tools": []app.ToolDefinition{{Name: "files.search"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/skills":
			writeTestJSON(w, map[string]any{"skills": []map[string]string{{"name": "local_files"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/evals/run":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/evals/eval_1":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := New(Config{GatewayURL: server.URL, EvalConfigPath: writeEvalProfileFixture(t, true)}).Run(t.Context())
	if err == nil {
		t.Fatal("expected evaluator failure")
	}
	if report.Status != "failed" {
		t.Fatalf("unexpected report status: %#v", report)
	}
}

func TestRunnerFailsWhenMTPABProfileIsIncomplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			writeTestJSON(w, map[string]any{"ok": true})
		case r.Method == http.MethodGet && r.URL.Path == "/readyz":
			writeTestJSON(w, map[string]any{"ok": true, "workspace_root": t.TempDir()})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tools":
			writeTestJSON(w, map[string]any{"tools": requiredToolDefinitions()})
		case r.Method == http.MethodGet && r.URL.Path == "/api/skills":
			writeTestJSON(w, map[string]any{"skills": []map[string]string{{"name": "local_files"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/evals/run":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/evals/eval_1":
			writeTestJSON(w, app.EvalRun{ID: "eval_1", Profile: "smoke", Status: "passed", Summary: "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := New(Config{GatewayURL: server.URL, EvalConfigPath: writeEvalProfileFixture(t, false)}).Run(t.Context())
	if err == nil {
		t.Fatal("expected evaluator failure")
	}
	if report.Status != "failed" {
		t.Fatalf("unexpected report status: %#v", report)
	}
	for _, evalCase := range report.Cases {
		if evalCase.Name == "mtp_ab_eval_profile" {
			if evalCase.Status != "failed" {
				t.Fatalf("mtp eval profile case should fail: %#v", evalCase)
			}
			return
		}
	}
	t.Fatalf("report missing mtp_ab_eval_profile: %#v", report.Cases)
}

func requiredToolDefinitions() []app.ToolDefinition {
	names := []string{
		"files.search",
		"files.read",
		"file.delete",
		"memory.search",
		"memory.write_candidate",
		"memory.propose",
		"memory.write_sensitive",
		"knowledge.index_workspace",
		"knowledge.search",
		"browser.read",
		"email.search",
		"email.read_thread",
		"email.draft_reply",
		"email.send",
		"calendar.read",
		"calendar.propose_event",
		"calendar.create",
		"shell.exec_sandboxed",
		"code.apply_patch",
		"notify.ask_approval",
	}
	out := make([]app.ToolDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, app.ToolDefinition{Name: name})
	}
	return out
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeEvalProfileFixture(t *testing.T, complete bool) string {
	t.Helper()
	body := `{
  "profiles": {
    "api-smoke": {
      "checks": [
        "gateway_health",
        "gateway_ready",
        "tool_registry",
        "skill_registry",
        "mtp_ab_eval_profile",
        "persisted_smoke_eval"
      ]
    },
    "mtp-ab": {
      "metrics": [
        "ttft_ms",
        "tokens_per_second",
        "total_latency_ms",
        "tool_json_validity",
        "task_completion",
        "hallucinated_tool_calls",
        "repair_rate",
        "verifier_disagreement_rate"
      ],
      "variants": [
        {"id":"mtp-off","mtp":false,"speculative_tokens":0,"lanes":["fast","deep"],"scenarios":["chat","summarize","coding","long_answer"]},
        {"id":"mtp-on-2","mtp":true,"speculative_tokens":2,"lanes":["fast","deep"],"scenarios":["chat","summarize","coding","long_answer"]}` + mtpOn3Fixture(complete) + `
      ]
    }
  }
}`
	path := filepath.Join(t.TempDir(), "eval.profiles.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mtpOn3Fixture(complete bool) string {
	if !complete {
		return ""
	}
	return `,
        {"id":"mtp-on-3-coding-long","mtp":true,"speculative_tokens":3,"lanes":["deep"],"scenarios":["coding","long_answer"]}`
}
