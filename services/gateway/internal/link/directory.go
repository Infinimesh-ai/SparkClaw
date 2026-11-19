// Package link is the Gateway's link entry (docs/client-link-layer-design.md
// 3.2). Connectors terminate transports, verify the device and hand the
// Gateway a Principal plus a duplex frame stream; business code registers
// handlers here and never sees Grants, sessions or the Relay.
package link

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
)

// State directory layout shared by the Gateway and the ISCP connector.
const (
	MembershipFile = "membership.json" // host's console membership (connector writes)
	DirectoryFile  = "directory.json"  // domain device directory (connector writes)
	LANFile        = "lan.json"        // LAN endpoints and TLS pin (Gateway writes)
	LANCertFile    = "lan-tls.crt"
	LANKeyFile     = "lan-tls.key"
	SocketFile     = "gateway.sock" // Gateway serves; connector dials
)

// Directory is the Gateway's local copy of the domain's device directory.
// The ISCP connector syncs it from the console; the Gateway reads it on
// every access so revocations apply without the console being reachable.
type Directory struct {
	dir string

	mu         sync.Mutex
	modTime    time.Time
	size       int64
	devices    map[string]consoleapi.DeviceRecord
	revision   int64
	membership *consoleapi.Membership
	memMod     time.Time
	onRevoke   []func(deviceID string)
	trusted    map[string]bool
}

// NewDirectory reads the shared state directory.
func NewDirectory(stateDir string) *Directory {
	return &Directory{dir: stateDir, devices: map[string]consoleapi.DeviceRecord{}, trusted: map[string]bool{}}
}

// StateDir returns the shared state directory.
func (d *Directory) StateDir() string { return d.dir }

// OnRevoke registers a callback for devices that stop being trusted.
func (d *Directory) OnRevoke(fn func(deviceID string)) {
	d.mu.Lock()
	d.onRevoke = append(d.onRevoke, fn)
	d.mu.Unlock()
}

// Refresh reloads the files when they changed and reports revocations.
func (d *Directory) Refresh() error {
	path := filepath.Join(d.dir, DirectoryFile)
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		d.reloadMembership()
		return nil
	}
	if err != nil {
		return err
	}
	d.mu.Lock()
	same := st.ModTime().Equal(d.modTime) && st.Size() == d.size
	d.mu.Unlock()
	d.reloadMembership()
	if same {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var dir consoleapi.Directory
	if err := json.Unmarshal(raw, &dir); err != nil {
		return err
	}
	next := map[string]consoleapi.DeviceRecord{}
	trusted := map[string]bool{}
	for _, rec := range dir.Devices {
		next[rec.DeviceID] = rec
		trusted[rec.DeviceID] = rec.State == consoleapi.StateTrusted
	}
	d.mu.Lock()
	var revoked []string
	for id, was := range d.trusted {
		if was && !trusted[id] {
			revoked = append(revoked, id)
		}
	}
	d.devices = next
	d.trusted = trusted
	d.revision = dir.Revision
	d.modTime = st.ModTime()
	d.size = st.Size()
	callbacks := append([]func(string){}, d.onRevoke...)
	d.mu.Unlock()
	for _, id := range revoked {
		for _, fn := range callbacks {
			fn(id)
		}
	}
	return nil
}

func (d *Directory) reloadMembership() {
	path := filepath.Join(d.dir, MembershipFile)
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	d.mu.Lock()
	same := st.ModTime().Equal(d.memMod)
	d.mu.Unlock()
	if same {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m consoleapi.Membership
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	d.mu.Lock()
	d.membership = &m
	d.memMod = st.ModTime()
	d.mu.Unlock()
}

// Lookup returns a trusted owner device.
func (d *Directory) Lookup(deviceID string) (consoleapi.DeviceRecord, bool) {
	_ = d.Refresh()
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.devices[deviceID]
	if !ok || rec.State != consoleapi.StateTrusted || rec.Role != consoleapi.RoleOwnerDevice {
		return consoleapi.DeviceRecord{}, false
	}
	return rec, true
}

// Membership returns the host's membership, if the backend has joined.
func (d *Directory) Membership() (consoleapi.Membership, bool) {
	_ = d.Refresh()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.membership == nil {
		return consoleapi.Membership{}, false
	}
	return *d.membership, true
}

// Devices returns every device record.
func (d *Directory) Devices() []consoleapi.DeviceRecord {
	_ = d.Refresh()
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]consoleapi.DeviceRecord, 0, len(d.devices))
	for _, r := range d.devices {
		out = append(out, r)
	}
	return out
}

// Watch refreshes periodically until stop closes.
func (d *Directory) Watch(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			_ = d.Refresh()
		}
	}
}

// WriteJSONAtomic writes a file atomically with mode 0600.
func WriteJSONAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
