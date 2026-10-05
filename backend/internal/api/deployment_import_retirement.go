package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) retiredImportReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			path := strings.TrimPrefix(r.URL.Path, "/api/v1/deploy/")
			segment, rest, _ := strings.Cut(path, "/")
			if segment == "drafts" && rest != "" {
				draftID, _, _ := strings.Cut(rest, "/")
				var retired bool
				err := s.Store.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM deploy_drafts WHERE id = ? AND json_type(data_json, '$.adoption') IS NOT NULL AND json_type(data_json, '$.adoption') <> 'null')`, draftID).Scan(&retired)
				if err != nil {
					httpx.WriteError(w, r, err)
					return
				}
				if retired {
					httpx.WriteError(w, r, httpx.Err(http.StatusUnprocessableEntity, "import_removed", "existing-workload import reviews are no longer supported"))
					return
				}
			}
			if projectID, err := strconv.ParseInt(segment, 10, 64); err == nil && projectID > 0 {
				if err := deploy.RequireSupportedProject(r.Context(), s.Store.DB, projectID); err != nil {
					httpx.WriteError(w, r, mapDeployError(err))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
