package semanticrouting

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed default_calibration.json
var defaultCalibrationJSON []byte

type Calibration struct {
	Revision              string  `json:"revision"`
	Alpha                 float64 `json:"alpha"`
	EmbeddingExampleTopM  int     `json:"embedding_example_top_m"`
	EmbeddingNegativeCost float64 `json:"embedding_negative_cost"`
	EmbeddingTimeoutMS    int     `json:"embedding_timeout_ms"`
	TreeTimeoutMS         int     `json:"tree_timeout_ms"`
	RoutingTimeoutMS      int     `json:"routing_timeout_ms"`
	ClearMinimum          float64 `json:"clear_minimum"`
	ClearMargin           float64 `json:"clear_margin"`
	AmbiguousMinimum      float64 `json:"ambiguous_minimum"`
	MutationMinimum       float64 `json:"mutation_minimum"`
	MutationMargin        float64 `json:"mutation_margin"`
	DegradedMinimum       float64 `json:"degraded_minimum"`
	DegradedMargin        float64 `json:"degraded_margin"`
}

func DefaultCalibration() Calibration {
	var calibration Calibration
	if err := json.Unmarshal(defaultCalibrationJSON, &calibration); err != nil {
		panic("invalid embedded semantic routing calibration: " + err.Error())
	}
	if err := calibration.Validate(); err != nil {
		panic("invalid embedded semantic routing calibration: " + err.Error())
	}
	return calibration
}

func (c Calibration) Validate() error {
	if c.Revision == "" {
		return errors.New("calibration revision is required")
	}
	for name, value := range map[string]float64{
		"alpha": c.Alpha, "embedding_negative_cost": c.EmbeddingNegativeCost,
		"clear_minimum": c.ClearMinimum,
		"clear_margin":  c.ClearMargin, "ambiguous_minimum": c.AmbiguousMinimum,
		"mutation_minimum": c.MutationMinimum, "mutation_margin": c.MutationMargin,
		"degraded_minimum": c.DegradedMinimum, "degraded_margin": c.DegradedMargin,
	} {
		if value < 0 || value > 1 {
			return fmt.Errorf("calibration %s is outside [0,1]", name)
		}
	}
	if c.EmbeddingExampleTopM < 1 || c.EmbeddingTimeoutMS < 1 || c.TreeTimeoutMS < 1 || c.RoutingTimeoutMS < 1 {
		return errors.New("calibration ranking bounds are invalid")
	}
	if c.ClearMinimum < c.AmbiguousMinimum || c.MutationMinimum < c.ClearMinimum || c.DegradedMinimum < c.ClearMinimum {
		return errors.New("calibration decision thresholds are not monotonic")
	}
	return nil
}
