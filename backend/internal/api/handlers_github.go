package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The GitHub half of the git page.
//
// Mounted from mountGitRoutes rather than from Routes(), for the reason
// handlers_docker_manage.go is: these are the git page's routes, and the route
// map for git stays in one place.
//
// The routes about a repository take ?path=, and it is not decoration. The
// credential belongs to the host account that owns the checkout — that is
// where gh stores it and where git looks for it — so "who is signed in" has a
// different answer per repository, and a sign-in has to say which account it
// is for.
//
// The sign-in routes take it too, but do not require it. A page that is not
// inside a checkout — the repository list, the files browser, a shell that has
// not cd'd anywhere yet — still wants to show who this dashboard is, and there
// is no repository to name. With no path the answer is the account the
// dashboard itself runs as, which is the one every root-owned checkout will
// use.
//
//	read            who is signed in, which repository this is, what is open
//	service.control opening a pull request
//	system.admin    signing in and out — this stores a credential that can push
//	                to every repository the account can reach, which is a
//	                larger thing than any one git operation
func (s *Server) mountGitHubRoutes(r chi.Router) {
	r.Route("/github", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleGitHubStatus))
		r.Method(http.MethodGet, "/repo", s.handle(s.handleGitHubRepo))
		r.Method(http.MethodGet, "/repos", s.handle(s.handleGitHubRepos))
		r.Method(http.MethodGet, "/branches", s.handle(s.handleGitHubBranches))
		r.Method(http.MethodGet, "/pulls", s.handle(s.handleGitHubPulls))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/pulls", s.handle(s.handleGitHubPullCreate))
		})

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/auth/device", s.handle(s.handleGitHubDeviceStart))
			r.Method(http.MethodPost, "/auth/device/{id}", s.handle(s.handleGitHubDevicePoll))
			r.Method(http.MethodPost, "/auth/token", s.handle(s.handleGitHubToken))
			r.Method(http.MethodPost, "/auth/configure", s.handle(s.handleGitHubConfigure))
			r.Method(http.MethodPost, "/auth/logout", s.handle(s.handleGitHubLogout))
		})
	})
}

func ghError(err error) error {
	if errors.Is(err, ghx.ErrNotInstalled) {
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	}
	// gh's failures are almost all ordinary answers — no permission, no
	// commits between the branches, a network that is down — and its own words
	// are the diagnosis.
	return httpx.BadRequest("%v", err)
}

// handleGitHubStatus answers in one shape whether or not gh is installed, so
// the page renders "not available on this host" as information rather than as
// an error — the same degradation every optional module gets.
// githubDir is gitRepo with "no repository" as a legal answer. An empty path
// means the dashboard's own account rather than a checkout's owner; a path that
// is given is still resolved against the configured roots.
func (s *Server) githubDir(r *http.Request) (string, error) {
	if r.URL.Query().Get("path") == "" {
		return "", nil
	}
	return s.gitRepo(r)
}

func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) error {
	path, err := s.githubDir(r)
	if err != nil {
		return err
	}
	if !s.modules.github.Available() {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false})
		return nil
	}
	acc, err := s.modules.github.Status(r.Context(), path)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "account": acc})
	return nil
}

func (s *Server) handleGitHubRepo(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	info, err := s.modules.github.RepoInfo(r.Context(), path)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, info)
	return nil
}

// handleGitHubRepos lists what the signed-in account can deploy.
//
// Unlike the routes around it this one takes no ?path=: the whole point is to
// choose a repository that has not been cloned onto this server yet, so the
// credential consulted is the dashboard's own — the same one every deployment
// clone will use.
func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	if !s.modules.github.Available() {
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", ghx.ErrNotInstalled.Error())
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	repos, err := s.modules.github.ListRepos(r.Context(), "", r.URL.Query().Get("query"), limit)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, repos)
	return nil
}

// handleGitHubBranches lists one repository's branches so a deployment can be
// pointed at something other than the default without typing a ref from memory.
func (s *Server) handleGitHubBranches(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	if !s.modules.github.Available() {
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", ghx.ErrNotInstalled.Error())
	}
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		return httpx.BadRequest("name the repository as owner/name")
	}
	branches, err := s.modules.github.ListBranches(r.Context(), "", repo)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, branches)
	return nil
}

func (s *Server) handleGitHubPulls(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	pulls, err := s.modules.github.ListPulls(r.Context(), path, limit)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, pulls)
	return nil
}

// handleGitHubPullCreate publishes the branch before opening the request.
//
// gh refuses to open a pull request for a branch the remote has never heard
// of, and its own remedy is an interactive prompt this request cannot answer.
// Pushing first is what the operator would have done, and Push sets the
// upstream when there is none — so the ordinary case, a branch made in this
// page five minutes ago, works without a detour through a terminal.
func (s *Server) handleGitHubPullCreate(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	var req ghx.NewPull
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Head == "" {
		if branch, err := s.modules.git.CurrentBranch(r.Context(), path); err == nil {
			req.Head = branch
		}
	}
	if res, err := s.modules.git.Push(r.Context(), path); err != nil {
		httpx.SetAudit(r, "github.pull.create", path, map[string]any{"ok": false})
		return httpx.BadRequest("the branch could not be pushed, so there is nothing to open a pull request from:\n%s", res.Output)
	}
	pull, err := s.modules.github.CreatePull(r.Context(), path, req)
	httpx.SetAudit(r, "github.pull.create", path, map[string]any{"ok": err == nil, "head": req.Head, "base": req.Base})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, pull)
	return nil
}

func (s *Server) handleGitHubDeviceStart(w http.ResponseWriter, r *http.Request) error {
	path, err := s.githubDir(r)
	if err != nil {
		return err
	}
	start, err := s.modules.github.StartDevice(r.Context(), path)
	httpx.SetAudit(r, "github.signin.start", path, map[string]any{"ok": err == nil})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, start)
	return nil
}

// handleGitHubDevicePoll is a POST because it is not a read: the call that
// finds the code has been entered is the call that stores the credential.
func (s *Server) handleGitHubDevicePoll(w http.ResponseWriter, r *http.Request) error {
	state, err := s.modules.github.PollDevice(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return ghError(err)
	}
	// Only the outcome is worth an audit entry; the pending answers are the
	// same request repeating every five seconds.
	if state.Status != "pending" {
		httpx.SetAudit(r, "github.signin", state.Status, map[string]any{"ok": state.Status == "complete"})
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

type githubTokenRequest struct {
	Token string `json:"token"`
	Host  string `json:"host"`
}

// handleGitHubToken is the way in for anything the device flow cannot do: a
// GitHub Enterprise host, a fine-grained token, or a machine account whose
// credential was minted elsewhere. The token arrives in the body and is handed
// to gh on its stdin, so it is never an argument in anybody's process table.
func (s *Server) handleGitHubToken(w http.ResponseWriter, r *http.Request) error {
	path, err := s.githubDir(r)
	if err != nil {
		return err
	}
	var req githubTokenRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	acc, err := s.modules.github.LoginWithToken(r.Context(), path, req.Host, req.Token)
	httpx.SetAudit(r, "github.signin.token", path, map[string]any{"ok": err == nil})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, acc)
	return nil
}

// handleGitHubConfigure is the button behind "git is not using this account":
// it writes the credential helper and the committer identity, which is the
// whole of the fix and is not something the operator can do from this page any
// other way.
func (s *Server) handleGitHubConfigure(w http.ResponseWriter, r *http.Request) error {
	path, err := s.githubDir(r)
	if err != nil {
		return err
	}
	acc, err := s.modules.github.Configure(r.Context(), path)
	httpx.SetAudit(r, "github.configure", path, map[string]any{"ok": err == nil})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, acc)
	return nil
}

func (s *Server) handleGitHubLogout(w http.ResponseWriter, r *http.Request) error {
	path, err := s.githubDir(r)
	if err != nil {
		return err
	}
	host := r.URL.Query().Get("host")
	err = s.modules.github.Logout(r.Context(), path, host)
	httpx.SetAudit(r, "github.signout", path, map[string]any{"ok": err == nil})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
