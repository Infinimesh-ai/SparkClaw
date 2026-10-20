package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestConnectionBindingRPCBudgetAllowsProviderPollAndPreservesCallerDeadline(t *testing.T) {
	initiator, _, _ := profileEndpoints(t, true, true, func(ctx context.Context, request Request) Response {
		deadline, ok := ctx.Deadline()
		want := requestTimeout
		if request.Operation == OperationSettingsBindingsStart || request.Operation == OperationSettingsBindingsPoll {
			want = 60 * time.Second
		}
		if remaining := time.Until(deadline); !ok || remaining > want || remaining < want-time.Second {
			t.Errorf("%s handler deadline does not preserve %s budget: %s", request.Operation, want, remaining)
		}
		if string(request.Body) == `{"wait_for_cancel":true}` {
			<-ctx.Done()
		}
		return Response{Status: 200, Body: json.RawMessage(`{}`)}
	})
	for _, operation := range []string{
		OperationSettingsBindingsStart, OperationSettingsBindingsPoll,
		OperationSettingsBindingsGet, OperationSettingsBindingsRevoke, OperationSettingsBrowserSave,
	} {
		response, err := initiator.Call(t.Context(), Request{
			Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: operation, Body: json.RawMessage(`{}`),
		})
		if err != nil || response.Status != 200 {
			t.Fatalf("%s failed: status=%d err=%v", operation, response.Status, err)
		}
	}
	for _, operation := range []string{OperationSettingsBindingsStart, OperationSettingsBindingsPoll} {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		started := time.Now()
		_, err := initiator.Call(ctx, Request{
			Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: operation, Body: json.RawMessage(`{"wait_for_cancel":true}`),
		})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
			t.Fatalf("%s extended the caller deadline: %v", operation, err)
		}
	}
}
