package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var localMindTaskIDLabelPattern = regexp.MustCompile(`(?i)\btask[_ ]?id\s*[:=：]\s*([a-z0-9][a-z0-9._:-]{0,511})`)

func (r Runtime) resolveLocalMindTaskTargets(ctx context.Context, sessionID, content string) ([]groundedTarget, error) {
	if r.store == nil {
		return localMindLabelledTaskTargets(content), nil
	}
	calls, err := r.store.ListToolCalls(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve LocalMind task context: %w", err)
	}
	known := map[string]groundedTarget{}
	latestDelegate := groundedTarget{}
	latestDelegateAt := time.Time{}
	for _, call := range calls {
		if !toolCallCompleted(call) || !isLocalMindTaskToolCall(call) {
			continue
		}
		output, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		taskID := strings.TrimSpace(stringValue(output["taskId"]))
		stateVersion := strings.TrimSpace(stringValue(output["stateVersion"]))
		status := strings.TrimSpace(stringValue(output["status"]))
		if taskID == "" || taskID == "<nil>" || stateVersion == "" || stateVersion == "<nil>" || status == "" || status == "<nil>" {
			continue
		}
		target := groundedTarget{Kind: string(app.TargetKindLocalMindTask), Ref: taskID, Facts: map[string]string{
			"state_version": stateVersion, "status": status, "task_provenance": call.ID,
		}}
		known[taskID] = target
		if isLocalMindDelegateToolCall(call) {
			observedAt := completedToolCallTime(call)
			if latestDelegate.Ref == "" || observedAt.After(latestDelegateAt) {
				latestDelegate, latestDelegateAt = target, observedAt
			}
		}
	}

	targets := []groundedTarget{}
	for taskID, target := range known {
		if strings.Contains(content, taskID) {
			targets = append(targets, target)
		}
	}
	for _, target := range localMindLabelledTaskTargets(content) {
		if knownTarget, ok := known[target.Ref]; ok {
			target = knownTarget
		}
		targets = append(targets, target)
	}
	if localMindRecentTaskReference(content) && latestDelegate.Ref != "" {
		targets = append(targets, latestDelegate)
	}
	return dedupeGroundedTargets(targets), nil
}

func localMindLabelledTaskTargets(content string) []groundedTarget {
	matches := localMindTaskIDLabelPattern.FindAllStringSubmatch(content, -1)
	targets := make([]groundedTarget, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}
		targets = append(targets, groundedTarget{
			Kind: string(app.TargetKindLocalMindTask), Ref: strings.TrimSpace(match[1]),
			Facts: map[string]string{"task_provenance": "current_turn"},
		})
	}
	return targets
}

func localMindRecentTaskReference(content string) bool {
	lower := strings.ToLower(content)
	for _, phrase := range []string{
		"最近的任务", "最近那个任务", "刚才的任务", "刚才那个任务", "上一个任务", "那个任务",
		"latest task", "most recent task", "recent task", "last task", "that task", "task just now",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isLocalMindTaskToolCall(call app.ToolCall) bool {
	return isLocalMindDelegateToolCall(call) || call.Tool == "localmind.task.get" || call.Tool == "localmind.task.cancel"
}

func isLocalMindDelegateToolCall(call app.ToolCall) bool {
	return call.Tool == "localmind.task.delegate" || call.Tool == "localmind.task.delegate_read"
}

func (r Runtime) groundLocalMindRouteIdentity(capability app.CapabilityID, route *app.RouteDecision) {
	if r.tools == nil || route == nil {
		return
	}
	requiredCapability := localMindRouteToolCapability(capability)
	if requiredCapability == "" {
		return
	}
	for _, definition := range r.tools.Definitions() {
		for _, descriptor := range definition.Capabilities {
			if descriptor.Name != requiredCapability {
				continue
			}
			endpointID := strings.TrimSpace(descriptor.Qualifiers[app.CapabilityQualifierEndpointID])
			snapshotRevision := strings.TrimSpace(descriptor.Qualifiers[app.CapabilityQualifierSnapshotRevision])
			if endpointID == "" || snapshotRevision == "" {
				continue
			}
			if route.Facts == nil {
				route.Facts = map[string]string{}
			}
			route.Facts[localMindEndpointFact] = endpointID
			route.Facts[localMindSnapshotFact] = snapshotRevision
			return
		}
	}
}

func localMindRouteToolCapability(capability app.CapabilityID) string {
	switch capability {
	case app.CapabilityLocalMindRead:
		return app.ToolCapabilityLocalMindDelegateRead
	case app.CapabilityLocalMindWrite:
		return app.ToolCapabilityLocalMindDelegateWrite
	case app.CapabilityLocalMindQuery:
		return app.ToolCapabilityLocalMindTaskStatus
	case app.CapabilityLocalMindCancel:
		return app.ToolCapabilityLocalMindTaskCancel
	default:
		return ""
	}
}
