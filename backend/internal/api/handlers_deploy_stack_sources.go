package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

// The media-source picker (stacks/media/prowlarr_sources.go). A Complete media
// stack is set up and connected but for one thing only the person may choose:
// a source they are allowed to use. These routes list what the stack's
// Prowlarr has and can add, and add or remove one, so that choice is made in
// the dashboard and everything after it (the test, the save, the sync to
// Radarr and Sonarr, the stack's own check) is the dashboard's. Nothing here
// picks a source.

// stackSourcesAnswer is GET .../stack/sources.
type stackSourcesAnswer struct {
	Sources []media.MediaSource `json:"sources"`
}

// stackSourceAddRequest is POST .../stack/sources: a definition of the
// catalogue and the values typed into its form, by setting name.
type stackSourceAddRequest struct {
	Definition string         `json:"definition"`
	Fields     map[string]any `json:"fields"`
}

// stackSourceAddAnswer is a source added. Synced: Prowlarr had shared it with
// Radarr and Sonarr before the answer.
type stackSourceAddAnswer struct {
	Source media.MediaSource `json:"source"`
	Synced bool              `json:"synced"`
}

// Reading what Prowlarr has is reading the stack's state, for whoever reads
// the project.
func (s *Server) mountStackSourceReads(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/stack/sources", s.handle(s.handleStackSources))
	r.Method(http.MethodGet, "/{id}/stack/sources/catalogue", s.handle(s.handleStackSourceCatalogue))
	r.Method(http.MethodGet, "/{id}/stack/sources/catalogue/{definition}", s.handle(s.handleStackSourceDefinition))
}

// Adding a source is configuring the stack's applications, the control Retry
// is.
func (s *Server) mountStackSourceWrites(r chi.Router) {
	r.Method(http.MethodPost, "/{id}/stack/sources", s.handle(s.handleStackSourceAdd))
}

// Removing one takes it from Prowlarr, Radarr and Sonarr with the account the
// person gave it: destructive, with an ordinary confirmation, since it is
// added again in a minute.
func (s *Server) mountStackSourceRemoval(r chi.Router) {
	r.Method(http.MethodDelete, "/{id}/stack/sources/{sourceId}", s.handle(s.handleStackSourceRemove))
}

// stackSourceEnv is the environment the routes reach Prowlarr in: the one a
// configuration pass runs in, over the live release's containers. A test
// replaces it with one over a stand-in.
var stackSourceEnv = func(ctx context.Context, s *Server, manifest *stacks.Manifest, preset stacks.Preset, record *stacks.Record, live stacks.Live) (stacks.Env, error) {
	return s.modules.stacks.Env(ctx, manifest, preset, *record, live)
}

// stackSources is a stack whose live release runs Prowlarr, and the
// environment to reach it in, or the refusal that says why not.
type stackSources struct {
	record *stacks.Record
	live   stacks.Live
	env    stacks.Env
}

func (s *Server) stackSourcesOf(ctx context.Context, r *http.Request) (*stackSources, error) {
	record, manifest, err := s.stackRecord(r)
	if err != nil {
		return nil, err
	}
	if s.modules.stacks == nil || s.modules.stackSources == nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "stacks_unavailable", "stack configuration is not running on this dashboard")
	}
	live, err := s.stackLiveRelease(ctx, record.EnvironmentID)
	if errors.Is(err, stacks.ErrNoLiveRelease) {
		return nil, httpx.Err(http.StatusConflict, "stack_waiting", "the stack has no live release yet; its sources are added once its deployment finishes")
	}
	if err != nil {
		return nil, err
	}
	preset, found := manifest.PresetForTemplate(live.TemplateID)
	if !found || live.Kind != "compose" {
		return nil, httpx.Err(http.StatusConflict, "stack_not_preset", "the deployment's live release is not one of the stack's presets, so it runs no Prowlarr of the stack's")
	}
	if manifest.ID != media.ID || !slices.Contains(preset.Services, media.SourcesService) {
		return nil, httpx.Err(http.StatusConflict, "stack_no_sources", fmt.Sprintf("the %s preset has no Prowlarr: it fetches nothing, so it has no sources", preset.Title))
	}
	env, err := stackSourceEnv(ctx, s, manifest, preset, record, live)
	if err != nil {
		return nil, err
	}
	return &stackSources{record: record, live: live, env: env}, nil
}

// stackSourceError turns what Prowlarr answered into the routes' words. Only
// the refusals the picker reads have their own code; anything else is
// Prowlarr not answering as it should, with what it said.
func stackSourceError(ctx context.Context, err error) error {
	var failed *media.SourceTestFailedError
	var invalid *media.SourceInvalidError
	var exists *media.SourceExistsError
	switch {
	case errors.As(err, &failed):
		return httpx.Err(http.StatusUnprocessableEntity, "source_test_failed", failed.Message)
	case errors.As(err, &invalid):
		refusal := httpx.Err(http.StatusUnprocessableEntity, "source_invalid", invalid.Message)
		refusal.Field = "fields." + invalid.Field
		return refusal
	case errors.As(err, &exists):
		return httpx.Err(http.StatusConflict, "source_exists", exists.Error()+"; remove it first, or keep it")
	case errors.Is(err, media.ErrSourceNotFound):
		return httpx.Err(http.StatusNotFound, "source_not_found", "Prowlarr has no source with that id")
	case errors.Is(err, media.ErrSourceDefinitionNotFound):
		return httpx.Err(http.StatusNotFound, "source_definition_not_found", "Prowlarr has no such source to add; the catalogue lists the ones it has")
	}
	// The client's errors name Prowlarr; the container's lookup names the
	// service ("prowlarr is not running").
	message := err.Error()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		message = "Prowlarr did not answer in time: " + message
	} else if !strings.HasPrefix(message, "Prowlarr") {
		message = "Prowlarr could not be reached: " + message
	}
	refusal := httpx.Err(http.StatusBadGateway, "stack_app_unreachable", message)
	refusal.Retryable = true
	return refusal
}

func (s *Server) handleStackSources(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	stack, err := s.stackSourcesOf(ctx, r)
	if err != nil {
		return err
	}
	sources, err := media.ListSources(ctx, stack.env)
	if err != nil {
		return stackSourceError(ctx, err)
	}
	httpx.JSON(w, http.StatusOK, stackSourcesAnswer{Sources: sources})
	return nil
}

// stackSourceCatalogue is the project's catalogue, read from its Prowlarr at
// most once in SourceCatalogueTTL. The key holds the live release, so a
// redeploy with another Prowlarr reads it again.
func (s *Server) stackSourceCatalogue(ctx context.Context, stack *stackSources) (*media.MediaSourceCatalogue, error) {
	key := strconv.FormatInt(stack.record.ProjectID, 10) + "/" + strconv.FormatInt(stack.live.ReleaseID, 10)
	catalogue, err := s.modules.stackSources.Catalogue(ctx, key, stack.env)
	if err != nil {
		return nil, stackSourceError(ctx, err)
	}
	return catalogue, nil
}

func (s *Server) handleStackSourceCatalogue(w http.ResponseWriter, r *http.Request) error {
	// Prowlarr takes several seconds to build its schema the first time.
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	stack, err := s.stackSourcesOf(ctx, r)
	if err != nil {
		return err
	}
	catalogue, err := s.stackSourceCatalogue(ctx, stack)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, catalogue)
	return nil
}

func (s *Server) handleStackSourceDefinition(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	stack, err := s.stackSourcesOf(ctx, r)
	if err != nil {
		return err
	}
	definition, err := url.PathUnescape(chi.URLParam(r, "definition"))
	if err != nil {
		return httpx.BadRequest("the definition is not a valid path segment")
	}
	catalogue, err := s.stackSourceCatalogue(ctx, stack)
	if err != nil {
		return err
	}
	detail, found := catalogue.Lookup(definition)
	if !found {
		return stackSourceError(ctx, media.ErrSourceDefinitionNotFound)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handleStackSourceAdd(w http.ResponseWriter, r *http.Request) error {
	var body stackSourceAddRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Definition == "" {
		return httpx.BadRequest("definition is required: one of the catalogue's")
	}
	// A test that signs in to a site can take Prowlarr's own query timeout.
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	stack, err := s.stackSourcesOf(ctx, r)
	if err != nil {
		return err
	}
	// The definition's name only: what was typed into its form is the
	// person's, and never recorded.
	httpx.SetAudit(r, "deploy.stack.source.add", strconv.FormatInt(stack.record.ProjectID, 10), map[string]any{
		"stack": stack.record.StackID, "definition": body.Definition,
	})
	catalogue, err := s.stackSourceCatalogue(ctx, stack)
	if err != nil {
		return err
	}
	prepared, err := catalogue.Prepare(body.Definition, body.Fields)
	if errors.Is(err, media.ErrSourceDefinitionNotFound) {
		return httpx.Err(http.StatusUnprocessableEntity, "source_invalid", "Prowlarr has no source called "+strconv.Quote(body.Definition)+" to add; the catalogue lists the ones it has")
	}
	if err != nil {
		return stackSourceError(ctx, err)
	}
	// What the assistant sends passed through the model's provider, which is
	// kept from every secret: a password, key or cookie for a site is typed
	// by the person into the form, as a terminal's password is.
	if prepared.Secret && httpx.MustPrincipal(r).Assistant {
		return httpx.Err(http.StatusForbidden, "source_needs_person",
			"a source that needs a password, key or cookie is added by the person on the stack's Sources page, so what they type never reaches the assistant; one that needs none the assistant may add")
	}
	outcome, err := media.AddSource(ctx, stack.env, prepared)
	if err != nil {
		return stackSourceError(ctx, err)
	}
	s.stackRequestPass(ctx, stack.record.ProjectID)
	httpx.JSON(w, http.StatusCreated, stackSourceAddAnswer{Source: outcome.Source, Synced: outcome.Synced})
	return nil
}

func (s *Server) handleStackSourceRemove(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.Atoi(chi.URLParam(r, "sourceId"))
	if err != nil || id <= 0 {
		return httpx.BadRequest("source id must be a positive integer: Prowlarr's, as the sources list gives it")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	stack, err := s.stackSourcesOf(ctx, r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.stack.source.remove", strconv.FormatInt(stack.record.ProjectID, 10), map[string]any{
		"stack": stack.record.StackID, "sourceId": id,
	})
	removed, err := media.RemoveSource(ctx, stack.env, id)
	if err != nil {
		return stackSourceError(ctx, err)
	}
	httpx.SetAudit(r, "deploy.stack.source.remove", strconv.FormatInt(stack.record.ProjectID, 10), map[string]any{
		"stack": stack.record.StackID, "sourceId": id, "source": removed.Name, "definition": removed.Definition,
	})
	s.stackRequestPass(ctx, stack.record.ProjectID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// stackRequestPass asks for a pass so the stack's own check of its sources
// (prowlarr.sources) reads what changed. One already running would have read
// Prowlarr before the change, so the request is kept for the pass after it.
func (s *Server) stackRequestPass(ctx context.Context, projectID int64) {
	if err := s.modules.stackStore.RequestPass(context.WithoutCancel(ctx), projectID); err != nil {
		if s.Log != nil {
			s.Log.Warn("a stack pass could not be requested after its sources changed", "project", projectID, "error", err)
		}
		return
	}
	s.modules.stacks.Kick()
}
