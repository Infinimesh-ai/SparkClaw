// Package iscpauth defines the local issuer's signed authorization metadata.
// It does not change ISCP Grant or Relay envelope formats.
package iscpauth

import (
	"errors"
	"strconv"
	"time"
)

const (
	UntilRevoked = "until_revoked"
	Bounded      = "bounded"
	Active       = "active"
	Revoked      = "revoked"
	Expired      = "expired"
	StatusPath   = "/v1/authorization-status"
)

// Policy is carried only inside an issuer-signed descriptor. The caller must
// authenticate its signature, peer scope and freshness before using it.
type Policy struct {
	Version   int
	Lifetime  string
	Revision  uint64
	State     string
	ExpiresAt time.Time
}

func Parse(metadata map[string]string) (Policy, error) {
	p := Policy{Version: 1, Lifetime: Bounded, State: Active}
	version := metadata["authorization_version"]
	if version != "" {
		if version != "2" {
			return p, errors.New("unsupported authorization policy version")
		}
		p.Version = 2
		p.Lifetime, p.State = metadata["authorization_lifetime"], metadata["authorization_state"]
		var err error
		p.Revision, err = strconv.ParseUint(metadata["authorization_revision"], 10, 64)
		if err != nil || p.Revision == 0 || strconv.FormatUint(p.Revision, 10) != metadata["authorization_revision"] {
			return p, errors.New("invalid authorization revision")
		}
		if p.State != Active && p.State != Revoked && p.State != Expired {
			return p, errors.New("invalid authorization state")
		}
		if p.Lifetime == UntilRevoked {
			if _, found := metadata["authorization_expires_at"]; found || p.State == Expired {
				return p, errors.New("permanent authorization cannot expire")
			}
			return p, nil
		}
		if p.Lifetime != Bounded {
			return p, errors.New("invalid authorization lifetime")
		}
	} else if metadata["authorization_lifetime"] != "" || metadata["authorization_state"] != "" || metadata["authorization_revision"] != "" {
		return p, errors.New("authorization policy requires a version")
	}
	var err error
	p.ExpiresAt, err = time.Parse(time.RFC3339Nano, metadata["authorization_expires_at"])
	if err != nil || p.ExpiresAt.IsZero() {
		return p, errors.New("invalid authorization expiry")
	}
	return p, nil
}

func (p Policy) AddTo(metadata map[string]string) {
	if p.Version == 2 {
		metadata["authorization_version"] = "2"
		metadata["authorization_lifetime"] = p.Lifetime
		metadata["authorization_revision"] = strconv.FormatUint(p.Revision, 10)
		metadata["authorization_state"] = p.State
	}
	if p.Lifetime == Bounded {
		metadata["authorization_expires_at"] = p.ExpiresAt.Format(time.RFC3339Nano)
	}
}
