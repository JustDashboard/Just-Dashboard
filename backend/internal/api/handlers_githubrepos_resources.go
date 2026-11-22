package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// What is inside one GitHub repository on the GitHub page, under
// /github/connections/{connection}/repos/{owner}/{repo}: branches, commits,
// pull requests, issues and labels, workflows and runs, collaborators.
//
// Every handler starts from s.githubRepo(r), which resolves the signed-in
// account's own connection and the repository with its abilities, and every
// change is checked against those abilities (the person's role there and
// the App's grant) before GitHub is asked, so a refusal says which is
// missing. GitHub still decides; its refusals come back through
// githubPageErr in its words.
//
//	read            branches and their protection, commits, pull requests
//	                with reviews and checks, issues, labels, workflows, runs,
//	                collaborators
//	service.control creating a branch, merging, closing and reopening a pull
//	                request, opening and editing an issue, starting,
//	                re-running and cancelling a workflow run, inviting a
//	                collaborator or changing their role — each recoverable on
//	                GitHub
//	s.destructive   deleting a branch, removing a collaborator, withdrawing
//	                an invitation (ordinary confirmation)
//
// A title, a body, a commit message or an input value is never an audit
// detail; the number, branch, username, role or method is.
func (s *Server) mountGitHubResourceRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/branches", s.handle(s.handleGitHubRepoBranches))
	r.Method(http.MethodGet, "/branches/protection", s.handle(s.handleGitHubRepoBranchProtection))
	r.Method(http.MethodGet, "/commits", s.handle(s.handleGitHubRepoCommits))
	r.Method(http.MethodGet, "/pulls", s.handle(s.handleGitHubRepoPulls))
	r.Method(http.MethodGet, "/pulls/{number}", s.handle(s.handleGitHubRepoPull))
	r.Method(http.MethodGet, "/issues", s.handle(s.handleGitHubRepoIssues))
	r.Method(http.MethodGet, "/issues/{number}", s.handle(s.handleGitHubRepoIssue))
	r.Method(http.MethodGet, "/labels", s.handle(s.handleGitHubRepoLabels))
	r.Method(http.MethodGet, "/actions/workflows", s.handle(s.handleGitHubRepoWorkflows))
	r.Method(http.MethodGet, "/actions/runs", s.handle(s.handleGitHubRepoRuns))
	r.Method(http.MethodGet, "/collaborators", s.handle(s.handleGitHubRepoCollaborators))

	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/branches", s.handle(s.handleGitHubRepoBranchCreate))
		r.Method(http.MethodPost, "/pulls/{number}/merge", s.handle(s.handleGitHubRepoPullMerge))
		r.Method(http.MethodPost, "/pulls/{number}/state", s.handle(s.handleGitHubRepoPullState))
		r.Method(http.MethodPost, "/issues", s.handle(s.handleGitHubRepoIssueCreate))
		r.Method(http.MethodPatch, "/issues/{number}", s.handle(s.handleGitHubRepoIssueUpdate))
		r.Method(http.MethodPost, "/actions/workflows/{workflow}/dispatch", s.handle(s.handleGitHubRepoWorkflowDispatch))
		r.Method(http.MethodPost, "/actions/runs/{run}/rerun", s.handle(s.handleGitHubRepoRunRerun))
		r.Method(http.MethodPost, "/actions/runs/{run}/cancel", s.handle(s.handleGitHubRepoRunCancel))
		r.Method(http.MethodPut, "/collaborators/{username}", s.handle(s.handleGitHubRepoCollaboratorSet))
	})

	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/branches", s.handle(s.handleGitHubRepoBranchDelete))
		r.Method(http.MethodDelete, "/collaborators/{username}", s.handle(s.handleGitHubRepoCollaboratorRemove))
		r.Method(http.MethodDelete, "/invitations/{invitation}", s.handle(s.handleGitHubRepoInvitationCancel))
	})
}

// githubResourceErr is githubPageErr with the package's own refusals of a
// value (a branch name git would not take, a merge without its head) as the
// 400 they are.
func githubResourceErr(err error) error {
	var bad *githubrepos.InvalidError
	if errors.As(err, &bad) {
		return httpx.BadRequest("%s", bad.Reason)
	}
	return githubPageErr(err)
}

// ghResourceNumber reads a positive integer path parameter before anything
// is resolved, so a malformed address costs no request to GitHub.
func ghResourceNumber(r *http.Request, param, what string) (int64, error) {
	n, err := githubrepos.ParsePositive(chi.URLParam(r, param), what)
	if err != nil {
		return 0, githubResourceErr(err)
	}
	return n, nil
}

func ghResourcePage(r *http.Request) int {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return page
}

// ghResourceTarget is the audit target of a change: GitHub's own spelling
// of the repository.
func ghResourceTarget(access *githubrepos.RepoAccess) string {
	return access.Owner + "/" + access.Name
}

// ghResourceChanged records a mutation that GitHub accepted or refused, and on
// success drops what is cached about the repository so the next read asks
// GitHub again.
func (s *Server) ghResourceChanged(r *http.Request, access *githubrepos.RepoAccess, action string, detail map[string]any, err error) {
	detail["ok"] = err == nil
	httpx.SetAudit(r, action, ghResourceTarget(access), detail)
	if err == nil {
		s.forgetGitHubRepo(access)
	}
}

// ghResourceAccess resolves the repository and answers its error in the page's
// words.
func (s *Server) ghResourceAccess(r *http.Request) (*githubrepos.RepoAccess, error) {
	access, err := s.githubRepo(r)
	if err != nil {
		return nil, githubResourceErr(err)
	}
	return access, nil
}

// ghResourceRead is the shape of every read: resolve, ask, answer 200.
func ghResourceRead[T any](s *Server, w http.ResponseWriter, r *http.Request, read func(*githubrepos.RepoAccess) (T, error)) error {
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	out, err := read(access)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// --- branches ---

func (s *Server) handleGitHubRepoBranches(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.BranchPage, error) {
		return access.Branches(r.Context(), ghResourcePage(r))
	})
}

func (s *Server) handleGitHubRepoBranchProtection(w http.ResponseWriter, r *http.Request) error {
	name := r.URL.Query().Get("name")
	if err := githubrepos.ValidBranchName(name); err != nil {
		return githubResourceErr(err)
	}
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.BranchProtection, error) {
		return access.BranchProtection(r.Context(), name)
	})
}

func (s *Server) handleGitHubRepoBranchCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
		From string `json:"from"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := githubrepos.ValidBranchName(req.Name); err != nil {
		return githubResourceErr(err)
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	branch, err := access.CreateBranch(r.Context(), req.Name, req.From)
	s.ghResourceChanged(r, access, "github.branch.create", map[string]any{"branch": req.Name}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusCreated, branch)
	return nil
}

func (s *Server) handleGitHubRepoBranchDelete(w http.ResponseWriter, r *http.Request) error {
	name := r.URL.Query().Get("name")
	if err := githubrepos.ValidBranchName(name); err != nil {
		return githubResourceErr(err)
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	err = access.DeleteBranch(r.Context(), name)
	s.ghResourceChanged(r, access, "github.branch.delete", map[string]any{"branch": name}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.NoContent(w)
	return nil
}

// --- commits ---

func (s *Server) handleGitHubRepoCommits(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.CommitPage, error) {
		return access.Commits(r.Context(), r.URL.Query().Get("sha"), ghResourcePage(r))
	})
}

// --- pull requests ---

func (s *Server) handleGitHubRepoPulls(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.PullPage, error) {
		return access.Pulls(r.Context(), r.URL.Query().Get("state"), ghResourcePage(r))
	})
}

func (s *Server) handleGitHubRepoPull(w http.ResponseWriter, r *http.Request) error {
	number, err := ghResourceNumber(r, "number", "pull request")
	if err != nil {
		return err
	}
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.PullDetail, error) {
		return access.Pull(r.Context(), number)
	})
}

func (s *Server) handleGitHubRepoPullMerge(w http.ResponseWriter, r *http.Request) error {
	number, err := ghResourceNumber(r, "number", "pull request")
	if err != nil {
		return err
	}
	var req githubrepos.MergeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The head guard is checked before anything is resolved: a merge that
	// does not name the commit the person reviewed is never sent.
	if !githubrepos.IsFullSHA(strings.ToLower(strings.TrimSpace(req.SHA))) {
		return httpx.BadRequest("the merge needs the full 40-character head commit you reviewed")
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	result, err := access.Merge(r.Context(), number, req)
	s.ghResourceChanged(r, access, "github.pull.merge", map[string]any{"number": number, "method": req.Method}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleGitHubRepoPullState(w http.ResponseWriter, r *http.Request) error {
	number, err := ghResourceNumber(r, "number", "pull request")
	if err != nil {
		return err
	}
	var req struct {
		State string `json:"state"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	pull, err := access.SetPullState(r.Context(), number, req.State)
	s.ghResourceChanged(r, access, "github.pull.state", map[string]any{"number": number, "state": req.State}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, pull)
	return nil
}

// --- issues ---

func (s *Server) handleGitHubRepoIssues(w http.ResponseWriter, r *http.Request) error {
	var labels []string
	for _, label := range strings.Split(r.URL.Query().Get("labels"), ",") {
		if label = strings.TrimSpace(label); label != "" {
			labels = append(labels, label)
		}
	}
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.IssuePage, error) {
		return access.Issues(r.Context(), r.URL.Query().Get("state"), labels, ghResourcePage(r))
	})
}

func (s *Server) handleGitHubRepoIssue(w http.ResponseWriter, r *http.Request) error {
	number, err := ghResourceNumber(r, "number", "issue")
	if err != nil {
		return err
	}
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.Issue, error) {
		return access.Issue(r.Context(), number)
	})
}

func (s *Server) handleGitHubRepoIssueCreate(w http.ResponseWriter, r *http.Request) error {
	var req githubrepos.IssueCreate
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	issue, err := access.CreateIssue(r.Context(), req)
	detail := map[string]any{"labels": len(req.Labels)}
	if issue != nil {
		detail["number"] = issue.Number
	}
	s.ghResourceChanged(r, access, "github.issue.create", detail, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusCreated, issue)
	return nil
}

func (s *Server) handleGitHubRepoIssueUpdate(w http.ResponseWriter, r *http.Request) error {
	number, err := ghResourceNumber(r, "number", "issue")
	if err != nil {
		return err
	}
	var req githubrepos.IssueUpdate
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	issue, err := access.UpdateIssue(r.Context(), number, req)
	// Which fields changed, never what they say.
	var fields []string
	for name, set := range map[string]bool{
		"title": req.Title != nil, "body": req.Body != nil, "state": req.State != nil,
		"stateReason": req.StateReason != nil, "labels": req.Labels != nil,
	} {
		if set {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	detail := map[string]any{"number": number, "fields": strings.Join(fields, ",")}
	if req.State != nil {
		detail["state"] = *req.State
	}
	s.ghResourceChanged(r, access, "github.issue.update", detail, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, issue)
	return nil
}

func (s *Server) handleGitHubRepoLabels(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) ([]githubrepos.Label, error) {
		return access.Labels(r.Context())
	})
}

// --- actions ---

func (s *Server) handleGitHubRepoWorkflows(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.WorkflowList, error) {
		return access.Workflows(r.Context())
	})
}

func (s *Server) handleGitHubRepoRuns(w http.ResponseWriter, r *http.Request) error {
	var workflow int64
	if raw := r.URL.Query().Get("workflow"); raw != "" {
		n, err := githubrepos.ParsePositive(raw, "workflow")
		if err != nil {
			return githubResourceErr(err)
		}
		workflow = n
	}
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.RunPage, error) {
		return access.Runs(r.Context(), workflow, r.URL.Query().Get("branch"), ghResourcePage(r))
	})
}

func (s *Server) handleGitHubRepoWorkflowDispatch(w http.ResponseWriter, r *http.Request) error {
	workflow, err := ghResourceNumber(r, "workflow", "workflow")
	if err != nil {
		return err
	}
	var req githubrepos.DispatchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	result, err := access.Dispatch(r.Context(), workflow, req)
	// The input names may say what a run is for; their values may be
	// secrets someone pasted, so only the count is kept.
	s.ghResourceChanged(r, access, "github.workflow.dispatch", map[string]any{"workflow": workflow, "ref": req.Ref, "inputs": len(req.Inputs)}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleGitHubRepoRunRerun(w http.ResponseWriter, r *http.Request) error {
	run, err := ghResourceNumber(r, "run", "run")
	if err != nil {
		return err
	}
	var req struct {
		FailedOnly bool `json:"failedOnly"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	err = access.Rerun(r.Context(), run, req.FailedOnly)
	s.ghResourceChanged(r, access, "github.run.rerun", map[string]any{"run": run, "failedOnly": req.FailedOnly}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleGitHubRepoRunCancel(w http.ResponseWriter, r *http.Request) error {
	run, err := ghResourceNumber(r, "run", "run")
	if err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	err = access.CancelRun(r.Context(), run)
	s.ghResourceChanged(r, access, "github.run.cancel", map[string]any{"run": run}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.NoContent(w)
	return nil
}

// --- collaborators ---

func (s *Server) handleGitHubRepoCollaborators(w http.ResponseWriter, r *http.Request) error {
	return ghResourceRead(s, w, r, func(access *githubrepos.RepoAccess) (*githubrepos.Collaborators, error) {
		return access.Collaborators(r.Context())
	})
}

func ghResourceUsername(r *http.Request) (string, error) {
	login := chi.URLParam(r, "username")
	if !githubrepos.ValidOwner(login) {
		return "", httpx.BadRequest("%q is not a GitHub username", login)
	}
	return login, nil
}

func (s *Server) handleGitHubRepoCollaboratorSet(w http.ResponseWriter, r *http.Request) error {
	login, err := ghResourceUsername(r)
	if err != nil {
		return err
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	result, err := access.SetCollaborator(r.Context(), login, req.Role)
	detail := map[string]any{"username": login, "role": req.Role}
	if result != nil {
		detail["invited"] = result.Invited
	}
	s.ghResourceChanged(r, access, "github.collaborator.set", detail, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleGitHubRepoCollaboratorRemove(w http.ResponseWriter, r *http.Request) error {
	login, err := ghResourceUsername(r)
	if err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	err = access.RemoveCollaborator(r.Context(), login)
	s.ghResourceChanged(r, access, "github.collaborator.remove", map[string]any{"username": login}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleGitHubRepoInvitationCancel(w http.ResponseWriter, r *http.Request) error {
	invitation, err := ghResourceNumber(r, "invitation", "invitation")
	if err != nil {
		return err
	}
	access, err := s.ghResourceAccess(r)
	if err != nil {
		return err
	}
	err = access.CancelInvitation(r.Context(), invitation)
	s.ghResourceChanged(r, access, "github.invitation.cancel", map[string]any{"invitation": invitation}, err)
	if err != nil {
		return githubResourceErr(err)
	}
	httpx.NoContent(w)
	return nil
}
