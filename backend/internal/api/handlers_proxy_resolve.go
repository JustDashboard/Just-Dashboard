package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleRouteResolve answers which server block and location nginx picks for
// a URL, from the configuration nginx -T prints. Nothing is sent to the URL:
// the answer is worked out from the files alone.
func (s *Server) handleRouteResolve(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.proxy.EffectiveConfig(r.Context())
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", "nginx could not print its configuration: "+err.Error())
	}
	tree, err := proxysvc.NginxTree(files)
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", err.Error())
	}
	out, err := proxysvc.ResolveRoute(tree, r.URL.Query().Get("url"))
	if errors.Is(err, proxysvc.ErrRouteURL) {
		return httpx.BadRequest("%s", err.Error())
	}
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
