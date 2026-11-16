package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var mcdEatTypePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func normalizeMCDSelectionConfig(cfg *MCDSelectionConfig) error {
	if cfg.QualifiedEatTypes == nil {
		cfg.QualifiedEatTypes = slices.Clone(Default().MCDSelection.QualifiedEatTypes)
	}
	if len(cfg.QualifiedEatTypes) == 0 {
		return fmt.Errorf("mcd_selection.qualified_eat_types must contain at least one qualified pickup method")
	}
	seen := make(map[string]bool, len(cfg.QualifiedEatTypes))
	for i, value := range cfg.QualifiedEatTypes {
		value = strings.TrimSpace(value)
		if !mcdEatTypePattern.MatchString(value) {
			return fmt.Errorf("mcd_selection.qualified_eat_types[%d] must be a lowercase pickup method identifier", i)
		}
		if seen[value] {
			return fmt.Errorf("mcd_selection.qualified_eat_types contains duplicate pickup method %q", value)
		}
		seen[value] = true
		cfg.QualifiedEatTypes[i] = value
	}
	return nil
}
