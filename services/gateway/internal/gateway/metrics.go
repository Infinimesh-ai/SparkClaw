package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if _, err := s.applyMemoryRetention(r.Context()); err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	messages := 0
	runs := 0
	allModelCalls, err := s.store.ListModelCalls(r.Context(), "", "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	allToolCalls, err := s.store.ListToolCalls(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	allEpisodes, err := s.store.ListEpisodeSummaries(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	modelCalls := len(allModelCalls)
	modelErrors := 0
	modelLatencyTotal := int64(0)
	modelTokensTotal := 0
	for _, session := range sessions {
		storedMessages, err := s.store.ListMessages(r.Context(), session.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		messages += len(storedMessages)
		storedRuns, err := s.store.ListRuns(r.Context(), session.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		runs += len(storedRuns)
	}
	for _, call := range allModelCalls {
		if call.Status == "failed" {
			modelErrors++
		}
		modelLatencyTotal += call.LatencyMS
		modelTokensTotal += call.TotalTokens
	}
	modelLatencyAverage := float64(0)
	if modelCalls > 0 {
		modelLatencyAverage = float64(modelLatencyTotal) / float64(modelCalls)
	}
	approvals, err := s.store.ListApprovals(r.Context(), "")
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	pendingApprovals := 0
	rateLimited := 0
	for _, approval := range approvals {
		if approval.Status == "pending" {
			pendingApprovals++
		}
	}
	if s.limiter != nil {
		rateLimited = s.limiter.rejectedCount()
	}
	memoryCandidates, err := s.store.ListMemoryCandidates(r.Context(), "")
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	memories, err := s.store.SearchMemories(r.Context(), "")
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	lines := []string{
		"# HELP sparkclaw_gateway_uptime_seconds Gateway process uptime in seconds.",
		"# TYPE sparkclaw_gateway_uptime_seconds gauge",
		fmt.Sprintf("sparkclaw_gateway_uptime_seconds %d", int(time.Since(s.started).Seconds())),
		"# HELP sparkclaw_gateway_auth_required Whether API authentication is required.",
		"# TYPE sparkclaw_gateway_auth_required gauge",
		fmt.Sprintf("sparkclaw_gateway_auth_required %d", boolMetric(s.authRequired())),
		"# HELP sparkclaw_gateway_rate_limit_rejections_total Total HTTP requests rejected by the Gateway rate limiter.",
		"# TYPE sparkclaw_gateway_rate_limit_rejections_total counter",
		fmt.Sprintf("sparkclaw_gateway_rate_limit_rejections_total %d", rateLimited),
		"# HELP sparkclaw_sessions_total Current session count.",
		"# TYPE sparkclaw_sessions_total gauge",
		fmt.Sprintf("sparkclaw_sessions_total %d", len(sessions)),
		"# HELP sparkclaw_messages_total Current message count.",
		"# TYPE sparkclaw_messages_total gauge",
		fmt.Sprintf("sparkclaw_messages_total %d", messages),
		"# HELP sparkclaw_agent_runs_total Current agent run count.",
		"# TYPE sparkclaw_agent_runs_total gauge",
		fmt.Sprintf("sparkclaw_agent_runs_total %d", runs),
		"# HELP sparkclaw_model_calls_total Current model call count.",
		"# TYPE sparkclaw_model_calls_total gauge",
		fmt.Sprintf("sparkclaw_model_calls_total %d", modelCalls),
		"# HELP sparkclaw_model_call_errors_total Current failed model call count.",
		"# TYPE sparkclaw_model_call_errors_total gauge",
		fmt.Sprintf("sparkclaw_model_call_errors_total %d", modelErrors),
		"# HELP sparkclaw_model_call_latency_ms_avg Average stored model call latency in milliseconds.",
		"# TYPE sparkclaw_model_call_latency_ms_avg gauge",
		fmt.Sprintf("sparkclaw_model_call_latency_ms_avg %.2f", modelLatencyAverage),
		"# HELP sparkclaw_model_call_tokens_total Total stored model call token usage.",
		"# TYPE sparkclaw_model_call_tokens_total gauge",
		fmt.Sprintf("sparkclaw_model_call_tokens_total %d", modelTokensTotal),
		"# HELP sparkclaw_tool_calls_total Current tool call count.",
		"# TYPE sparkclaw_tool_calls_total gauge",
		fmt.Sprintf("sparkclaw_tool_calls_total %d", len(allToolCalls)),
		"# HELP sparkclaw_approvals_total Current approval count.",
		"# TYPE sparkclaw_approvals_total gauge",
		fmt.Sprintf("sparkclaw_approvals_total %d", len(approvals)),
		"# HELP sparkclaw_approvals_pending Current pending approval count.",
		"# TYPE sparkclaw_approvals_pending gauge",
		fmt.Sprintf("sparkclaw_approvals_pending %d", pendingApprovals),
		"# HELP sparkclaw_memory_candidates_total Current memory candidate count.",
		"# TYPE sparkclaw_memory_candidates_total gauge",
		fmt.Sprintf("sparkclaw_memory_candidates_total %d", len(memoryCandidates)),
		"# HELP sparkclaw_memories_total Current accepted memory count.",
		"# TYPE sparkclaw_memories_total gauge",
		fmt.Sprintf("sparkclaw_memories_total %d", len(memories)),
		"# HELP sparkclaw_episode_summaries_total Current episode summary count.",
		"# TYPE sparkclaw_episode_summaries_total gauge",
		fmt.Sprintf("sparkclaw_episode_summaries_total %d", len(allEpisodes)),
	}
	lines = append(lines, s.tools.DocumentMetrics()...)
	lines = append(lines, s.storeOperationMetrics()...)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
}

func (s *Server) storeOperationMetrics() []string {
	if s.storeRuntime == nil {
		return nil
	}
	status := s.storeRuntime.Status()
	lines := []string{
		"# HELP sparkclaw_store_ready Whether the selected Store backend is ready.",
		"# TYPE sparkclaw_store_ready gauge",
		fmt.Sprintf("sparkclaw_store_ready{backend=%q} %d", status.Backend, boolMetric(status.Ready)),
		"# HELP sparkclaw_store_active_operations Current admitted Store operations.",
		"# TYPE sparkclaw_store_active_operations gauge",
		fmt.Sprintf("sparkclaw_store_active_operations{backend=%q} %d", status.Backend, status.Active),
		"# HELP sparkclaw_store_operations_total Completed Store operations by bounded result.",
		"# TYPE sparkclaw_store_operations_total counter",
		"# HELP sparkclaw_store_operation_duration_seconds_total Total Store operation duration by bounded result.",
		"# TYPE sparkclaw_store_operation_duration_seconds_total counter",
	}
	for _, metric := range s.storeRuntime.Metrics() {
		labels := fmt.Sprintf("backend=%q,repository=%q,operation=%q,mode=%q,outcome=%q", status.Backend, metric.Repository, metric.Operation, metric.Mode, metric.Outcome)
		lines = append(lines,
			fmt.Sprintf("sparkclaw_store_operations_total{%s} %d", labels, metric.Count),
			fmt.Sprintf("sparkclaw_store_operation_duration_seconds_total{%s} %.6f", labels, metric.DurationSeconds),
		)
	}
	return lines
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}
