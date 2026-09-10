package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Service) parse(ctx context.Context, job app.EmailJob) error {
	cmd := jobCommand(job, "parse")
	if done, err := s.committed(ctx, cmd); err != nil || done {
		return err
	}
	mail, found, err := s.repository.GetEmailMail(ctx, job.OwnerID, job.TargetID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("email_mail_not_found")
	}
	box, found, err := s.repository.GetEmailMailbox(ctx, job.OwnerID, mail.MailboxID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("email_mailbox_not_found")
	}
	capture, found, err := s.repository.GetEmailCapture(ctx, job.OwnerID, mail.CaptureID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("email_capture_missing")
	}
	representation, err := parseCapturedMail(ctx, s.opts.WorkspaceRoot, job.OwnerID, mail, box, capture)
	if err != nil {
		return err
	}
	relative := path.Join("email", ownerScope(job.OwnerID), "normalized", representation.ID, "representation.json")
	if err = publishJSON(ctx, s.opts.WorkspaceRoot, relative, representation); err != nil {
		return err
	}
	raw, _ := json.Marshal(representation)
	representation.ManifestPath, representation.ManifestSHA256 = relative, sourceHash(raw)
	_, err = s.repository.PublishEmailRepresentation(ctx, store.EmailRepresentationCommand{EmailCommand: cmd, Lease: lease(job, s.now()), Representation: representation})
	return s.reconcileError(ctx, cmd, err)
}

// Stabilize dependencies before invoking the model. If candidate selection adds
// inputs, the Store creates a new generation and that job re-reads the sources.
// Reads bracketed by the same monotonic fingerprint cannot mix revisions.
func (s *Service) analyze(ctx context.Context, job app.EmailJob) error {
	cmd := jobCommand(job, "analysis")
	if done, err := s.committed(ctx, cmd); err != nil || done {
		return err
	}
	if job.Kind == app.EmailJobClassification {
		m, ok, err := s.repository.GetEmailMail(ctx, job.OwnerID, job.TargetID)
		if err != nil {
			return err
		}
		if ok && m.Classification != nil && (m.Classification.Source == "manual" || m.Classification.Source == "rule") {
			_, err = s.repository.PublishEmailClassification(ctx, store.EmailClassificationCommand{EmailCommand: cmd, Lease: lease(job, s.now()), MailID: job.TargetID, Generation: job.Generation, Classification: app.EmailClassification{Category: "unknown", InputFingerprint: job.InputFingerprint}})
			return s.reconcileError(ctx, cmd, err)
		}
	}
	before, found, err := s.repository.GetEmailAnalysisTarget(ctx, job.OwnerID, job.Kind, job.TargetID)
	if err != nil {
		return err
	}
	if !found || before.InputFingerprint != job.InputFingerprint || before.State == app.EmailSummaryStale {
		return nil
	}
	input, refs, epoch, err := s.buildAnalysis(ctx, job)
	if err != nil {
		return err
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)
	next, err := s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(job.OwnerID, app.NewID("analysis_inputs")), Kind: job.Kind, TargetID: job.TargetID, Dependencies: refs})
	if err != nil {
		return err
	}
	if next.ID != job.ID {
		return nil
	}
	after, found, err := s.repository.GetEmailAnalysisTarget(ctx, job.OwnerID, job.Kind, job.TargetID)
	if err != nil {
		return err
	}
	if !found || after.InputFingerprint != before.InputFingerprint || after.State == app.EmailSummaryStale {
		return nil
	}
	if s.analyzer == nil {
		return errors.New("email_model_unavailable")
	}
	bodyInput := input
	if input.Kind == app.EmailJobClassification && input.PolicyVersion == analysisPromptVersion {
		input.ClassificationStage = "subject"
		input.Evidence = []Evidence{{Ref: "mail:" + input.TargetID + ":subject", Text: input.Subject}}
		// Keep an explicit subject source location independent of optional headers.
		for _, e := range bodyInput.Evidence {
			if strings.HasSuffix(e.Ref, ":body") {
				input.Evidence[0].Ref = strings.TrimSuffix(e.Ref, ":body") + ":subject"
				break
			}
		}
		input.Participants = nil
		input.MissingContext = nil
	}
	artifactID := app.NewID("analysis")
	directory := path.Join("email", ownerScope(job.OwnerID), "analysis", artifactID)
	inputPath, outputPath := path.Join(directory, "input.json"), path.Join(directory, "output.json")
	artifact := struct {
		PromptVersion string           `json:"prompt_version"`
		Generation    int64            `json:"generation"`
		Fingerprint   string           `json:"input_fingerprint"`
		OwnerEpoch    int64            `json:"owner_epoch"`
		Inputs        map[string]int64 `json:"inputs"`
		Analysis      AnalysisInput    `json:"analysis"`
	}{analysisPromptVersion, job.Generation, job.InputFingerprint, epoch, after.Inputs, input}
	if err = publishJSON(ctx, s.opts.WorkspaceRoot, inputPath, artifact); err != nil {
		return err
	}
	started := s.now()
	output, modelErr := s.analyzer.Analyze(ctx, input)
	if modelErr == nil {
		modelErr = validateAnalysis(input, output)
	}
	if modelErr == nil && (output.Mock || output.ModelVersion == "") {
		modelErr = errors.New("email_model_mock_unqualified")
	}
	if auditErr := s.recordAnalysisArtifact(ctx, directory, input.ClassificationStage, started, output, modelErr); auditErr != nil {
		return auditErr
	}
	// Preserve unsuccessful output as well as successful calls for audit.
	if err = publishJSON(ctx, s.opts.WorkspaceRoot, outputPath, output); err != nil {
		return err
	}
	if modelErr != nil {
		return modelErr
	}
	if input.ClassificationStage == "subject" && (output.Category != "interaction" || output.Uncertainty) {
		// A notification-looking subject cannot rule out a concrete request in
		// the body. Confirm notification decisions with bounded body evidence.
		input = bodyInput
		inputPath, outputPath = path.Join(directory, "body-input.json"), path.Join(directory, "body-output.json")
		artifact.Analysis = input
		if err = publishJSON(ctx, s.opts.WorkspaceRoot, inputPath, artifact); err != nil {
			return err
		}
		started = s.now()
		output, modelErr = s.analyzer.Analyze(ctx, input)
		if modelErr == nil {
			modelErr = validateAnalysis(input, output)
		}
		if modelErr == nil && (output.Mock || output.ModelVersion == "") {
			modelErr = errors.New("email_model_mock_unqualified")
		}
		if auditErr := s.recordAnalysisArtifact(ctx, directory, input.ClassificationStage, started, output, modelErr); auditErr != nil {
			return auditErr
		}
		if err = publishJSON(ctx, s.opts.WorkspaceRoot, outputPath, output); err != nil {
			return err
		}
		if modelErr != nil {
			return modelErr
		}
	}

	inputRaw, _ := json.Marshal(artifact)
	outputRaw, _ := json.Marshal(output)
	inputHash, outputHash := sourceHash(inputRaw), sourceHash(outputRaw)
	candidateIDs := make([]string, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	switch job.Kind {
	case app.EmailJobClassification:
		var verification *app.EmailVerification
		if input.PolicyVersion != analysisPromptVersion {
			verification, _ = verificationFromOutput(input, output)
		}
		reason := "body_evidence"
		if input.ClassificationStage == "subject" {
			reason = "subject_evidence"
		}
		if output.Category == "unknown" || output.Uncertainty {
			reason = "classification_uncertain"
		}
		_, err = s.repository.PublishEmailClassification(ctx, store.EmailClassificationCommand{EmailCommand: cmd, Lease: lease(job, s.now()), MailID: job.TargetID, Generation: job.Generation, Verification: verification, Classification: app.EmailClassification{ID: artifactID, InputPath: inputPath, InputSHA256: inputHash, OutputPath: outputPath, OutputSHA256: outputHash, ModelVersion: output.ModelVersion, PromptVersion: analysisPromptVersion, Stage: input.ClassificationStage, RequestedResponse: output.RequestedResponse, Purpose: output.Purpose, ServiceLabel: output.ServiceLabel, Reason: output.Reason, Evidence: classificationEvidence(input, output), Category: output.Category, NotificationSubtype: output.NotificationSubtype, Uncertainty: output.Uncertainty, ReasonCode: reason, EvidenceRefs: output.EvidenceRefs, InputFingerprint: job.InputFingerprint}})

	case app.EmailJobAssignment:
		_, err = s.repository.CommitEmailAssignment(ctx, store.EmailAssignmentCommand{EmailCommand: cmd, Lease: lease(job, s.now()), Generation: job.Generation, CandidateConversationIDs: candidateIDs,
			Decision: app.EmailAssignmentDecision{ID: artifactID, MailID: job.TargetID, Action: output.Action, ConversationID: output.TargetConversationID, Title: output.Title, OwnerEpoch: epoch,
				InputFingerprint: job.InputFingerprint, InputPath: inputPath, InputSHA256: inputHash, OutputPath: outputPath, OutputSHA256: outputHash, ModelVersion: output.ModelVersion, PromptVersion: analysisPromptVersion, Reason: output.Reason, EvidenceRefs: output.EvidenceRefs}})
	case app.EmailJobRelationshipCheck:
		ids := append([]string{input.CurrentConversationID}, output.RelatedConversationIDs...)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		_, err = s.repository.PublishEmailConcern(ctx, store.EmailConcernCommand{EmailCommand: cmd, Lease: lease(job, s.now()), TargetID: job.TargetID, Generation: job.Generation,
			Concern: app.EmailAssignmentConcern{InputPath: inputPath, InputSHA256: inputHash, OutputPath: outputPath, OutputSHA256: outputHash, ModelVersion: output.ModelVersion, PromptVersion: analysisPromptVersion, ID: artifactID, Kind: output.Concern, MailIDs: []string{job.TargetID}, ConversationIDs: ids, Reason: output.Reason, EvidenceRefs: output.EvidenceRefs, InputFingerprint: job.InputFingerprint}})
	default:
		coverage := "complete_for_inputs"
		if len(output.MissingContext) > 0 {
			coverage = strings.Join(output.MissingContext, "; ")
		}
		_, err = s.repository.PublishEmailSummary(ctx, store.EmailSummaryCommand{EmailCommand: cmd, Lease: lease(job, s.now()), Summary: app.EmailSummary{
			ID: artifactID, TargetKind: job.Kind, TargetID: job.TargetID, Text: output.Summary, EvidenceRefs: output.EvidenceRefs, Coverage: coverage, InputPath: inputPath, InputSHA256: inputHash,
			OutputPath: outputPath, OutputSHA256: outputHash, ModelVersion: output.ModelVersion, PromptVersion: analysisPromptVersion, Generation: job.Generation, InputFingerprint: job.InputFingerprint}})
	}
	return s.reconcileError(ctx, cmd, err)
}

func (s *Service) recordAnalysis(ctx context.Context, started time.Time, output AnalysisOutput, modelErr error) string {
	ended := s.now()
	status := app.ModelCallStatusCompleted
	if modelErr != nil {
		status = app.ModelCallStatusFailed
	}
	record := app.ModelCall{ID: app.NewID("model"), Lane: "fast", Profile: "fast", Model: output.ModelVersion, Operation: string(modelcapacity.OperationEmailAnalysis), Mock: output.Mock, Status: status,
		PromptTokens: output.PromptTokens, ResponseTokens: output.ResponseTokens, TotalTokens: output.TotalTokens, LatencyMS: ended.Sub(started).Milliseconds(), Error: safeCode(modelErr), StartedAt: started, CompletedAt: &ended}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.repository.SaveModelCall(recordCtx, record); err != nil {
		slog.Warn("email model telemetry unavailable", "code", safeCode(err))
	}
	return record.ID
}

func (s *Service) recordAnalysisArtifact(ctx context.Context, directory, stage string, started time.Time, output AnalysisOutput, modelErr error) error {
	id := s.recordAnalysis(ctx, started, output, modelErr)
	if stage == "" {
		stage = "event"
	}
	metadata := map[string]any{"raw_output": output.RawOutput, "model_call_id": id, "stage": stage, "model_version": output.ModelVersion, "model_checkpoint_pinned": false, "prompt_version": analysisPromptVersion, "prompt_tokens": output.PromptTokens, "response_tokens": output.ResponseTokens, "total_tokens": output.TotalTokens, "started_at": started, "completed_at": s.now(), "error_code": safeCode(modelErr)}
	return publishJSON(ctx, s.opts.WorkspaceRoot, path.Join(directory, stage+"-execution.json"), metadata)
}

func classificationEvidence(input AnalysisInput, output AnalysisOutput) []app.EmailClassificationEvidence {
	out := []app.EmailClassificationEvidence{}
	for _, ref := range output.EvidenceRefs {
		for _, e := range input.Evidence {
			if e.Ref != ref || (!strings.HasSuffix(ref, ":body") && !strings.HasSuffix(ref, ":subject")) {
				continue
			}
			text, _ := boundedUTF8(e.Text, 500)
			text = redactVerificationTokens(input, text)
			if output.VerificationCode != "" {
				text = strings.ReplaceAll(text, output.VerificationCode, "[verification code]")
			}
			out = append(out, app.EmailClassificationEvidence{Ref: ref, Text: text})
			break
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}
