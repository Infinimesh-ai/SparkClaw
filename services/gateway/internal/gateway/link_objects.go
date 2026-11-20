package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// Attachment quotas (design 3.10).
const (
	linkMaxFileBytes     = 25 << 20
	linkMaxStagingBytes  = 512 << 20
	linkMaxOpenUploads   = 8
	linkUploadExpiry     = 24 * time.Hour
	linkReadChunkDefault = contract.MaxChunkBytes
)

// linkObjects stages resumable, chunked uploads before they become
// attachments in the conversation's workspace.
type linkObjects struct {
	dir     string
	mu      sync.Mutex
	uploads map[string]*linkUpload
}

type linkUpload struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	DeviceID    string    `json:"device_id"`
	SessionID   string    `json:"session_id,omitempty"`
	Purpose     string    `json:"purpose,omitempty"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type,omitempty"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	Received    int64     `json:"received"`
	ObjectID    string    `json:"object_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func openLinkObjects(dir string) (*linkObjects, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	o := &linkObjects{dir: dir, uploads: map[string]*linkUpload{}}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var u linkUpload
		if json.Unmarshal(raw, &u) != nil || u.ID == "" {
			continue
		}
		if time.Since(u.CreatedAt) > linkUploadExpiry {
			o.remove(&u)
			continue
		}
		o.uploads[u.ID] = &u
	}
	return o, nil
}

func (o *linkObjects) partPath(u *linkUpload) string { return filepath.Join(o.dir, u.ID+".part") }
func (o *linkObjects) metaPath(u *linkUpload) string { return filepath.Join(o.dir, u.ID+".json") }

func (o *linkObjects) remove(u *linkUpload) {
	_ = os.Remove(o.partPath(u))
	_ = os.Remove(o.metaPath(u))
}

func (o *linkObjects) save(u *linkUpload) error { return link.WriteJSONAtomic(o.metaPath(u), u) }

func (o *linkObjects) stagedBytes() int64 {
	var total int64
	for _, u := range o.uploads {
		if u.ObjectID == "" {
			total += u.Size
		}
	}
	return total
}

func uploadID(deviceID, key string) string {
	sum := sha256.Sum256([]byte(deviceID + "\x00" + key))
	return "up_" + hex.EncodeToString(sum[:12])
}

func (s *Server) linkUploadBegin(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ObjectUploadBeginParams](req)
	if err != nil {
		return nil, err
	}
	name := cleanUploadFilename(p.Name)
	if strings.TrimSpace(p.UploadKey) == "" || name == "" || p.Size <= 0 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "upload_key, name and size are required")
	}
	if p.Size > linkMaxFileBytes {
		return nil, link.Errorf(contract.CodeQuotaExceeded, "a file may be at most %d bytes", linkMaxFileBytes)
	}
	if len(p.SHA256) != 64 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "sha256 must be a hex digest")
	}
	if p.Purpose != "" && (!linkDomainPurposePattern.MatchString(p.Purpose) || p.SessionID != "") {
		return nil, link.Errorf(contract.CodeInvalidRequest, "purpose names a domain object and takes no session_id")
	}
	if p.SessionID != "" {
		if _, ok, err := s.sessionForPrincipal(ctx, linkRequestPrincipal(req), p.SessionID); err != nil || !ok {
			return nil, link.Errorf(contract.CodeNotFound, "conversation not found")
		}
	}
	o := s.link.objects
	o.mu.Lock()
	defer o.mu.Unlock()
	id := uploadID(req.Principal.DeviceID, p.UploadKey)
	if u, ok := o.uploads[id]; ok {
		if u.SHA256 != strings.ToLower(p.SHA256) || u.Size != p.Size || u.Purpose != p.Purpose {
			return nil, link.Errorf(contract.CodeConflict, "upload key reused with different content")
		}
		return contract.ObjectUploadState{UploadID: u.ID, Received: u.Received, ChunkSize: contract.MaxChunkBytes, ObjectID: u.ObjectID}, nil
	}
	open := 0
	for _, u := range o.uploads {
		if u.DeviceID == req.Principal.DeviceID && u.ObjectID == "" {
			open++
		}
	}
	if open >= linkMaxOpenUploads {
		return nil, link.Errorf(contract.CodeQuotaExceeded, "too many unfinished uploads")
	}
	if o.stagedBytes()+p.Size > linkMaxStagingBytes {
		return nil, link.Errorf(contract.CodeQuotaExceeded, "upload staging is full")
	}
	u := &linkUpload{ID: id, Key: p.UploadKey, DeviceID: req.Principal.DeviceID, SessionID: p.SessionID, Purpose: p.Purpose, Name: name, ContentType: strings.TrimSpace(p.ContentType), Size: p.Size, SHA256: strings.ToLower(p.SHA256), CreatedAt: time.Now().UTC()}
	f, err := os.OpenFile(o.partPath(u), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := o.save(u); err != nil {
		return nil, err
	}
	o.uploads[id] = u
	return contract.ObjectUploadState{UploadID: u.ID, ChunkSize: contract.MaxChunkBytes}, nil
}

func (s *Server) linkUploadChunk(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ObjectUploadChunkParams](req)
	if err != nil {
		return nil, err
	}
	o := s.link.objects
	o.mu.Lock()
	defer o.mu.Unlock()
	u, ok := o.uploads[p.UploadID]
	if !ok || u.DeviceID != req.Principal.DeviceID {
		return nil, link.Errorf(contract.CodeNotFound, "upload not found")
	}
	if u.ObjectID != "" || p.Offset != u.Received {
		// The client resumes from Received.
		return contract.ObjectUploadState{UploadID: u.ID, Received: u.Received, ChunkSize: contract.MaxChunkBytes, ObjectID: u.ObjectID}, nil
	}
	if len(p.Data) == 0 || len(p.Data) > contract.MaxChunkBytes || u.Received+int64(len(p.Data)) > u.Size {
		return nil, link.Errorf(contract.CodeInvalidRequest, "chunk size out of range")
	}
	f, err := os.OpenFile(o.partPath(u), os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(p.Data, u.Received); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	u.Received += int64(len(p.Data))
	if err := o.save(u); err != nil {
		return nil, err
	}
	return contract.ObjectUploadState{UploadID: u.ID, Received: u.Received, ChunkSize: contract.MaxChunkBytes}, nil
}

func (s *Server) linkUploadCommit(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ObjectUploadCommitParams](req)
	if err != nil {
		return nil, err
	}
	o := s.link.objects
	o.mu.Lock()
	defer o.mu.Unlock()
	u, ok := o.uploads[p.UploadID]
	if !ok || u.DeviceID != req.Principal.DeviceID {
		return nil, link.Errorf(contract.CodeNotFound, "upload not found")
	}
	if u.ObjectID != "" {
		return contract.ObjectUploadState{UploadID: u.ID, Received: u.Received, ObjectID: u.ObjectID}, nil
	}
	if u.Received != u.Size {
		return nil, link.Errorf(contract.CodeConflict, "upload incomplete: %d of %d bytes", u.Received, u.Size)
	}
	f, err := os.Open(o.partPath(u))
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != u.SHA256 {
		o.remove(u)
		delete(o.uploads, u.ID)
		return nil, link.Errorf(contract.CodeConflict, "content digest mismatch; upload again")
	}
	if u.Purpose != "" {
		// A domain object: passed to domain methods by reference.
		meta, err := s.link.domainObj.adopt(linkDomainObject{DeviceID: u.DeviceID, Purpose: u.Purpose, Name: u.Name, MediaType: u.ContentType, Size: u.Size, SHA256: u.SHA256}, o.partPath(u))
		if err != nil {
			return nil, err
		}
		u.ObjectID = meta.ID
	} else {
		object, err := s.storeLinkUpload(ctx, linkRequestPrincipal(req), u, o.partPath(u))
		if err != nil {
			return nil, err
		}
		u.ObjectID = linkWorkspaceObjectID(u.SessionID, object.Key)
		_ = os.Remove(o.partPath(u))
	}
	if err := o.save(u); err != nil {
		return nil, err
	}
	return contract.ObjectUploadState{UploadID: u.ID, Received: u.Received, ObjectID: u.ObjectID}, nil
}

// storeLinkUpload places a committed upload where the host WebChat puts
// uploads and records the same artifact metadata.
func (s *Server) storeLinkUpload(ctx context.Context, principal requestPrincipal, u *linkUpload, staged string) (app.ArtifactObject, error) {
	workspaceRoot, err := s.linkWorkspaceRoot(ctx, principal, u.SessionID)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	src, err := os.Open(staged)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	defer src.Close()
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(src, sniff)
	sniff = sniff[:n]
	detected := http.DetectContentType(sniff)
	contentType := u.ContentType
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = detected
	}
	name := ensureUploadFilenameExtension(u.Name, contentType, detected)
	isImage := isImageContentType(contentType)
	rootName, kind := "uploads", "document_upload"
	if isImage {
		rootName, kind = "media", "media_image_upload"
	}
	uploadRoot := filepath.Join(workspaceRoot, rootName)
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		return app.ArtifactObject{}, err
	}
	out, storedName, err := workspacefiles.OpenVersionedFile(uploadRoot, name, 0o644)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	path := out.Name()
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		out.Close()
		_ = os.Remove(path)
		return app.ArtifactObject{}, err
	}
	written, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return app.ArtifactObject{}, errors.Join(copyErr, closeErr)
	}
	relPath := filepath.Join(rootName, storedName)
	object := app.ArtifactObject{ID: app.NewID("obj"), Kind: kind, SessionID: u.SessionID, Backend: "workspace", Key: relPath, URI: "workspace://" + filepath.ToSlash(relPath), Path: path, ContentType: contentType, Bytes: int(written), CreatedAt: time.Now().UTC()}
	stored, err := s.store.SaveArtifactObject(ctx, object)
	if err != nil {
		_ = os.Remove(path)
		return app.ArtifactObject{}, err
	}
	if !isImage && s.tools != nil {
		s.tools.PrebuildDocumentIndex(path)
	}
	return stored, nil
}

func (s *Server) linkWorkspaceRoot(ctx context.Context, principal requestPrincipal, sessionID string) (string, error) {
	if sessionID != "" {
		session, ok, err := s.sessionForPrincipal(ctx, principal, sessionID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", link.Errorf(contract.CodeNotFound, "conversation not found")
		}
		if root := strings.TrimSpace(session.WorkspaceRoot); root != "" {
			return root, nil
		}
	}
	profile, found, err := s.store.GetOwnerProfileByID(ctx, principal.OwnerID)
	if err == nil && found && strings.TrimSpace(profile.WorkspaceRoot) != "" {
		return strings.TrimSpace(profile.WorkspaceRoot), nil
	}
	if root := strings.TrimSpace(s.cfg.Workspaces.DefaultRoot); root != "" {
		return root, nil
	}
	return "", link.Errorf(contract.CodeBackendUnavailable, "no workspace is configured")
}

// linkAttachmentObjectID names a message attachment for object.read: the
// conversation and the workspace-relative path.
func linkAttachmentObjectID(sessionID string, a app.MessageAttachment) string {
	if a.RelPath == "" {
		return ""
	}
	return linkWorkspaceObjectID(sessionID, a.RelPath)
}

func linkWorkspaceObjectID(sessionID, relPath string) string {
	return "ws_" + base64.RawURLEncoding.EncodeToString([]byte(sessionID+"\x00"+filepath.ToSlash(relPath)))
}

func decodeLinkObjectID(objectID string) (string, string, bool) {
	if !strings.HasPrefix(objectID, "ws_") {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(objectID, "ws_"))
	parts := strings.SplitN(string(raw), "\x00", 2)
	if err != nil || len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Server) linkResolveObject(ctx context.Context, principal requestPrincipal, objectID string) (contract.AttachmentRef, string, error) {
	sessionID, relPath, ok := decodeLinkObjectID(objectID)
	if !ok {
		return contract.AttachmentRef{}, "", link.Errorf(contract.CodeNotFound, "object not found")
	}
	root, err := s.linkWorkspaceRoot(ctx, principal, sessionID)
	if err != nil {
		return contract.AttachmentRef{}, "", err
	}
	clean, ok := cleanWorkspaceRelativePath(relPath)
	if !ok {
		return contract.AttachmentRef{}, "", link.Errorf(contract.CodeNotFound, "object not found")
	}
	path := filepath.Join(root, clean)
	resolvedRoot, e1 := filepath.EvalSymlinks(root)
	resolvedPath, e2 := filepath.EvalSymlinks(path)
	if e1 != nil || e2 != nil || !pathWithinRoot(resolvedRoot, resolvedPath) {
		return contract.AttachmentRef{}, "", link.Errorf(contract.CodeNotFound, "object not found")
	}
	st, err := os.Stat(resolvedPath)
	if err != nil || st.IsDir() {
		return contract.AttachmentRef{}, "", link.Errorf(contract.CodeNotFound, "object not found")
	}
	ref := contract.AttachmentRef{ObjectID: objectID, Name: filepath.Base(clean), Size: st.Size()}
	if object, found, err := s.store.FindArtifactObjectByURI(ctx, "workspace://"+filepath.ToSlash(clean), sessionID, ""); err == nil && found {
		ref.ContentType = object.ContentType
	}
	return ref, resolvedPath, nil
}

func (s *Server) linkObjectDescribe(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ObjectRef](req)
	if err != nil {
		return nil, err
	}
	ref, _, err := s.linkOpenObject(ctx, req, p.ObjectID)
	return ref, err
}

// linkOpenObject resolves a conversation attachment or one of the
// device's domain objects.
func (s *Server) linkOpenObject(ctx context.Context, req *link.Request, objectID string) (contract.AttachmentRef, string, error) {
	if linkDomainObjectIDPattern.MatchString(objectID) {
		meta, path, err := s.link.domainObj.open(req.Principal.DeviceID, objectID)
		if err != nil {
			return contract.AttachmentRef{}, "", err
		}
		return meta.attachmentRef(), path, nil
	}
	return s.linkResolveObject(ctx, linkRequestPrincipal(req), objectID)
}

func (s *Server) linkObjectRead(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.ObjectReadParams](req)
	if err != nil {
		return nil, err
	}
	ref, path, err := s.linkOpenObject(ctx, req, p.ObjectID)
	if err != nil {
		return nil, err
	}
	length := p.Length
	if length <= 0 || length > contract.MaxChunkBytes {
		length = linkReadChunkDefault
	}
	if p.Offset < 0 || p.Offset > ref.Size {
		return nil, link.Errorf(contract.CodeInvalidRequest, "offset out of range")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, p.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return contract.ObjectReadResult{Object: ref, Offset: p.Offset, Data: buf[:n], EOF: p.Offset+int64(n) >= ref.Size}, nil
}

// linkAttachments maps committed upload references to message attachments.
func (s *Server) linkAttachments(ctx context.Context, principal requestPrincipal, refs []contract.AttachmentRef) ([]agent.MessageAttachment, error) {
	out := make([]agent.MessageAttachment, 0, len(refs))
	for _, ref := range refs {
		resolved, _, err := s.linkResolveObject(ctx, principal, ref.ObjectID)
		if err != nil {
			return nil, link.Errorf(contract.CodeNotFound, "attachment %s was not uploaded", ref.ObjectID)
		}
		sessionID, relPath, _ := decodeLinkObjectID(ref.ObjectID)
		a := agent.MessageAttachment{Name: resolved.Name, RelPath: relPath, URI: "workspace://" + relPath, ContentType: resolved.ContentType, Bytes: int(resolved.Size)}
		if object, found, err := s.store.FindArtifactObjectByURI(ctx, a.URI, sessionID, ""); err == nil && found {
			a.ArtifactID = object.ID
		}
		out = append(out, a)
	}
	return out, nil
}
