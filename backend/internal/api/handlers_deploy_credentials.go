package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// mapCredentialError adds the credential-specific sentinels to the shared
// planning error surface: a bad shape is the same "this request cannot work"
// class as an invalid source, and everything else a credential route can
// fail with (revision-free, so none of the draft/environment cases apply)
// already has a home in mapDeploymentPlanningError's default internal case.
func mapCredentialError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrCredentialNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, deploy.ErrCredentialInUse):
		return httpx.Err(http.StatusConflict, "credential_in_use", err.Error())
	case errors.Is(err, deploy.ErrCredentialNameTaken):
		return httpx.Err(http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, deploy.ErrInvalidCredential):
		return httpx.Err(http.StatusBadRequest, "invalid_credential", err.Error())
	default:
		return mapDeploymentPlanningError(err)
	}
}

func (s *Server) handleDeploymentCredentials(w http.ResponseWriter, r *http.Request) error {
	items, err := s.modules.deployPlanning.ListCredentials(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, items)
	return nil
}

func (s *Server) handleDeploymentCredentialCreate(w http.ResponseWriter, r *http.Request) error {
	var request deploy.CredentialCreateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	credential, err := s.modules.deployPlanning.CreateCredential(r.Context(), request)
	if err != nil {
		return mapCredentialError(err)
	}
	httpx.SetAudit(r, "deploy.credential.create", credential.Name, map[string]any{"kind": credential.Kind})
	httpx.JSON(w, http.StatusCreated, credential)
	return nil
}

func (s *Server) handleDeploymentCredentialUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var request deploy.CredentialUpdateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	credential, err := s.modules.deployPlanning.UpdateCredential(r.Context(), id, request)
	if err != nil {
		return mapCredentialError(err)
	}
	httpx.SetAudit(r, "deploy.credential.update", credential.Name, map[string]any{
		"secretChanged": request.Secret != nil,
	})
	httpx.JSON(w, http.StatusOK, credential)
	return nil
}

func (s *Server) handleDeploymentCredentialDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if err := s.modules.deployPlanning.DeleteCredential(r.Context(), id); err != nil {
		return mapCredentialError(err)
	}
	httpx.SetAudit(r, "deploy.credential.delete", fmt.Sprint(id), nil)
	httpx.NoContent(w)
	return nil
}

// handleDeploymentCredentialTest resolves the credential first so a mistyped
// id answers 404 rather than a misleading connectivity failure, then runs
// the actual probe through the source adapter's own isolation. A probe that
// runs but fails (bad token, wrong repository, unreachable host) is still a
// 200 with ok:false — the route answered the question it was asked.
func (s *Server) handleDeploymentCredentialTest(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var request deploy.CredentialTestRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	credential, err := s.modules.deployPlanning.GetCredential(r.Context(), id)
	if err != nil {
		return mapCredentialError(err)
	}
	ok, message, err := s.modules.deploySources.TestCredential(r.Context(), id, request.Repository)
	if err != nil {
		return mapCredentialError(err)
	}
	httpx.SetAudit(r, "deploy.credential.test", credential.Name, map[string]any{"kind": credential.Kind, "ok": ok})
	httpx.JSON(w, http.StatusOK, deploy.CredentialTestResult{OK: ok, Message: message})
	return nil
}
