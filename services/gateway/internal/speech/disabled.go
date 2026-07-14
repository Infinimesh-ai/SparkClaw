package speech

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type DisabledTranscriber struct {
	status Status
}

func NewDisabled(cfg config.SpeechConfig) *DisabledTranscriber {
	status := baseStatus(cfg)
	status.Enabled = false
	status.Ready = false
	status.State = StateDisabled
	status.Backend = "disabled"
	status.Reason = "speech transcription is disabled"
	return &DisabledTranscriber{status: status}
}

func (d *DisabledTranscriber) Status(context.Context) Status {
	return d.status
}

func (d *DisabledTranscriber) Transcribe(context.Context, Request) (Result, error) {
	return Result{}, NewError(CodeDisabled, "speech transcription is disabled", false, nil)
}

func (d *DisabledTranscriber) Close() error {
	return nil
}
