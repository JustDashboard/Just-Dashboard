package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountBlueprintRoutes(r chi.Router) {
	r.Route("/blueprints", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleBlueprintList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleBlueprintGet))
		r.Method(http.MethodGet, "/{id}/versions", s.handle(s.handleBlueprintVersions))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin), httpx.RequireSession)
			// Rendering is a pure preview. It is behind admin anyway because the
			// plan it returns is the plan the operator is about to commit.
			r.Method(http.MethodPost, "/{id}/render", s.handle(s.handleBlueprintRender))
		})
	})
}

func mapBlueprintError(err error) error {
	switch {
	case errors.Is(err, blueprint.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, blueprint.ErrInvalidInput):
		return httpx.Err(http.StatusBadRequest, "invalid_blueprint_input", err.Error())
	case errors.Is(err, blueprint.ErrInvalidBlueprint), errors.Is(err, blueprint.ErrUnknownField):
		return httpx.Internal(err)
	case errors.Is(err, gameserver.ErrUnsupportedGame):
		return httpx.Err(http.StatusNotFound, "unsupported_game", err.Error())
	case errors.Is(err, gameserver.ErrCommandRefused):
		return httpx.Err(http.StatusBadRequest, "command_refused", err.Error())
	case errors.Is(err, gameserver.ErrConsoleUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "console_unavailable", err.Error())
	case errors.Is(err, gameserver.ErrInvalidProperty):
		return httpx.Err(http.StatusBadRequest, "invalid_property", err.Error())
	case errors.Is(err, gameserver.ErrInvalidArchive), errors.Is(err, gameserver.ErrNoServerFound):
		return httpx.Err(http.StatusBadRequest, "invalid_import", err.Error())
	default:
		return mapDeployError(err)
	}
}

func (s *Server) handleBlueprintList(w http.ResponseWriter, r *http.Request) error {
	summaries, err := blueprint.Summaries()
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.JSON(w, http.StatusOK, summaries)
	return nil
}

func (s *Server) handleBlueprintGet(w http.ResponseWriter, r *http.Request) error {
	found, err := blueprint.Get(chi.URLParam(r, "id"))
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.JSON(w, http.StatusOK, struct {
		*blueprint.Blueprint
		DeploymentSupported bool   `json:"deploymentSupported"`
		UnavailableReason   string `json:"unavailableReason"`
	}{Blueprint: found, UnavailableReason: blueprint.DeploymentUnavailableReason})
	return nil
}

// handleBlueprintVersions answers with the upstream versions a game blueprint
// can be deployed at. A non-game blueprint has no version list of its own; its
// image tag policy is the answer.
func (s *Server) handleBlueprintVersions(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	found, err := blueprint.Get(id)
	if err != nil {
		return mapBlueprintError(err)
	}
	if found.Profile != blueprint.ProfileGame {
		httpx.JSON(w, http.StatusOK, gameserver.VersionList{
			Status: "unavailable", Source: found.Provenance.UpstreamURL, Versions: []gameserver.Version{},
			Reason: "This blueprint follows its image tag policy rather than an upstream version list.",
		})
		return nil
	}
	list, err := s.modules.gameVersions.Versions(r.Context(), id)
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

type blueprintRenderRequest struct {
	Name   string            `json:"name"`
	Inputs map[string]string `json:"inputs"`
}

func (s *Server) handleBlueprintRender(w http.ResponseWriter, r *http.Request) error {
	var request blueprintRenderRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	found, err := blueprint.Get(chi.URLParam(r, "id"))
	if err != nil {
		return mapBlueprintError(err)
	}
	plan, err := deploy.RenderBlueprintPlan(deploy.DraftSourceConfig{
		Kind: deploy.SourceBlueprint, Mode: deploy.SourceModeBlueprint,
		BlueprintID: found.ID, BlueprintVersion: found.Version, BlueprintInputs: request.Inputs,
	}, request.Name)
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}
