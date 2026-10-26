package emailmanagement

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"regexp"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

var legacySendDiagnosticID = regexp.MustCompile(`^email_send_(?:[0-9a-f]{16}|fallback)$`)

// Caller-supplied draft IDs may be descriptive. Hash them with their owner;
// operation IDs are emitted only in the product's generated identity formats.
func recordSendDiagnostic(owner string, d store.EmailDraft, stage, mode, outcome string, cause error) {
	digest := sha256.Sum256([]byte(owner + "\x00" + d.ID))
	invocation := ""
	if d.Snapshot != nil {
		id := d.Snapshot.InvocationID
		if _, _, ok := app.OperationEpoch(id); ok || legacySendDiagnosticID.MatchString(id) {
			invocation = id
		}
	}
	detail := emailautomation.FailureDetails(cause)
	if code := diagnosticStoreCode(cause); code != "" {
		detail = emailautomation.FailureDiagnostic{Code: code, Stage: stage, Source: "store"}
	}
	slog.Info("email send boundary", "schema_version", 1, "draft_scope_sha256", hex.EncodeToString(digest[:]), "operation_id", invocation,
		"stage", stage, "identity_mode", mode, "business_outcome", outcome,
		"business_persisted", (stage == "persist_result" && cause == nil) || stage == "ack", "evidence", "boundary_only",
		"cause_code", detail.Code, "cause_stage", detail.Stage, "cause_source", detail.Source)
}
