package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

// A conversation input remains immutable and is never exposed as an execution
// result. Publishing allocates an independent versioned workspace output.
func (r Runtime) copyConversationPublication(ctx context.Context, root, source string, part app.MessagePart) (app.MessagePart, error) {
	if err := ctx.Err(); err != nil {
		return app.MessagePart{}, err
	}
	input, err := os.Open(source)
	if err != nil {
		return app.MessagePart{}, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return app.MessagePart{}, errors.New("conversation file exceeds publication limit")
	}
	name := part.Name
	if name == "" {
		name = filepath.Base(source)
	}
	output, ref, err := workspacefiles.OpenVersionedFile(root, name, 0600)
	if err != nil {
		return app.MessagePart{}, err
	}
	if r.tools != nil {
		if err := r.tools.ReserveWorkspaceBytes(filepath.Join(root, ref), int(info.Size())); err != nil {
			output.Close()
			os.Remove(filepath.Join(root, ref))
			return app.MessagePart{}, err
		}
	}
	success := false
	defer func() {
		output.Close()
		if !success {
			os.Remove(filepath.Join(root, ref))
			if r.tools != nil {
				r.tools.ReleaseWorkspaceBytes(filepath.Join(root, ref))
			}
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, (8<<20)+1))
	if err != nil {
		return app.MessagePart{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if n != info.Size() || part.SHA256 != "" && digest != part.SHA256 {
		return app.MessagePart{}, errors.New("conversation file changed during publication")
	}
	if err = ctx.Err(); err != nil {
		return app.MessagePart{}, err
	}
	if err = output.Close(); err != nil {
		return app.MessagePart{}, err
	}
	copyDigest, err := workflowFileSHA256(filepath.Join(root, ref))
	if err != nil || copyDigest != digest {
		return app.MessagePart{}, errors.New("conversation file changed during publication")
	}
	part.ArtifactID = ""
	part.Resource = &app.ResourceRef{Kind: "workspace_file", Ref: ref, Provenance: "conversation_copy"}
	part.Name = ref
	part.SHA256 = digest
	part.Bytes = int(n)
	success = true
	return part, nil
}
