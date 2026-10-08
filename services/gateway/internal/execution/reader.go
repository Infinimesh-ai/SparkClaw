package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Reader exposes no execution, maintenance or persistence operation.
// A process with no live Service uses an atomic private snapshot for GET.
type Reader interface {
	Installation(owner, client, installation string) error
	Lookup(owner, client, request string) (Status, error)
	File(owner, client, request, file string) ([]byte, error)
}

func Read(root string) (Reader, error) { return readSnapshot(root, true) }

func readSnapshot(root string, withContent bool) (*Service, error) {
	reader := &Service{root: root, closed: true, now: func() time.Time { return time.Now().UTC() }, control: control{Version: 2, Installations: map[string]string{}, Fences: map[string]Fence{}, WorkbenchFences: map[string]workbenchFence{}}}
	if !filepath.IsAbs(root) {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return reader, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if !owned || stat.Uid != uint32(os.Getuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	raw, err := readPrivate(filepath.Join(root, "control.json"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return reader, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reader.control) != nil || !json.Valid(raw) || reader.validateControl() != nil {
		return nil, ErrUnavailable
	}
	if withContent {
		key, err := readPrivate(filepath.Join(root, "spool.key"), 32)
		if err != nil || len(key) != 32 {
			return nil, ErrUnavailable
		}
		reader.key = key
	}
	return reader, nil
}

// Authenticate both the encrypted bundle and its original result/file
// manifests before exposing any bytes through either the live or snapshot view.
func validateResultBundle(f Fence, content bundle) error {
	if len(content.Payload) > ResultBytes || Digest([]byte(content.Payload)) != f.ResultDigest {
		return ErrUnavailable
	}
	var payload Payload
	decoder := json.NewDecoder(bytes.NewBufferString(content.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || !json.Valid([]byte(content.Payload)) || len(payload.Files) != len(content.Files) {
		return ErrUnavailable
	}
	total := len(content.Payload)
	ids, names := map[string]bool{}, map[string]bool{}
	for _, file := range payload.Files {
		raw, found := content.Files[file.ID]
		if !found || !UUID(file.ID) || !validName(file.Name) || ids[file.ID] || names[file.Name] || file.Size < 0 || file.Size > ResultBytes || len(raw) != file.Size || !digestPattern.MatchString(file.SHA256) || Digest(raw) != file.SHA256 {
			return ErrUnavailable
		}
		ids[file.ID], names[file.Name] = true, true
		total += len(raw)
		if total > ResultBytes {
			return ErrUnavailable
		}
	}
	return nil
}
