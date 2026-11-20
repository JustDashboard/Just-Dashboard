package api

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/thumbnail"
	"github.com/go-chi/chi/v5"
)

// The Files page's pictures, drawn small: a copy the server makes once and
// keeps (internal/thumbnail), so a folder of forty 12 MB photographs scrolls
// like a folder of icons instead of pulling 480 MB to draw at 96 pixels.

// filesViewing is the Files page's media state: the thumbnail cache, made on
// first use under the data directory, so nothing is created on a server
// whose Files page is never opened.
type filesViewing struct {
	once   sync.Once
	thumbs *thumbnail.Cache
}

func (s *Server) thumbnails() *thumbnail.Cache {
	s.modules.viewing.once.Do(func() {
		s.modules.viewing.thumbs = thumbnail.New(filepath.Join(s.Cfg.DataDir, "thumbnails"), 0)
	})
	return s.modules.viewing.thumbs
}

// mountFilesViewingRoutes is the read routes of this file, inside /files and
// its guards: a thumbnail is a read, as /files/raw is.
func (s *Server) mountFilesViewingRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/thumbnail", s.handle(s.handleFileThumbnail))
}

// handleFileThumbnail serves a picture's small copy with the raw route's
// posture: a closed list of what it answers with (JPEG, or PNG where the
// picture has transparency, both made here rather than passed through), the
// `sandbox` policy, inline, and a short private cache. The caller puts the
// file's modification time in the address, so a changed picture is a new
// address, and the copy is keyed on the opened file, so it is a new copy.
//
// The path is opened through the Files resolver first, every time, before the
// cache is consulted: a copy is only ever served for a file the caller could
// have read through /files/raw.
func (s *Server) handleFileThumbnail(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	if !thumbnail.Decodable(filepath.Base(path)) {
		return httpx.Err(http.StatusUnsupportedMediaType, "no_thumbnail",
			"thumbnails are made of PNG, JPEG and GIF pictures; fetch this one from /files/raw")
	}
	f, st, err := s.modules.hostFiles.Open(path)
	if err != nil {
		return mapFileError(err)
	}
	defer f.Close()
	if st.IsDir() {
		return httpx.BadRequest("path is a directory")
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	thumb, err := s.thumbnails().Get(ctx, f, st)
	switch {
	case errors.Is(err, thumbnail.ErrTooLarge):
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, thumbnail.ErrUnsupported):
		return httpx.Err(http.StatusUnsupportedMediaType, "no_thumbnail", err.Error())
	case err != nil && ctx.Err() != nil:
		return httpx.Err(http.StatusServiceUnavailable, "busy", "the thumbnail took too long to make")
	case err != nil:
		return mapFileError(err)
	}
	name := strings.TrimSuffix(filepath.Base(st.Name()), filepath.Ext(st.Name())) + ".thumbnail"
	if thumb.ContentType == "image/png" {
		name += ".png"
	} else {
		name += ".jpg"
	}
	w.Header().Set("Content-Type", thumb.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "private, max-age=30")
	http.ServeContent(w, r, name, st.ModTime(), bytes.NewReader(thumb.Data))
	return nil
}
