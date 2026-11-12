package browsercontrol

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
)

const ClientControllerProtocol = "browser.controller.v1"
const ClientControllerProfile = "client_embedded"
const controllerWireLimit = 63488
const controllerChunkLimit = 32 << 10
const maxJSSafeInteger = 1<<53 - 1

//go:embed controller_operations.json
var controllerOperationsJSON []byte

type controllerOperation struct {
	Mutation bool     `json:"mutation"`
	Client   bool     `json:"client"`
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

var controllerOperations = func() map[string]controllerOperation {
	var result map[string]controllerOperation
	if err := json.Unmarshal(controllerOperationsJSON, &result); err != nil {
		panic(err)
	}
	return result
}()
var controllerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var controllerIncarnation = regexp.MustCompile(`^[a-f0-9]{32}$`)
var controllerHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ClientControllerOperation(operation string, arguments map[string]any) (bool, error) {
	spec, ok := controllerOperations[operation]
	if !ok || !spec.Client || arguments == nil {
		return false, newError(CodeOperationUnavailable, false, nil)
	}
	keys := map[string]bool{}
	for _, key := range spec.Required {
		if _, ok := arguments[key]; !ok {
			return false, newError(CodeInvalidRequest, false, nil)
		}
		keys[key] = true
	}
	for _, key := range spec.Optional {
		keys[key] = true
	}
	for key := range arguments {
		if !keys[key] {
			return false, newError(CodeInvalidRequest, false, nil)
		}
	}
	raw, err := json.Marshal(arguments)
	if err != nil || len(raw) > 32<<10 {
		return false, newError(CodeInvalidRequest, false, err)
	}
	if operation == "page.wait" {
		if value, ok := arguments["duration_ms"]; ok {
			encoded, _ := json.Marshal(value)
			var duration int64
			if json.Unmarshal(encoded, &duration) != nil || duration < 0 || duration > 15000 {
				return false, newError(CodeInvalidRequest, false, nil)
			}
		}
	}
	if operation == "page.observe" {
		limits := map[string]int64{}
		bounded := func(key string, minimum, maximum int64, fallback int64) bool {
			value, found := arguments[key]
			if !found {
				return fallback >= minimum && fallback <= maximum
			}
			encoded, _ := json.Marshal(value)
			var number int64
			valid := json.Unmarshal(encoded, &number) == nil && number >= minimum && number <= maximum
			limits[key] = number
			return valid
		}
		if !bounded("timeout_ms", 1, 20000, 0) || !bounded("quiet_period_ms", 100, 10000, 0) ||
			!bounded("poll_interval_ms", 25, 10000, 0) || !bounded("minimum_samples", 2, 401, 2) ||
			limits["poll_interval_ms"] > limits["quiet_period_ms"] {
			return false, newError(CodeInvalidRequest, false, nil)
		}
		var signals []string
		raw, _ := json.Marshal(arguments["signals"])
		if json.Unmarshal(raw, &signals) != nil || len(signals) != 4 {
			return false, newError(CodeInvalidRequest, false, nil)
		}
		seen := map[string]bool{}
		for _, s := range signals {
			seen[s] = true
		}
		for _, s := range []string{"url", "ready_state", "rendered_content", "frames"} {
			if !seen[s] {
				return false, newError(CodeInvalidRequest, false, nil)
			}
		}
		if value, ok := arguments["before_digest"]; ok {
			s, ok := value.(string)
			if !ok || !controllerHash.MatchString(s) {
				return false, newError(CodeInvalidRequest, false, nil)
			}
		}
		if value, ok := arguments["allow_no_change"]; ok {
			if _, ok := value.(bool); !ok {
				return false, newError(CodeInvalidRequest, false, nil)
			}
		}
	}
	return spec.Mutation, nil
}

// Decode through JSON values so struct field order never enters the digest.
// Go's encoder sorts UTF-8 map keys and escapes HTML/U+2028/U+2029. Request
// numbers must be JS-safe integers; the desktop uses the same closed grammar.
func canonicalControllerJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	var validate func(any) bool
	validate = func(value any) bool {
		switch item := value.(type) {
		case float64:
			return math.Trunc(item) == item && math.Abs(item) <= maxJSSafeInteger
		case []any:
			for _, nested := range item {
				if !validate(nested) {
					return false
				}
			}
		case map[string]any:
			for _, nested := range item {
				if !validate(nested) {
					return false
				}
			}
		}
		return true
	}
	if !validate(decoded) {
		return nil, errors.New("Controller digest requires safe integers")
	}
	return json.Marshal(decoded)
}
func ControllerRequestDigest(operation string, body map[string]any) (string, error) {
	copy := make(map[string]any, len(body))
	for key, value := range body {
		copy[key] = value
	}
	copy["digest"] = ""
	raw, err := canonicalControllerJSON(map[string]any{"operation": operation, "body": copy})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func strictControllerJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Controller response has trailing data")
	}
	return nil
}
func sameControllerJSON(left, right json.RawMessage) bool {
	a, err := canonicalControllerJSON(left)
	if err != nil {
		return false
	}
	b, err := canonicalControllerJSON(right)
	return err == nil && bytes.Equal(a, b)
}
