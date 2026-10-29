package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The routes of data-aware rollback (deploy/data_rollback.go). Listing an
// environment's copies is a read like its releases are, and carries no
// credential. Asking for a copy, reading its address, keeping it longer,
// removing it and comparing it with the live database are an administrator's
// in a session: a copy is a second copy of production's data, and its address
// carries a password. None of them is destructive — nothing production holds
// is written — so none spends the destructive budget; restoring a point into
// production itself is the Databases' own restore, on its own routes, with its
// own confirmation.

func mapDataCopyError(err error) error {
	switch {
	case errors.Is(err, deploy.ErrDataCopyUnavailable):
		return httpx.Err(http.StatusConflict, "data_copy_unavailable", err.Error())
	case errors.Is(err, deploy.ErrDataCopyExists):
		return httpx.Err(http.StatusConflict, "data_copy_exists", err.Error())
	case errors.Is(err, deploy.ErrDataCopyNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, deploy.ErrDataCopyNotReady):
		return httpx.Err(http.StatusConflict, "data_copy_not_ready", "the copy is not ready: it is still waiting or being restored, or it has ended")
	case errors.Is(err, deploy.ErrDataCopyEnded):
		return httpx.Err(http.StatusConflict, "data_copy_ended", err.Error())
	case errors.Is(err, deploy.ErrInvalidPlan):
		return httpx.BadRequest("%s", err.Error())
	}
	return mapDeployError(err)
}

// dataCopyOf is the copy a route names, held to its deployment and
// environment.
func (s *Server) dataCopyOf(r *http.Request) (*deploy.DataCopy, error) {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return nil, err
	}
	copyID, err := strconv.ParseInt(chi.URLParam(r, "copy"), 10, 64)
	if err != nil || copyID <= 0 {
		return nil, httpx.BadRequest("copy id must be a positive integer")
	}
	copy, err := s.modules.deployRuns.DataCopy(r.Context(), copyID)
	if err != nil {
		return nil, mapDataCopyError(err)
	}
	if copy.ProjectID != projectID || copy.EnvironmentID != environmentID {
		return nil, httpx.ErrNotFound
	}
	return copy, nil
}

func (s *Server) handleDeploymentDataCopies(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	if _, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID); err != nil {
		return mapDeployError(err)
	}
	filter := deploy.DataCopyFilter{}
	for name, into := range map[string]*int64{"run": &filter.RunID, "release": &filter.ReleaseID} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				return httpx.BadRequest("%s must be a positive id", name)
			}
			*into = value
		}
	}
	copies, err := s.modules.deployRuns.DataCopies(r.Context(), environmentID, filter)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, copies)
	return nil
}

type dataCopyRequest struct {
	// AfterRunID makes the copy wait for that run — the rollback the same
	// dialog started — and restore only once it succeeded.
	AfterRunID int64 `json:"afterRunId,omitempty"`
}

func (s *Server) handleDeploymentDataCopyCreate(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	releaseID, err := strconv.ParseInt(chi.URLParam(r, "release"), 10, 64)
	if err != nil || releaseID <= 0 {
		return httpx.BadRequest("release id must be a positive integer")
	}
	var req dataCopyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.AfterRunID < 0 {
		return httpx.BadRequest("afterRunId must be a positive run id")
	}
	release, err := s.modules.deployRuns.Release(r.Context(), releaseID)
	if err != nil {
		return mapDeployError(err)
	}
	if release.Release.ProjectID != projectID || release.Release.EnvironmentID != environmentID {
		return httpx.ErrNotFound
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket to start a copy with")
	}
	copies, err := s.modules.deployRuns.CreateDataCopies(r.Context(), release.Release, req.AfterRunID, httpx.MustPrincipal(r).Username())
	if err != nil {
		httpx.SetAudit(r, "deploy.release.data_copy", strconv.FormatInt(releaseID, 10), map[string]any{
			"deploymentId": projectID, "environmentId": environmentID, "afterRunId": req.AfterRunID, "error": err.Error(),
		})
		return mapDataCopyError(err)
	}
	ids := make([]int64, 0, len(copies))
	connections := make([]int64, 0, len(copies))
	for _, copy := range copies {
		ids, connections = append(ids, copy.ID), append(connections, copy.ConnectionID)
	}
	httpx.SetAudit(r, "deploy.release.data_copy", strconv.FormatInt(releaseID, 10), map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "releaseNumber": release.Release.Number,
		"afterRunId": req.AfterRunID, "copies": ids, "connections": connections,
	})
	s.modules.dataCopies.Poke()
	httpx.JSON(w, http.StatusAccepted, copies)
	return nil
}

func (s *Server) handleDeploymentDataCopyURL(w http.ResponseWriter, r *http.Request) error {
	copy, err := s.dataCopyOf(r)
	if err != nil {
		return err
	}
	if copy.State != deploy.DataCopyReady {
		return mapDataCopyError(deploy.ErrDataCopyNotReady)
	}
	sealed, err := s.modules.deployRuns.DataCopySecret(r.Context(), copy.ID)
	if err != nil {
		return mapDataCopyError(err)
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return httpx.Internal(err)
	}
	var secret dataCopySecret
	if err := json.Unmarshal([]byte(plain), &secret); err != nil {
		return httpx.Internal(err)
	}
	// A GET, so the mutation audit leaves it out: the disclosure is
	// recorded here, without the password.
	s.recordAudit(r, "deploy.data_copy.reveal", strconv.FormatInt(copy.ID, 10), map[string]any{
		"deploymentId": copy.ProjectID, "environmentId": copy.EnvironmentID, "releaseId": copy.ReleaseID,
		"connectionId": copy.ConnectionID, "host": copy.Host,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"url": secret.URL, "host": copy.Host, "port": copy.Port, "user": secret.User, "database": copy.Database,
	})
	return nil
}

type dataCopyExtendRequest struct {
	Hours int `json:"hours"`
}

func (s *Server) handleDeploymentDataCopyExtend(w http.ResponseWriter, r *http.Request) error {
	copy, err := s.dataCopyOf(r)
	if err != nil {
		return err
	}
	var req dataCopyExtendRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	extended, err := s.modules.deployRuns.ExtendDataCopy(r.Context(), copy.ID, req.Hours)
	if err != nil {
		return mapDataCopyError(err)
	}
	httpx.SetAudit(r, "deploy.data_copy.extend", strconv.FormatInt(copy.ID, 10), map[string]any{
		"deploymentId": copy.ProjectID, "environmentId": copy.EnvironmentID, "hours": req.Hours,
		"expiresAt": extended.ExpiresAt.Format(time.RFC3339),
	})
	httpx.JSON(w, http.StatusOK, extended)
	return nil
}

func (s *Server) handleDeploymentDataCopyRemove(w http.ResponseWriter, r *http.Request) error {
	copy, err := s.dataCopyOf(r)
	if err != nil {
		return err
	}
	removed, err := s.modules.dataCopies.Remove(r.Context(), *copy, "removed by "+httpx.MustPrincipal(r).Username(), httpx.MustPrincipal(r).Username())
	if removed == nil {
		return mapDataCopyError(err)
	}
	detail := map[string]any{"deploymentId": copy.ProjectID, "environmentId": copy.EnvironmentID, "container": copy.Container}
	if err != nil {
		// The row is ended either way; what Docker kept is the sweep's.
		detail["leftover"] = err.Error()
	}
	httpx.SetAudit(r, "deploy.data_copy.remove", strconv.FormatInt(copy.ID, 10), detail)
	httpx.JSON(w, http.StatusOK, removed)
	return nil
}

func (s *Server) handleDeploymentDataCopyComparison(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	copy, err := s.dataCopyOf(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	comparison, err := s.modules.dataCopies.compare(ctx, *copy)
	if err != nil {
		return mapDataCopyError(err)
	}
	httpx.JSON(w, http.StatusOK, comparison)
	return nil
}
