package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const mcdCancelOption = "__cancel_order__"
const mcdOtherStoreOption = "__search_stores__"

func mcdSelect(name, label string, rows []map[string]any, key string) app.ApprovalInputField {
	field := app.ApprovalInputField{Name: name, Label: label, Type: app.ApprovalInputSelect, Required: true}
	for _, row := range rows {
		value := mcdText(row[key])
		if value != "" {
			field.Options = append(field.Options, app.ApprovalInputOption{Value: value, Label: mcdText(row["label"])})
		}
	}
	field.Options = append(field.Options, app.ApprovalInputOption{Value: mcdCancelOption, Label: "都不符合，取消点餐"})
	return field
}
func (s *mcdOrderStage) ask(kind string, entries []app.ApprovalInputEntry, candidates map[string]map[string]any) error {
	if s.run.MessageContext == nil || s.run.MessageContext.Source.Kind != app.MessageSourceWeb || s.runtime.tools.ExecutionTextOnly() {
		s.state.StopReply = "请在 SparkX 客户端点餐"
		return nil
	}
	form := &app.ApprovalInputForm{SchemaVersion: app.ApprovalInputSchemaVersion, SubmitLabel: "Confirm order details", Entries: entries}
	for _, ch := range s.run.Workflow.Route.Slots.Query {
		if unicode.Is(unicode.Han, ch) {
			form.SubmitLabel = "确认点餐信息"
			break
		}
	}
	if err := app.ValidateApprovalInputForm(form); err != nil {
		return err
	}
	capability, err := s.runtime.materializedWorkflowCapability(s.ctx, s.run.ID, s.stage.WorkflowNodeID, s.stage.ScopeRevision, "notify.ask_approval")
	if err != nil {
		return err
	}
	def, ok := s.runtime.tools.Definition("notify.ask_approval")
	if !ok || !s.runtime.policy.MayExpose(def).Allowed {
		return errors.New("mcd_form_unavailable")
	}
	now := time.Now().UTC()
	id := app.NewID("ap")
	s.state.PendingApproval = id
	s.state.PendingKind = kind
	s.state.Candidates = candidates
	s.state.Answers = nil
	snapshotDigest, err := mcdStateDigest(s.state)
	if err != nil {
		return err
	}
	args := map[string]any{"summary": "请确认点餐信息", "reason": "确认本次订单的准确选择", "input_context": map[string]any{"mcd_state_digest": snapshotDigest, "candidate_form": form}}
	if err = s.runtime.tools.Validate("notify.ask_approval", args); err != nil {
		return err
	}
	call := app.ToolCall{ID: app.NewID("tc"), SessionID: s.run.SessionID, RunID: s.run.ID, Tool: "notify.ask_approval", Risk: app.RiskDraft, Status: app.ToolCallStatusApprovalPending, Arguments: args, StartedAt: now, WorkflowID: s.stage.WorkflowID, WorkflowNodeID: s.stage.WorkflowNodeID, ScopeRevision: s.stage.ScopeRevision, Capability: capability, ApprovalID: id, ObservationSummary: "Waiting for ordering details"}
	approval := app.Approval{ID: id, Source: app.ApprovalSourceTool, SessionID: s.run.SessionID, RunID: s.run.ID, ToolCallID: call.ID, Tool: call.Tool, Risk: app.RiskDraft, Status: app.ApprovalStatusPending, Summary: "请确认点餐信息", Reason: "确认本次订单的准确选择", Arguments: args, ArgumentsImmutable: true, CreatedAt: now, InputForm: form}
	if _, err = s.runtime.saveToolCall(s.ctx, call); err != nil {
		return err
	}
	approval, err = s.runtime.saveApproval(s.ctx, approval)
	if err != nil {
		return err
	}
	s.budget.observeToolCall(call)
	s.result.ToolCalls = append(s.result.ToolCalls, call)
	s.result.Approvals = append(s.result.Approvals, approval)
	return nil
}
func (s *mcdOrderStage) resumeForm() error {
	if s.state.PendingApproval == "" {
		return nil
	}
	approval, ok, err := s.runtime.store.GetApproval(s.ctx, s.state.PendingApproval)
	if err != nil {
		return err
	}
	if !ok || approval.RunID != s.run.ID || approval.SessionID != s.run.SessionID || approval.InputForm == nil || !approval.ArgumentsImmutable {
		return errors.New("mcd_original_form_missing")
	}
	if approval.Status == app.ApprovalStatusRejected {
		s.state.StopReply = mcdCancelled
		s.state.PendingApproval = ""
		return nil
	}
	if approval.Status != app.ApprovalStatusApproved {
		return errors.New("mcd_form_pending")
	}
	call, ok, err := s.runtime.store.GetToolCall(s.ctx, approval.ToolCallID)
	if err != nil {
		return err
	}
	if !ok || call.ApprovalID != approval.ID || call.WorkflowNodeID != s.stage.WorkflowNodeID || !toolCallCompleted(call) || !reflect.DeepEqual(call.Arguments, approval.Arguments) {
		return errors.New("mcd_form_changed")
	}
	if err = app.ValidateApprovalInputs(approval.InputForm, approval.InputValues); err != nil {
		return err
	}
	digest, err := app.ApprovalInputsDigest(approval.InputValues)
	if err != nil || digest != approval.InputsDigest {
		return errors.New("mcd_answers_changed")
	}
	stateDigest, err := mcdStateDigest(s.state)
	if err != nil || stateDigest != mcdText(mcdMap(approval.Arguments["input_context"])["mcd_state_digest"]) {
		return errors.New("mcd_form_snapshot_changed")
	}
	s.state.PendingApproval = ""
	s.state.Answers = approval.InputValues
	for _, entry := range approval.InputValues {
		for _, value := range entry {
			if value == mcdCancelOption {
				s.state.StopReply = mcdCancelled
			}
		}
	}
	return nil
}
func mcdEntry(id, label string, fields ...app.ApprovalInputField) app.ApprovalInputEntry {
	return app.ApprovalInputEntry{ID: id, Label: label, Summary: "", Fields: fields}
}
func (s *mcdOrderStage) answer(entry, field string) string {
	return mcdText(s.state.Answers[entry][field])
}
func (s *mcdOrderStage) consumeAnswers() {
	s.state.PendingKind = ""
	s.state.Answers = nil
	s.state.Candidates = nil
}
func mcdCandidates(rows []map[string]any, key string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range rows {
		if id := mcdText(r[key]); id != "" {
			out[id] = r
		}
	}
	return out
}
func mcdQuestionID(i int) string { return fmt.Sprintf("item_%d", i+1) }

func mcdStateDigest(state *app.MCDOrderState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
