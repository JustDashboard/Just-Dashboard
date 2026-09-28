package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleProxyFiles lists the nginx directory for the Configuration page:
// every file, whether nginx reads it, and whether the dashboard wrote it. It
// names files and never shows a password file's content, which is what the
// config editor's read route already lets every account see, so it is a read.
func (s *Server) handleProxyFiles(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.proxy.ConfigFiles()
	if errors.Is(err, fs.ErrNotExist) {
		return httpx.Err(http.StatusNotFound, "not_found", "The nginx configuration directory is not there.").
			Because("JD_NGINX_DIR names the directory nginx reads its configuration from.", err.Error())
	}
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "internal", err)
	}
	httpx.JSON(w, http.StatusOK, files)
	return nil
}

// effectiveBudget bounds the wait for `nginx -T`. The dump waits for the
// service lock, which a certificate order can hold for minutes, and a page
// that says "busy, try again" is better than one that spins that long.
const effectiveBudget = 20 * time.Second

// effectiveError answers a read of what nginx loads that could not be made:
// a refused configuration carries its test, so the page can place each of
// nginx's lines at its file, and a dump stuck behind the service lock is busy
// rather than failed.
func effectiveError(w http.ResponseWriter, r *http.Request, err error) error {
	var refused *proxysvc.DumpRefusedError
	switch {
	case errors.As(err, &refused):
		return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config",
			"nginx refuses its configuration, so it has nothing loaded to show.\n"+refused.Validation.Output),
			refused.Validation)
	case errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
		return httpx.Err(http.StatusServiceUnavailable, "busy", "nginx could not be asked for its configuration in time.").
			Because("Another change to the proxy — a certificate order, a site being applied — holds it for now.", err.Error()).
			Retry()
	}
	return httpx.Wrap(http.StatusBadGateway, "effective_failed", err)
}

// effectiveFile is one file nginx loads, as `nginx -T` printed it. Target is
// the file a link resolves to, which is the one the config editor opens.
type effectiveFile struct {
	Path    string `json:"path"`
	Target  string `json:"target,omitempty"`
	Content string `json:"content"`
}

// handleProxyEffective answers the configuration nginx loads, file by file
// in the order it reads them, with every directive placed for a search by
// name. It runs the host's nginx, which is the config test's trust, so it is
// gated with the test.
//
// A configuration that fails its test prints nothing to load: that is a 422
// carrying the test, as a refused reload's is, so the page can place each of
// nginx's lines at its file.
func (s *Server) handleProxyEffective(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), effectiveBudget)
	defer cancel()
	files, at, err := s.modules.proxy.EffectiveConfigAt(ctx)
	if err != nil {
		return effectiveError(w, r, err)
	}
	out := make([]effectiveFile, len(files))
	for i, f := range files {
		out[i] = effectiveFile{Path: f.Path, Content: f.Content}
		if target, err := s.modules.proxy.ResolveConfigPath(f.Path); err == nil && target != f.Path {
			out[i].Target = target
		}
	}
	body := map[string]any{"files": out, "checkedAt": at}
	// nginx accepted these files, so a tree that does not build is this
	// reader's shortfall, not the configuration's: the search by directive
	// is then unavailable and says why, and the text search still works.
	if tree, err := proxysvc.NginxTree(files); err != nil {
		body["directives"] = nil
		body["treeError"] = err.Error()
	} else {
		body["directives"] = proxysvc.PlaceDirectives(tree)
	}
	httpx.JSON(w, http.StatusOK, body)
	return nil
}
