package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Reopen an existing object namespace at lifecycle startup so a still-connected
// desktop retains its capability after a Gateway restart. Reads never create it.
func (s *Server) restoreLANMailObjects() {
	root := s.executionRoot
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	if root == ".execution" {
		return
	}
	if info, err := os.Stat(filepath.Join(root, "lan-mail-objects")); err == nil && info.IsDir() {
		if _, err := s.lanMailObjectStore(); err != nil {
			slog.Warn("LAN mail object storage could not be restored")
		}
	}
}

// LAN uses the same bounded, expiring object implementation as ISCP, in a
// separate namespace. No HTTP operation accepts a purpose, path or owner.
func (s *Server) lanMailObjectStore() (*iscpobjects.Store, error) {
	s.mailObjectsMu.Lock()
	defer s.mailObjectsMu.Unlock()
	if s.mailObjects != nil {
		return s.mailObjects, nil
	}
	root := s.executionRoot
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	if root == ".execution" {
		return nil, execution.ErrUnavailable
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	objects, err := iscpobjects.NewStore(filepath.Join(root, "lan-mail-objects"), iscpobjects.Limits{})
	if err != nil {
		return nil, err
	}
	s.mailObjects = objects
	if lifecycle := s.executionContext(); lifecycle.Done() != nil {
		go func() {
			if err := objects.Run(lifecycle); err != nil && lifecycle.Err() == nil {
				slog.Warn("LAN mail object expiry cleanup stopped")
			}
		}()
	}
	return objects, nil
}

func (s *Server) lanMailObjectAccess(r *http.Request) (iscpObjectAccess, error) {
	if r.TLS == nil || s.cfg.Gateway.DeploymentID == "" {
		return iscpObjectAccess{}, emailmanagement.ErrAttachmentPermission
	}
	// This read-only identity lookup cannot initialize storage or bind an
	// installation merely because an HTTP header was supplied.
	p, _, err := s.executionReadPrincipal(r)
	if err != nil {
		return iscpObjectAccess{}, err
	}
	s.mailObjectsMu.Lock()
	objects := s.mailObjects
	s.mailObjectsMu.Unlock()
	if objects == nil {
		return iscpObjectAccess{}, execution.ErrUnavailable
	}
	return iscpObjectAccess{objects, iscpobjects.Binding{
		DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: p.OwnerID, ClientID: p.ClientID,
		InstallationID: r.Header.Get("X-SparkClaw-Installation"), AuthorizationRevision: 1,
	}}, nil
}

func (s *Server) withLANMailObjects(r *http.Request) context.Context {
	ctx := r.Context()
	// A reconnecting desktop may submit before repeating its installation
	// handshake. POST may reopen durable objects; GET never creates storage.
	if r.TLS != nil && r.Header.Get("X-SparkClaw-Installation") != "" {
		if _, err := s.executionPrincipal(r); err == nil {
			_, _ = s.lanMailObjectStore()
		}
	}
	if access, err := s.lanMailObjectAccess(r); err == nil {
		ctx = context.WithValue(ctx, iscpObjectContextKey{}, access)
		ctx = emailmanagement.WithAttachmentObjects(ctx, true, mailAttachmentObjects{})
	}
	return ctx
}

func (s *Server) uploadLANMailAttachment(w http.ResponseWriter, r *http.Request) {
	access, err := s.lanMailObjectAccess(r)
	if err != nil {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentPermission)
		return
	}
	name, err := url.PathUnescape(r.Header.Get("X-SparkClaw-File-Name"))
	transferID := r.Header.Get("X-SparkClaw-Transfer")
	if err != nil || name == "" || r.URL.RawQuery != "" || !execution.UUID(transferID) {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentInvalid)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, app.EmailSendMaxAttachmentBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("mail attachment is oversized"))
		return
	}
	defer clear(raw)
	if execution.Digest(raw) != r.Header.Get("X-SparkClaw-Digest") {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentInvalid)
		return
	}
	ref, err := access.store.PutWithID(r.Context(), access.binding, transferID, app.EmailSendAttachmentPurpose, name, "application/octet-stream", raw)
	if err != nil {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentInvalid)
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

func (s *Server) describeLANMailAttachment(w http.ResponseWriter, r *http.Request) {
	access, err := s.lanMailObjectAccess(r)
	if err != nil {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentPermission)
		return
	}
	ref, err := access.store.Describe(r.Context(), access.binding, r.PathValue("object"), 1)
	if err != nil || ref.Purpose != app.EmailSendAttachmentPurpose || r.URL.RawQuery != "" {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentChanged)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ref)
}

func (s *Server) releaseLANMailAttachment(w http.ResponseWriter, r *http.Request) {
	access, err := s.lanMailObjectAccess(r)
	if err != nil {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentPermission)
		return
	}
	ref, err := access.store.Describe(r.Context(), access.binding, r.PathValue("object"), 1)
	if err != nil || ref.Purpose != app.EmailSendAttachmentPurpose || r.URL.RawQuery != "" {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentChanged)
		return
	}
	body, _ := json.Marshal(map[string]any{"object_id": ref.ObjectID, "version": ref.Version})
	response, _ := access.store.Handle(r.Context(), access.binding, iscpworkbench.Request{Operation: iscpworkbench.OperationObjectRelease, Body: body})
	if response.Status != http.StatusOK {
		writeEmailComposeError(w, emailmanagement.ErrAttachmentChanged)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"released": true})
}
