package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// Domain objects are the large inputs and outputs of business-domain
// methods (mail attachments, recordings, large results). They travel with
// the link object operations and are passed to domain methods by
// reference. Unlike conversation attachments they never enter a
// workspace: each belongs to the device that uploaded or received it and
// expires after a day.
const (
	linkDomainObjectExpiry   = 24 * time.Hour
	linkDomainObjectMaxBytes = 64 << 20
	linkDomainObjectTotal    = 512 << 20
	// Results up to this size stay inline in the domain.call reply; a link
	// frame carries 600 KiB.
	linkDomainInlineBytes = 480 << 10
	// Arguments too large for one frame arrive as a domain object of
	// purpose domain_args, up to this size.
	linkDomainArgsMaxBytes = 8 << 20
)

// domainObjectRef names one domain object by its content: the reference a
// domain method takes for an object the device uploaded and returns for
// one it delivers.
type domainObjectRef struct {
	ObjectID  string `json:"object_id"`
	Version   uint64 `json:"version"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Purpose   string `json:"purpose"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// Validate checks the reference's shape.
func (r domainObjectRef) Validate() error {
	if !linkDomainObjectIDPattern.MatchString(r.ObjectID) || r.Version == 0 || r.Size < 0 || r.Size > linkDomainObjectMaxBytes || len(r.SHA256) != 64 || !linkDomainPurposePattern.MatchString(r.Purpose) {
		return errors.New("invalid object reference")
	}
	if _, err := hex.DecodeString(r.SHA256); err != nil || strings.ToLower(r.SHA256) != r.SHA256 {
		return errors.New("invalid object digest")
	}
	return nil
}

var (
	linkDomainObjectIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	linkDomainPurposePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type linkDomainObjects struct {
	dir string
	mu  sync.Mutex
}

type linkDomainObject struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"device_id"`
	Purpose   string    `json:"purpose"`
	Name      string    `json:"name,omitempty"`
	MediaType string    `json:"media_type,omitempty"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	ExpiresAt time.Time `json:"expires_at"`
}

func openLinkDomainObjects(dir string) (*linkDomainObjects, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d := &linkDomainObjects{dir: dir}
	d.sweep()
	return d, nil
}

func (d *linkDomainObjects) metaPath(id string) string { return filepath.Join(d.dir, id+".json") }
func (d *linkDomainObjects) dataPath(id string) string { return filepath.Join(d.dir, id+".bin") }

// sweep removes expired objects.
func (d *linkDomainObjects) sweep() {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		id := name[:len(name)-len(".json")]
		meta, err := d.meta(id)
		if err != nil || now.After(meta.ExpiresAt) {
			_ = os.Remove(d.dataPath(id))
			_ = os.Remove(d.metaPath(id))
		}
	}
}

func (d *linkDomainObjects) usage() int64 {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil && filepath.Ext(e.Name()) == ".bin" {
			total += info.Size()
		}
	}
	return total
}

func (d *linkDomainObjects) meta(id string) (linkDomainObject, error) {
	if !linkDomainObjectIDPattern.MatchString(id) {
		return linkDomainObject{}, os.ErrNotExist
	}
	raw, err := os.ReadFile(d.metaPath(id))
	if err != nil {
		return linkDomainObject{}, err
	}
	var meta linkDomainObject
	if err := json.Unmarshal(raw, &meta); err != nil || meta.ID != id {
		return linkDomainObject{}, os.ErrNotExist
	}
	return meta, nil
}

// adopt moves a verified staged upload into the store.
func (d *linkDomainObjects) adopt(meta linkDomainObject, staged string) (linkDomainObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweep()
	if d.usage()+meta.Size > linkDomainObjectTotal {
		return linkDomainObject{}, link.Errorf(contract.CodeQuotaExceeded, "domain object storage is full")
	}
	meta.ID = newLinkUUID()
	meta.ExpiresAt = time.Now().UTC().Add(linkDomainObjectExpiry)
	if err := os.Rename(staged, d.dataPath(meta.ID)); err != nil {
		return linkDomainObject{}, err
	}
	if err := link.WriteJSONAtomic(d.metaPath(meta.ID), meta); err != nil {
		_ = os.Remove(d.dataPath(meta.ID))
		return linkDomainObject{}, err
	}
	return meta, nil
}

// put stores bytes produced by the backend for a device.
func (d *linkDomainObjects) put(deviceID, purpose, name, mediaType string, raw []byte) (linkDomainObject, error) {
	if int64(len(raw)) > linkDomainObjectMaxBytes {
		return linkDomainObject{}, link.Errorf(contract.CodeTooLarge, "object exceeds %d bytes", linkDomainObjectMaxBytes)
	}
	staged, err := os.CreateTemp(d.dir, "put-*.part")
	if err != nil {
		return linkDomainObject{}, err
	}
	_, werr := staged.Write(raw)
	cerr := staged.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(staged.Name())
		return linkDomainObject{}, errors.Join(werr, cerr)
	}
	sum := sha256.Sum256(raw)
	meta, err := d.adopt(linkDomainObject{DeviceID: deviceID, Purpose: purpose, Name: name, MediaType: mediaType, Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}, staged.Name())
	if err != nil {
		_ = os.Remove(staged.Name())
	}
	return meta, err
}

// open returns a device's live object.
func (d *linkDomainObjects) open(deviceID, id string) (linkDomainObject, string, error) {
	meta, err := d.meta(id)
	if err != nil || meta.DeviceID != deviceID || time.Now().After(meta.ExpiresAt) {
		return linkDomainObject{}, "", link.Errorf(contract.CodeNotFound, "object not found")
	}
	return meta, d.dataPath(id), nil
}

// readAll reads a device's object whole, checking the reference it was
// passed with.
func (d *linkDomainObjects) readAll(deviceID string, ref domainObjectRef, purpose string, limit int64) ([]byte, error) {
	meta, path, err := d.open(deviceID, ref.ObjectID)
	if err != nil {
		return nil, err
	}
	if meta.Purpose != purpose || (ref.SHA256 != "" && ref.SHA256 != meta.SHA256) || (ref.Size != 0 && ref.Size != meta.Size) || (limit > 0 && meta.Size > limit) {
		return nil, errors.New("object reference does not match")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, meta.Size+1))
	if err != nil || int64(len(raw)) != meta.Size {
		return nil, errors.New("object content is unavailable")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		return nil, errors.New("object content changed")
	}
	return raw, nil
}

func (m linkDomainObject) reference() domainObjectRef {
	return domainObjectRef{ObjectID: m.ID, Version: 1, Size: m.Size, SHA256: m.SHA256, Purpose: m.Purpose, Name: m.Name, MediaType: m.MediaType, ExpiresAt: m.ExpiresAt.Format(time.RFC3339)}
}

func (m linkDomainObject) attachmentRef() contract.AttachmentRef {
	return contract.AttachmentRef{ObjectID: m.ID, Name: m.Name, Size: m.Size, ContentType: m.MediaType, SHA256: m.SHA256}
}

// linkObjectAccess is one device's domain objects.
type linkObjectAccess struct {
	objects  *linkDomainObjects
	deviceID string
	ownerID  string
}

func (a linkObjectAccess) objectOwner() string { return a.ownerID }

func (a linkObjectAccess) describeObject(id string, version uint64) (domainObjectRef, error) {
	meta, _, err := a.objects.open(a.deviceID, id)
	if err != nil || version != 1 {
		return domainObjectRef{}, errors.New("object not found")
	}
	return meta.reference(), nil
}

func (a linkObjectAccess) readObject(ref domainObjectRef, purpose string, limit int64) ([]byte, error) {
	return a.objects.readAll(a.deviceID, ref, purpose, limit)
}
