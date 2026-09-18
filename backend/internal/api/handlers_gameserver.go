package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gameserver"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

const maxImportArchiveBytes = 2 << 30

func (s *Server) mountGameRoutes(r chi.Router) {
	r.Route("/{id}/game", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleGameOverview))
		r.Method(http.MethodGet, "/players", s.handle(s.handleGamePlayers))
		r.Method(http.MethodGet, "/properties", s.handle(s.handleGamePropertiesGet))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/console", s.handle(s.handleGameConsole))
			r.Method(http.MethodPost, "/players/{action}", s.handle(s.handleGamePlayerAction))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin), httpx.RequireSession)
			r.Method(http.MethodPut, "/properties", s.handle(s.handleGamePropertiesPut))
		})
	})
}

// gameContext resolves the one running container a game deployment's console,
// player list and settings act on. Every game route fails the same honest way
// when there is nothing live to talk to.
type gameContext struct {
	Summary     deploy.DeploymentSummary
	Definition  *blueprint.Blueprint
	ContainerID string
	Reason      string
}

func (s *Server) gameContext(r *http.Request) (*gameContext, error) {
	projectID, err := parseID(r)
	if err != nil {
		return nil, err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), projectID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return nil, mapDeployError(err)
	}
	if summary.Profile != deploy.ProfileGame {
		return nil, httpx.Err(http.StatusBadRequest, "not_a_game_server",
			"this deployment is not a game server")
	}
	result := &gameContext{Summary: *summary}
	if definition, blueprintErr := s.deploymentBlueprint(r.Context(), *summary); blueprintErr == nil {
		result.Definition = definition
	}
	var owner deploy.RuntimeObserver
	if s.modules.docker != nil {
		owner = s.modules.docker
	}
	runtime := deploy.ObserveRuntimeServices(r.Context(), owner, summary.EnvironmentID, summary.LiveReleaseID)
	if runtime.Status != "available" {
		result.Reason = runtime.Reason
		return result, nil
	}
	for _, service := range runtime.Services {
		if service.LiveRelease && service.State == "running" {
			result.ContainerID = service.ContainerID
			break
		}
	}
	if result.ContainerID == "" {
		result.Reason = "This game server has no running container of its live release. Start or redeploy it first."
	}
	return result, nil
}

// deploymentBlueprint reads which reviewed blueprint the live release came from.
// The declared properties in that blueprint are the only keys the settings
// editor will write.
func (s *Server) deploymentBlueprint(
	ctx context.Context,
	summary deploy.DeploymentSummary,
) (*blueprint.Blueprint, error) {
	if summary.LiveReleaseID <= 0 {
		return nil, errors.New("this deployment has no live release")
	}
	release, err := s.modules.deployRuns.Release(ctx, summary.LiveReleaseID)
	if err != nil {
		return nil, err
	}
	if release.Release.BlueprintID == "" {
		return nil, errors.New("this release did not come from a blueprint")
	}
	return blueprint.GetVersion(release.Release.BlueprintID, release.Release.BlueprintVersion)
}

func (s *Server) gameConsole() *gameserver.Console {
	if s.modules.docker == nil {
		return nil
	}
	return gameserver.NewConsole(s.modules.docker, nil)
}

type gameOverview struct {
	Status      string                 `json:"status"`
	Reason      string                 `json:"reason,omitempty"`
	BlueprintID string                 `json:"blueprintId,omitempty"`
	Edition     string                 `json:"edition,omitempty"`
	ContainerID string                 `json:"containerId,omitempty"`
	Address     string                 `json:"address,omitempty"`
	Players     *gameserver.Players    `json:"players,omitempty"`
	Console     bool                   `json:"console"`
	Files       []blueprint.ConfigFile `json:"files"`
}

func (s *Server) handleGameOverview(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	overview := gameOverview{Status: "available", Files: []blueprint.ConfigFile{}}
	if game.Definition != nil {
		overview.BlueprintID = game.Definition.ID
		overview.Files = game.Definition.Files
		overview.Edition = strings.TrimPrefix(game.Definition.ID, "minecraft-")
	}
	if game.ContainerID == "" {
		overview.Status, overview.Reason = "unavailable", game.Reason
		httpx.JSON(w, http.StatusOK, overview)
		return nil
	}
	overview.ContainerID, overview.Console = game.ContainerID, true
	if game.Summary.HostPort > 0 {
		overview.Address = fmt.Sprintf("%s:%d", r.Host, game.Summary.HostPort)
		if host, _, found := strings.Cut(r.Host, ":"); found {
			overview.Address = fmt.Sprintf("%s:%d", host, game.Summary.HostPort)
		}
	}
	if players := s.observePlayers(r.Context(), game); players != nil {
		overview.Players = players
	}
	httpx.JSON(w, http.StatusOK, overview)
	return nil
}

// observePlayers asks the game who is online. A game whose reply this dashboard
// cannot parse reports "not supported" rather than an empty player list, so the
// UI hides player controls instead of showing controls that do nothing.
func (s *Server) observePlayers(ctx context.Context, game *gameContext) *gameserver.Players {
	console := s.gameConsole()
	if console == nil || game.ContainerID == "" {
		return &gameserver.Players{
			Supported: false, Status: "unavailable", Names: []string{},
			Reason: "The game console is unavailable, so the online list cannot be read.",
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := console.Run(ctx, game.ContainerID, "list")
	if err != nil {
		return &gameserver.Players{
			Supported: false, Status: "unavailable", Names: []string{},
			Reason: "The server did not answer the player list command.",
		}
	}
	players, ok := gameserver.ParsePlayerList(result.Output)
	if !ok {
		return &gameserver.Players{
			Supported: false, Status: "unsupported", Names: []string{},
			Reason: "This server's reply is not a player list this dashboard can read. Use the console directly.",
		}
	}
	return &players
}

func (s *Server) handleGamePlayers(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.observePlayers(r.Context(), game))
	return nil
}

type gameConsoleRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleGameConsole(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	var request gameConsoleRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	console := s.gameConsole()
	if console == nil {
		return httpx.Err(http.StatusServiceUnavailable, "console_unavailable",
			"Docker is unavailable, so the game console cannot be reached")
	}
	result, err := console.Run(r.Context(), game.ContainerID, request.Command)
	if err != nil {
		return mapBlueprintError(err)
	}
	// The command is audited, not its output: a game's reply can name players
	// and coordinates that do not belong in a permanent audit record.
	httpx.SetAudit(r, "deploy.game.console", game.Summary.Name,
		map[string]any{"command": result.Command, "exitCode": result.ExitCode})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

type gamePlayerRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleGamePlayerAction(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	var request gamePlayerRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	command, err := gameserver.PlayerCommand(
		gameserver.PlayerAction(chi.URLParam(r, "action")), request.Name)
	if err != nil {
		return mapBlueprintError(err)
	}
	console := s.gameConsole()
	if console == nil {
		return httpx.Err(http.StatusServiceUnavailable, "console_unavailable",
			"Docker is unavailable, so the game console cannot be reached")
	}
	result, err := console.Run(r.Context(), game.ContainerID, command)
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.SetAudit(r, "deploy.game.player", game.Summary.Name,
		map[string]any{"action": chi.URLParam(r, "action"), "player": request.Name})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

type gameProperties struct {
	Status          string               `json:"status"`
	Reason          string               `json:"reason,omitempty"`
	Path            string               `json:"path,omitempty"`
	Raw             string               `json:"raw,omitempty"`
	Values          map[string]string    `json:"values,omitempty"`
	Known           []blueprint.Property `json:"known"`
	RestartRequired bool                 `json:"restartRequired,omitempty"`
}

// propertiesFile finds the blueprint's declared settings file. Only a declared
// file can be read or written here; there is no arbitrary container path.
func propertiesFile(definition *blueprint.Blueprint) (*blueprint.ConfigFile, error) {
	if definition == nil {
		return nil, errors.New("this deployment's blueprint is unknown, so its settings file is not declared")
	}
	for index := range definition.Files {
		if definition.Files[index].Format == "properties" {
			return &definition.Files[index], nil
		}
	}
	return nil, errors.New("this blueprint declares no structured settings file")
}

func (s *Server) handleGamePropertiesGet(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	result := gameProperties{Status: "unavailable", Known: []blueprint.Property{}}
	file, fileErr := propertiesFile(game.Definition)
	if fileErr != nil {
		result.Reason = fileErr.Error()
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	result.Path, result.Known = file.Path, file.Properties
	result.RestartRequired = file.RestartRequired
	if game.ContainerID == "" || s.modules.docker == nil {
		result.Reason = game.Reason
		if result.Reason == "" {
			result.Reason = "Docker is unavailable, so this file cannot be read."
		}
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	content, readErr := s.modules.docker.ReadContainerFile(r.Context(), game.ContainerID, file.Path)
	if readErr != nil {
		result.Reason = "The server has not written this file yet, or it could not be read."
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	result.Status = "available"
	allowed := make([]string, 0, len(file.Properties))
	for _, property := range file.Properties {
		allowed = append(allowed, property.Key)
	}
	result.Values, result.Raw = gameserver.PublicProperties(string(content), allowed)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

type gamePropertiesRequest struct {
	Changes map[string]string `json:"changes"`
}

func (s *Server) handleGamePropertiesPut(w http.ResponseWriter, r *http.Request) error {
	game, err := s.gameContext(r)
	if err != nil {
		return err
	}
	var request gamePropertiesRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if len(request.Changes) == 0 || len(request.Changes) > 64 {
		return httpx.BadRequest("between one and 64 settings can be changed at a time")
	}
	file, fileErr := propertiesFile(game.Definition)
	if fileErr != nil {
		return httpx.BadRequest("%s", fileErr.Error())
	}
	if game.ContainerID == "" || s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "runtime_unavailable",
			"this game server has no running container to write settings into")
	}
	content, err := s.modules.docker.ReadContainerFile(r.Context(), game.ContainerID, file.Path)
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "settings_unavailable",
			"the server has not written its settings file yet")
	}
	parsed := gameserver.ParseProperties(string(content))
	known := make([]gameserver.KnownProperty, 0, len(file.Properties))
	for _, property := range file.Properties {
		choices := make([]string, 0, len(property.Choices))
		for _, choice := range property.Choices {
			choices = append(choices, choice.Value)
		}
		known = append(known, gameserver.KnownProperty{
			Key: property.Key, Kind: string(property.Kind),
			Minimum: property.Minimum, Maximum: property.Maximum, Choices: choices,
		})
	}
	applied, err := gameserver.ApplyProperties(parsed, known, request.Changes)
	if err != nil {
		return mapBlueprintError(err)
	}
	if len(applied) == 0 {
		httpx.JSON(w, http.StatusOK, map[string]any{"applied": []string{}, "restartRequired": false})
		return nil
	}
	if err := s.modules.docker.WriteContainerFile(
		r.Context(), game.ContainerID, file.Path, []byte(parsed.Render()), 0o644); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "deploy.game.properties", game.Summary.Name, map[string]any{"keys": applied})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"applied": applied, "restartRequired": file.RestartRequired,
	})
	return nil
}

type gameImportRequest struct {
	Path string `json:"path"`
}

// handleGameImportPreview inspects an existing server without changing it.
// A directory is read in place through the Files module's own root rules; an
// uploaded archive is validated entry by entry before anything is extracted.
func (s *Server) handleGameImportPreview(w http.ResponseWriter, r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/zip") {
		return s.previewGameArchive(w, r)
	}
	var request gameImportRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if s.modules.files == nil {
		return httpx.Err(http.StatusServiceUnavailable, "files_unavailable",
			"the Files module is unavailable, so no directory can be inspected")
	}
	resolved, err := s.modules.files.Resolve(request.Path)
	if err != nil {
		return httpx.BadRequest("that path is outside the directories this dashboard may read")
	}
	preview, err := gameserver.PreviewDirectory(resolved)
	if err != nil {
		return mapBlueprintError(err)
	}
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

func (s *Server) previewGameArchive(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportArchiveBytes+1))
	if err != nil {
		return httpx.BadRequest("the archive could not be read")
	}
	if int64(len(body)) > maxImportArchiveBytes {
		return httpx.Err(http.StatusRequestEntityTooLarge, "archive_too_large",
			"server archives are limited to 2 GiB through this endpoint")
	}
	entries, root, err := gameserver.InspectArchive(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return mapBlueprintError(err)
	}
	paths := make([]string, 0, len(entries))
	var total int64
	for _, entry := range entries {
		total += entry.Bytes
		if len(paths) < 200 {
			paths = append(paths, entry.Path)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"root": root, "entries": len(entries), "totalBytes": total, "paths": paths,
	})
	return nil
}
