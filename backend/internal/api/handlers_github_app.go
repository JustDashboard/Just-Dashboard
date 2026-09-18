package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The GitHub App is a deployment-wide credential, so its routes live under
// /deploy beside the credential list.
//
//	read          whether an App is connected, who installed it, which
//	              repositories it can reach
//	system.admin  creating, connecting and disconnecting the App — it holds a
//	              key that can read every repository it is installed on
func (s *Server) mountGitHubAppRoutes(r chi.Router) {
	r.Route("/github-app", func(r chi.Router) {
		r.Use(httpx.RequireSession)
		r.Method(http.MethodGet, "/", s.handle(s.handleGitHubAppStatus))
		r.Method(http.MethodGet, "/repositories", s.handle(s.handleGitHubAppRepositories))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/manifest", s.handle(s.handleGitHubAppManifest))
			// GitHub sends the operator's browser back here after creating
			// the App: a GET, carrying the state this dashboard issued.
			r.Method(http.MethodGet, "/callback", s.handle(s.handleGitHubAppCallback))
			r.Method(http.MethodDelete, "/", s.handle(s.handleGitHubAppDisconnect))
		})
	})
}

func (s *Server) handleGitHubAppStatus(w http.ResponseWriter, r *http.Request) error {
	status, err := s.modules.githubApp.Status(r.Context(), s.dashboardEndpoint())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// handleGitHubAppRepositories lists what the App can reach, for the import
// page. No App is an empty list, not an error: the page shows the other ways
// in.
func (s *Server) handleGitHubAppRepositories(w http.ResponseWriter, r *http.Request) error {
	repositories, err := s.modules.githubApp.Repositories(r.Context())
	if errors.Is(err, githubapp.ErrNotConfigured) {
		httpx.JSON(w, http.StatusOK, []githubapp.Repository{})
		return nil
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "github_unavailable", err.Error())
	}
	httpx.JSON(w, http.StatusOK, repositories)
	return nil
}

// handleGitHubAppManifest starts GitHub's manifest flow. The answer is what
// the page posts to GitHub from the operator's own browser; the state in it
// is what ties GitHub's redirect back to this dashboard.
func (s *Server) handleGitHubAppManifest(w http.ResponseWriter, r *http.Request) error {
	var request githubapp.ManifestRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	start, err := s.modules.githubApp.BeginManifest(s.dashboardEndpoint(), request)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "deploy.githubapp.manifest", start.Manifest.Name, map[string]any{"organization": request.Organization})
	httpx.JSON(w, http.StatusOK, start)
	return nil
}

// handleGitHubAppCallback is GitHub's redirect after the App is created. It
// lands in a browser, so its answers are redirects to the credentials page
// rather than JSON: connected, or failed with the reason.
func (s *Server) handleGitHubAppCallback(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	credentials, err := s.modules.githubApp.CompleteManifest(r.Context(), query.Get("code"), query.Get("state"))
	if err != nil {
		httpx.SetAudit(r, "deploy.githubapp.connect", "", map[string]any{"error": err.Error()})
		http.Redirect(w, r, "/deploy/credentials?github-app=failed&reason="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return nil
	}
	httpx.SetAudit(r, "deploy.githubapp.connect", credentials.Slug, map[string]any{"appId": credentials.ID, "owner": credentials.Owner})
	http.Redirect(w, r, "/deploy/credentials?github-app=connected", http.StatusSeeOther)
	return nil
}

func (s *Server) handleGitHubAppDisconnect(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.githubApp.Disconnect(r.Context()); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.githubapp.disconnect", "", nil)
	httpx.NoContent(w)
	return nil
}

// handleGitHubAppWebhook receives every delivery the App's one webhook sends,
// verified with the App's secret, and hands it to each trigger that asked for
// App delivery on that repository. A push can legitimately reach several
// triggers, so the answer lists what each decided; GitHub only needs the 2xx.
func (s *Server) handleGitHubAppWebhook(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAuditActor(r, "webhook")
	secret, err := s.modules.githubApp.WebhookSecret(r.Context())
	if errors.Is(err, githubapp.ErrNotConfigured) {
		return httpx.Err(http.StatusNotFound, "not_configured", "no GitHub App is connected to this dashboard")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deploy.MaxAutomationBody))
	if err != nil {
		return httpx.Err(http.StatusRequestEntityTooLarge, "payload_too_large", "webhook payload exceeds 4 MiB")
	}
	name := r.Header.Get("X-GitHub-Event")
	event, err := deploy.VerifyProvider("github", r.Header, body, secret)
	switch {
	case errors.Is(err, deploy.ErrBadHookSignature):
		return mapAutomationError(err)
	case errors.Is(err, deploy.ErrWrongEvent):
		// Signed, but not a push or pull request: the App's own lifecycle
		// events, pings, anything else GitHub sends an App. An install
		// changes what the App can reach, so the cached answer is dropped.
		if name == "installation" || name == "installation_repositories" {
			s.modules.githubApp.Invalidate()
		}
		httpx.SetAudit(r, "deploy.githubapp.delivery", name, map[string]any{"accepted": false, "reason": "event_ignored"})
		httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": false, "reason": "event_ignored", "event": name})
		return nil
	case err != nil:
		return mapAutomationError(err)
	}
	triggers, err := s.modules.deployAutomation.TriggersForAppDelivery(r.Context(), event.Repository)
	if err != nil {
		return httpx.Internal(err)
	}
	results := make([]map[string]any, 0, len(triggers))
	accepted := 0
	for index := range triggers {
		trigger := &triggers[index]
		result := map[string]any{"triggerId": trigger.ID, "environmentId": trigger.EnvironmentID, "name": trigger.Name}
		outcome, err := s.dispatchAutomationEvent(r.Context(), trigger, event, body)
		if err != nil {
			result["accepted"], result["reason"] = false, "error"
			var refusal *httpx.APIError
			if errors.As(err, &refusal) {
				result["reason"] = refusal.Code
			}
		} else {
			for key, value := range outcome.payload {
				result[key] = value
			}
			if outcome.payload["accepted"] == true {
				accepted++
			}
		}
		results = append(results, result)
	}
	httpx.SetAudit(r, "deploy.githubapp.delivery", event.Repository, map[string]any{"deliveryId": event.DeliveryID, "event": event.Event, "triggers": len(triggers), "accepted": accepted})
	httpx.JSON(w, http.StatusAccepted, map[string]any{"repository": event.Repository, "deliveryId": event.DeliveryID, "event": event.Event, "triggers": results})
	return nil
}
