package api

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func deploymentPreviewURL(raw string) (*url.URL, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\t ;'\"<>*\\") {
		return nil, fmt.Errorf("no valid website address is recorded")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("no valid HTTP(S) website address is recorded")
	}
	return u, nil
}

// The wrapper contains no script and never fetches the website on the server.
// Its policy permits only the recorded origin, leaving the dashboard CSP intact.
func (s *Server) handleDeploymentPreviewFrame(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{})
	if err != nil {
		return mapDeployError(err)
	}
	u, err := deploymentPreviewURL(summary.Endpoint)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-src "+u.Scheme+"://"+u.Host+"; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	_, err = fmt.Fprintf(w, `<!doctype html><html><head><meta name="referrer" content="no-referrer"><title>Website preview</title><style>html,body,iframe{margin:0;width:100%%;height:100%%;border:0}body{overflow:hidden}</style></head><body><iframe title="Deployed website" src="%s" sandbox="allow-scripts allow-same-origin allow-forms" referrerpolicy="no-referrer"></iframe></body></html>`, html.EscapeString(u.String()))
	return err
}
