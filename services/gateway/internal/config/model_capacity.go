package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

var legacyModelCapacityEnvironment = []string{
	"SPARKCLAW_FAST_CONTEXT_TOKENS",
	"SPARKCLAW_FAST_MAX_INPUT_TOKENS",
	"SPARKCLAW_FAST_MAX_TOKENS",
	"SPARKCLAW_DEEP_CONTEXT_TOKENS",
	"SPARKCLAW_DEEP_MAX_INPUT_TOKENS",
	"SPARKCLAW_DEEP_MAX_TOKENS",
	"SPARKCLAW_EMBEDDING_CONTEXT_TOKENS",
	"SPARKCLAW_GUARD_CONTEXT_TOKENS",
	"SPARKCLAW_GUARD_MAX_TOKENS",
	"SPARKCLAW_OCR_CONTEXT_TOKENS",
	"SPARKCLAW_OCR_MAX_TOKENS",
}

type modelCapacityCatalog struct {
	Profiles map[string]modelCapacityProfile `json:"profiles"`
}

type modelCapacityProfile struct {
	Description    string                       `json:"description"`
	Executable     bool                         `json:"executable"`
	Mock           bool                         `json:"mock"`
	PhysicalModels map[string]physicalModelSpec `json:"physical_models"`
	Lanes          map[string]capacityLaneSpec  `json:"lanes"`
}

type physicalModelSpec struct {
	ContextTokens int `json:"context_tokens"`
}

type capacityLaneSpec struct {
	PhysicalModel string         `json:"physical_model"`
	OutputBudgets map[string]int `json:"output_budgets"`
}

func defaultModelCapacityCatalogPath() string {
	if path := strings.TrimSpace(os.Getenv("SPARKCLAW_MODEL_CAPACITY_CATALOG")); path != "" {
		return path
	}
	if _, source, _, ok := runtime.Caller(0); ok {
		return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "configs", "model.profiles.json"))
	}
	return filepath.Join("configs", "model.profiles.json")
}

func rejectLegacyModelCapacity(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var model map[string]json.RawMessage
	if rawModel := root["model"]; len(rawModel) > 0 {
		if err := json.Unmarshal(rawModel, &model); err != nil {
			return fmt.Errorf("decode model configuration: %w", err)
		}
	}
	for _, lane := range []string{"fast", "deep", "embedding", "guard"} {
		var profile map[string]json.RawMessage
		if rawProfile := model[lane]; len(rawProfile) > 0 {
			if err := json.Unmarshal(rawProfile, &profile); err != nil {
				return fmt.Errorf("decode model.%s configuration: %w", lane, err)
			}
		}
		for _, field := range []string{"context_tokens", "max_input_tokens", "max_tokens", "output_budgets"} {
			if _, exists := profile[field]; exists {
				return fmt.Errorf("model.%s.%s is not allowed; select capacity through model.capacity_profile", lane, field)
			}
		}
	}
	var adapters map[string]json.RawMessage
	if rawAdapters := root["adapters"]; len(rawAdapters) > 0 {
		if err := json.Unmarshal(rawAdapters, &adapters); err != nil {
			return fmt.Errorf("decode adapters configuration: %w", err)
		}
	}
	var ocr map[string]json.RawMessage
	if rawOCR := adapters["documentOCR"]; len(rawOCR) > 0 {
		if err := json.Unmarshal(rawOCR, &ocr); err != nil {
			return fmt.Errorf("decode adapters.documentOCR configuration: %w", err)
		}
	}
	if _, exists := ocr["maxTokens"]; exists {
		return errors.New("adapters.documentOCR.maxTokens is not allowed; select capacity through model.capacity_profile")
	}
	return nil
}

func rejectLegacyModelCapacityEnv() error {
	for _, key := range legacyModelCapacityEnvironment {
		if _, exists := os.LookupEnv(key); exists {
			return fmt.Errorf("%s is not supported; model capacity comes from the selected capacity profile", key)
		}
	}
	return nil
}

func applySelectedModelCapacity(cfg *Config, configPath string) error {
	if cfg == nil {
		return errors.New("model capacity target config is nil")
	}
	profileID := strings.TrimSpace(cfg.Model.CapacityProfile)
	if profileID == "" {
		return errors.New("model.capacity_profile is required")
	}
	catalogPath := strings.TrimSpace(cfg.Model.CapacityCatalog)
	if catalogPath == "" {
		return errors.New("model.capacity_catalog is required")
	}
	if !filepath.IsAbs(catalogPath) && strings.TrimSpace(configPath) != "" {
		catalogPath = filepath.Join(filepath.Dir(configPath), catalogPath)
	}
	catalogPath, err := filepath.Abs(catalogPath)
	if err != nil {
		return fmt.Errorf("resolve model capacity catalog: %w", err)
	}
	catalog, err := readModelCapacityCatalog(catalogPath)
	if err != nil {
		return err
	}
	profile, exists := catalog.Profiles[profileID]
	if !exists {
		return fmt.Errorf("model capacity profile %q is not present in %s", profileID, catalogPath)
	}
	if err := validateCapacityProfile(profileID, profile); err != nil {
		return err
	}
	cfg.Model.CapacityProfile = profileID
	cfg.Model.CapacityCatalog = catalogPath
	cfg.Model.Mock = profile.Mock
	resolved := map[modelcapacity.Lane]ModelProfile{
		modelcapacity.LaneFast:      cfg.Model.Fast,
		modelcapacity.LaneDeep:      cfg.Model.Deep,
		modelcapacity.LaneEmbedding: cfg.Model.Embedding,
		modelcapacity.LaneGuard:     cfg.Model.Guard,
	}
	for lane, current := range resolved {
		laneSpec := profile.Lanes[string(lane)]
		physical := profile.PhysicalModels[laneSpec.PhysicalModel]
		current.CapacityPhysicalModel = laneSpec.PhysicalModel
		current.ContextTokens = physical.ContextTokens
		current.OutputBudgets = typedOutputBudgets(laneSpec.OutputBudgets)
		switch lane {
		case modelcapacity.LaneFast:
			cfg.Model.Fast = current
		case modelcapacity.LaneDeep:
			cfg.Model.Deep = current
		case modelcapacity.LaneEmbedding:
			cfg.Model.Embedding = current
		case modelcapacity.LaneGuard:
			cfg.Model.Guard = current
		}
	}
	ocrLane := profile.Lanes[string(modelcapacity.LaneOCR)]
	cfg.Adapters.DocumentOCR.ContextTokens = profile.PhysicalModels[ocrLane.PhysicalModel].ContextTokens
	cfg.Adapters.DocumentOCR.MaxTokens = ocrLane.OutputBudgets[string(modelcapacity.OutputOCRDocument)]
	return nil
}

func typedOutputBudgets(values map[string]int) map[modelcapacity.OutputBudgetClass]int {
	out := make(map[modelcapacity.OutputBudgetClass]int, len(values))
	for class, value := range values {
		out[modelcapacity.OutputBudgetClass(class)] = value
	}
	return out
}

func readModelCapacityCatalog(path string) (modelCapacityCatalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return modelCapacityCatalog{}, fmt.Errorf("read model capacity catalog %s: %w", path, err)
	}
	var catalog modelCapacityCatalog
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&catalog); err != nil {
		return modelCapacityCatalog{}, fmt.Errorf("decode model capacity catalog %s: %w", path, err)
	}
	if len(catalog.Profiles) == 0 {
		return modelCapacityCatalog{}, errors.New("model capacity catalog has no profiles")
	}
	return catalog, nil
}

// ValidateModelCapacityCatalog validates every executable profile. Runtime
// startup calls the narrower selected-profile path; CI and catalog tests call
// this function to prevent a broken inactive deployment from being promoted.
func ValidateModelCapacityCatalog(path string) error {
	catalog, err := readModelCapacityCatalog(path)
	if err != nil {
		return err
	}
	executable := 0
	for profileID, profile := range catalog.Profiles {
		if !profile.Executable {
			continue
		}
		executable++
		if err := validateCapacityProfile(profileID, profile); err != nil {
			return err
		}
	}
	if executable == 0 {
		return errors.New("model capacity catalog has no executable profiles")
	}
	return nil
}

func validateCapacityProfile(profileID string, profile modelCapacityProfile) error {
	if !profile.Executable {
		return fmt.Errorf("model capacity profile %q is not executable", profileID)
	}
	if len(profile.PhysicalModels) == 0 {
		return fmt.Errorf("model capacity profile %q has no physical_models", profileID)
	}
	for physicalID, physical := range profile.PhysicalModels {
		if strings.TrimSpace(physicalID) == "" {
			return fmt.Errorf("model capacity profile %q has an empty physical model ID", profileID)
		}
		if physical.ContextTokens <= 0 {
			return fmt.Errorf("model capacity profile %q physical model %q context_tokens must be positive", profileID, physicalID)
		}
	}
	for laneName := range profile.Lanes {
		if !modelcapacity.IsKnownLane(laneName) {
			return fmt.Errorf("model capacity profile %q has unknown lane %q", profileID, laneName)
		}
	}
	for _, lane := range modelcapacity.Lanes() {
		laneSpec, exists := profile.Lanes[string(lane)]
		if !exists {
			return fmt.Errorf("model capacity profile %q is missing lane %q", profileID, lane)
		}
		physical, exists := profile.PhysicalModels[strings.TrimSpace(laneSpec.PhysicalModel)]
		if !exists {
			return fmt.Errorf("model capacity profile %q lane %q references missing physical model %q", profileID, lane, laneSpec.PhysicalModel)
		}
		for class := range laneSpec.OutputBudgets {
			if !modelcapacity.IsKnownClass(class) {
				return fmt.Errorf("model capacity profile %q lane %q has unknown output class %q", profileID, lane, class)
			}
		}
		required := modelcapacity.RequiredClasses(lane)
		for _, class := range required {
			budget, exists := laneSpec.OutputBudgets[string(class)]
			if !exists {
				return fmt.Errorf("model capacity profile %q lane %q is missing output class %q", profileID, lane, class)
			}
			if budget <= 0 {
				return fmt.Errorf("model capacity profile %q lane %q output class %q must be positive", profileID, lane, class)
			}
			if budget >= physical.ContextTokens {
				return fmt.Errorf("model capacity profile %q lane %q output class %q must be less than context_tokens", profileID, lane, class)
			}
		}
		for class := range laneSpec.OutputBudgets {
			if !slices.Contains(required, modelcapacity.OutputBudgetClass(class)) {
				return fmt.Errorf("model capacity profile %q lane %q configures unused output class %q", profileID, lane, class)
			}
		}
	}
	return nil
}
