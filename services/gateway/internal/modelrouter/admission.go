package modelrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

const (
	maxModelResponseBytes   = 8 << 20
	maxTokenizerReplyBytes  = 1 << 20
	maxEmbeddingBatchInputs = 64
)

type InputTooLongError struct {
	Operation   modelcapacity.Operation
	Lane        modelcapacity.Lane
	InputTokens int
	InputBudget int
	Exact       bool
	Cause       error
}

func (e *InputTooLongError) Error() string {
	if e == nil {
		return "model input is too long"
	}
	message := fmt.Sprintf("model_input_too_long: operation=%s lane=%s input_tokens=%d input_budget=%d exact=%t", e.Operation, e.Lane, e.InputTokens, e.InputBudget, e.Exact)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *InputTooLongError) Unwrap() error { return e.Cause }

type IncompleteOutputError struct {
	FinishReason string
}

func (e *IncompleteOutputError) Error() string {
	return fmt.Sprintf("model output is incomplete (finish_reason=%s)", e.FinishReason)
}

func (r Router) laneAndBudget(profile config.ModelProfile, operation modelcapacity.Operation) (modelcapacity.Lane, int, error) {
	spec, err := modelcapacity.Spec(operation)
	if err != nil {
		return "", 0, err
	}
	lane := modelcapacity.Lane(r.LaneFor(profile))
	if !modelcapacity.Allows(spec, lane) {
		return lane, 0, fmt.Errorf("model operation %q is not allowed on lane %q", operation, lane)
	}
	if !spec.Generates {
		return lane, 0, nil
	}
	budget, exists := profile.OutputBudgets[spec.OutputClass]
	if !exists || budget <= 0 {
		return lane, 0, fmt.Errorf("model operation %q has no positive %q output budget on lane %q", operation, spec.OutputClass, lane)
	}
	if profile.ContextTokens <= 0 || budget >= profile.ContextTokens {
		return lane, 0, fmt.Errorf("model operation %q has invalid capacity relation on lane %q", operation, lane)
	}
	return lane, budget, nil
}

func (r Router) CapacityForTask(task Task) (config.ModelProfile, int, int, error) {
	profile := r.ChooseModel(task)
	_, outputBudget, err := r.laneAndBudget(profile, task.Operation)
	if err != nil {
		return config.ModelProfile{}, 0, 0, err
	}
	inputBudget := profile.ContextTokens - outputBudget
	if inputBudget <= 0 {
		return config.ModelProfile{}, 0, 0, fmt.Errorf("model operation %q has no positive input budget", task.Operation)
	}
	return profile, inputBudget, outputBudget, nil
}

// CountTaskChatInput counts a complete non-image chat request without
// dispatching generation. The conservative count avoids tokenizer round trips
// for prompts that plainly fit; prompts near the boundary use the selected
// model's tokenizer.
func (r Router) CountTaskChatInput(ctx context.Context, task Task, system, user string) (int, error) {
	profile := r.ChooseModel(task)
	count, _, _, err := r.countChatInput(ctx, profile, task.Operation, system, user, ChatOptions{}, 0)
	return count, err
}

// CountProfileChatInput is the profile-explicit counterpart used by Tree and
// other callers whose lane is part of their typed operation contract.
func (r Router) CountProfileChatInput(ctx context.Context, operation modelcapacity.Operation, profileName, system, user string, options ChatOptions) (int, error) {
	if err := options.validate(); err != nil {
		return 0, err
	}
	profile, err := r.Profile(profileName)
	if err != nil {
		return 0, err
	}
	count, _, _, err := r.countChatInput(ctx, profile, operation, system, user, options, 0)
	return count, err
}

func (r Router) countChatInput(ctx context.Context, profile config.ModelProfile, operation modelcapacity.Operation, system, user string, options ChatOptions, imageTokens int) (int, int, bool, error) {
	lane, outputBudget, err := r.laneAndBudget(profile, operation)
	if err != nil {
		return 0, 0, false, err
	}
	inputBudget := profile.ContextTokens - outputBudget
	if r.cfg.Model.Mock {
		estimated := 64 + estimateTokens(modelID(profile)) + estimateTokens(system) + estimateTokens(user) + imageTokens
		if schemaBytes := strictJSONSchemaTokenEnvelope(options); schemaBytes > 0 {
			estimated += (schemaBytes + 3) / 4
		}
		return estimated, inputBudget, false, nil
	}
	upper := conservativeChatTokens(profile, system, user, options, imageTokens)
	if upper <= inputBudget {
		return upper, inputBudget, false, nil
	}
	exact, countErr := r.countChatTokens(ctx, profile, system, user, options)
	if countErr != nil {
		return upper, inputBudget, false, fmt.Errorf("count %s input on lane %s: %w", operation, lane, countErr)
	}
	exact += imageTokens + strictJSONSchemaTokenEnvelope(options)
	return exact, inputBudget, true, nil
}

func (r Router) admitChat(ctx context.Context, profile config.ModelProfile, operation modelcapacity.Operation, system, user string, options ChatOptions, imageTokens int) (int, error) {
	lane, outputBudget, err := r.laneAndBudget(profile, operation)
	if err != nil {
		return 0, err
	}
	inputTokens, inputBudget, exact, countErr := r.countChatInput(ctx, profile, operation, system, user, options, imageTokens)
	if countErr != nil {
		return 0, countErr
	}
	if inputTokens > inputBudget {
		return 0, &InputTooLongError{Operation: operation, Lane: lane, InputTokens: inputTokens, InputBudget: inputBudget, Exact: exact}
	}
	return outputBudget, nil
}

func (r Router) admitEmbedding(ctx context.Context, profile config.ModelProfile, operation modelcapacity.Operation, inputs []string) error {
	lane, _, err := r.laneAndBudget(profile, operation)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		upper := conservativeTextTokens(input) + 8
		if upper <= profile.ContextTokens {
			continue
		}
		if r.cfg.Model.Mock {
			return &InputTooLongError{Operation: operation, Lane: lane, InputTokens: upper, InputBudget: profile.ContextTokens}
		}
		exact, countErr := r.countTextTokens(ctx, profile, input)
		if countErr != nil {
			return fmt.Errorf("count %s input on lane %s: %w", operation, lane, countErr)
		}
		if exact > profile.ContextTokens {
			return &InputTooLongError{Operation: operation, Lane: lane, InputTokens: exact, InputBudget: profile.ContextTokens, Exact: true}
		}
	}
	return nil
}

// AdmitOwnerQuestion proves the unchanged owner text independently against
// the mandatory Embedding and Guard requests without dispatching either model.
func (r Router) AdmitOwnerQuestion(ctx context.Context, question string) error {
	if err := r.admitEmbedding(ctx, r.cfg.Model.Embedding, modelcapacity.OperationIntentQueryEmbedding, []string{question}); err != nil {
		return err
	}
	_, err := r.admitChat(ctx, r.cfg.Model.Guard, modelcapacity.OperationGuardModeration, guardSystemPrompt, question, ChatOptions{}, 0)
	return err
}

func conservativeChatTokens(profile config.ModelProfile, system, user string, options ChatOptions, imageTokens int) int {
	return 64 + conservativeTextTokens(modelID(profile)) + conservativeTextTokens(system) + conservativeTextTokens(user) +
		imageTokens + strictJSONSchemaTokenEnvelope(options)
}

func strictJSONSchemaTokenEnvelope(options ChatOptions) int {
	if options.StrictJSONSchema == nil {
		return 0
	}
	jsonSchema := map[string]any{
		"name":   strings.TrimSpace(options.StrictJSONSchema.Name),
		"strict": true,
		"schema": options.StrictJSONSchema.Schema,
	}
	if description := strings.TrimSpace(options.StrictJSONSchema.Description); description != "" {
		jsonSchema["description"] = description
	}
	raw, err := json.Marshal(map[string]any{
		"type":        "json_schema",
		"json_schema": jsonSchema,
	})
	if err != nil {
		return 0
	}
	return len(raw)
}

// One tokenizer token cannot encode more input bytes than are present. Using
// UTF-8 byte length is intentionally conservative for the supported BPE model
// families and lets ordinary requests avoid a network count round trip.
func conservativeTextTokens(value string) int {
	return len([]byte(value))
}

func estimateImageTokens(input ImageInput) int {
	config, _, err := image.DecodeConfig(bytes.NewReader(input.Content))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return (len(input.Content)+255)/256 + 64
	}
	// Qwen vision inputs are patchified on an effective 28-pixel grid. The
	// additive envelope covers special image markers and resize rounding.
	return ((config.Width+27)/28)*((config.Height+27)/28) + 128
}

func (r Router) countChatTokens(ctx context.Context, profile config.ModelProfile, system, user string, options ChatOptions) (int, error) {
	body := map[string]any{
		"model": modelID(profile),
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"add_generation_prompt": true,
	}
	if r.cfg.Model.DisableThinking || options.ForceDisableThinking {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	return r.tokenize(ctx, profile, body)
}

func (r Router) countTextTokens(ctx context.Context, profile config.ModelProfile, input string) (int, error) {
	return r.tokenize(ctx, profile, map[string]any{"model": modelID(profile), "prompt": input})
}

func (r Router) tokenize(ctx context.Context, profile config.ModelProfile, body map[string]any) (int, error) {
	if strings.TrimSpace(profile.BaseURL) == "" {
		return 0, errors.New("tokenizer endpoint is unavailable")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/tokenize"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("tokenizer endpoint returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Count  int   `json:"count"`
		Tokens []int `json:"tokens"`
	}
	if err := decodeJSONLimit(resp.Body, &decoded, maxTokenizerReplyBytes); err != nil {
		return 0, err
	}
	if decoded.Count <= 0 {
		decoded.Count = len(decoded.Tokens)
	}
	if decoded.Count <= 0 {
		return 0, errors.New("tokenizer response had no positive count")
	}
	return decoded.Count, nil
}

func validateFinishReason(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return "unspecified", nil
	case "stop", "tool_calls":
		return value, nil
	case "length":
		return value, &IncompleteOutputError{FinishReason: value}
	default:
		return value, fmt.Errorf("model returned unsupported finish_reason %q", value)
	}
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	return decodeJSONLimit(reader, target, maxModelResponseBytes)
}

func decodeJSONLimit(reader io.Reader, target any, limit int64) error {
	limited := io.LimitReader(reader, limit+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return fmt.Errorf("model response exceeded %d-byte limit", limit)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	return nil
}
