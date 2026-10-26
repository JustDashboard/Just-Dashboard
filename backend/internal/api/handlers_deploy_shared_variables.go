package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Shared variables are answered at two addresses with one set of handlers:
// /deploy/shared-variables for the server's own, and
// /deploy/{id}/shared-variables for one project's. The scope is the project
// id in the address, 0 when there is none.

func mapSharedVariableError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrSharedVariableNotFound):
		return httpx.Err(http.StatusNotFound, "shared_variable_not_found", err.Error())
	case errors.Is(err, deploy.ErrSharedVariableInUse):
		return httpx.Err(http.StatusConflict, "shared_variable_in_use", err.Error())
	default:
		return mapDeploymentPlanningError(err)
	}
}

// sharedVariableProject reads the scope from the address: a project's id
// where the route has one, and 0, the server's, where it does not.
func sharedVariableProject(r *http.Request) (int64, error) {
	if chi.URLParam(r, "id") == "" {
		return 0, nil
	}
	return parseID(r)
}

// sharedVariableAudit is what every entry about a shared variable names
// besides its name: which scope it lives in.
func sharedVariableAudit(projectID int64, detail map[string]any) map[string]any {
	if detail == nil {
		detail = map[string]any{}
	}
	if projectID == 0 {
		detail["scope"] = deploy.SharedScopeServer
	} else {
		detail["scope"] = deploy.SharedScopeProject
		detail["deploymentId"] = projectID
	}
	return detail
}

func (s *Server) handleSharedVariables(w http.ResponseWriter, r *http.Request) error {
	projectID, err := sharedVariableProject(r)
	if err != nil {
		return err
	}
	variables, err := s.modules.deployPlanning.ListSharedVariables(r.Context(), projectID)
	if err != nil {
		return mapSharedVariableError(err)
	}
	httpx.JSON(w, http.StatusOK, variables)
	return nil
}

func (s *Server) handleSharedVariablePut(w http.ResponseWriter, r *http.Request) error {
	projectID, err := sharedVariableProject(r)
	if err != nil {
		return err
	}
	var request deploy.SharedVariableWrite
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	variable, change, err := s.modules.deployPlanning.PutSharedVariable(
		r.Context(), projectID, name, httpx.MustPrincipal(r).Username(), request,
	)
	if err != nil {
		return mapSharedVariableError(err)
	}
	action, status := "deploy.shared_variable.update", http.StatusOK
	if change.Created {
		action, status = "deploy.shared_variable.create", http.StatusCreated
	}
	httpx.SetAudit(r, action, name, sharedVariableAudit(projectID, map[string]any{
		"sensitivity": request.Sensitivity, "valueChanged": change.ValueChanged,
		"usedBy": len(variable.UsedBy),
	}))
	httpx.JSON(w, status, variable)
	return nil
}

func (s *Server) handleSharedVariableReveal(w http.ResponseWriter, r *http.Request) error {
	projectID, err := sharedVariableProject(r)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	value, err := s.modules.deployPlanning.RevealSharedVariable(r.Context(), projectID, name)
	if err != nil {
		return mapSharedVariableError(err)
	}
	// A GET, which the mutation audit skips: the disclosure is recorded here,
	// without its value.
	s.recordAudit(r, "deploy.shared_variable.reveal", name, sharedVariableAudit(projectID, nil))
	httpx.JSON(w, http.StatusOK, value)
	return nil
}

func (s *Server) handleSharedVariableDelete(w http.ResponseWriter, r *http.Request) error {
	projectID, err := sharedVariableProject(r)
	if err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
	if err := s.modules.deployPlanning.DeleteSharedVariable(r.Context(), projectID, name); err != nil {
		return mapSharedVariableError(err)
	}
	httpx.SetAudit(r, "deploy.shared_variable.delete", name, sharedVariableAudit(projectID, nil))
	httpx.NoContent(w)
	return nil
}
