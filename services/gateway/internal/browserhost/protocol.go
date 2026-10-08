// Package browserhost implements the closed workbench browser host protocol. Business
// authorization stays in Workflow; this transport cannot submit arbitrary JS/CDP.
package browserhost

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"
)

const LeaseDuration = 30 * time.Second
const HeartbeatInterval = 10 * time.Second
const GrantDuration = 15 * time.Minute
const MaxMessageBytes = 128 << 10
const MaxFences = 100000

var ErrUnavailable = errors.New("workbench embedded browser host is unavailable")
var ErrFence = errors.New("workbench browser command is fenced")
var ErrUnknown = errors.New("workbench browser write outcome requires reconciliation")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)

type Identity struct {
	OwnerID        string `json:"owner_id"`
	ClientID       string `json:"client_id"`
	InstallationID string `json:"installation_id"`
}
type Scope struct {
	Identity
	ConversationID string `json:"local_conversation_id"`
	TaskID         string `json:"local_task_id"`
}

func (i Identity) valid() bool {
	return idPattern.MatchString(i.OwnerID) && idPattern.MatchString(i.ClientID) && idPattern.MatchString(i.InstallationID)
}
func (s Scope) valid() bool {
	return s.Identity.valid() && idPattern.MatchString(s.ConversationID) && idPattern.MatchString(s.TaskID)
}
func (i Identity) key() string { return i.OwnerID + "\x00" + i.ClientID + "\x00" + i.InstallationID }

type Grant struct {
	HostID    string    `json:"host_id"`
	Token     string    `json:"grant_token"`
	Digest    string    `json:"grant_digest"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Binding struct {
	Scope
	HostID              string    `json:"host_id"`
	RuntimeGeneration   string    `json:"runtime_generation"`
	ConnectionEpoch     string    `json:"connection_epoch"`
	LeaseID             string    `json:"lease_id"`
	PageID              string    `json:"page_id"`
	PageGeneration      uint64    `json:"page_generation"`
	AuthorizationDigest string    `json:"authorization_digest"`
	LeaseExpiresAt      time.Time `json:"lease_expires_at"`
}
type Command struct {
	SchemaVersion int            `json:"schema_version"`
	Type          string         `json:"type"`
	CommandID     string         `json:"command_id"`
	Binding       Binding        `json:"binding"`
	Operation     string         `json:"operation"`
	Arguments     map[string]any `json:"arguments"`
	Digest        string         `json:"digest"`
}
type Message struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	CommandID     string          `json:"command_id,omitempty"`
	Binding       *Binding        `json:"binding,omitempty"`
	Status        string          `json:"status,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
}

func opaque(prefix string) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(raw)
}
func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func commandDigest(command Command) string {
	command.Digest = ""
	raw, _ := json.Marshal(command)
	var canonical any
	_ = json.Unmarshal(raw, &canonical)
	return digest(canonical)
}
func writeOperation(operation string) bool {
	return operation == "click" || operation == "fill" || operation == "select"
}
func validateOperation(operation string, args map[string]any) error {
	allowed := map[string][]string{"acquire": {}, "release": {}, "read": {"max_chars"}, "snapshot": {}, "navigate": {"url"}, "click": {"ref", "snapshot_id"}, "fill": {"ref", "snapshot_id", "value"}, "select": {"ref", "snapshot_id", "value"}, "screenshot": {}, "wait": {"milliseconds"}}
	keys, ok := allowed[operation]
	if !ok {
		return ErrFence
	}
	for key := range args {
		found := false
		for _, allowedKey := range keys {
			found = found || key == allowedKey
		}
		if !found {
			return ErrFence
		}
	}
	raw, _ := json.Marshal(args)
	if len(raw) > 24<<10 {
		return ErrFence
	}
	switch operation {
	case "navigate":
		raw, ok := args["url"].(string)
		parsed, err := url.Parse(raw)
		if !ok || err != nil || len(raw) > 16384 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return ErrFence
		}
	case "click", "fill", "select":
		if ref, ok := args["ref"].(string); !ok || len(ref) > 256 || ref == "" {
			return ErrFence
		}
		if snap, ok := args["snapshot_id"].(string); !ok || len(snap) > 160 || snap == "" {
			return ErrFence
		}
		if operation != "click" {
			if value, ok := args["value"].(string); !ok || len(value) > 16384 {
				return ErrFence
			}
		}
	case "read":
		if value, ok := args["max_chars"]; ok {
			n, ok := value.(float64)
			if !ok {
				if integer, yes := value.(int); yes {
					n = float64(integer)
					ok = true
				}
			}
			if !ok || n < 1 || n > 65536 || n != float64(int(n)) {
				return ErrFence
			}
		}
	case "wait":
		n, ok := args["milliseconds"].(int)
		if !ok {
			if v, yes := args["milliseconds"].(float64); yes && v == float64(int(v)) {
				n = int(v)
				ok = true
			}
		}
		if !ok || n < 0 || n > 5000 {
			return ErrFence
		}
	}
	return nil
}
