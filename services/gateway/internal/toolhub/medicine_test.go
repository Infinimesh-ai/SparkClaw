package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

type medicineInfoFixture struct {
	calls int
	fail  bool
}

func (f *medicineInfoFixture) Search(_ context.Context, r websearch.Request) (websearch.Result, error) {
	f.calls++
	if f.fail {
		return websearch.Result{}, errors.New("Info unavailable")
	}
	return websearch.Result{SchemaVersion: 2, RequestID: "synthetic", Status: "ok", Query: r.Query, Provider: "infinimesh-info", RetrievedAt: "2026-11-02T00:00:00Z", Untrusted: true, Aggregate: websearch.Aggregate{Summary: "Synthetic evidence", Facts: []websearch.Fact{{Claim: "synthetic fact", Sources: []string{"one"}}}}, Sources: []websearch.Source{{ID: "one", Title: "Synthetic source", URL: "https://example.test/medicine"}}}, nil
}
func TestMedicineSourceSchemaRejectsAmbiguousAndInvalidPairs(t *testing.T) {
	for _, raw := range []string{`{"source":"info","reason_code":"stable_knowledge"}`, `{"source":"model","reason_code":"safety_notice"}`, `{"source":"model","source":"info","reason_code":"safety_notice"}`, `{"source":"info","reason_code":"safety_notice","x":1}`, `{"source":"model","reason_code":"explicit_no_search"} {}`} {
		if _, e := parseMedicineSource(raw); e == nil {
			t.Fatal(raw)
		}
	}
	if _, e := parseMedicineSource(`{"source":"model","reason_code":"explicit_no_search"}`); e != nil {
		t.Fatal(e)
	}
}
func TestMedicineInfoSourceFallbackAndGrounding(t *testing.T) {
	for _, c := range []struct {
		name, source                   string
		fail, wantSearch, wantFallback bool
	}{
		{"stable", `{"source":"model","reason_code":"stable_knowledge"}`, false, false, false},
		{"explicit_no_search", `{"source":"model","reason_code":"explicit_no_search"}`, false, false, false},
		{"current", `{"source":"info","reason_code":"safety_notice"}`, false, true, false},
		{"info_failed", `{"source":"info","reason_code":"safety_notice"}`, true, true, true},
		{"decision_failed", "invalid", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			sourceCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				content := "一般知识回答"
				if strings.Contains(body.Messages[0].Content, "Choose the source") {
					sourceCalls++
					content = c.source
				} else {
					joined := body.Messages[len(body.Messages)-1].Content
					if c.wantSearch && !c.fail && !strings.Contains(joined, "Synthetic evidence") {
						t.Error("Info evidence omitted")
					}
					if !strings.Contains(body.Messages[0].Content, "untrusted") {
						t.Error("missing untrusted evidence boundary")
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			}))
			defer server.Close()
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL = server.URL
			hub := New(cfg, store.NewMemoryStore())
			defer hub.Close()
			info := &medicineInfoFixture{fail: c.fail}
			hub.info = newInfoRuntime(info, nil)
			result, err := hub.medicineDrugInfo(t.Context(), map[string]any{"query": "合成药品问题", "today": "2026-11-02"}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			answer := out["answer"].(string)
			if (info.calls > 0) != c.wantSearch || out["verification_failed"] != c.wantFallback || strings.Contains(answer, "未能联网核实") != c.wantFallback || !strings.HasSuffix(answer, MedicineClosing) {
				t.Fatalf("calls=%d output=%+v", info.calls, out)
			}
			expected := 1
			if c.name == "decision_failed" {
				expected = 2
				if out["reason_code"] != "decision_failed" {
					t.Fatal(out)
				}
			}
			if sourceCalls != expected {
				t.Fatal(sourceCalls)
			}
		})
	}
}
func TestMedicineExecutionScopeUsesAuthoritativeRepositoryAndOwner(t *testing.T) {
	persistent := store.NewMemoryStore()
	hub := New(config.Default(), persistent).WithMedicineRepository(persistent)
	defer hub.Close()
	scoped, local, session := executionScopeFixture(t, hub, nil)
	fields := medicine.Fields{Name: "Owner A medicine", Category: "other", Quantity: 1, Unit: "box", NoExpiry: true}
	batch := medicine.Batch{OperationID: "medicine_run", Operation: "create", Items: []medicine.Entry{{EntryID: "e", Fields: fields}}}
	if _, err := scoped.medicineWrite(t.Context(), map[string]any{"batch": batch}, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	box, err := persistent.ReadMedicineBox(t.Context(), session.OwnerID)
	if err != nil || len(box.Items) != 1 {
		t.Fatalf("%+v %v", box, err)
	}
	transient, _ := local.ReadMedicineBox(t.Context(), session.OwnerID)
	if len(transient.Items) != 0 {
		t.Fatal("inventory leaked into ephemeral repository")
	}
	other, _ := persistent.ReadMedicineBox(t.Context(), "other-owner")
	if len(other.Items) != 0 {
		t.Fatal("owner isolation broken")
	}
	if _, err = scoped.medicineWrite(t.Context(), map[string]any{"batch": batch}, session.ID, "other-run"); err == nil {
		t.Fatal("unbound operation accepted")
	}
}
func TestRealMedicineInfoSource(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_MEDICINE") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_MEDICINE=1 to call configured Fast")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	hub := New(cfg, store.NewMemoryStore())
	defer hub.Close()
	for _, c := range []struct{ query, source, reason string }{{"布洛芬的常见副作用是什么？", "model", "stable_knowledge"}, {"这个药最近有没有召回？", "info", "safety_notice"}, {"不要联网，布洛芬现在多少钱？不确定请说明。", "model", "explicit_no_search"}} {
		source, err := hub.chooseMedicineSource(t.Context(), "synthetic", "run", c.query, "2026-11-02")
		if err != nil || source.Source != c.source || source.ReasonCode != c.reason {
			t.Errorf("query=%q source=%+v err=%v", c.query, source, err)
		} else {
			t.Logf("source=%s reason=%s", source.Source, source.ReasonCode)
		}
	}
}
