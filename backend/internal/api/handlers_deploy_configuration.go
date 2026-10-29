package api

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
	"github.com/go-chi/chi/v5"
)

func deploymentEnvironmentIDs(r *http.Request) (int64, int64, error) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || projectID <= 0 {
		return 0, 0, httpx.BadRequest("deployment id must be a positive integer")
	}
	environmentID, err := strconv.ParseInt(chi.URLParam(r, "env"), 10, 64)
	if err != nil || environmentID <= 0 {
		return 0, 0, httpx.BadRequest("environment id must be a positive integer")
	}
	return projectID, environmentID, nil
}

func (s *Server) handleDeploymentArchive(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Archive(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	httpx.SetAudit(r, "deploy.archive", project.Name, map[string]any{
		"deploymentId": projectID, "resourcesRemoved": false, "schedulesDisabled": true,
	})
	httpx.JSON(w, http.StatusOK, project)
	return nil
}

// handleDeploymentUnarchive restores the display name Archive tombstoned and
// clears the archived marker. Triggers and schedules are left exactly as
// Archive set them — disabled — so a project does not come back auto-firing;
// the operator re-enables what they want running.
func (s *Server) handleDeploymentUnarchive(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	project, err := s.modules.deployStore.Unarchive(r.Context(), projectID)
	if err != nil {
		return mapDeployError(err)
	}
	s.enrichProject(r, project)
	httpx.SetAudit(r, "deploy.unarchive", project.Name, map[string]any{"deploymentId": projectID})
	httpx.JSON(w, http.StatusOK, project)
	return nil
}

func (s *Server) handleDeploymentRemovalPlan(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	plan, err := s.modules.deployPlanning.RemovalPlan(r.Context(), projectID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.removal.preview", strconv.FormatInt(projectID, 10), map[string]any{
		"targetCount": len(plan.Targets), "archived": plan.Archived, "digest": plan.Digest,
	})
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

func (s *Server) handleDeploymentRemoveManaged(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	var request deploy.RemoveManagedRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	plan, err := s.modules.deployPlanning.RemovalPlan(r.Context(), projectID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	selected := map[string]bool{}
	for _, id := range request.TargetIDs {
		selected[id] = true
	}
	stacks := []deploy.RemovalTarget{}
	principal := httpx.MustPrincipal(r)
	for _, target := range plan.Targets {
		if !selected[target.ID] {
			continue
		}
		if target.RequiresAdmin && !principal.Can(auth.CapSystemAdmin) {
			return httpx.Err(http.StatusForbidden, "advanced_authorization_required", "this removal target requires system administration")
		}
		if target.Kind == "compose_stack" {
			stacks = append(stacks, target)
		}
	}
	if len(stacks) > 1 {
		return httpx.BadRequest("remove stacks one at a time so each exact name is confirmed")
	}
	if len(stacks) == 1 {
		if err := httpx.RequireTypedConfirmation(w, r, stacks[0].ResourceID); err != nil {
			return err
		}
	}
	remover := newDeploymentResourceRemover(s, principal.Username())
	execution, err := s.modules.deployPlanning.RemoveManaged(
		r.Context(), projectID, principal.Username(), request, remover,
	)
	// A removal-plan or precondition failure (not archived, stale digest, bad
	// target) has no partial execution at all; only a RemovalFailure from the
	// remover itself carries one worth showing beside the error.
	var removalFailure *deploy.RemovalFailure
	if err != nil && !errors.As(err, &removalFailure) {
		return mapDeploymentPlanningError(err)
	}
	targets := make([]map[string]string, 0, len(execution.Removed))
	for _, target := range execution.Removed {
		targets = append(targets, map[string]string{"kind": target.Kind, "resourceId": target.ResourceID})
	}
	detail := map[string]any{"targets": targets, "remaining": len(execution.Remaining)}
	if len(remover.ignored) > 0 {
		// Removing a connection that was the last one to a found server also
		// tells discovery to leave that server alone from now on.
		detail["ignoredServers"] = remover.ignored
	}
	if err == nil {
		httpx.SetAudit(r, "deploy.resources.remove", strconv.FormatInt(projectID, 10), detail)
		httpx.JSON(w, http.StatusOK, execution)
		return nil
	}
	// The remover's own sentence, and everything it already removed before
	// hitting this target, travel with the error instead of being lost to a
	// bare 500.
	var apiErr *httpx.APIError
	if !errors.As(mapDeploymentPlanningError(err), &apiErr) {
		return httpx.Internal(err)
	}
	detail["error"] = apiErr.Message
	httpx.SetAudit(r, "deploy.resources.remove", strconv.FormatInt(projectID, 10), detail)
	httpx.JSON(w, apiErr.Status, map[string]any{"error": apiErr, "execution": execution})
	return nil
}

func (s *Server) handleDeploymentVariables(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	variables, err := s.modules.deployPlanning.ListVariables(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.JSON(w, http.StatusOK, variables)
	return nil
}

func (s *Server) handleDeploymentVariableReveal(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	value, err := s.modules.deployPlanning.RevealVariable(r.Context(), projectID, environmentID, name)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	// Secret reads use GET, so the mutation audit middleware intentionally skips
	// them. Record the privileged disclosure immediately without its value.
	s.recordAudit(r, "deploy.variable.reveal", name, map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
	})
	httpx.JSON(w, http.StatusOK, value)
	return nil
}

func (s *Server) handleDeploymentVariablePut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request deploy.VariableWriteRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	principal := httpx.MustPrincipal(r)
	if principal.DeployKey() && request.Reference != "" {
		previous := ""
		variables, err := s.modules.deployPlanning.ListVariables(r.Context(), projectID, environmentID)
		if err != nil {
			return mapDeploymentPlanningError(err)
		}
		for _, variable := range variables {
			if variable.Name == name {
				previous = variableReference(variable)
			}
		}
		if err := keyReferenceGuard(principal, name, request.Reference, previous); err != nil {
			return err
		}
	}
	result, err := s.modules.deployPlanning.PutVariable(
		r.Context(), projectID, environmentID, name, principal.Username(), request,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.variable.update", name, map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": result.DesiredRevision, "sensitivity": request.Sensitivity,
		"scopes": request.Scopes, "reference": request.Reference != "",
	})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleDeploymentVariableGenerate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request variableGenerateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	principal := httpx.MustPrincipal(r)
	result, err := s.modules.deployPlanning.GenerateVariable(
		r.Context(), projectID, environmentID, name, principal.Username(), request.Revision, request.Scopes,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.variable.generate", name, map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": result.DesiredRevision, "scopes": request.Scopes,
	})
	withholdGeneratedValue(principal, result)
	httpx.JSON(w, http.StatusCreated, result)
	return nil
}

func (s *Server) handleDeploymentVariableRotate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request variableRevisionRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	principal := httpx.MustPrincipal(r)
	result, err := s.modules.deployPlanning.RotateVariable(
		r.Context(), projectID, environmentID, name, principal.Username(), request.Revision,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.variable.rotate", name, map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": result.DesiredRevision,
	})
	withholdGeneratedValue(principal, result)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleDeploymentDotenvImport(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request deploy.DotenvImportRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	// A preview that failed to parse as one must never fall through to the
	// import, so anything but a boolean is refused rather than read as false.
	if raw := r.URL.Query().Get("dryRun"); raw != "" {
		dryRun, err := strconv.ParseBool(raw)
		if err != nil {
			return httpx.BadRequest("dryRun must be 1 or 0")
		}
		if dryRun {
			preview, err := s.modules.deployPlanning.PreviewDotenvImport(r.Context(), projectID, environmentID, request)
			if err != nil {
				return mapDeploymentPlanningError(err)
			}
			httpx.SetAudit(r, "deploy.variable.import_preview", strconv.Itoa(len(preview.Variables)), map[string]any{
				"deploymentId": projectID, "environmentId": environmentID,
				"sensitivity": request.Sensitivity, "scopes": request.Scopes,
			})
			httpx.JSON(w, http.StatusOK, preview)
			return nil
		}
	}
	principal := httpx.MustPrincipal(r)
	result, err := s.modules.deployPlanning.ImportDotenv(
		r.Context(), projectID, environmentID, principal.Username(), request,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.variable.import", strconv.Itoa(len(result.Variables)), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID,
		"revision": result.DesiredRevision, "sensitivity": request.Sensitivity,
		"scopes": request.Scopes,
	})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleDeploymentVariableDelete(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request variableRevisionRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	revision, err := s.modules.deployPlanning.DeleteVariable(
		r.Context(), projectID, environmentID, name, request.Revision,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.SetAudit(r, "deploy.variable.delete", name, map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "revision": revision,
	})
	httpx.JSON(w, http.StatusOK, map[string]int{"desiredRevision": revision})
	return nil
}

func (s *Server) handleDeploymentPending(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	state, err := s.modules.deployPlanning.PendingState(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

func (s *Server) handleDeploymentConfiguration(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	httpx.JSON(w, http.StatusOK, configuration)
	return nil
}

func (s *Server) handleDeploymentConfigurationSave(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request deploy.ConfigurationWriteRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if httpx.MustPrincipal(r).DeployKey() {
		current, err := s.keyEnvironmentGuard(r.Context(), projectID, environmentID)
		if err != nil {
			return err
		}
		if reasons := hostAuthority(current.Source, request.Build, request.Runtime); len(reasons) > 0 {
			return refuseHostAuthority("this configuration", reasons)
		}
		// Who may open a tailnet-only deployment is a person's decision: a
		// key that could change it could let anyone on the tailnet in, or
		// take the deployment off its public route.
		if !sameTailnetPublication(current.Runtime.Tailnet, request.Runtime.Tailnet) {
			return refuseForDeployKey("a deploy key cannot change whether a deployment is tailnet-only or who on the tailnet may open it")
		}
	}
	if err := s.refuseSleepWithContainerSchedules(r.Context(), projectID, environmentID, request.Runtime); err != nil {
		return err
	}
	configuration, err := s.modules.deployPlanning.SaveEnvironmentConfiguration(
		r.Context(), projectID, environmentID, request,
	)
	if err != nil {
		return mapDeploymentPlanningError(err)
	}
	detail := map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "revision": configuration.Revision,
		"domains": len(configuration.Domains), "dependencies": len(configuration.Dependencies),
		"checks": len(configuration.Checks), "publicBind": request.Runtime.BindAddress == "0.0.0.0" || request.Runtime.BindAddress == "::",
	}
	if tailnet := configuration.Runtime.Tailnet; tailnet != nil {
		// The list itself, so the trail says who was let in and when.
		detail["tailnet"] = map[string]any{"allow": append([]string{}, tailnet.Allow...)}
	}
	httpx.SetAudit(r, "deploy.configuration.update", strconv.FormatInt(environmentID, 10), detail)
	httpx.JSON(w, http.StatusOK, configuration)
	return nil
}

// withholdGeneratedValue keeps a generated secret from a deploy key: variables
// are write-only to it, a value it did not send as much as one it did. The
// variable is generated and set all the same; a person reads it in the
// dashboard, and the deployment reads it at run time.
func withholdGeneratedValue(p *httpx.Principal, result *deploy.VariableMutationResult) {
	if p.DeployKey() {
		result.GeneratedValue = ""
	}
}

// variableRevisionRequest names the environment revision a variable write
// was read at, so a write over a change somebody else made is refused.
type variableRevisionRequest struct {
	Revision int `json:"revision"`
}

// variableGenerateRequest asks for a generated secret in the given scopes.
type variableGenerateRequest struct {
	Revision int      `json:"revision"`
	Scopes   []string `json:"scopes"`
}

// sameTailnetPublication says two plans publish on the tailnet alike: both
// not at all, or both tailnet-only admitting the same logins and tags.
func sameTailnetPublication(a, b *deploy.RouteTailnet) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	canonical := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, entry := range list {
			if value, _, err := tailnetgate.Canonical(entry); err == nil {
				entry = value
			}
			out = append(out, entry)
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(canonical(a.Allow), canonical(b.Allow))
}
