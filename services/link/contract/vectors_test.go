package contract_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

type vectors struct {
	Version string `json:"version"`
	Frames  struct {
		Valid   []json.RawMessage `json:"valid"`
		Invalid []json.RawMessage `json:"invalid"`
	} `json:"frames"`
	Availability []struct {
		Conditions contract.Conditions `json:"conditions"`
		Profile    string              `json:"profile"`
		Includes   []string            `json:"includes"`
		Excludes   []string            `json:"excludes"`
	} `json:"availability"`
	Reverse []struct {
		Op         string              `json:"op"`
		Conditions contract.Conditions `json:"conditions"`
		Allowed    bool                `json:"allowed"`
	} `json:"reverse"`
	Credentials struct {
		Codes []struct {
			Input   string `json:"input"`
			Code    string `json:"code"`
			Display string `json:"display"`
			Error   bool   `json:"error"`
		} `json:"codes"`
		Full struct {
			Credential consoleapi.Credential `json:"credential"`
			Prefix     string                `json:"prefix"`
		} `json:"full"`
		JoinChallenge struct {
			Code      string `json:"code"`
			Challenge string `json:"challenge"`
		} `json:"join_challenge"`
	} `json:"credentials"`
	LAN struct {
		Path            string `json:"path"`
		Subprotocol     string `json:"subprotocol"`
		ChallengePrefix string `json:"challenge_prefix"`
	} `json:"lan"`
}

// The vectors are the conformance suite every client implementation (Go,
// Java/Kotlin, Swift) checks itself against (design 3.1).
func TestConformanceVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != contract.Version {
		t.Fatal("vector version")
	}
	for i, f := range v.Frames.Valid {
		if _, err := contract.Decode(f); err != nil {
			t.Errorf("valid frame %d rejected: %v", i, err)
		}
	}
	for i, f := range v.Frames.Invalid {
		if _, err := contract.Decode(f); err == nil {
			t.Errorf("invalid frame %d accepted: %s", i, f)
		}
	}
	for i, a := range v.Availability {
		got := contract.Available(a.Conditions, a.Profile)
		for _, op := range a.Includes {
			if !slices.Contains(got, op) {
				t.Errorf("availability %d: %s missing", i, op)
			}
		}
		for _, op := range a.Excludes {
			if slices.Contains(got, op) {
				t.Errorf("availability %d: %s must be closed", i, op)
			}
		}
	}
	for _, r := range v.Reverse {
		if contract.ReverseAllowed(r.Op, r.Conditions) != r.Allowed {
			t.Errorf("reverse %s %+v", r.Op, r.Conditions)
		}
	}
	for _, c := range v.Credentials.Codes {
		code, err := consoleapi.NormalizeCode(c.Input)
		if c.Error != (err != nil) || (!c.Error && code != c.Code) {
			t.Errorf("code %q -> %q %v", c.Input, code, err)
		}
		if c.Display != "" && consoleapi.FormatCode(code) != c.Display {
			t.Errorf("display %q", c.Display)
		}
	}
	text := v.Credentials.Full.Credential.Encode()
	if !strings.HasPrefix(text, v.Credentials.Full.Prefix) {
		t.Fatal("credential prefix")
	}
	back, err := consoleapi.ParseCredential(text)
	if err != nil || back.Code != v.Credentials.Full.Credential.Code || back.Console != v.Credentials.Full.Credential.Console || back.Type != "phone" {
		t.Fatalf("credential round trip %+v %v", back, err)
	}
	if !strings.HasPrefix(consoleapi.JoinChallenge(v.Credentials.JoinChallenge.Code), v.Credentials.JoinChallenge.Challenge) {
		t.Fatal("join challenge prefix")
	}
	if v.LAN.Path != contract.LANPath || v.LAN.Subprotocol != contract.LANProtocol || v.LAN.ChallengePrefix != contract.LANChallengePrefix {
		t.Fatalf("LAN constants %+v", v.LAN)
	}
}
