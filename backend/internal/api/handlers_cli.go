package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The jd command line is built into the backend's image beside the dashboard
// (backend/Dockerfile), and installed on the server from there by install.sh
// and manage.sh. This route hands the same file to another machine, for a
// person who would rather not build it: it is the open source program the
// install already holds, so any signed-in account may fetch it, and it serves
// one fixed file and takes nothing from the request.

const cliName = "jd"

func (s *Server) mountCLIRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/cli/jd", s.handle(s.handleCLIDownload))
}

func (s *Server) handleCLIDownload(w http.ResponseWriter, r *http.Request) error {
	exe, err := os.Executable()
	if err != nil {
		return httpx.Internal(err)
	}
	path := filepath.Join(filepath.Dir(exe), cliName)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return httpx.Err(http.StatusNotFound, "cli_unavailable",
			"this install has no jd built in; build it from the repository with: cd backend && go build ./cmd/jd")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return httpx.Internal(err)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="jd"`)
	// What it was built for, so a script fetching it for another machine can
	// tell it got the wrong one instead of finding out on exec.
	w.Header().Set("X-JD-CLI-Platform", runtime.GOOS+"/"+runtime.GOARCH)
	http.ServeContent(w, r, cliName, info.ModTime(), f)
	return nil
}
