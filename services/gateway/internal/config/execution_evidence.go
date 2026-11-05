package config

import (
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

// ExecutionEvidenceConfig bounds diagnostic capture independently of the
// business result. Reaching a quota leaves an explicit gap, never a retry of
// the user's operation. Existing evidence is not automatically deleted.
type ExecutionEvidenceConfig struct {
	Enabled                 bool  `json:"enabled"`
	MaxPayloadBytes         int64 `json:"max_payload_bytes"`
	MaxRunBytes             int64 `json:"max_run_bytes"`
	MaxTotalBytes           int64 `json:"max_total_bytes"`
	MaxExportBytes          int64 `json:"max_export_bytes"`
	MaxEventBytes           int64 `json:"max_event_bytes"`
	MaxEvents               int   `json:"max_events"`
	OperationTimeoutSeconds int   `json:"operation_timeout_seconds"`
	ExportTimeoutSeconds    int   `json:"export_timeout_seconds"`
}

func DefaultExecutionEvidenceConfig() ExecutionEvidenceConfig {
	o := executionevidence.DefaultOptions()
	return ExecutionEvidenceConfig{
		MaxPayloadBytes: o.MaxPayloadBytes, MaxRunBytes: o.MaxRunBytes,
		MaxTotalBytes: o.MaxTotalBytes, MaxExportBytes: o.MaxExportBytes,
		MaxEventBytes: o.MaxEventBytes, MaxEvents: o.MaxEvents,
		OperationTimeoutSeconds: int(o.OperationTimeout / time.Second),
		ExportTimeoutSeconds:    int(o.ExportTimeout / time.Second),
	}
}

func (c ExecutionEvidenceConfig) Options() executionevidence.Options {
	return executionevidence.Options{
		MaxPayloadBytes: c.MaxPayloadBytes, MaxRunBytes: c.MaxRunBytes,
		MaxTotalBytes: c.MaxTotalBytes, MaxExportBytes: c.MaxExportBytes,
		MaxEventBytes: c.MaxEventBytes, MaxEvents: c.MaxEvents,
		OperationTimeout: time.Duration(c.OperationTimeoutSeconds) * time.Second,
		ExportTimeout:    time.Duration(c.ExportTimeoutSeconds) * time.Second,
	}
}

func normalizeExecutionEvidenceConfig(c *ExecutionEvidenceConfig) error {
	d := DefaultExecutionEvidenceConfig()
	for _, field := range []struct {
		name     string
		value    *int64
		fallback int64
	}{
		{"max_payload_bytes", &c.MaxPayloadBytes, d.MaxPayloadBytes},
		{"max_run_bytes", &c.MaxRunBytes, d.MaxRunBytes},
		{"max_total_bytes", &c.MaxTotalBytes, d.MaxTotalBytes},
		{"max_export_bytes", &c.MaxExportBytes, d.MaxExportBytes},
		{"max_event_bytes", &c.MaxEventBytes, d.MaxEventBytes},
	} {
		if *field.value == 0 {
			*field.value = field.fallback
		}
		if *field.value < 1024 || *field.value > 1<<40 {
			return fmt.Errorf("storage.execution_evidence.%s must be between 1024 and 1099511627776", field.name)
		}
	}
	if c.MaxEvents == 0 {
		c.MaxEvents = d.MaxEvents
	}
	if c.OperationTimeoutSeconds == 0 {
		c.OperationTimeoutSeconds = d.OperationTimeoutSeconds
	}
	if c.ExportTimeoutSeconds == 0 {
		c.ExportTimeoutSeconds = d.ExportTimeoutSeconds
	}
	if c.MaxEvents < 1 || c.MaxEvents > 1_000_000 || c.OperationTimeoutSeconds < 1 || c.OperationTimeoutSeconds > 300 || c.ExportTimeoutSeconds < 1 || c.ExportTimeoutSeconds > 3600 {
		return fmt.Errorf("storage.execution_evidence event/time limits are out of range")
	}
	if c.MaxPayloadBytes > c.MaxRunBytes || c.MaxRunBytes > c.MaxTotalBytes || c.MaxEventBytes > c.MaxRunBytes {
		return fmt.Errorf("storage.execution_evidence requires payload/event <= run <= total byte limits")
	}
	return nil
}
