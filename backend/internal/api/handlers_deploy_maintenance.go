package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// An environment's maintenance switch and the pages its route answers with.
// The switch is a file beside the live route that the route reads on every
// request (proxysvc.SetDeploymentMaintenance), so these act at once and
// never write the route: that stays the release's, written by activation.

// deploymentRouteFor is the environment's route name, refusing an environment
// that is not this project's.
func (s *Server) deploymentRouteFor(r *http.Request) (projectID, environmentID int64, name string, err error) {
	projectID, environmentID, err = deploymentEnvironmentIDs(r)
	if err != nil {
		return 0, 0, "", err
	}
	name, err = s.modules.deployPlanning.EnvironmentRouteName(r.Context(), projectID, environmentID)
	if err != nil {
		return 0, 0, "", mapDeploymentPlanningError(err)
	}
	if s.modules.proxy == nil {
		return 0, 0, "", httpx.Err(http.StatusServiceUnavailable, "no_proxy", "the proxy is unavailable on this host")
	}
	return projectID, environmentID, name, nil
}

// handleDeploymentMaintenance reads where the switch stands: whether the
// live route can read it, and whether it is on.
func (s *Server) handleDeploymentMaintenance(w http.ResponseWriter, r *http.Request) error {
	_, _, name, err := s.deploymentRouteFor(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	state, err := s.modules.proxy.DeploymentMaintenance(ctx, name)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

// handleDeploymentMaintenanceSet turns the switch on or off. It takes the
// application away from its visitors, as a Proxy site's maintenance does,
// so it is behind the same gate.
func (s *Server) handleDeploymentMaintenanceSet(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, name, err := s.deploymentRouteFor(r)
	if err != nil {
		return err
	}
	var req maintenanceSwitchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	state, err := s.modules.proxy.SetDeploymentMaintenance(ctx, name, req.On)
	detail := map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "on": req.On, "driver": state.Driver,
	}
	target := strconv.FormatInt(environmentID, 10)
	if err != nil {
		if errors.Is(err, proxysvc.ErrMaintenanceUnready) {
			detail["result"] = "unready"
			httpx.SetAudit(r, "deploy.maintenance", target, detail)
			if !state.Route {
				return httpx.Err(http.StatusConflict, "no_public_route",
					"This environment has no public route yet: add a domain and deploy, and maintenance can be switched from then on.")
			}
			return httpx.Err(http.StatusConflict, "maintenance_unready",
				"The live release's route was written before maintenance could be switched. Deploy once, and from then on it switches at once.")
		}
		detail["result"] = "failed"
		httpx.SetAudit(r, "deploy.maintenance", target, detail)
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "deploy.maintenance", target, detail)
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

// handleDeploymentPageGet reads one of the route's pages. A page is what
// every visitor may be shown, so reading one needs no more than reading the
// deployment.
func (s *Server) handleDeploymentPageGet(w http.ResponseWriter, r *http.Request) error {
	_, _, name, err := s.deploymentRouteFor(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	page, err := s.modules.proxy.ReadDeploymentPage(ctx, name, chi.URLParam(r, "page"))
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

// handleDeploymentPagePut saves one of the route's pages, served from the
// next answer that uses it.
func (s *Server) handleDeploymentPagePut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, name, err := s.deploymentRouteFor(r)
	if err != nil {
		return err
	}
	var req sitePageWrite
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	pageName := chi.URLParam(r, "page")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail := map[string]any{"deploymentId": projectID, "environmentId": environmentID, "page": pageName}
	page, err := s.modules.proxy.WriteDeploymentPage(ctx, name, pageName, req.Content)
	if err != nil {
		detail["result"] = "failed"
		httpx.SetAudit(r, "deploy.page", strconv.FormatInt(environmentID, 10), detail)
		return mapProxyError(err)
	}
	detail["bytes"] = len(req.Content)
	httpx.SetAudit(r, "deploy.page", strconv.FormatInt(environmentID, 10), detail)
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

// maintenanceSwitchRequest switches the route's maintenance page on or off.
type maintenanceSwitchRequest struct {
	On bool `json:"on"`
}

// sitePageWrite replaces one of the route's own pages.
type sitePageWrite struct {
	Content string `json:"content"`
}
