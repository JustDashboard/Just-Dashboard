package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/go-chi/chi/v5"
)

// The GitHub page's core: the repository App, the two sign-in flows, the
// account's own connections, their repository lists, creating a repository,
// and one repository's settings, archive, visibility, deletion and cloning
// onto this server. The capability of each route is in the table at the top
// of handlers_githubrepos.go.
//
// A hub session is refused on every route that starts a sign-in, finishes
// one, creates the App or deletes a repository: what those make outlives the
// two-hour session and the grant behind it.
func (s *Server) mountGitHubAccountRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/app", s.handle(s.handleGitHubSetup))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.With(httpx.RefuseHubSession).Method(http.MethodPost, "/app/manifest", s.handle(s.handleGitHubManifest))
		r.Method(http.MethodPost, "/app/callback-urls", s.handle(s.handleGitHubCallbackURL))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/app", s.handle(s.handleGitHubForgetApp))
		})
	})
	// The capability depends on which flow the state finishes, so the
	// handler checks it once the state is read.
	r.With(httpx.RefuseHubSession).Method(http.MethodPost, "/callback", s.handle(s.handleGitHubCallback))

	r.Method(http.MethodGet, "/connections", s.handle(s.handleGitHubConnections))
	r.Method(http.MethodPost, "/connections/{connection}/refresh", s.handle(s.handleGitHubConnectionRefresh))
	r.Method(http.MethodGet, "/connections/{connection}/repositories", s.handle(s.handleGitHubRepositories))
	r.Method(http.MethodGet, "/connections/{connection}/create-options", s.handle(s.handleGitHubCreateOptions))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.With(httpx.RefuseHubSession).Method(http.MethodPost, "/connections/authorize", s.handle(s.handleGitHubAuthorize))
		r.With(httpx.RefuseHubSession).Method(http.MethodPost, "/connections/device", s.handle(s.handleGitHubPageDeviceStart))
		r.With(httpx.RefuseHubSession).Method(http.MethodGet, "/connections/device/{flow}", s.handle(s.handleGitHubPageDevicePoll))
		r.Method(http.MethodDelete, "/connections/{connection}", s.handle(s.handleGitHubDisconnect))
		r.Method(http.MethodPost, "/connections/{connection}/repositories", s.handle(s.handleGitHubCreateRepository))
	})
}

func (s *Server) mountGitHubRepositoryRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleGitHubRepository))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPatch, "/", s.handle(s.handleGitHubRepositoryUpdate))
		r.Method(http.MethodPost, "/archive", s.handle(s.handleGitHubRepositoryArchive))
		r.Method(http.MethodPost, "/clone", s.handle(s.handleGitHubRepositoryClone))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodPost, "/visibility", s.handle(s.handleGitHubRepositoryVisibility))
		r.With(httpx.RefuseHubSession).Method(http.MethodDelete, "/", s.handle(s.handleGitHubRepositoryDelete))
	})
}

// githubConnectionID reads {connection}. A malformed id is answered as a
// missing connection, like another account's.
func githubConnectionID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "connection"), 10, 64)
	if err != nil || id <= 0 {
		return 0, githubPageErr(githubrepos.ErrConnectionNotFound)
	}
	return id, nil
}

func githubUser(r *http.Request) int64 { return httpx.MustPrincipal(r).UserID() }

// githubOrigin checks the dashboard address a body names against the
// request's own Origin header. Every address the App is told about, and
// every sign-in's return address, is built from it; taking it from the
// browser rather than from configuration is what lets a dashboard reached at
// a private address or behind a proxy work, and the check is what keeps a
// request from naming an address the browser is not on.
func githubOrigin(r *http.Request, body string) (string, error) {
	claimed, err := githubrepos.ParseOrigin(body)
	if err != nil {
		return "", httpx.BadRequest("%s", err.Error())
	}
	actual, err := githubrepos.ParseOrigin(r.Header.Get("Origin"))
	if err != nil || actual != claimed {
		return "", httpx.BadRequest("origin %s is not the address this request came from", claimed)
	}
	return claimed, nil
}

func (s *Server) handleGitHubSetup(w http.ResponseWriter, r *http.Request) error {
	setup, err := s.modules.githubRepos.Setup(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, setup)
	return nil
}

func (s *Server) handleGitHubManifest(w http.ResponseWriter, r *http.Request) error {
	var request githubrepos.ManifestRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	origin, err := githubOrigin(r, request.Origin)
	if err != nil {
		return err
	}
	request.Origin = origin
	start, err := s.modules.githubRepos.BeginManifest(r.Context(), githubUser(r), request)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.app.manifest", start.Manifest.Name, map[string]any{"organization": request.Organization, "public": start.Manifest.Public, "origin": origin})
	httpx.JSON(w, http.StatusOK, start)
	return nil
}

type githubOriginRequest struct {
	Origin string `json:"origin"`
}

func (s *Server) handleGitHubCallbackURL(w http.ResponseWriter, r *http.Request) error {
	var request githubOriginRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	origin, err := githubOrigin(r, request.Origin)
	if err != nil {
		return err
	}
	setup, err := s.modules.githubRepos.AddCallbackOrigin(r.Context(), origin)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.app.callback-url", origin+githubrepos.CallbackPath, nil)
	httpx.JSON(w, http.StatusOK, setup)
	return nil
}

func (s *Server) handleGitHubForgetApp(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.githubRepos.ForgetApp(r.Context()); err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.app.forget", "", nil)
	httpx.NoContent(w)
	return nil
}

type githubCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// handleGitHubCallback finishes either flow GitHub sent the browser back
// from. The page reads code and state off its own address and posts them
// here with its session; the state says which flow it was, and that decides
// the capability: creating the App is an administrator's, connecting an
// account service.control's.
func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) error {
	var request githubCallbackRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	pending, err := s.modules.githubRepos.TakeState(githubUser(r), request.State)
	if err != nil {
		return githubPageErr(err)
	}
	principal := httpx.MustPrincipal(r)
	switch pending.Kind {
	case githubrepos.KindApp:
		if !principal.Can(auth.CapSystemAdmin) {
			return httpx.ErrForbidden
		}
		app, err := s.modules.githubRepos.CompleteApp(r.Context(), pending, request.Code)
		if err != nil {
			return githubPageErr(err)
		}
		httpx.SetAudit(r, "github.app.create", app.Slug, map[string]any{"appId": app.ID, "owner": app.Owner})
		httpx.JSON(w, http.StatusOK, map[string]any{"kind": githubrepos.KindApp})
	default:
		if !principal.Can(auth.CapServiceControl) {
			return httpx.ErrForbidden
		}
		connection, err := s.modules.githubRepos.CompleteConnection(r.Context(), pending, request.Code)
		if err != nil {
			return githubPageErr(err)
		}
		httpx.SetAudit(r, "github.connection.connect", connection.Login, map[string]any{"connection": connection.ID, "method": "web"})
		httpx.JSON(w, http.StatusOK, map[string]any{"kind": githubrepos.KindConnection, "connection": connection})
	}
	return nil
}

func (s *Server) handleGitHubConnections(w http.ResponseWriter, r *http.Request) error {
	connections, err := s.modules.githubRepos.Connections(r.Context(), githubUser(r))
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, connections)
	return nil
}

func (s *Server) handleGitHubAuthorize(w http.ResponseWriter, r *http.Request) error {
	var request githubOriginRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	origin, err := githubOrigin(r, request.Origin)
	if err != nil {
		return err
	}
	address, err := s.modules.githubRepos.AuthorizeURL(r.Context(), githubUser(r), origin)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.connection.authorize", "", map[string]any{"origin": origin})
	httpx.JSON(w, http.StatusOK, map[string]string{"url": address})
	return nil
}

func (s *Server) handleGitHubPageDeviceStart(w http.ResponseWriter, r *http.Request) error {
	start, err := s.modules.githubRepos.StartDevice(r.Context(), githubUser(r))
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.connection.device", "", nil)
	httpx.JSON(w, http.StatusOK, start)
	return nil
}

// handleGitHubPageDevicePoll is a GET because the page polls it, but the poll
// that finds the code entered makes the connection, so that one is audited
// as a connect.
func (s *Server) handleGitHubPageDevicePoll(w http.ResponseWriter, r *http.Request) error {
	state, err := s.modules.githubRepos.PollDevice(r.Context(), githubUser(r), chi.URLParam(r, "flow"))
	if err != nil {
		return githubPageErr(err)
	}
	if state.Status == "complete" && state.Connection != nil {
		httpx.AuditRead(s.Audit, r, "github.connection.connect", state.Connection.Login)
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

func (s *Server) handleGitHubConnectionRefresh(w http.ResponseWriter, r *http.Request) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	connection, err := s.modules.githubRepos.Refresh(r.Context(), githubUser(r), id)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.connection.refresh", connection.Login, nil)
	httpx.JSON(w, http.StatusOK, connection)
	return nil
}

func (s *Server) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	connection, err := s.modules.githubRepos.Disconnect(r.Context(), githubUser(r), id)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.connection.disconnect", connection.Login, map[string]any{"connection": connection.ID})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleGitHubRepositories(w http.ResponseWriter, r *http.Request) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	list, err := s.modules.githubRepos.Repositories(r.Context(), githubUser(r), id, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		return githubPageErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleGitHubCreateOptions(w http.ResponseWriter, r *http.Request) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	options, err := s.modules.githubRepos.CreateOptions(r.Context(), githubUser(r), id)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.JSON(w, http.StatusOK, options)
	return nil
}

func (s *Server) handleGitHubCreateRepository(w http.ResponseWriter, r *http.Request) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	var request githubrepos.CreateRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	result, err := s.modules.githubRepos.Create(r.Context(), githubUser(r), id, request)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.repository.create", result.Repository.FullName, map[string]any{"visibility": request.Visibility, "reachable": result.Reachable})
	httpx.JSON(w, http.StatusCreated, result)
	return nil
}

// resolveGitHubRepoAccess is {connection}/{owner}/{repo} for the signed-in
// account: its own connection, GitHub's answer for the repository with the
// person's token (reused for thirty seconds), the installation that covers
// it, and the abilities.
func (s *Server) resolveGitHubRepoAccess(r *http.Request) (*githubrepos.RepoAccess, error) {
	id, err := githubConnectionID(r)
	if err != nil {
		return nil, err
	}
	access, err := s.modules.githubRepos.Access(r.Context(), githubUser(r), id, chi.URLParam(r, "owner"), chi.URLParam(r, "repo"))
	if err != nil {
		return nil, githubPageErr(err)
	}
	return access, nil
}

// forgetGitHubRepo drops what is cached about one repository after a change
// to it (its access, the connection's repository list).
func (s *Server) forgetGitHubRepo(access *githubrepos.RepoAccess) {
	if access == nil {
		return
	}
	s.modules.githubRepos.Forget(access.ConnectionID, access.Owner, access.Name)
}

func (s *Server) handleGitHubRepository(w http.ResponseWriter, r *http.Request) error {
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	detail, err := s.modules.githubRepos.Detail(r.Context(), access)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handleGitHubRepositoryUpdate(w http.ResponseWriter, r *http.Request) error {
	var update githubrepos.RepositoryUpdate
	if err := httpx.DecodeJSON(r, &update); err != nil {
		return err
	}
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	detail, err := s.modules.githubRepos.Update(r.Context(), access, update)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.repository.update", access.Repository.FullName, map[string]any{"fields": updatedFields(update), "fullName": detail.FullName})
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// updatedFields names the settings an update changed, for the audit entry:
// the names only, never the values a person typed.
func updatedFields(update githubrepos.RepositoryUpdate) []string {
	fields := []string{}
	for name, set := range map[string]bool{
		"name": update.Name != nil, "description": update.Description != nil, "homepage": update.Homepage != nil,
		"defaultBranch": update.DefaultBranch != nil, "topics": update.Topics != nil, "isTemplate": update.IsTemplate != nil,
		"hasIssues": update.HasIssues != nil, "hasWiki": update.HasWiki != nil, "hasProjects": update.HasProjects != nil,
		"hasDiscussions": update.HasDiscussions != nil, "allowMergeCommit": update.AllowMergeCommit != nil,
		"allowSquashMerge": update.AllowSquashMerge != nil, "allowRebaseMerge": update.AllowRebaseMerge != nil,
		"allowAutoMerge": update.AllowAutoMerge != nil, "deleteBranchOnMerge": update.DeleteBranchOnMerge != nil,
		"allowUpdateBranch": update.AllowUpdateBranch != nil, "webCommitSignoffRequired": update.WebCommitSignoffRequired != nil,
	} {
		if set {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func (s *Server) handleGitHubRepositoryArchive(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Archived *bool `json:"archived"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.Archived == nil {
		return httpx.BadRequest("archived is required")
	}
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	detail, err := s.modules.githubRepos.SetArchived(r.Context(), access, *request.Archived)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.repository.archive", access.Repository.FullName, map[string]any{"archived": *request.Archived})
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handleGitHubRepositoryVisibility(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Visibility string `json:"visibility"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	detail, err := s.modules.githubRepos.SetVisibility(r.Context(), access, request.Visibility)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.repository.visibility", access.Repository.FullName, map[string]any{"from": access.Repository.Visibility, "to": detail.Visibility})
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleGitHubRepositoryDelete deletes a repository on GitHub, which nothing
// here can bring back: GitHub restores a deleted repository for ninety days
// only through its own settings, and not every one. It takes the typed
// phrase of the repository's full name (invariant 3), asked for only once
// the person's role and the App's grant allow the deletion at all.
func (s *Server) handleGitHubRepositoryDelete(w http.ResponseWriter, r *http.Request) error {
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	if err := access.Require(access.Repository.Can.Delete, "delete"); err != nil {
		return githubPageErr(err)
	}
	if err := httpx.RequireTypedConfirmation(w, r, access.Repository.FullName); err != nil {
		return err
	}
	if err := s.modules.githubRepos.Delete(r.Context(), access); err != nil {
		return githubPageErr(err)
	}
	httpx.SetAudit(r, "github.repository.delete", access.Repository.FullName, map[string]any{"visibility": access.Repository.Visibility})
	httpx.NoContent(w)
	return nil
}

type githubCloneRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name,omitempty"`
	Branch string `json:"branch,omitempty"`
	Depth  int    `json:"depth,omitempty"`
}

// handleGitHubRepositoryClone clones the repository onto this server under
// the Git page's roots, as a job whose console the page shows. Everything
// the Git page's own clone checks is checked here, before the job starts,
// by the same code; the address is built from GitHub's spelling of the
// repository, not from the request. A private repository is fetched with an
// installation token that can only read it, minted for this clone and
// revoked when it ends, and handed to git only in its environment.
func (s *Server) handleGitHubRepositoryClone(w http.ResponseWriter, r *http.Request) error {
	var request githubCloneRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if request.Parent == "" {
		return httpx.BadRequest("parent is required")
	}
	access, err := s.githubRepo(r)
	if err != nil {
		return githubPageErr(err)
	}
	remote := s.modules.githubRepos.CloneURL(access)
	plan, err := s.modules.git.PrepareClone(request.Parent, remote, request.Name, gitx.CloneOptions{Branch: request.Branch, Depth: request.Depth})
	if err != nil {
		return gitErr(err)
	}
	fullName := access.Repository.FullName
	repos := s.modules.githubRepos
	git := s.modules.git
	job := s.modules.jobs.Start(jobs.Spec{
		Kind: "github.clone", Title: "Clone " + fullName, Target: plan.Target, StartedBy: httpx.MustPrincipal(r).Username(),
		Capability: auth.CapServiceControl, Timeout: 15 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("Cloning %s from GitHub into %s.", fullName, plan.Target)
		token, revoke, err := repos.CloneToken(ctx, access)
		if err != nil {
			return fmt.Errorf("could not get a read-only token for %s: %v", fullName, err)
		}
		defer revoke()
		if token != "" {
			out.Status("%s is %s: it is fetched with a token that can only read it, made for this clone and revoked when it ends.", fullName, access.Repository.Visibility)
		} else {
			out.Status("%s is public: it is fetched without a credential.", fullName)
		}
		out.Status("$ %s", plan.Command())
		if err := git.CloneStreaming(ctx, plan, githubrepos.CloneEnvironment(remote, token), out.Line); err != nil {
			return err
		}
		out.Status("Cloned into %s. Local Git lists it with the server's other checkouts.", plan.Target)
		return nil
	})
	httpx.SetAudit(r, "github.repository.clone", fullName, map[string]any{"target": plan.Target, "job": job.ID})
	httpx.JSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "target": plan.Target})
	return nil
}
