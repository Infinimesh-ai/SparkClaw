package emailmanagement

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Seed source-pipeline tests through the same incremental collection worker as
// production, so parse/model tests cannot resurrect the retired capture lane.
func seedFixtureCollection(ctx context.Context, s *Service) error {
	if err := s.plan(ctx); err != nil {
		return err
	}
	worked, err := s.workOne(ctx, []string{app.EmailJobDiscover})
	if err != nil {
		return err
	}
	if !worked {
		return errors.New("fixture collection was not claimed")
	}
	return nil
}
