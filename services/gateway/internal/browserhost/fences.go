package browserhost

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Fence deliberately has no arguments, URLs, snapshots or result content.
// Capacity exhaustion fails admission: never trim lifetime deduplication rows.
type Fence struct {
	CommandID           string    `json:"command_id"`
	Scope               Scope     `json:"scope"`
	HostID              string    `json:"host_id"`
	RuntimeGeneration   string    `json:"runtime_generation"`
	ConnectionEpoch     string    `json:"connection_epoch"`
	LeaseID             string    `json:"lease_id"`
	PageID              string    `json:"page_id"`
	PageGeneration      uint64    `json:"page_generation"`
	AuthorizationDigest string    `json:"authorization_digest"`
	Digest              string    `json:"digest"`
	Write               bool      `json:"write"`
	State               string    `json:"state"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func loadFences(root string) (map[string]Fence, error) { return readFences(root, true) }

func readFences(root string, create bool) (map[string]Fence, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("browser control root must be absolute")
	}
	for current := filepath.Clean(root); current != string(filepath.Separator); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("browser control path is unsafe")
		}
	}
	if create {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(root)
	if !create && errors.Is(err, os.ErrNotExist) {
		return map[string]Fence{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("browser control root must be private")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("browser control root owner differs")
	}
	out := map[string]Fence{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
			return nil, errors.New("browser control record is unsafe")
		}
		file, err := os.Open(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(io.LimitReader(file, 8193))
		decoder.DisallowUnknownFields()
		var fence Fence
		err = decoder.Decode(&fence)
		file.Close()
		if err != nil || fence.CommandID+".json" != entry.Name() || !idPattern.MatchString(fence.CommandID) || !fence.Scope.valid() {
			return nil, errors.New("invalid browser control fence")
		}
		if _, exists := out[fence.CommandID]; exists {
			return nil, ErrFence
		}
		out[fence.CommandID] = fence
	}
	if len(out) > MaxFences {
		return nil, ErrFence
	}
	return out, nil
}
func persistFence(root string, fence Fence) error {
	if !idPattern.MatchString(fence.CommandID) {
		return ErrFence
	}
	raw, err := json.Marshal(fence)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".control-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(root, fence.CommandID+".json"))
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// ReadFences projects interrupted command receipts from private storage without
// creating the broker, changing timestamps or persisting terminal transitions.
func ReadFences(root string, identity Identity) ([]Fence, error) {
	if !identity.valid() {
		return nil, ErrFence
	}
	fences, err := readFences(root, false)
	if err != nil {
		return nil, err
	}
	out := []Fence{}
	for _, f := range fences {
		if f.Scope.Identity != identity {
			continue
		}
		if f.State == "dispatched" && f.Write {
			f.State = "unknown"
		}
		if f.State == "unknown" {
			out = append(out, f)
		}
	}
	return out, nil
}
