package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Templates added on this dashboard (deploy/user_templates.go): the
// operator's own definitions beside the built-ins, which the picker lists
// from GET /deploy/blueprints/ with the built-ins, each marked by its origin.
//
// Reading one is as open as reading the catalogue. Making, changing and
// importing one takes what a draft takes — an administrator, in a session —
// and a deploy key is kept from all of them: a template is the operator's
// catalogue, which every deployment chooses from, not a deployment a key is
// narrowed to. Deleting one is destructive with an ordinary confirmation,
// since the deployments made from it keep running from their own copy and
// the template is made again in a minute.

func (s *Server) mountTemplateReads(r chi.Router) {
	r.Method(http.MethodGet, "/templates", s.handle(s.handleTemplateList))
	r.Method(http.MethodGet, "/templates/{template}", s.handle(s.handleTemplateGet))
	r.Method(http.MethodGet, "/templates/{template}/export", s.handle(s.handleTemplateExport))
}

// mountTemplateWrites is mounted inside the administrator-and-session group
// the draft routes are in.
func (s *Server) mountTemplateWrites(r chi.Router) {
	r.Method(http.MethodPost, "/templates", s.handle(s.handleTemplateCreate))
	r.Method(http.MethodPost, "/templates/from-deployment", s.handle(s.handleTemplateFromDeployment))
	r.Method(http.MethodPost, "/templates/import", s.handle(s.handleTemplateImport))
	r.Method(http.MethodPut, "/templates/{template}", s.handle(s.handleTemplateUpdate))
}

// mountTemplateRemoval is mounted inside s.destructive.
func (s *Server) mountTemplateRemoval(r chi.Router) {
	r.With(httpx.RequireCapability(auth.CapSystemAdmin), httpx.RequireSession).
		Method(http.MethodDelete, "/templates/{template}", s.handle(s.handleTemplateDelete))
}

func mapTemplateError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrTemplateNotFound), errors.Is(err, blueprint.ErrNotFound):
		return httpx.Err(http.StatusNotFound, "template_not_found", err.Error())
	case errors.Is(err, deploy.ErrTemplateExists):
		return httpx.Err(http.StatusConflict, "template_exists", err.Error())
	case errors.Is(err, deploy.ErrInvalidTemplate):
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_template", deploy.TemplateRefusal(err))
	default:
		return mapDeploymentPlanningError(err)
	}
}

// templateAudit is what an entry says of a template: which it is and how it
// was made. A definition holds no value of any deployment's, but the entry
// names it rather than copying it.
func templateAudit(template deploy.UserTemplate, extra map[string]any) map[string]any {
	detail := map[string]any{
		"name": template.Name, "version": template.Version, "source": template.Source,
		"sourceKind": template.Summary.SourceKind,
	}
	if template.FromDeploymentID > 0 {
		detail["deploymentId"] = template.FromDeploymentID
	}
	for key, value := range extra {
		detail[key] = value
	}
	return detail
}

func (s *Server) handleTemplateList(w http.ResponseWriter, r *http.Request) error {
	templates, err := s.modules.deployPlanning.UserTemplates(r.Context())
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.JSON(w, http.StatusOK, templates)
	return nil
}

func (s *Server) handleTemplateGet(w http.ResponseWriter, r *http.Request) error {
	template, err := s.modules.deployPlanning.UserTemplate(r.Context(), chi.URLParam(r, "template"))
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.JSON(w, http.StatusOK, template)
	return nil
}

// handleTemplateExport answers a template's definition as a file: a
// built-in's or one added here, in the catalogue's own schema, so it can be
// imported into another dashboard (where it becomes that dashboard's own) or
// edited and imported again.
func (s *Server) handleTemplateExport(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "template")
	definition, err := s.modules.deployPlanning.ResolveTemplate(r.Context(), id, "")
	if err != nil {
		return mapTemplateError(err)
	}
	encoded, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return httpx.Internal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+definition.ID+`.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(encoded, '\n'))
	return nil
}

func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) error {
	var request deploy.ComposeTemplateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	actor := httpx.MustPrincipal(r).Username()
	if request.DryRun {
		preview, err := s.modules.deployPlanning.PreviewComposeTemplate(r.Context(), request, actor)
		if err != nil {
			return mapTemplateError(err)
		}
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, preview)
		return nil
	}
	created, err := s.modules.deployPlanning.CreateComposeTemplate(r.Context(), request, actor)
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.SetAudit(r, "deploy.template.create", created.ID, templateAudit(created.UserTemplate, nil))
	httpx.JSON(w, http.StatusCreated, created)
	return nil
}

// handleTemplateFromDeployment is Save as template: the definition the live
// release of a deployment's environment was deployed from, read from the
// plan that release froze (deploy.TemplateFromPlan).
func (s *Server) handleTemplateFromDeployment(w http.ResponseWriter, r *http.Request) error {
	var request deploy.DeploymentTemplateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.DeploymentID <= 0 {
		return httpx.BadRequest("deploymentId must be a positive integer")
	}
	ctx := r.Context()
	environmentID := request.EnvironmentID
	if environmentID == 0 {
		production, _, err := s.modules.deployRuns.ProductionEnvironment(ctx, request.DeploymentID)
		if err != nil {
			return mapDeployError(err)
		}
		environmentID = production
	} else {
		var one int
		err := s.Store.DB.QueryRowContext(ctx, `SELECT 1 FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
			environmentID, request.DeploymentID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.Err(http.StatusNotFound, "environment_not_found", "this deployment has no such environment")
		}
		if err != nil {
			return httpx.Internal(err)
		}
	}
	live, err := s.modules.deployRuns.LiveRelease(ctx, environmentID)
	if errors.Is(err, deploy.ErrArtifactMissing) {
		return httpx.Err(http.StatusConflict, "no_live_release",
			"this environment has not gone live yet; a template is saved from the plan a running release was deployed with")
	}
	if err != nil {
		return mapDeployError(err)
	}
	run, err := s.modules.deployRuns.Run(ctx, live.Release.RunID)
	if err != nil {
		return mapDeployError(err)
	}
	plan, err := s.modules.deployRuns.ExecutionPlan(ctx, *run)
	if err != nil {
		return mapDeployError(err)
	}
	actor := httpx.MustPrincipal(r).Username()
	definition, raw, err := s.modules.deployPlanning.DeploymentTemplate(ctx, plan, run.ID, environmentID, request, actor)
	if err != nil {
		return mapTemplateError(err)
	}
	if request.DryRun {
		preview, err := s.modules.deployPlanning.PreviewDeploymentTemplate(ctx, definition)
		if err != nil {
			return mapTemplateError(err)
		}
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, preview)
		return nil
	}
	created, err := s.modules.deployPlanning.SaveDeploymentTemplate(ctx, definition, raw, request.DeploymentID, actor)
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.SetAudit(r, "deploy.template.create", created.ID, templateAudit(created.UserTemplate, map[string]any{
		"environmentId": environmentID, "releaseId": live.Release.ID,
	}))
	httpx.JSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) handleTemplateImport(w http.ResponseWriter, r *http.Request) error {
	var raw json.RawMessage
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		return err
	}
	created, err := s.modules.deployPlanning.ImportTemplate(r.Context(), raw, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.SetAudit(r, "deploy.template.import", created.ID, templateAudit(created.UserTemplate, nil))
	httpx.JSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) handleTemplateUpdate(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "template")
	var raw json.RawMessage
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		return err
	}
	before, err := s.modules.deployPlanning.UserTemplate(r.Context(), id)
	if err != nil {
		return mapTemplateError(err)
	}
	updated, err := s.modules.deployPlanning.UpdateTemplate(r.Context(), id, raw, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.SetAudit(r, "deploy.template.update", id, templateAudit(updated.UserTemplate, map[string]any{
		"previousVersion": before.Version,
	}))
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) error {
	removed, err := s.modules.deployPlanning.DeleteTemplate(r.Context(), chi.URLParam(r, "template"))
	if err != nil {
		return mapTemplateError(err)
	}
	httpx.SetAudit(r, "deploy.template.delete", removed.ID, templateAudit(*removed, map[string]any{
		"deployments": removed.Deployments,
	}))
	w.WriteHeader(http.StatusNoContent)
	return nil
}
