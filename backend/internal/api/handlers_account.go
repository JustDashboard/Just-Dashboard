package api

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"strconv"

	// Registered for their side effect: image.DecodeConfig needs a decoder
	// for each type the avatar route accepts, and it accepts exactly these.
	_ "image/jpeg"
	_ "image/png"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// --- the account's own profile ---

type updateProfileRequest struct {
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"displayName,omitempty"`
}

// handleUpdateProfile renames the caller. The sign-in name is what the audit
// log and every deployment record name the actor by, so a rename is itself
// audited with both spellings — the trail stays followable across it.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) error {
	var req updateProfileRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Username == nil && req.DisplayName == nil {
		return httpx.BadRequest("nothing to change")
	}
	p := httpx.MustPrincipal(r)
	before := p.Username()
	if err := s.Auth.SetProfile(r.Context(), p.UserID(), auth.Profile{
		Username: req.Username, DisplayName: req.DisplayName,
	}); err != nil {
		return httpx.BadRequest("%v", err)
	}
	updated, err := s.Auth.UserByID(r.Context(), p.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	detail := map[string]any{}
	if req.Username != nil && updated.Username != before {
		detail["from"] = before
		detail["to"] = updated.Username
	}
	if req.DisplayName != nil {
		detail["displayName"] = updated.DisplayName
	}
	httpx.SetAudit(r, "account.profile.update", updated.Username, detail)
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

// The picture is small by construction — the client draws it onto a 256px
// canvas before sending — so anything larger is refused up front rather than
// resized here: the server has no image codec beyond the two decoders it
// needs to prove the bytes are what they claim to be.
const (
	maxAvatarEdge       = 1024
	avatarBodyAllowance = 64 << 10
)

// handleUploadOwnAvatar stores the caller's picture. The bytes are decoded
// far enough to know their type and size and no further; what is stored is
// what was sent, and what is served later is served under the type that was
// proven here — never the type the upload declared.
func (s *Server) handleUploadOwnAvatar(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	data, mimeType, err := readAvatarUpload(w, r)
	if err != nil {
		return err
	}
	if err := s.Auth.SetAvatar(r.Context(), p.UserID(), data, mimeType); err != nil {
		return httpx.BadRequest("%v", err)
	}
	updated, err := s.Auth.UserByID(r.Context(), p.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "account.avatar.update", p.Username(), map[string]any{"bytes": len(data), "type": mimeType})
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleDeleteOwnAvatar(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if err := s.Auth.ClearAvatar(r.Context(), p.UserID()); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "account.avatar.remove", p.Username(), nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleOwnAvatar(w http.ResponseWriter, r *http.Request) error {
	return s.serveAvatar(w, r, httpx.MustPrincipal(r).UserID())
}

// handleUserAvatar is the same picture for the users table, which only a
// system.admin can see — the route is mounted inside that group.
func (s *Server) handleUserAvatar(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid user id")
	}
	return s.serveAvatar(w, r, id)
}

func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request, userID int64) error {
	data, mimeType, err := s.Auth.Avatar(r.Context(), userID)
	if errors.Is(err, auth.ErrNotFound) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return httpx.Internal(err)
	}
	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	// The URL carries the picture's version, so a copy may be kept for as
	// long as the browser likes: a new upload is a new URL, and the global
	// no-store would otherwise fetch the same bytes on every page.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

// readAvatarUpload takes the one file part of a multipart body and proves it
// is a PNG or JPEG of sane dimensions. The declared content type is ignored:
// the bytes say what they are.
func readAvatarUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, auth.MaxAvatarBytes+avatarBodyAllowance)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", httpx.BadRequest("expected a multipart image upload: %v", err)
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, "", httpx.Err(http.StatusRequestEntityTooLarge, "avatar_too_large", "the image is too large")
			}
			return nil, "", httpx.BadRequest("malformed upload: %v", err)
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, auth.MaxAvatarBytes+1))
		part.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, "", httpx.Err(http.StatusRequestEntityTooLarge, "avatar_too_large", "the image is too large")
			}
			return nil, "", httpx.BadRequest("could not read upload: %v", err)
		}
		if len(data) > auth.MaxAvatarBytes {
			return nil, "", httpx.Err(http.StatusRequestEntityTooLarge, "avatar_too_large", "the image is too large")
		}
		mimeType := http.DetectContentType(data)
		if mimeType != "image/png" && mimeType != "image/jpeg" {
			return nil, "", httpx.Err(http.StatusUnsupportedMediaType, "avatar_type", "the picture must be a PNG or JPEG")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, "", httpx.BadRequest("the picture could not be decoded")
		}
		if cfg.Width > maxAvatarEdge || cfg.Height > maxAvatarEdge || cfg.Width == 0 || cfg.Height == 0 {
			return nil, "", httpx.BadRequest("the picture must be at most %d pixels on each side", maxAvatarEdge)
		}
		return data, mimeType, nil
	}
	return nil, "", httpx.BadRequest("no file in the upload")
}

// --- the account's other sessions ---

func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	n, err := s.Auth.RevokeOtherSessions(r.Context(), p.UserID(), p.SessionID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.session.revoke_others", p.Username(), map[string]any{"revoked": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"revoked": n})
	return nil
}
