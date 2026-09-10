package emailmanagement

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Missing records on older installations use the first successful enable time.
// Store binds that fallback once per owner, so later mailboxes do not reset it.
func (s *Service) deploymentBoundary() (time.Time, error) {
	file, err := os.Open(filepath.Join(s.opts.WorkspaceRoot, ".sparkclaw-deployment.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s.now(), nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return time.Time{}, err
	}
	var record struct {
		StartedAt time.Time `json:"started_at"`
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &record) != nil || record.StartedAt.IsZero() || record.StartedAt.After(s.now()) {
		return time.Time{}, errors.New("email_deployment_boundary_invalid")
	}
	return record.StartedAt.UTC(), nil
}
