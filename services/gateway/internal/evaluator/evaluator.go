package evaluator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Config struct {
	GatewayURL     string
	APIToken       string
	Profile        string
	OutputPath     string
	EvalConfigPath string
}

type Report struct {
	ID          string         `json:"id"`
	Profile     string         `json:"profile"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	GatewayURL  string         `json:"gateway_url"`
	Cases       []app.EvalCase `json:"cases"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	GatewayEval *app.EvalRun   `json:"gateway_eval,omitempty"`
}

type Runner struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) Runner {
	cfg.GatewayURL = strings.TrimRight(strings.TrimSpace(cfg.GatewayURL), "/")
	if cfg.GatewayURL == "" {
		cfg.GatewayURL = "http://127.0.0.1:18789"
	}
	if cfg.Profile == "" {
		cfg.Profile = "api-smoke"
	}
	if strings.TrimSpace(cfg.EvalConfigPath) == "" {
		cfg.EvalConfigPath = defaultEvalConfigPath()
	}
	return Runner{
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (r Runner) Run(ctx context.Context) (Report, error) {
	started := time.Now().UTC()
	report := Report{
		ID:         app.NewID("evalsvc"),
		Profile:    r.cfg.Profile,
		Status:     "running",
		Summary:    "Evaluator is running.",
		GatewayURL: r.cfg.GatewayURL,
		StartedAt:  started,
	}
	var gatewayEval *app.EvalRun
	cases := []app.EvalCase{
		r.caseHealth(ctx),
		r.caseReady(ctx),
		r.caseToolRegistry(ctx),
		r.caseMTPABEvalProfile(ctx),
		r.casePersistedSmokeEval(ctx, &gatewayEval),
	}
	report.Cases = cases
	report.GatewayEval = gatewayEval
	failed := 0
	for _, c := range cases {
		if c.Status != "passed" {
			failed++
		}
	}
	report.CompletedAt = time.Now().UTC()
	if failed > 0 {
		report.Status = "failed"
		report.Summary = fmt.Sprintf("%d/%d evaluator case(s) failed.", failed, len(cases))
		return report, errors.New(report.Summary)
	}
	report.Status = "passed"
	report.Summary = fmt.Sprintf("%d evaluator case(s) passed.", len(cases))
	return report, nil
}

func (r Runner) WriteReport(report Report) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if r.cfg.OutputPath == "" {
		_, err = os.Stdout.Write(append(raw, '\n'))
		return err
	}
	return os.WriteFile(r.cfg.OutputPath, append(raw, '\n'), 0o644)
}

func (r Runner) caseHealth(ctx context.Context) app.EvalCase {
	return evalCase("gateway_health", func() error {
		var body map[string]any
		if err := r.get(ctx, "/healthz", &body); err != nil {
			return err
		}
		if body["ok"] != true {
			return fmt.Errorf("healthz returned not ok: %#v", body)
		}
		return nil
	})
}

func (r Runner) caseReady(ctx context.Context) app.EvalCase {
	return evalCase("gateway_ready", func() error {
		var body map[string]any
		if err := r.get(ctx, "/readyz", &body); err != nil {
			return err
		}
		if body["ok"] != true {
			return fmt.Errorf("readyz returned not ok: %#v", body)
		}
		if strings.TrimSpace(fmt.Sprint(body["workspace_root"])) == "" {
			return errors.New("readyz did not include workspace_root")
		}
		return nil
	})
}

func (r Runner) caseToolRegistry(ctx context.Context) app.EvalCase {
	required := []string{
		"files.search",
		"files.read",
		"file.delete",
		"memory.search",
		"memory.write_candidate",
		"memory.propose",
		"memory.write_sensitive",
		"browser.read",
		"shell.exec_sandboxed",
		"notify.ask_approval",
	}
	return evalCase("tool_registry", func() error {
		var body struct {
			Tools []app.ToolDefinition `json:"tools"`
		}
		if err := r.get(ctx, "/api/tools", &body); err != nil {
			return err
		}
		names := make([]string, 0, len(body.Tools))
		for _, tool := range body.Tools {
			names = append(names, tool.Name)
		}
		for _, name := range required {
			if !slices.Contains(names, name) {
				return fmt.Errorf("missing tool %s", name)
			}
		}
		if slices.Contains(names, "code.apply_patch") {
			return errors.New("tool registry still exposes retired code.apply_patch")
		}
		return nil
	})
}

func (r Runner) casePersistedSmokeEval(ctx context.Context, gatewayEval **app.EvalRun) app.EvalCase {
	return evalCase("persisted_smoke_eval", func() error {
		var run app.EvalRun
		if err := r.post(ctx, "/api/evals/run", map[string]string{"profile": "smoke"}, &run); err != nil {
			return err
		}
		if run.ID == "" {
			return errors.New("gateway smoke eval did not return an id")
		}
		if run.Status != "passed" {
			return fmt.Errorf("gateway smoke eval status = %q: %s", run.Status, run.Summary)
		}
		var fetched app.EvalRun
		if err := r.get(ctx, "/api/evals/"+run.ID, &fetched); err != nil {
			return err
		}
		if fetched.ID != run.ID || fetched.Status != run.Status {
			return fmt.Errorf("persisted eval mismatch: created=%#v fetched=%#v", run, fetched)
		}
		*gatewayEval = &fetched
		return nil
	})
}

func (r Runner) caseMTPABEvalProfile(ctx context.Context) app.EvalCase {
	return evalCase("mtp_ab_eval_profile", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		profiles, err := loadEvalProfiles(r.cfg.EvalConfigPath)
		if err != nil {
			return err
		}
		apiSmoke, ok := profiles.Profiles["api-smoke"]
		if !ok {
			return errors.New("eval profile api-smoke is missing")
		}
		if !slices.Contains(apiSmoke.Checks, "mtp_ab_eval_profile") {
			return errors.New("api-smoke profile does not include mtp_ab_eval_profile")
		}
		mtp, ok := profiles.Profiles["mtp-ab"]
		if !ok {
			return errors.New("eval profile mtp-ab is missing")
		}
		requiredMetrics := []string{
			"ttft_ms",
			"tokens_per_second",
			"total_latency_ms",
			"tool_json_validity",
			"task_completion",
			"hallucinated_tool_calls",
			"repair_rate",
			"verifier_disagreement_rate",
		}
		for _, metric := range requiredMetrics {
			if !slices.Contains(mtp.Metrics, metric) {
				return fmt.Errorf("mtp-ab profile missing metric %s", metric)
			}
		}
		variants := map[string]evalMTPVariant{}
		for _, variant := range mtp.Variants {
			variants[variant.ID] = variant
		}
		off, ok := variants["mtp-off"]
		if !ok {
			return errors.New("mtp-ab profile missing mtp-off variant")
		}
		if off.MTP || off.SpeculativeTokens != 0 || !containsAll(off.Lanes, "fast", "deep") {
			return fmt.Errorf("invalid mtp-off variant: %#v", off)
		}
		on2, ok := variants["mtp-on-2"]
		if !ok {
			return errors.New("mtp-ab profile missing mtp-on-2 variant")
		}
		if !on2.MTP || on2.SpeculativeTokens != 2 || !containsAll(on2.Lanes, "fast", "deep") {
			return fmt.Errorf("invalid mtp-on-2 variant: %#v", on2)
		}
		on3, ok := variants["mtp-on-3-coding-long"]
		if !ok {
			return errors.New("mtp-ab profile missing mtp-on-3-coding-long variant")
		}
		if !on3.MTP || on3.SpeculativeTokens != 3 || !containsAll(on3.Scenarios, "coding", "long_answer") {
			return fmt.Errorf("invalid mtp-on-3-coding-long variant: %#v", on3)
		}
		for _, scenario := range on3.Scenarios {
			if scenario != "coding" && scenario != "long_answer" {
				return fmt.Errorf("mtp-on-3 variant includes non-coding/long scenario %q", scenario)
			}
		}
		return nil
	})
}

func (r Runner) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.GatewayURL+path, nil)
	if err != nil {
		return err
	}
	return r.do(req, out)
}

func (r Runner) post(ctx context.Context, path string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.GatewayURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return r.do(req, out)
}

func (r Runner) do(req *http.Request, out any) error {
	if r.cfg.APIToken != "" && strings.HasPrefix(req.URL.Path, "/api/") {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIToken)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if msg, _ := errBody["error"].(string); msg != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func evalCase(name string, fn func() error) app.EvalCase {
	start := time.Now()
	c := app.EvalCase{Name: name, Status: "passed", Message: "ok"}
	if err := fn(); err != nil {
		c.Status = "failed"
		c.Message = err.Error()
	}
	c.DurationMS = time.Since(start).Milliseconds()
	return c
}

type evalProfilesFile struct {
	Profiles map[string]evalProfileConfig `json:"profiles"`
}

type evalProfileConfig struct {
	Checks   []string         `json:"checks"`
	Metrics  []string         `json:"metrics"`
	Variants []evalMTPVariant `json:"variants"`
}

type evalMTPVariant struct {
	ID                string   `json:"id"`
	MTP               bool     `json:"mtp"`
	SpeculativeTokens int      `json:"speculative_tokens"`
	Lanes             []string `json:"lanes"`
	Scenarios         []string `json:"scenarios"`
}

func loadEvalProfiles(path string) (evalProfilesFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return evalProfilesFile{}, err
	}
	var profiles evalProfilesFile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		return evalProfilesFile{}, err
	}
	if len(profiles.Profiles) == 0 {
		return evalProfilesFile{}, errors.New("eval profiles file has no profiles")
	}
	return profiles, nil
}

func defaultEvalConfigPath() string {
	candidates := []string{
		"configs/eval.profiles.json",
		filepath.Join("..", "configs", "eval.profiles.json"),
		filepath.Join("..", "..", "configs", "eval.profiles.json"),
		filepath.Join("/app", "configs", "eval.profiles.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func containsAll(values []string, required ...string) bool {
	for _, item := range required {
		if !slices.Contains(values, item) {
			return false
		}
	}
	return true
}
