package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// fileTransfer is the Files page's state that outlives a request: the folders
// being followed.
type fileTransfer struct {
	watches *files.WatchHub
}

func newFileTransfer(s *Server) *fileTransfer {
	return &fileTransfer{
		watches: files.NewWatchHub(s.modules.hostFiles, files.DefaultWatchTotal, files.DefaultWatchPerOwner),
	}
}

// run is the transfer module's life: nothing yet beyond waiting for the end.
func (t *fileTransfer) run(ctx context.Context) {
	<-ctx.Done()
}

// mountFilesTransferRoutes adds live folders to /files. It is called inside
// the /files route, so the read routes here are guarded as the other reads
// are and the writes sit behind file.write.
func (s *Server) mountFilesTransferRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/watch", s.handle(s.handleFileWatch))
	r.Method(http.MethodGet, "/changes", s.handle(s.handleFileChanges))
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
