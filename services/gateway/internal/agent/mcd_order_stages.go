package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const mcdUnavailable = "麦当劳点餐暂不可用，请在 SparkX 连接设置中连接麦当劳账号"
const mcdCancelled = "已取消，未下单"
const mcdUnknown = "付款结果未确认，未重复下单；下次点餐前会自动核对"

type mcdOrderStage struct {
	ctx     context.Context
	runtime Runtime
	run     app.AgentRun
	stage   workflowStageContext
	tools   []app.ToolDefinition
	budget  *workflowRunBudget
	result  workflowExecutionResult
	state   *app.MCDOrderState
	// pickupChecked is set once this stage run has confirmed the pickup slot is
	// still usable, so the planning reserve no longer includes its re-read.
	pickupChecked bool
}

func (r Runtime) runMCDOrderStage(ctx context.Context, run app.AgentRun, stage workflowStageContext, tools []app.ToolDefinition, obs []workflowObservation, budget *workflowRunBudget) workflowExecutionResult {
	if run.Workflow.MCDOrder == nil {
		run.Workflow.MCDOrder = &app.MCDOrderState{}
	}
	s := &mcdOrderStage{ctx: ctx, runtime: r, run: run, stage: stage, tools: tools, budget: budget, state: run.Workflow.MCDOrder, result: workflowExecutionResult{Observations: append([]workflowObservation{}, obs...)}}
	err := s.resumeForm()
	if err == nil && (s.state.StopReply == "" || stage.WorkflowNodeID == "settle") {
		switch stage.WorkflowNodeID {
		case "guard":
			err = s.guard()
		case "intent":
			err = s.intent()
		case "context":
			err = s.context()
		case "menu":
			err = s.menu()
		case "offer":
			err = s.offer()
		case "cart":
			err = s.cart(false)
		case "quote":
			err = s.quote()
		case "pay":
			err = s.pay()
		case "settle":
			err = s.settle()
		default:
			err = errors.New("mcd_invalid_stage")
		}
	}
	if err != nil {
		// No failed operation is replayed; the settlement stage only queries or
		// aborts the original preparation. Raw provider messages are not replies.
		if s.state.StopReply == "" {
			s.state.StopReply = "未下单：点餐信息暂时无法核实，请稍后重试"
		}
		if s.state.PaymentQueued {
			s.state.StopReply = mcdUnknown
		}
	}
	if saveErr := s.save(); saveErr != nil {
		s.result.fail(workflowFailureStateInvalid, saveErr)
		return s.result
	}
	if len(s.result.Approvals) > 0 {
		return s.result
	}
	s.result.StageOutcome = &app.ToolOutcome{ID: app.NewID("mcd_stage"), NodeID: stage.WorkflowNodeID, Status: app.ToolCallStatusCompleted}
	if stage.WorkflowNodeID == "settle" {
		s.result.FinalAnswer = s.state.StopReply
		if s.state.PriorReply != "" {
			s.result.FinalAnswer = s.state.PriorReply + "\n" + s.result.FinalAnswer
		}
		s.result.Completed = true
	}
	return s.result
}
func (s *mcdOrderStage) save() error {
	current, ok, err := s.runtime.store.GetRun(s.ctx, s.run.ID)
	if err != nil {
		return err
	}
	if !ok || current.Workflow == nil {
		return errors.New("mcd_run_missing")
	}
	current.Workflow.MCDOrder = s.state
	node := current.Workflow.Nodes[s.stage.WorkflowNodeID]
	for _, call := range s.result.ToolCalls {
		if call.Status != app.ToolCallStatusApprovalPending {
			node.ToolCallIDs = appendUniqueString(node.ToolCallIDs, call.ID)
		}
	}
	current.Workflow.Nodes[s.stage.WorkflowNodeID] = node
	_, err = s.runtime.saveRun(s.ctx, current)
	return err
}
func (s *mcdOrderStage) tool(name string, args map[string]any) (map[string]any, error) {
	if err := s.save(); err != nil {
		return nil, err
	}
	if s.ctx.Err() != nil {
		return nil, s.ctx.Err()
	}
	if stop, _ := s.budget.exceeded(s.result.Observations); stop {
		return nil, errors.New("mcd_execution_budget")
	}
	var defs []app.ToolDefinition
	for _, d := range s.tools {
		if d.Name == name {
			defs = []app.ToolDefinition{d}
			break
		}
	}
	if len(defs) != 1 {
		return nil, errors.New("mcd_capability_unavailable")
	}
	result := s.runtime.runWorkflowDirectStage(s.ctx, s.run.SessionID, s.run, s.stage, defs, nil, args, s.budget)
	s.result.ToolCalls = append(s.result.ToolCalls, result.ToolCalls...)
	s.result.Approvals = append(s.result.Approvals, result.Approvals...)
	// Provider data stays in the scoped tool results, never generic observations.
	if result.FailureCode != "" || len(result.ToolCalls) != 1 {
		return nil, errors.New("mcd_operation_unavailable")
	}
	call := result.ToolCalls[0]
	if len(result.Approvals) > 0 {
		return nil, nil
	}
	out, _ := anyMap(call.Result)
	if !toolCallCompleted(call) {
		return out, errors.New(call.Error)
	}
	return out, nil
}
func (s *mcdOrderStage) read(op string, extra map[string]any) (map[string]any, error) {
	args := s.contextArgs()
	args["operation"] = op
	for k, v := range extra {
		args[k] = v
	}
	out, err := s.tool("mcd.selection.read", args)
	if err != nil {
		return nil, err
	}
	value, ok := anyMap(out["result"])
	if !ok {
		return nil, errors.New("mcd_evidence_missing")
	}
	return value, nil
}
func (s *mcdOrderStage) contextArgs() map[string]any {
	args := map[string]any{}
	if code := mcdText(s.state.Store["store_code"]); code != "" {
		args["store_code"] = code
	}
	if s.state.PickupAt != "" {
		args["pickup_at"] = s.state.PickupAt
	}
	if s.state.EatType != "" {
		args["eat_type"] = s.state.EatType
	}
	if s.state.MaxOrderCents > 0 {
		// Read quotes within the unchanged account Policy ceiling; enforce the
		// owner's tighter ceiling before creating any payment approval.
		args["max_amount_cny"] = mcdYuan(s.state.MaxOrderCents)
	}
	return args
}
func mcdText(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func mcdInt(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	case json.Number:
		i, _ := x.Int64()
		return i
	}
	return 0
}
func mcdRows(v any) []map[string]any {
	raw, _ := json.Marshal(v)
	var rows []map[string]any
	_ = json.Unmarshal(raw, &rows)
	return rows
}
func mcdMap(v any) map[string]any { m, _ := anyMap(v); return m }
func mcdYuan(c int64) string      { return fmt.Sprintf("%d.%02d", c/100, c%100) }
func mcdClone(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
