package emailmanagement

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

// This opt-in smoke uses the production Presenter, prompt, strict schema and
// output validator with entirely synthetic source bodies. It is separate from
// semantic classification accuracy and does not read or send mail.
func TestPresentationRealLanguageSmoke(t *testing.T) {
	if os.Getenv("SPARKCLAW_EMAIL_REAL_EVAL") != "1" {
		t.Skip("opt-in real-model presentation smoke")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Mock {
		t.Fatal("mock presentation is not language qualification")
	}
	presenter := NewModelAnalyzer(modelrouter.New(cfg))
	type row struct {
		Entry    string             `json:"entry"`
		Language string             `json:"language"`
		Output   PresentationOutput `json:"output"`
		Error    string             `json:"error,omitempty"`
	}
	rows := []row{{Entry: "notification", Language: "zh"}, {Entry: "notification", Language: "en"}, {Entry: "interaction", Language: "zh"}, {Entry: "interaction", Language: "en"}}
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := "CloudDesk: Your package has shipped. This is a status notification and no reply is needed."
			request := ""
			if rows[i].Entry == "interaction" {
				body = "CloudDesk: Please confirm whether you accept the new delivery date of September18."
				request = "Confirm acceptance of the revised delivery date."
			}
			input := PresentationInput{OutputLanguage: rows[i].Language, TargetKind: "mail", Messages: []PresentationMessage{{Subject: "CloudDesk delivery", Body: body, Classification: &PresentationClassification{Entry: rows[i].Entry, Source: "model", ReasonCode: "body_evidence", RequestedResponse: request}}}, Concerns: []PresentationConcern{}, MissingContext: []string{}}
			output, err := presenter.Present(t.Context(), input)
			rows[i].Output = output
			if err != nil {
				rows[i].Error = safeCode(err)
			}
		}(i)
	}
	wg.Wait()
	for _, r := range rows {
		if r.Error != "" {
			t.Errorf("presentation %s/%s failed: %s", r.Entry, r.Language, r.Error)
			continue
		}
		han := false
		for _, ch := range r.Output.Summary {
			han = han || unicode.Is(unicode.Han, ch)
		}
		if r.Language == "zh" && !han || r.Language == "en" && han {
			t.Errorf("summary language does not follow requested %s", r.Language)
		}
		if r.Entry == "notification" && r.Output.RequestedResponse != "" {
			t.Error("notification invented requested response")
		}
	}
	raw, _ := json.MarshalIndent(map[string]any{"model_profile": cfg.Model.CapacityProfile, "model_alias": cfg.Model.Fast.Model, "prompt_version": "email-presentation-v7", "source": "synthetic; no mail read or sent", "results": rows}, "", "  ")
	path := os.Getenv("SPARKCLAW_EMAIL_EVAL_REPORT")
	if path != "" {
		if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
