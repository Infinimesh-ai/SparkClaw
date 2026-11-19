package devconsole

import (
	"net/http"
	"sort"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
)

// Actors in the audit record.
const (
	actorAccount = "account"
	actorDevice  = "device"
)

func (s *Server) auditLocked(domainID, action, decision, actorType, actorID, resourceType, resourceID, reason string) {
	s.st.audit(&auditRec{
		AuditID:      "aud_" + randomID(10),
		DomainID:     domainID,
		CreatedAt:    s.now(),
		Action:       action,
		Decision:     decision,
		ActorType:    actorType,
		ActorID:      actorID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Reason:       reason,
	})
}

// touchLocked records a directory change of dev.
func (s *Server) touchLocked(dev *deviceRec, now time.Time) {
	s.st.Revision++
	dev.DirectoryRevision = s.st.Revision
	dev.UpdatedAt = now
}

// record returns the device as devices and the backend see it.
func (dev *deviceRec) record() consoleapi.DeviceRecord {
	r := dev.DeviceRecord
	r.GrantID, r.GrantExpiresAt = "", nil
	if dev.Grant != nil {
		r.GrantID = dev.Grant.GrantID
		exp := dev.Grant.ExpiresAt
		r.GrantExpiresAt = &exp
	}
	return r
}

// hostLocked returns the domain's domain_host, or nil.
func (s *Server) hostLocked(d *domainRec) *deviceRec {
	if d == nil || d.HostDeviceID == "" {
		return nil
	}
	h := s.st.device(d.HostDeviceID)
	if h == nil || h.State != consoleapi.StateTrusted {
		return nil
	}
	return h
}

func (s *Server) hostViewLocked(d *domainRec) *consoleapi.Host {
	h := s.hostLocked(d)
	if h == nil {
		return nil
	}
	return &consoleapi.Host{DeviceID: h.DeviceID, Identity: h.Identity, Name: h.Name, LAN: d.HostLAN}
}

// grantUsable reports whether dev's stored Grant can still be handed out
// unchanged: it names the current host and epoch and is not yet in its
// renewal window.
func grantUsable(dev *deviceRec, host *deviceRec, now time.Time) bool {
	g := dev.Grant
	if g == nil || host == nil {
		return false
	}
	if g.Audience != host.DeviceID || g.RevocationEpoch != dev.RevocationEpoch || g.SubjectDeviceID != dev.DeviceID {
		return false
	}
	if now.Before(g.NotBefore) || !now.Before(g.ExpiresAt) {
		return false
	}
	return g.ExpiresAt.Sub(now) > consoleapi.RenewBefore(*g)
}

// grantActive reports whether dev holds a currently valid Grant for host.
func grantActive(dev *deviceRec, host *deviceRec, now time.Time) bool {
	g := dev.Grant
	return g != nil && host != nil && g.Audience == host.DeviceID && g.RevocationEpoch == dev.RevocationEpoch && !now.Before(g.NotBefore) && now.Before(g.ExpiresAt)
}

// signGrantLocked issues a new Grant between dev and host and stores it.
func (s *Server) signGrantLocked(dev, host *deviceRec, now time.Time) (*trust.Grant, error) {
	now = now.Truncate(time.Second)
	g, err := trust.SignGrant(s.provider, s.signer, trust.Grant{
		GrantID:                "grant-" + randomID(12),
		SubjectDeviceID:        dev.DeviceID,
		Audience:               host.DeviceID,
		ConfirmationThumbprint: dev.Identity.PublicKey.KID,
		Permissions:            []string{consoleapi.Permission},
		RelayConstraints:       []string{s.endpoint.RelayID},
		NotBefore:              now.Add(-time.Second),
		ExpiresAt:              now.Add(consoleapi.GrantTTL),
		RevocationEpoch:        dev.RevocationEpoch,
	})
	if err != nil {
		return nil, err
	}
	dev.Grant = &g
	s.touchLocked(dev, now)
	return &g, nil
}

// revokeLocked marks dev revoked. It returns whether anything changed.
func (s *Server) revokeLocked(dev *deviceRec, now time.Time) bool {
	if dev.State == consoleapi.StateRevoked {
		return false
	}
	dev.State = consoleapi.StateRevoked
	t := now
	dev.RevokedAt = &t
	dev.RevocationEpoch++
	dev.Grant = nil
	if d := s.st.domain(dev.DomainID); d != nil && d.HostDeviceID == dev.DeviceID {
		d.HostDeviceID = ""
		d.HostLAN = consoleapi.LAN{}
		d.UpdatedAt = now
	}
	s.touchLocked(dev, now)
	return true
}

// revoke revokes a device in the state and then at the Relay.
func (s *Server) revoke(domainID, deviceID, action, actorType, actorID string) (consoleapi.DeviceRecord, *apiError) {
	s.mu.Lock()
	d := s.st.domain(domainID)
	dev := s.st.device(deviceID)
	if d == nil || dev == nil || dev.DomainID != domainID {
		s.mu.Unlock()
		return consoleapi.DeviceRecord{}, errf(http.StatusNotFound, codeNotFound, "device not found")
	}
	changed := s.revokeLocked(dev, s.now())
	rec := dev.record()
	if changed {
		s.auditLocked(domainID, action, resultAllow, actorType, actorID, "device", deviceID, "")
		if err := s.saveLocked(); err != nil {
			s.mu.Unlock()
			return rec, errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
		}
	}
	s.mu.Unlock()
	if changed {
		if err := s.relayRevoke(deviceID); err != nil {
			s.logger.Printf("dev-console: relay revoke %s: %v", deviceID, err)
			s.mu.Lock()
			s.auditLocked(domainID, "relay.revoke", resultDeny, actorType, actorID, "device", deviceID, err.Error())
			_ = s.saveLocked()
			s.mu.Unlock()
		}
	}
	return rec, nil
}

// reauthorizeRenewal resets dev's renewal authorization to now+limit.
func (s *Server) reauthorizeRenewal(domainID, deviceID, actorID string) (consoleapi.DeviceRecord, *apiError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.st.device(deviceID)
	if dev == nil || dev.DomainID != domainID {
		return consoleapi.DeviceRecord{}, errf(http.StatusNotFound, codeNotFound, "device not found")
	}
	if dev.State != consoleapi.StateTrusted {
		return consoleapi.DeviceRecord{}, errf(http.StatusForbidden, consoleapi.ErrCodeRevoked, "device is revoked")
	}
	if dev.Role != consoleapi.RoleOwnerDevice {
		return consoleapi.DeviceRecord{}, errf(http.StatusBadRequest, codeNotOwnerDevice, "only owner devices hold Grants")
	}
	now := s.now()
	exp := now.Add(consoleapi.RenewalLimit)
	dev.RenewalExpiresAt = &exp
	s.touchLocked(dev, now)
	s.auditLocked(domainID, "device.renewal_authorize", resultAllow, actorAccount, actorID, "device", deviceID, "")
	if err := s.saveLocked(); err != nil {
		return dev.record(), errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
	}
	return dev.record(), nil
}

func sortDevices(list []*deviceRec) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
}
