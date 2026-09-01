package toolhub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const (
	PPTXSealedCandidateArgument = "_sparkclaw_pptx_sealed_candidate"

	pptxSealedCandidateBindingSchema  = "sparkclaw.pptx_sealed_candidate_binding.v1"
	pptxSealedCandidateManifestSchema = "sparkclaw.pptx_sealed_candidate_manifest.v1"
	pptxSealedCandidateTTL            = 24 * time.Hour
	pptxVisualQAPolicyVersion         = "sparkclaw.pptx_visual_qa_policy.v1"
)

type PPTXSealedCandidateBinding struct {
	SchemaVersion   string    `json:"schema_version"`
	ManifestKey     string    `json:"manifest_key"`
	ManifestSHA256  string    `json:"manifest_sha256"`
	CandidateSHA256 string    `json:"candidate_sha256"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type pptxSealedCandidateManifest struct {
	SchemaVersion      string                       `json:"schema_version"`
	Tool               string                       `json:"tool"`
	Operation          string                       `json:"operation"`
	SessionID          string                       `json:"session_id"`
	RunID              string                       `json:"run_id"`
	OwnerID            string                       `json:"owner_id"`
	SourcePath         string                       `json:"source_path"`
	SourceSHA256       string                       `json:"source_sha256"`
	OutputPath         string                       `json:"output_path"`
	ArgumentSHA256     string                       `json:"argument_sha256"`
	CandidateKey       string                       `json:"candidate_key"`
	CandidateSHA256    string                       `json:"candidate_sha256"`
	CandidateBytes     int                          `json:"candidate_bytes"`
	MutationOutput     map[string]any               `json:"mutation_output"`
	VisualReport       PPTXVisualReport             `json:"visual_report"`
	VisualReportSHA256 string                       `json:"visual_report_sha256"`
	Attempts           []pptxSealedCandidateAttempt `json:"attempts"`
	RolloutPhase       string                       `json:"rollout_phase"`
	PolicyVersion      string                       `json:"policy_version"`
	PolicyConfigSHA256 string                       `json:"policy_config_sha256"`
	GotenbergVersion   string                       `json:"gotenberg_version"`
	LibreOfficeVersion string                       `json:"libreoffice_version"`
	PDFiumVersion      string                       `json:"pdfium_version"`
	CreatedAt          time.Time                    `json:"created_at"`
	ExpiresAt          time.Time                    `json:"expires_at"`
}

func (h *ToolHub) IsPPTXMutationTool(name string, args map[string]any) bool {
	_, ok := pptxMutationOperationForArguments(name, args)
	return ok
}

func (h *ToolHub) HasPPTXSealedCandidateArguments(args map[string]any) bool {
	_, ok := args[PPTXSealedCandidateArgument]
	return ok
}

func PPTXPublicArguments(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		if key == PPTXSealedCandidateArgument {
			continue
		}
		out[key] = value
	}
	return out
}

func AttachPPTXSealedCandidate(args map[string]any, binding PPTXSealedCandidateBinding) map[string]any {
	out := PPTXPublicArguments(args)
	out[PPTXSealedCandidateArgument] = binding
	return out
}

func PPTXSealedCandidateFromArguments(args map[string]any) (PPTXSealedCandidateBinding, bool, error) {
	value, ok := args[PPTXSealedCandidateArgument]
	if !ok {
		return PPTXSealedCandidateBinding{}, false, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return PPTXSealedCandidateBinding{}, true, errors.New("PPTX sealed candidate binding is not serializable")
	}
	var binding PPTXSealedCandidateBinding
	if err := decodePPTXVisualStrictJSON(raw, &binding); err != nil {
		return PPTXSealedCandidateBinding{}, true, fmt.Errorf("decode PPTX sealed candidate binding: %w", err)
	}
	if binding.SchemaVersion != pptxSealedCandidateBindingSchema || strings.TrimSpace(binding.ManifestKey) == "" ||
		!validPPTXSHA256(binding.ManifestSHA256) || !validPPTXSHA256(binding.CandidateSHA256) || binding.ExpiresAt.IsZero() {
		return PPTXSealedCandidateBinding{}, true, errors.New("PPTX sealed candidate binding is invalid")
	}
	return binding, true, nil
}

func ValidatePPTXSealedApprovalArguments(callArgs, approvalArgs map[string]any) error {
	callBinding, callSealed, err := PPTXSealedCandidateFromArguments(callArgs)
	if err != nil {
		return err
	}
	approvalBinding, approvalSealed, err := PPTXSealedCandidateFromArguments(approvalArgs)
	if err != nil {
		return err
	}
	if !callSealed || !approvalSealed || callBinding != approvalBinding {
		return errors.New("approved PPTX sealed candidate does not match its tool call")
	}
	callDigest, err := pptxMutationArgumentSHA256(callArgs)
	if err != nil {
		return err
	}
	approvalDigest, err := pptxMutationArgumentSHA256(approvalArgs)
	if err != nil {
		return err
	}
	if callDigest != approvalDigest {
		return errors.New("approved PPTX arguments do not match the sealed tool call")
	}
	return nil
}

func (h *ToolHub) PreparePPTXCandidate(ctx context.Context, name string, args map[string]any, sessionID, runID string) (PPTXSealedCandidateBinding, error) {
	operation, ok := pptxMutationOperationForArguments(name, args)
	if !ok {
		return PPTXSealedCandidateBinding{}, errors.New("tool is not a governed PPTX mutation")
	}
	publicArgs := PPTXPublicArguments(args)
	if err := h.Validate(name, publicArgs); err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	scoped, err := h.forSession(ctx, sessionID)
	if err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	if scoped.artifacts == nil {
		return PPTXSealedCandidateBinding{}, errors.New("PPTX sealed candidate artifact store is unavailable")
	}
	root := scoped.cfg.Workspaces.DefaultRoot
	if err := os.MkdirAll(root, 0o755); err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	tempDir, err := os.MkdirTemp(root, ".sparkclaw-pptx-preflight-")
	if err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	defer os.RemoveAll(tempDir)

	preflightArgs := PPTXPublicArguments(publicArgs)
	preflightArgs["output_path"] = filepath.Join(tempDir, "candidate.pptx")
	preflightCtx, cancel := context.WithTimeout(ctx, pptxCandidatePreparationTimeout(scoped.cfg.Adapters.PPTXVisualQA))
	defer cancel()
	mutationResult, err := scoped.executeDocumentOperation(preflightCtx, name, operation, preflightArgs)
	if err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	mutationOutput, ok := mutationResult.Output.(map[string]any)
	if !ok {
		return PPTXSealedCandidateBinding{}, errors.New("PPTX preflight mutation returned an invalid result")
	}
	candidatePath := preflightArgs["output_path"].(string)
	selectedSlides, changedShapeIndexes, changedAllSlides, selectionErr := pptxVisualQASelection(operation, preflightArgs, mutationOutput)
	if selectionErr != nil {
		code := app.ToolErrorPPTXRenderPageMismatch
		scoped.auditPPTXVisualQAFailure(preflightCtx, sessionID, runID, operation, nil, pptxVisualQAIntegrityError, code, "Recorded PPTX final-render visual QA selection failure")
		return PPTXSealedCandidateBinding{}, &app.CodedToolError{Code: code, Err: fmt.Errorf("derive PPTX final-render page selection: %w", selectionErr)}
	}
	visualPreparation, err := scoped.preparePPTXVisualCandidate(
		preflightCtx, candidatePath, operation, tempDir, sessionID, runID,
		selectedSlides, changedShapeIndexes, changedAllSlides,
	)
	if err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	candidatePath = visualPreparation.CandidatePath
	visualReport := visualPreparation.Report

	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		return PPTXSealedCandidateBinding{}, fmt.Errorf("read PPTX preflight candidate: %w", err)
	}
	if int64(len(candidate)) > scoped.cfg.Adapters.PPTXVisualQA.MaxInputBytes {
		return PPTXSealedCandidateBinding{}, errors.New("PPTX preflight candidate exceeds the configured byte limit")
	}
	visualRaw, err := json.Marshal(visualReport)
	if err != nil {
		return PPTXSealedCandidateBinding{}, fmt.Errorf("encode PPTX visual report: %w", err)
	}
	candidateSHA := pptxBytesSHA256(candidate)
	acceptedAttempt := false
	for index := range visualPreparation.Attempts {
		attempt := &visualPreparation.Attempts[index]
		if attempt.CandidateSHA256 == candidateSHA && attempt.VisualReportSHA256 == pptxBytesSHA256(visualRaw) {
			attempt.Accepted = true
			acceptedAttempt = true
		}
	}
	if !acceptedAttempt {
		return PPTXSealedCandidateBinding{}, errors.New("PPTX visual preparation did not bind the accepted candidate attempt")
	}
	argumentSHA, err := pptxMutationArgumentSHA256(publicArgs)
	if err != nil {
		return PPTXSealedCandidateBinding{}, err
	}
	ownerID := app.DefaultOwnerID
	if scoped.store != nil && strings.TrimSpace(sessionID) != "" {
		if session, found, sessionErr := scoped.store.GetSession(preflightCtx, sessionID); sessionErr != nil {
			return PPTXSealedCandidateBinding{}, fmt.Errorf("load PPTX candidate owner: %w", sessionErr)
		} else if found && strings.TrimSpace(session.OwnerID) != "" {
			ownerID = strings.TrimSpace(session.OwnerID)
		}
	}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := createdAt.Add(pptxSealedCandidateTTL)
	policyConfigSHA, err := pptxVisualPolicyConfigSHA256(scoped.cfg.Adapters.PPTXVisualQA)
	if err != nil {
		return PPTXSealedCandidateBinding{}, fmt.Errorf("encode PPTX visual policy configuration: %w", err)
	}
	scopeKey := pptxBytesSHA256([]byte(ownerID + "\x00" + sessionID + "\x00" + runID))[:24]
	candidateKey := filepath.ToSlash(filepath.Join("pptx", "sealed", scopeKey, candidateSHA+".pptx"))
	sealedOutput := clonePPTXMap(mutationOutput)
	sealedOutput["output_path"] = strings.TrimSpace(stringArg(publicArgs, "output_path", ""))
	if _, ok := sealedOutput["outputs"]; ok {
		sealedOutput["outputs"] = []string{strings.TrimSpace(stringArg(publicArgs, "output_path", ""))}
	}
	manifest := pptxSealedCandidateManifest{
		SchemaVersion: pptxSealedCandidateManifestSchema, Tool: name, Operation: operation,
		SessionID: sessionID, RunID: runID, OwnerID: ownerID,
		SourcePath: strings.TrimSpace(stringArg(publicArgs, "path", "")), SourceSHA256: strings.TrimSpace(stringArg(publicArgs, app.DocumentSourceSHA256Argument, "")),
		OutputPath: strings.TrimSpace(stringArg(publicArgs, "output_path", "")), ArgumentSHA256: argumentSHA,
		CandidateKey: candidateKey, CandidateSHA256: candidateSHA, CandidateBytes: len(candidate), MutationOutput: sealedOutput,
		VisualReport: visualReport, VisualReportSHA256: pptxBytesSHA256(visualRaw), Attempts: visualPreparation.Attempts,
		RolloutPhase: strings.ToLower(strings.TrimSpace(scoped.cfg.Adapters.PPTXVisualQA.Phase)), PolicyVersion: pptxVisualQAPolicyVersion, PolicyConfigSHA256: policyConfigSHA,
		GotenbergVersion: "8.36.0", LibreOfficeVersion: "26.2.5.2", PDFiumVersion: "5.12.1",
		CreatedAt: createdAt, ExpiresAt: expiresAt,
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return PPTXSealedCandidateBinding{}, fmt.Errorf("encode PPTX sealed candidate manifest: %w", err)
	}
	manifestSHA := pptxBytesSHA256(manifestRaw)
	manifestKey := filepath.ToSlash(filepath.Join("pptx", "sealed", scopeKey, manifestSHA+".json"))
	if _, err := scoped.artifacts.Put(preflightCtx, candidateKey, "application/vnd.openxmlformats-officedocument.presentationml.presentation", candidate); err != nil {
		return PPTXSealedCandidateBinding{}, fmt.Errorf("seal PPTX candidate: %w", err)
	}
	if _, err := scoped.artifacts.Put(preflightCtx, manifestKey, "application/json", manifestRaw); err != nil {
		_ = scoped.artifacts.Delete(context.WithoutCancel(preflightCtx), candidateKey)
		return PPTXSealedCandidateBinding{}, fmt.Errorf("seal PPTX candidate manifest: %w", err)
	}
	binding := PPTXSealedCandidateBinding{
		SchemaVersion: pptxSealedCandidateBindingSchema, ManifestKey: manifestKey,
		ManifestSHA256: manifestSHA, CandidateSHA256: candidateSHA, ExpiresAt: expiresAt,
	}
	scoped.addAudit(preflightCtx, app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
		Type: "document.pptx.candidate_sealed", Summary: "Sealed the exact PPTX candidate prepared for approval",
		Fields: map[string]any{
			"tool": name, "operation": operation, "candidate_sha256": candidateSHA,
			"manifest_sha256": manifestSHA, "argument_sha256": argumentSHA, "expires_at": expiresAt,
		},
	})
	return binding, nil
}

func pptxCandidatePreparationTimeout(cfg config.PPTXVisualQAAdapterConfig) time.Duration {
	base := time.Duration(pptxEditTimeoutMS) * time.Millisecond
	if strings.EqualFold(strings.TrimSpace(cfg.Phase), "disabled") {
		return base
	}
	perRender := time.Duration(cfg.TimeoutSeconds) * time.Second
	if perRender <= 0 {
		perRender = 120 * time.Second
	}
	required := time.Duration(cfg.MaxRepairAttempts+1)*perRender + time.Duration(cfg.MaxRepairAttempts)*60*time.Second + 30*time.Second
	if required < base {
		return base
	}
	if required > 10*time.Minute {
		return 10 * time.Minute
	}
	return required
}

func (h *ToolHub) PublishSealedPPTXCandidate(ctx context.Context, name string, args map[string]any, sessionID, runID string) (Result, error) {
	operation, ok := pptxMutationOperationForArguments(name, args)
	if !ok {
		return Result{}, errors.New("tool is not a governed PPTX mutation")
	}
	binding, sealed, err := PPTXSealedCandidateFromArguments(args)
	if err != nil || !sealed {
		if err == nil {
			err = errors.New("approved PPTX mutation has no sealed candidate")
		}
		return Result{}, err
	}
	publicArgs := PPTXPublicArguments(args)
	if err := h.Validate(name, publicArgs); err != nil {
		return Result{}, err
	}
	scoped, err := h.forSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	if scoped.artifacts == nil {
		return Result{}, errors.New("PPTX sealed candidate artifact store is unavailable")
	}
	manifestRaw, err := scoped.artifacts.Get(ctx, binding.ManifestKey)
	if err != nil {
		return Result{}, fmt.Errorf("read PPTX sealed candidate manifest: %w", err)
	}
	if pptxBytesSHA256(manifestRaw) != binding.ManifestSHA256 {
		return Result{}, errors.New("PPTX sealed candidate manifest digest mismatch")
	}
	var manifest pptxSealedCandidateManifest
	if err := decodePPTXVisualStrictJSON(manifestRaw, &manifest); err != nil {
		return Result{}, fmt.Errorf("decode PPTX sealed candidate manifest: %w", err)
	}
	if err := scoped.validatePPTXSealedManifest(ctx, manifest, binding, name, operation, publicArgs, sessionID, runID); err != nil {
		return Result{}, err
	}
	candidate, err := scoped.artifacts.Get(ctx, manifest.CandidateKey)
	if err != nil {
		return Result{}, fmt.Errorf("read PPTX sealed candidate: %w", err)
	}
	if len(candidate) != manifest.CandidateBytes || pptxBytesSHA256(candidate) != manifest.CandidateSHA256 || manifest.CandidateSHA256 != binding.CandidateSHA256 {
		return Result{}, errors.New("PPTX sealed candidate bytes do not match the approved manifest")
	}
	outputPath, err := scoped.resolveNewOutputPath(stringArg(publicArgs, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if err := publishPPTXBytesAtomically(ctx, outputPath, candidate, manifest.CandidateSHA256); err != nil {
		return Result{}, err
	}
	output := clonePPTXMap(manifest.MutationOutput)
	output["output_path"] = outputPath
	output["bytes"] = len(candidate)
	if _, ok := output["outputs"]; ok {
		output["outputs"] = []string{outputPath}
	}
	scoped.addAudit(ctx, app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
		Type: "document.pptx.candidate_published", Summary: "Published the exact approved sealed PPTX candidate",
		Fields: map[string]any{
			"tool": name, "operation": operation, "candidate_sha256": manifest.CandidateSHA256,
			"manifest_sha256": binding.ManifestSHA256, "output_path": stringArg(publicArgs, "output_path", ""),
		},
	})
	return Result{Output: output}, nil
}

func (h *ToolHub) validatePPTXSealedManifest(ctx context.Context, manifest pptxSealedCandidateManifest, binding PPTXSealedCandidateBinding, name, operation string, args map[string]any, sessionID, runID string) error {
	if manifest.SchemaVersion != pptxSealedCandidateManifestSchema || manifest.Tool != name || manifest.Operation != operation ||
		manifest.SessionID != sessionID || manifest.RunID != runID || manifest.CandidateSHA256 != binding.CandidateSHA256 ||
		manifest.ExpiresAt.IsZero() || !manifest.ExpiresAt.Equal(binding.ExpiresAt) || manifest.CreatedAt.IsZero() ||
		manifest.PolicyVersion != pptxVisualQAPolicyVersion || !validPPTXSHA256(manifest.CandidateSHA256) ||
		!validPPTXSHA256(manifest.PolicyConfigSHA256) || !validPPTXSHA256(manifest.ArgumentSHA256) || !validPPTXSHA256(manifest.VisualReportSHA256) || manifest.CandidateBytes <= 0 ||
		manifest.VisualReport.SchemaVersion != pptxVisualReportSchema || len(manifest.Attempts) == 0 {
		return errors.New("PPTX sealed candidate manifest is invalid or belongs to a different operation")
	}
	visualReportRaw, err := json.Marshal(manifest.VisualReport)
	if err != nil || pptxBytesSHA256(visualReportRaw) != manifest.VisualReportSHA256 {
		return errors.New("PPTX sealed candidate visual report digest is invalid")
	}
	acceptedAttempts := 0
	previousCandidateSHA := ""
	for index, attempt := range manifest.Attempts {
		if attempt.Attempt != index || !validPPTXSHA256(attempt.CandidateSHA256) || !validPPTXSHA256(attempt.VisualReportSHA256) ||
			(index > 0 && attempt.InputCandidateSHA256 != previousCandidateSHA) || (index == 0 && attempt.InputCandidateSHA256 != "") {
			return errors.New("PPTX sealed candidate attempt chain is invalid")
		}
		for _, planSHA := range attempt.RepairPlanSHA256 {
			if !validPPTXSHA256(planSHA) {
				return errors.New("PPTX sealed candidate repair plan digest is invalid")
			}
		}
		if attempt.Accepted {
			acceptedAttempts++
			if attempt.CandidateSHA256 != manifest.CandidateSHA256 || attempt.VisualReportSHA256 != manifest.VisualReportSHA256 {
				return errors.New("PPTX sealed candidate accepted attempt does not match the final candidate")
			}
		}
		previousCandidateSHA = attempt.CandidateSHA256
	}
	if acceptedAttempts != 1 {
		return errors.New("PPTX sealed candidate manifest must contain exactly one accepted attempt")
	}
	if time.Now().UTC().After(manifest.ExpiresAt) {
		return errors.New("PPTX sealed candidate approval expired; prepare a new candidate")
	}
	currentPolicySHA, err := pptxVisualPolicyConfigSHA256(h.cfg.Adapters.PPTXVisualQA)
	if err != nil {
		return err
	}
	if currentPolicySHA != manifest.PolicyConfigSHA256 {
		return &app.CodedToolError{Code: app.ToolErrorPPTXRenderSourceStale, Err: errors.New("approved PPTX mutation is stale because the visual policy changed")}
	}
	argumentSHA, err := pptxMutationArgumentSHA256(args)
	if err != nil {
		return err
	}
	if argumentSHA != manifest.ArgumentSHA256 || manifest.SourcePath != strings.TrimSpace(stringArg(args, "path", "")) ||
		manifest.SourceSHA256 != strings.TrimSpace(stringArg(args, app.DocumentSourceSHA256Argument, "")) ||
		manifest.OutputPath != strings.TrimSpace(stringArg(args, "output_path", "")) {
		return errors.New("PPTX sealed candidate arguments do not match the approved manifest")
	}
	ownerID := app.DefaultOwnerID
	if h.store != nil && strings.TrimSpace(sessionID) != "" {
		session, found, err := h.store.GetSession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("load approved PPTX owner: %w", err)
		}
		if found && strings.TrimSpace(session.OwnerID) != "" {
			ownerID = strings.TrimSpace(session.OwnerID)
		}
	}
	if manifest.OwnerID != ownerID {
		return errors.New("PPTX sealed candidate belongs to a different owner")
	}
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return err
	}
	metadata, err := document.InspectFile(ctx, h.cfg.Workspaces.DefaultRoot, inputPath)
	if err != nil {
		return fmt.Errorf("revalidate approved PPTX source: %w", err)
	}
	if !strings.EqualFold(metadata.SHA256, manifest.SourceSHA256) {
		return &app.CodedToolError{Code: app.ToolErrorPPTXRenderSourceStale, Err: errors.New("approved PPTX mutation is stale because its source changed")}
	}
	return nil
}

func (h *ToolHub) PPTXSealedCandidateWarningSummary(ctx context.Context, args map[string]any) (string, error) {
	binding, sealed, err := PPTXSealedCandidateFromArguments(args)
	if err != nil || !sealed {
		return "", err
	}
	if h == nil || h.artifacts == nil {
		return "", errors.New("PPTX sealed candidate artifact store is unavailable")
	}
	raw, err := h.artifacts.Get(ctx, binding.ManifestKey)
	if err != nil {
		return "", err
	}
	if pptxBytesSHA256(raw) != binding.ManifestSHA256 {
		return "", errors.New("PPTX sealed candidate manifest digest mismatch")
	}
	var manifest pptxSealedCandidateManifest
	if err := decodePPTXVisualStrictJSON(raw, &manifest); err != nil {
		return "", err
	}
	reportRaw, err := json.Marshal(manifest.VisualReport)
	if err != nil || pptxBytesSHA256(reportRaw) != manifest.VisualReportSHA256 {
		return "", errors.New("PPTX sealed candidate visual report digest mismatch")
	}
	return pptxVisualWarningSummary(manifest.VisualReport, h.cfg.Adapters.PPTXVisualQA), nil
}

func (h *ToolHub) auditPPTXVisualQAFailure(ctx context.Context, sessionID, runID, operation string, slides []int, kind pptxVisualQAErrorKind, code app.ToolErrorCode, summary string) {
	h.addAudit(ctx, app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
		Type: "document.pptx.visual_qa", Summary: summary,
		Fields: map[string]any{
			"phase": h.cfg.Adapters.PPTXVisualQA.Phase, "status": "failed", "error_kind": string(kind),
			"error_code": string(code), "operation": operation, "slide_indexes": append([]int(nil), slides...),
		},
	})
}

func pptxMutationOperation(name string) (string, bool) {
	registry := toolhubDocumentProviderRegistry()
	for _, operation := range canonicalDocumentOperationOrder(app.DocumentFormatPPTX) {
		provider, ok := registry.operation(app.DocumentFormatPPTX, operation)
		if ok && provider.acceptsTool(strings.TrimSpace(name)) {
			return operation, true
		}
	}
	return "", false
}

func pptxMutationOperationForArguments(name string, args map[string]any) (string, bool) {
	operation, ok := pptxMutationOperation(name)
	if !ok {
		return "", false
	}
	provider, ok := toolhubDocumentProviderRegistry().operation(app.DocumentFormatPPTX, operation)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(name) == provider.ToolName {
		return operation, true
	}
	path := stringArg(PPTXPublicArguments(args), "path", "")
	if document.InferFormatFromMetadata(path, "") != app.DocumentFormatPPTX {
		return "", false
	}
	return operation, true
}

func pptxMutationArgumentSHA256(args map[string]any) (string, error) {
	canonical := make(map[string]any, len(args))
	for key, value := range args {
		if key == PPTXSealedCandidateArgument || key == "_verifier" {
			continue
		}
		canonical[key] = value
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode PPTX mutation arguments: %w", err)
	}
	return pptxBytesSHA256(raw), nil
}

func pptxBytesSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validPPTXSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func clonePPTXMap(input map[string]any) map[string]any {
	raw, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{}
	}
	return out
}

func publishPPTXBytesAtomically(ctx context.Context, outputPath string, raw []byte, expectedSHA string) error {
	if err := verifyExistingPPTXPublication(ctx, outputPath, expectedSHA); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(outputPath), ".sparkclaw-sealed-*.pptx")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := io.Copy(temp, bytes.NewReader(raw)); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.Link(tempPath, outputPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return verifyExistingPPTXPublication(ctx, outputPath, expectedSHA)
		}
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		_ = os.Remove(outputPath)
		return err
	}
	removeTemp = false
	if directory, err := os.Open(filepath.Dir(outputPath)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	if err := verifyExistingPPTXPublication(ctx, outputPath, expectedSHA); err != nil {
		_ = os.Remove(outputPath)
		return err
	}
	return nil
}

func verifyExistingPPTXPublication(ctx context.Context, path, expectedSHA string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return errors.New("PPTX output path already exists with different bytes")
	}
	return nil
}
