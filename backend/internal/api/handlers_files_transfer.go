package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// fileTransfer is the Files page's state that outlives a request: the folders
// being followed, the uploads arriving in pieces and the links being fetched.
type fileTransfer struct {
	watches *files.WatchHub
	uploads *files.Uploads
	fetches *files.Fetches
}

func newFileTransfer(s *Server) *fileTransfer {
	// Upload records are the dashboard's own: kept in its data folder, root's
	// alone, made here without anybody being asked.
	uploads, err := files.NewUploads(s.modules.hostFiles, filepath.Join(s.Cfg.DataDir, "file-uploads"))
	if err != nil && s.Log != nil {
		s.Log.Warn("uploads in pieces cannot be kept across a restart", "error", err)
	}
	return &fileTransfer{
		watches: files.NewWatchHub(s.modules.hostFiles, files.DefaultWatchTotal, files.DefaultWatchPerOwner),
		uploads: uploads,
		fetches: files.NewFetches(s.modules.hostFiles),
	}
}

// run removes uploads left untouched for a day, now and every hour, and
// stops the running fetches when the dashboard stops.
func (t *fileTransfer) run(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	defer t.fetches.Close()
	for {
		t.uploads.Sweep()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// mountFilesTransferRoutes adds live folders, uploads in pieces and fetched
// links to /files. It is called inside
// the /files route, so the read routes here are guarded as the other reads
// are and the writes sit behind file.write.
func (s *Server) mountFilesTransferRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/watch", s.handle(s.handleFileWatch))
	r.Method(http.MethodGet, "/changes", s.handle(s.handleFileChanges))
	r.Method(http.MethodGet, "/uploads", s.handle(s.handleFileUploadList))
	r.Method(http.MethodGet, "/uploads/{id}", s.handle(s.handleFileUploadGet))
	r.Method(http.MethodGet, "/fetches", s.handle(s.handleFileFetchList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapFileWrite))
		r.Method(http.MethodPost, "/uploads", s.handle(s.handleFileUploadBegin))
		r.Method(http.MethodPut, "/uploads/{id}", s.handle(s.handleFileUploadPiece))
		r.Method(http.MethodPost, "/uploads/{id}/complete", s.handle(s.handleFileUploadComplete))
		r.Method(http.MethodDelete, "/uploads/{id}", s.handle(s.handleFileUploadCancel))
		r.Method(http.MethodPost, "/fetch", s.handle(s.handleFileFetch))
		r.Method(http.MethodDelete, "/fetches/{id}", s.handle(s.handleFileFetchCancel))
	})
}

// watchOwner is who a watch counts against: the session, or the key.
func watchOwner(r *http.Request) string {
	p, ok := httpx.PrincipalFrom(r.Context())
	if !ok {
		return "anonymous"
	}
	if p.SessionID != "" {
		return "session:" + p.SessionID
	}
	return "token:" + strconv.FormatInt(p.TokenID, 10)
}

// handleFileWatch is a socket that follows one folder. The watch is taken
// before the upgrade, so a path the roots refuse is an ordinary error answer;
// a folder that cannot be watched opens the socket only to say "fallback",
// which is the page's cue to poll /files/changes instead.
//
// Frames: watching {path}, changes {entries, removed}, resync, gone,
// fallback {reason}.
func (s *Server) handleFileWatch(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	path := q.Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	hidden, err := httpx.QueryBool(r, "hidden")
	if err != nil {
		return err
	}
	watch, err := s.modules.fileTransfer.watches.Watch(path, watchOwner(r), hidden, q.Get("name"))
	fallback := errors.Is(err, files.ErrWatchLimit) || errors.Is(err, files.ErrWatchUnavailable)
	if err != nil && !fallback {
		return mapFileError(err)
	}
	conn, upgradeErr := s.WS.Upgrade(w, r)
	if upgradeErr != nil {
		if watch != nil {
			watch.Close()
		}
		return nil
	}
	defer conn.Close()
	if fallback {
		conn.Send("fallback", map[string]string{"reason": err.Error()})
		return nil
	}
	defer watch.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)
	if err := conn.Send("watching", map[string]string{"path": watch.Path}); err != nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case change, ok := <-watch.C:
			if !ok {
				return nil
			}
			kind := "changes"
			var data any = change
			switch {
			case change.Gone:
				kind, data = "gone", nil
			case change.Resync:
				kind, data = "resync", nil
			}
			if err := conn.Send(kind, data); err != nil || change.Gone {
				return nil
			}
		}
	}
}

// handleFileChanges is the cheap check a page polls where a folder cannot be
// watched: a fingerprint of the folder's names, sizes, times and modes.
func (s *Server) handleFileChanges(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	hidden, err := httpx.QueryBool(r, "hidden")
	if err != nil {
		return err
	}
	print, err := s.modules.hostFiles.Fingerprint(path, hidden)
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"path": path, "fingerprint": print})
	return nil
}

// transferOwner is whose an upload or a fetch is: the account, so a page
// signed in again can carry on what it started.
func transferOwner(r *http.Request) string {
	p, ok := httpx.PrincipalFrom(r.Context())
	if !ok || p.User == nil {
		return "anonymous"
	}
	return "user:" + strconv.FormatInt(p.User.ID, 10)
}

// mapTransferError says what went wrong with an upload or a fetch in the
// codes the page reads, and leaves everything else to mapFileError.
func mapTransferError(err error) error {
	switch {
	case errors.Is(err, files.ErrNoRoom):
		return httpx.Err(http.StatusInsufficientStorage, "no_space", err.Error())
	case errors.Is(err, files.ErrUploadNotFound):
		return httpx.Err(http.StatusNotFound, "upload_not_found", err.Error())
	case errors.Is(err, files.ErrUploadIncomplete):
		return httpx.Err(http.StatusConflict, "upload_incomplete", err.Error())
	case errors.Is(err, files.ErrUploadLimit):
		return httpx.Err(http.StatusConflict, "too_many_uploads", err.Error())
	case errors.Is(err, files.ErrPiece):
		return httpx.Err(http.StatusBadRequest, "bad_piece", err.Error())
	case errors.Is(err, fs.ErrInvalid):
		return httpx.Err(http.StatusBadRequest, "invalid_name", err.Error())
	case errors.Is(err, files.ErrFetchScheme):
		return httpx.Err(http.StatusBadRequest, "unsupported_link", err.Error())
	case errors.Is(err, files.ErrFetchLimit):
		return httpx.Err(http.StatusConflict, "too_many_fetches", err.Error())
	case errors.Is(err, files.ErrFetchNotFound):
		return httpx.Err(http.StatusNotFound, "fetch_not_found", err.Error())
	}
	return mapFileError(err)
}

func (s *Server) handleFileUploadList(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.fileTransfer.uploads.List(transferOwner(r)))
	return nil
}

func (s *Server) handleFileUploadGet(w http.ResponseWriter, r *http.Request) error {
	session, err := s.modules.fileTransfer.uploads.Get(transferOwner(r), chi.URLParam(r, "id"))
	if err != nil {
		return mapTransferError(err)
	}
	httpx.JSON(w, http.StatusOK, session)
	return nil
}

// handleFileUploadBegin starts an upload in pieces, or answers with the one
// this account already has for the same file, so a page that was reloaded and
// given the same file again carries on where it stood.
func (s *Server) handleFileUploadBegin(w http.ResponseWriter, r *http.Request) error {
	var req files.BeginUpload
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Dir == "" || req.Name == "" {
		return httpx.BadRequest("path and name are both required")
	}
	if err := validRevision(req.Expected); err != nil {
		return err
	}
	session, err := s.modules.fileTransfer.uploads.Begin(transferOwner(r), req)
	if err != nil {
		return mapTransferError(err)
	}
	httpx.SetAudit(r, "file.upload.begin", session.Path,
		map[string]any{"size": session.Size, "overwrite": session.Overwrite, "received": session.Received})
	httpx.JSON(w, http.StatusOK, session)
	return nil
}

// handleFileUploadPiece writes one piece at ?offset=. A piece that lands is
// not audited on its own — a large file is thousands of them — the upload is,
// when it begins and when it is published; a refused piece is.
func (s *Server) handleFileUploadPiece(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		return httpx.BadRequest("offset query parameter must be a byte offset")
	}
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxPiece+1)
	session, err := s.modules.fileTransfer.uploads.Put(transferOwner(r), id, offset, r.Body)
	if err != nil {
		httpx.SetAudit(r, "file.upload.piece", session.Path, map[string]any{"upload": id, "offset": offset})
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return httpx.Err(http.StatusRequestEntityTooLarge, "too_large",
				"a piece is at most "+files.HumanBytes(files.MaxPiece))
		}
		return mapTransferError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, session)
	return nil
}

func (s *Server) handleFileUploadComplete(w http.ResponseWriter, r *http.Request) error {
	session, err := s.modules.fileTransfer.uploads.Complete(transferOwner(r), chi.URLParam(r, "id"))
	if err != nil {
		httpx.SetAudit(r, "file.upload", session.Path, map[string]any{"pieces": true})
		return mapTransferError(err)
	}
	httpx.SetAudit(r, "file.upload", session.Dir,
		map[string]any{"files": []string{session.Name}, "bytes": session.Size, "pieces": true})
	httpx.JSON(w, http.StatusOK, session)
	return nil
}

func (s *Server) handleFileUploadCancel(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	session, _ := s.modules.fileTransfer.uploads.Get(transferOwner(r), id)
	if err := s.modules.fileTransfer.uploads.Cancel(transferOwner(r), id); err != nil {
		return mapTransferError(err)
	}
	httpx.SetAudit(r, "file.upload.cancel", session.Path, map[string]any{"received": session.Received})
	httpx.NoContent(w)
	return nil
}

// validRevision is the upload route's rule for expectedRevision: a SHA-256
// revision, or empty for a name that must be new.
func validRevision(expected *string) error {
	if expected == nil || *expected == "" {
		return nil
	}
	if len(*expected) != 64 {
		return httpx.BadRequest("expectedRevision must be a SHA-256 revision or empty for a new file")
	}
	for _, c := range *expected {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return httpx.BadRequest("expectedRevision must be a SHA-256 revision or empty for a new file")
		}
	}
	return nil
}

// fetchView is a fetch as the page reads it: whether it is this account's
// says whether its row offers Cancel.
type fetchView struct {
	files.Fetch
	Mine bool `json:"mine"`
}

func viewFetch(f files.Fetch, owner string) fetchView {
	return fetchView{Fetch: f, Mine: f.Owner == owner}
}

// handleFileFetch starts downloading a link into a folder, on the server. It
// answers at once; the fetch carries on whether or not the page stays, and
// GET /files/fetches is where its progress is read. The audit entry holds
// the link without its query string or credentials.
func (s *Server) handleFileFetch(w http.ResponseWriter, r *http.Request) error {
	var req files.FetchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Dir == "" || req.URL == "" {
		return httpx.BadRequest("path and url are both required")
	}
	shown := files.ShownURL(req.URL)
	httpx.SetAudit(r, "file.fetch", req.Dir, map[string]any{"url": shown, "name": req.Name, "overwrite": req.Overwrite})
	owner := transferOwner(r)
	fetch, err := s.modules.fileTransfer.fetches.Start(owner, req)
	if err != nil {
		return mapTransferError(err)
	}
	httpx.JSON(w, http.StatusAccepted, viewFetch(fetch, owner))
	return nil
}

func (s *Server) handleFileFetchList(w http.ResponseWriter, r *http.Request) error {
	owner := transferOwner(r)
	list := s.modules.fileTransfer.fetches.List()
	out := make([]fetchView, 0, len(list))
	for _, f := range list {
		out = append(out, viewFetch(f, owner))
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleFileFetchCancel stops a running fetch, removing what it had staged,
// or takes a finished one off the list.
func (s *Server) handleFileFetchCancel(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	current, err := s.modules.fileTransfer.fetches.Get(id)
	if err != nil {
		return mapTransferError(err)
	}
	httpx.SetAudit(r, "file.fetch.cancel", current.Dir, map[string]any{"url": current.URL, "status": current.Status})
	fetch, err := s.modules.fileTransfer.fetches.Cancel(id)
	if err != nil {
		return mapTransferError(err)
	}
	httpx.JSON(w, http.StatusOK, viewFetch(fetch, transferOwner(r)))
	return nil
}
