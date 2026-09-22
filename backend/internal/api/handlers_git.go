package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Git routes are split by what they can cost you.
//
//	read            anyone authenticated
//	service.control fetch, pull, push, checkout, branch, tag, stash, stage,
//	                commit, merge, revert, cherry-pick, identity, remotes,
//	                clone, init — every one of them recoverable
//	destructive     discard and reset (typed: they throw away uncommitted
//	                work), dropping a stash (typed: the same), and deleting a
//	                branch, a tag or a remote (ordinary confirmation: the
//	                commits survive)
func (s *Server) mountGitRoutes(r chi.Router) {
	r.Route("/git", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleGitRepos))
		r.Method(http.MethodGet, "/roots", s.handle(s.handleGitRoots))
		// detect answers "is this shell sitting in a checkout, and where is its
		// root", for the terminal page's git panel. It takes an arbitrary
		// directory rather than a repository the list already knows, which is
		// why it lives beside the list rather than inside gitRepo's resolver.
		r.Method(http.MethodGet, "/detect", s.handle(s.handleGitDetect))
		r.Method(http.MethodGet, "/status", s.handle(s.handleGitStatus))
		r.Method(http.MethodGet, "/log", s.handle(s.handleGitLog))
		r.Method(http.MethodGet, "/commit", s.handle(s.handleGitCommit))
		r.Method(http.MethodGet, "/compare", s.handle(s.handleGitCompare))
		r.Method(http.MethodGet, "/compare/diff", s.handle(s.handleGitCompareDiff))
		r.Method(http.MethodGet, "/reflog", s.handle(s.handleGitReflog))
		r.Method(http.MethodGet, "/blame", s.handle(s.handleGitBlame))
		r.Method(http.MethodGet, "/signature", s.handle(s.handleGitSignature))
		r.Method(http.MethodGet, "/worktrees", s.handle(s.handleGitWorktrees))
		r.Method(http.MethodGet, "/conflict", s.handle(s.handleGitConflict))
		r.Method(http.MethodGet, "/patch", s.handle(s.handleGitPartialDiff))
		r.Method(http.MethodGet, "/rebase/plan", s.handle(s.handleGitRebasePlan))
		r.Method(http.MethodGet, "/submodules", s.handle(s.handleGitSubmodules))
		r.Method(http.MethodGet, "/lfs", s.handle(s.handleGitLFS))
		r.Method(http.MethodGet, "/patch/export", s.handle(s.handleGitPatchExport))
		r.Method(http.MethodPost, "/patch/check", s.handle(s.handleGitPatchCheck))
		r.Method(http.MethodGet, "/branches", s.handle(s.handleGitBranches))
		r.Method(http.MethodGet, "/tags", s.handle(s.handleGitTags))
		r.Method(http.MethodGet, "/stashes", s.handle(s.handleGitStashes))
		r.Method(http.MethodGet, "/stash/diff", s.handle(s.handleGitStashDiff))
		r.Method(http.MethodGet, "/remotes", s.handle(s.handleGitRemotes))
		// The branch topology across every ref — a read, so it sits with /log
		// and /branches rather than in the service.control group.
		r.Method(http.MethodGet, "/graph", s.handle(s.handleGitGraph))
		r.Method(http.MethodGet, "/diff", s.handle(s.handleGitDiff))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/clone", s.handle(s.handleGitClone))
			r.Method(http.MethodPost, "/init", s.handle(s.handleGitInit))
			r.Method(http.MethodPost, "/fetch", s.handle(s.handleGitFetch))
			r.Method(http.MethodPost, "/pull", s.handle(s.handleGitPull))
			r.Method(http.MethodPost, "/push", s.handle(s.handleGitPush))
			r.Method(http.MethodPost, "/push/tags", s.handle(s.handleGitPushTags))
			r.Method(http.MethodPost, "/checkout", s.handle(s.handleGitCheckout))
			r.Method(http.MethodPost, "/branch", s.handle(s.handleGitBranch))
			r.Method(http.MethodPost, "/branch/rename", s.handle(s.handleGitBranchRename))
			r.Method(http.MethodPost, "/merge", s.handle(s.handleGitMerge))
			r.Method(http.MethodPost, "/revert", s.handle(s.handleGitRevert))
			r.Method(http.MethodPost, "/cherry-pick", s.handle(s.handleGitCherryPick))
			r.Method(http.MethodPost, "/tag", s.handle(s.handleGitTag))
			r.Method(http.MethodPost, "/stash", s.handle(s.handleGitStash))
			r.Method(http.MethodPost, "/stash/pop", s.handle(s.handleGitStashPop))
			r.Method(http.MethodPost, "/stash/apply", s.handle(s.handleGitStashApply))
			// Staging, unstaging and committing are recoverable: nothing here
			// destroys work that exists nowhere else (a commit can be reset, a
			// stage unstaged), so they share the service.control tier rather
			// than the destructive one.
			r.Method(http.MethodPost, "/stage", s.handle(s.handleGitStage))
			r.Method(http.MethodPost, "/unstage", s.handle(s.handleGitUnstage))
			r.Method(http.MethodPost, "/commit", s.handle(s.handleGitCommitCreate))
			r.Method(http.MethodPost, "/identity", s.handle(s.handleGitIdentity))
			r.Method(http.MethodPost, "/remote", s.handle(s.handleGitRemoteAdd))
			r.Method(http.MethodPost, "/remote/update", s.handle(s.handleGitRemoteUpdate))
			r.Method(http.MethodPost, "/upstream", s.handle(s.handleGitUpstream))
			r.Method(http.MethodPost, "/recover", s.handle(s.handleGitRecover))
			r.Method(http.MethodPost, "/worktree", s.handle(s.handleGitWorktreeAdd))
			r.Method(http.MethodPost, "/operation/start", s.handle(s.handleGitOperationStart))
			r.Method(http.MethodPost, "/operation/continue", s.handle(s.handleGitOperationContinue))
			r.Method(http.MethodPost, "/patch/stage", s.handle(s.handleGitStagePartial))
			r.Method(http.MethodPost, "/rebase", s.handle(s.handleGitRebase))
			r.Method(http.MethodPost, "/submodule", s.handle(s.handleGitSubmodule))
			r.With(httpx.RequireCapability(auth.CapFileWrite)).Method(http.MethodPost, "/lfs", s.handle(s.handleGitLFSAction))
			r.With(httpx.RequireCapability(auth.CapFileWrite)).Method(http.MethodPost, "/patch/import", s.handle(s.handleGitPatchImport))
			r.With(httpx.RequireCapability(auth.CapFileWrite)).Method(http.MethodPost, "/conflict/resolve", s.handle(s.handleGitConflictResolve))
		})

		// Signing in to GitHub, and the operations git has no verb for.
		s.mountGitHubRoutes(r)
		s.mountForgeRoutes(r)

		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/submodule/remove", s.handle(s.handleGitSubmoduleRemove))
			r.Method(http.MethodPost, "/worktree/remove", s.handle(s.handleGitWorktreeRemove))
			r.Method(http.MethodPost, "/operation/abort", s.handle(s.handleGitOperationAbort))
			r.Method(http.MethodPost, "/conflict/choose", s.handle(s.handleGitConflictChoose))
			r.Method(http.MethodPost, "/discard", s.handle(s.handleGitDiscard))
			r.Method(http.MethodPost, "/reset", s.handle(s.handleGitReset))
			r.Method(http.MethodPost, "/stash/drop", s.handle(s.handleGitStashDrop))
			// Deleting a branch, a tag or a remote is destructive but not typed
			// for: the commits they pointed at survive in the reflog and on the
			// remote, so an ordinary confirmation is the right weight (see
			// handleGitBranchDelete, and invariant 3 for why frequency rather
			// than severity decides).
			r.Method(http.MethodPost, "/branch/delete", s.handle(s.handleGitBranchDelete))
			r.Method(http.MethodPost, "/branch/delete-remote", s.handle(s.handleGitBranchDeleteRemote))
			r.Method(http.MethodPost, "/tag/delete", s.handle(s.handleGitTagDelete))
			r.Method(http.MethodPost, "/remote/delete", s.handle(s.handleGitRemoteDelete))
		})
	})
}

// gitRepo resolves the ?path= parameter to a repository inside the configured
// roots, turning the package's sentinel errors into the right status codes.
func (s *Server) gitRepo(r *http.Request) (string, error) {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		return "", httpx.BadRequest("path query parameter is required")
	}
	path, err := s.modules.git.Resolve(raw)
	if err != nil {
		return "", gitErr(err)
	}
	return path, nil
}

func gitErr(err error) error {
	switch {
	case errors.Is(err, gitx.ErrNotInstalled):
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	case errors.Is(err, gitx.ErrOutsideRoots):
		return httpx.Err(http.StatusForbidden, "outside_roots", err.Error())
	case errors.Is(err, gitx.ErrNotARepo), errors.Is(err, gitx.ErrInvalidRef):
		return httpx.BadRequest("%v", err)
	}
	return httpx.Internal(err)
}

func (s *Server) handleGitRepos(w http.ResponseWriter, r *http.Request) error {
	repos, err := s.modules.git.Discover(r.Context())
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"available": s.modules.git.Available(),
		"repos":     repos,
	})
	return nil
}

// handleGitRoots lists where a clone may land: the configured roots that
// exist on this host.
func (s *Server) handleGitRoots(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"roots": s.modules.git.Roots()})
	return nil
}

// handleGitDetect resolves an arbitrary directory — the terminal's current
// working directory — to the checkout that contains it, if any. It answers in
// three shapes, all with HTTP 200 because "not a repository" is an ordinary
// answer and not a failure:
//
//	{available:false}                      git is not installed
//	{available:true}                       the directory is not inside a checkout
//	{available:true, root, inRoots:false}  a checkout, but outside JD_GIT_ROOTS
//	{available:true, inRoots:true, repo}   a checkout the panel can operate on
//
// The inRoots distinction matters: everything else in /git is gated on the
// configured roots, so a repo outside them can be reported but its buttons
// would fail. Saying which it is up front is kinder than letting them 403.
func (s *Server) handleGitDetect(w http.ResponseWriter, r *http.Request) error {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	if !s.modules.git.Available() {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false})
		return nil
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	root, err := s.modules.git.Toplevel(ctx, raw)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": true})
		return nil
	}
	resolved, rerr := s.modules.git.Resolve(root)
	if rerr != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "root": root, "inRoots": false})
		return nil
	}
	repo, err := s.modules.git.Summary(ctx, resolved)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "inRoots": true, "repo": repo})
	return nil
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	st, err := s.modules.git.Status(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

func (s *Server) handleGitLog(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	skip, _ := strconv.Atoi(q.Get("skip"))
	commits, err := s.modules.git.Log(r.Context(), path, gitx.LogQuery{
		Ref: q.Get("ref"), Limit: limit, Skip: skip,
		Search: q.Get("search"), Author: q.Get("author"), File: q.Get("file"),
	})
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, commits)
	return nil
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		return httpx.BadRequest("ref query parameter is required")
	}
	detail, err := s.modules.git.Show(r.Context(), path, ref)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handleGitCompare(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if q.Get("base") == "" || q.Get("head") == "" {
		return httpx.BadRequest("base and head query parameters are required")
	}
	cmp, err := s.modules.git.Compare(r.Context(), path, q.Get("base"), q.Get("head"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, cmp)
	return nil
}

func (s *Server) handleGitBranches(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	branches, err := s.modules.git.Branches(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, branches)
	return nil
}

func (s *Server) handleGitTags(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	tags, err := s.modules.git.Tags(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, tags)
	return nil
}

func (s *Server) handleGitStashes(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	stashes, err := s.modules.git.Stashes(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, stashes)
	return nil
}

func (s *Server) handleGitStashDiff(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil {
		return httpx.BadRequest("index query parameter is required")
	}
	diff, err := s.modules.git.StashDiff(r.Context(), path, index)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"diff": diff})
	return nil
}

func (s *Server) handleGitRemotes(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	remotes, err := s.modules.git.Remotes(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, remotes)
	return nil
}

func (s *Server) handleGitGraph(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	q := r.URL.Query()
	skip, _ := strconv.Atoi(q.Get("skip"))
	graph, err := s.modules.git.GraphPage(r.Context(), path, gitx.GraphQuery{
		Limit: limit, Skip: skip, Search: q.Get("search"), Ref: q.Get("ref"),
	})
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, graph)
	return nil
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	diff, err := s.modules.git.Diff(r.Context(), path, q.Get("ref"), q.Get("file"), q.Get("staged") == "true")
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"diff": diff})
	return nil
}

// gitAction runs one operation and records it, so every state change to a
// repository is in the audit trail with the repository it touched.
func (s *Server) gitAction(w http.ResponseWriter, r *http.Request, action string,
	fn func(path string) (*gitx.Result, error),
) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	return s.gitActionAt(w, r, action, path, fn)
}

// gitActionAt is gitAction for a path the caller has already resolved — a
// clone's parent directory, which is a place rather than a repository.
func (s *Server) gitActionAt(w http.ResponseWriter, r *http.Request, action, path string,
	fn func(path string) (*gitx.Result, error),
) error {
	unlock := s.modules.git.Lock(path)
	defer unlock()
	res, err := fn(path)
	httpx.SetAudit(r, "git."+action, path, map[string]any{"ok": err == nil})
	if err != nil {
		// git's own message is the useful part; a failed pull or checkout is
		// an ordinary outcome, not a server fault.
		if res != nil {
			return httpx.BadRequest("%s", res.Output)
		}
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type gitCloneRequest struct {
	URL    string `json:"url"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
	gitx.CloneOptions
}

// handleGitClone gets a repository onto this server. The parent directory is
// resolved against the git roots rather than the file roots: it is the git
// page's boundary, and a clone the list could not then show would be a
// checkout the page could not operate on.
func (s *Server) handleGitClone(w http.ResponseWriter, r *http.Request) error {
	var req gitCloneRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.URL == "" || req.Parent == "" {
		return httpx.BadRequest("url and parent are required")
	}
	// A clone can take minutes; the request waits for it rather than
	// answering before the files exist.
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	target, res, err := s.modules.git.CloneWithOptions(ctx, req.Parent, req.URL, req.Name, req.CloneOptions)
	httpx.SetAudit(r, "git.clone", req.Parent, map[string]any{"ok": err == nil, "url": req.URL, "name": req.Name})
	if err != nil {
		if res != nil {
			return httpx.BadRequest("%s", res.Output)
		}
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": target, "result": res})
	return nil
}

type gitPathRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleGitInit(w http.ResponseWriter, r *http.Request) error {
	var req gitPathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Path == "" {
		return httpx.BadRequest("path is required")
	}
	res, err := s.modules.git.Init(r.Context(), req.Path)
	httpx.SetAudit(r, "git.init", req.Path, map[string]any{"ok": err == nil})
	if err != nil {
		if res != nil {
			return httpx.BadRequest("%s", res.Output)
		}
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleGitFetch(w http.ResponseWriter, r *http.Request) error {
	prune := r.URL.Query().Get("prune") == "true"
	return s.gitAction(w, r, "fetch", func(p string) (*gitx.Result, error) {
		return s.modules.git.Fetch(r.Context(), p, prune)
	})
}

func (s *Server) handleGitPull(w http.ResponseWriter, r *http.Request) error {
	return s.gitAction(w, r, "pull", func(p string) (*gitx.Result, error) {
		return s.modules.git.Pull(r.Context(), p)
	})
}

func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) error {
	return s.gitAction(w, r, "push", func(p string) (*gitx.Result, error) {
		return s.modules.git.Push(r.Context(), p)
	})
}

type gitRefRequest struct {
	Ref     string   `json:"ref"`
	From    string   `json:"from"`
	Name    string   `json:"name"`
	Local   string   `json:"local"`
	Remote  string   `json:"remote"`
	URL     string   `json:"url"`
	Email   string   `json:"email"`
	Message string   `json:"message"`
	File    string   `json:"file"`
	Hard    bool     `json:"hard"`
	Clean   bool     `json:"clean"`
	Files   []string `json:"files"`
	Amend   bool     `json:"amend"`
	Index   int      `json:"index"`
	Pop     bool     `json:"pop"`
}

func (s *Server) handleGitPushTags(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	_ = httpx.DecodeJSON(r, &req)
	return s.gitAction(w, r, "push.tags", func(p string) (*gitx.Result, error) {
		return s.modules.git.PushTags(r.Context(), p, req.Ref)
	})
}

// handleGitCheckout switches to a local branch, or — when `local` names the
// branch to create — makes a tracking branch from a remote one and switches
// to that.
func (s *Server) handleGitCheckout(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "checkout", func(p string) (*gitx.Result, error) {
		if req.Local != "" {
			return s.modules.git.CheckoutRemote(r.Context(), p, req.Ref, req.Local)
		}
		return s.modules.git.Checkout(r.Context(), p, req.Ref)
	})
}

func (s *Server) handleGitBranch(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "branch.create", func(p string) (*gitx.Result, error) {
		return s.modules.git.CreateBranch(r.Context(), p, req.Ref, req.From)
	})
}

func (s *Server) handleGitBranchRename(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "branch.rename", func(p string) (*gitx.Result, error) {
		return s.modules.git.RenameBranch(r.Context(), p, req.Ref, req.Name)
	})
}

func (s *Server) handleGitMerge(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "merge", func(p string) (*gitx.Result, error) {
		return s.modules.git.Merge(r.Context(), p, req.Ref)
	})
}

func (s *Server) handleGitRevert(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "revert", func(p string) (*gitx.Result, error) {
		return s.modules.git.Revert(r.Context(), p, req.Ref)
	})
}

func (s *Server) handleGitCherryPick(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "cherry-pick", func(p string) (*gitx.Result, error) {
		return s.modules.git.CherryPick(r.Context(), p, req.Ref)
	})
}

func (s *Server) handleGitTag(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "tag.create", func(p string) (*gitx.Result, error) {
		return s.modules.git.CreateTag(r.Context(), p, req.Name, req.Ref, req.Message)
	})
}

func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "stage", func(p string) (*gitx.Result, error) {
		return s.modules.git.Stage(r.Context(), p, req.Files)
	})
}

func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "unstage", func(p string) (*gitx.Result, error) {
		return s.modules.git.Unstage(r.Context(), p, req.Files)
	})
}

func (s *Server) handleGitCommitCreate(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "commit", func(p string) (*gitx.Result, error) {
		return s.modules.git.Commit(r.Context(), p, req.Message, req.Amend)
	})
}

func (s *Server) handleGitIdentity(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "identity", func(p string) (*gitx.Result, error) {
		return s.modules.git.SetIdentity(r.Context(), p, req.Name, req.Email)
	})
}

func (s *Server) handleGitRemoteAdd(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "remote.add", func(p string) (*gitx.Result, error) {
		return s.modules.git.AddRemote(r.Context(), p, req.Name, req.URL)
	})
}

func (s *Server) handleGitRemoteDelete(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Ordinary confirmation: nothing on the remote changes, and adding the
	// remote back is the whole of the undo.
	return s.gitAction(w, r, "remote.delete", func(p string) (*gitx.Result, error) {
		return s.modules.git.RemoveRemote(r.Context(), p, req.Name)
	})
}

func (s *Server) handleGitStash(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	_ = httpx.DecodeJSON(r, &req)
	return s.gitAction(w, r, "stash", func(p string) (*gitx.Result, error) {
		return s.modules.git.Stash(r.Context(), p, req.Message)
	})
}

func (s *Server) handleGitStashPop(w http.ResponseWriter, r *http.Request) error {
	return s.gitAction(w, r, "stash.pop", func(p string) (*gitx.Result, error) {
		return s.modules.git.StashPop(r.Context(), p)
	})
}

func (s *Server) handleGitStashApply(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "stash.apply", func(p string) (*gitx.Result, error) {
		return s.modules.git.StashApply(r.Context(), p, req.Index, req.Pop)
	})
}

func (s *Server) handleGitStashDrop(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The work in a stash exists nowhere else once it is dropped — the same
	// argument as discard, and the same phrase weight.
	if err := httpx.RequireTypedConfirmation(w, r, "drop stash"); err != nil {
		return err
	}
	return s.gitAction(w, r, "stash.drop", func(p string) (*gitx.Result, error) {
		return s.modules.git.StashDrop(r.Context(), p, req.Index)
	})
}

func (s *Server) handleGitDiscard(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Discarding rewrites a file to its committed state, or deletes an
	// untracked one; the copy being overwritten exists nowhere else.
	if err := httpx.RequireTypedConfirmation(w, r, "discard changes"); err != nil {
		return err
	}
	return s.gitAction(w, r, "discard", func(p string) (*gitx.Result, error) {
		return s.modules.git.Discard(r.Context(), p, req.File)
	})
}

func (s *Server) handleGitBranchDelete(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// An ordinary confirmation, not a typed phrase: a deleted branch is a name
	// pointing at a commit, and the commit survives in the reflog and on the
	// remote. Losing the pointer is recoverable in a way that discarding
	// uncommitted work is not, which is where the phrase still stands.
	return s.gitAction(w, r, "branch.delete", func(p string) (*gitx.Result, error) {
		return s.modules.git.DeleteBranch(r.Context(), p, req.Ref, req.Hard)
	})
}

func (s *Server) handleGitBranchDeleteRemote(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "branch.delete.remote", func(p string) (*gitx.Result, error) {
		return s.modules.git.DeleteRemoteBranch(r.Context(), p, req.Remote, req.Ref)
	})
}

func (s *Server) handleGitTagDelete(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "tag.delete", func(p string) (*gitx.Result, error) {
		return s.modules.git.DeleteTag(r.Context(), p, req.Name)
	})
}

func (s *Server) handleGitReset(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Only a hard reset is typed for. A soft or mixed reset moves the branch
	// pointer and leaves the working tree alone, so the work is still on disk;
	// --hard is the one that overwrites it with the commit and leaves no copy.
	if req.Hard {
		if err := httpx.RequireTypedConfirmation(w, r, "reset hard"); err != nil {
			return err
		}
	}
	return s.gitAction(w, r, "reset", func(p string) (*gitx.Result, error) {
		return s.modules.git.Reset(r.Context(), p, req.Ref, req.Hard, req.Clean)
	})
}
