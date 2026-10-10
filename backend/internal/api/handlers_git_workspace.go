package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) handleGitReflog(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
	rows, err := s.modules.git.Reflog(r.Context(), path, skip)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}

func (s *Server) handleGitBlame(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("start"))
	result, err := s.modules.git.Blame(r.Context(), path, q.Get("ref"), q.Get("file"), start)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleGitSignature(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	result, err := s.modules.git.Signature(r.Context(), path, r.URL.Query().Get("ref"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleGitCompareDiff(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	diff, err := s.modules.git.CompareDiff(r.Context(), path, q.Get("base"), q.Get("head"), q.Get("file"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"diff": diff})
	return nil
}

func (s *Server) handleGitWorktrees(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	rows, err := s.modules.git.Worktrees(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}

func (s *Server) handleGitWorktreeAdd(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
		Branch string `json:"branch"`
		From   string `json:"from"`
		Create bool   `json:"create"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "worktree.add", func(path string) (*gitx.Result, error) {
		return s.modules.git.AddWorktree(r.Context(), path, req.Parent, req.Name, req.Branch, req.From, req.Create)
	})
}

func (s *Server) handleGitWorktreeRemove(w http.ResponseWriter, r *http.Request) error {
	var req gitPathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "worktree.remove", func(path string) (*gitx.Result, error) {
		return s.modules.git.RemoveWorktree(r.Context(), path, req.Path)
	})
}

// handleGitRemoval answers what deleting a checkout would lose — the
// uncommitted files, stashes and unpushed branches that exist only here, the
// worktrees and nested checkouts that would go with it — and, in `protected`,
// why the delete would be refused.
func (s *Server) handleGitRemoval(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, time.Minute)
	defer cancel()
	removal, err := s.modules.git.Removal(ctx, path, s.Cfg.UpdateDir)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, removal)
	return nil
}

// handleGitRepositoryDelete deletes a checkout from this server.
//
// It is one of the few routes that takes a typed phrase — the checkout's
// directory name — because it is rare and nothing brings it back: the work a
// checkout holds that was never pushed (uncommitted files, stashes, local
// branches) lives only in that directory. What it refuses (a root, the
// dashboard's own install, a repository with worktrees or other checkouts
// inside it, a locked worktree) is refused before the phrase is asked for,
// so nobody types a name for an act that would not happen.
func (s *Server) handleGitRepositoryDelete(w http.ResponseWriter, r *http.Request) error {
	var req gitPathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Path == "" {
		req.Path = r.URL.Query().Get("path")
	}
	if req.Path == "" {
		return httpx.BadRequest("path is required")
	}
	path, err := s.modules.git.Resolve(req.Path)
	if err != nil {
		return gitErr(err)
	}
	unlock := s.modules.git.Lock(path)
	defer unlock()
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	removal, err := s.modules.git.Removal(ctx, path, s.Cfg.UpdateDir)
	if err != nil {
		return gitErr(err)
	}
	if removal.Protected != "" {
		return httpx.Err(http.StatusConflict, "protected", removal.Protected)
	}
	if err := httpx.RequireTypedConfirmation(w, r, filepath.Base(path)); err != nil {
		return err
	}
	res, err := s.modules.git.DeleteRepository(ctx, path, s.Cfg.UpdateDir)
	httpx.SetAudit(r, "git.repository.delete", path, map[string]any{"ok": err == nil, "worktree": removal.Worktree})
	if err != nil {
		if res != nil {
			return httpx.BadRequest("%s", res.Output)
		}
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "removed": true})
	return nil
}

func (s *Server) handleGitRecover(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "recover", func(path string) (*gitx.Result, error) {
		return s.modules.git.Recover(r.Context(), path, req.Name, req.Ref)
	})
}

func (s *Server) handleGitRemoteUpdate(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "remote.update", func(path string) (*gitx.Result, error) {
		return s.modules.git.SetRemoteURL(r.Context(), path, req.Name, req.URL)
	})
}

func (s *Server) handleGitUpstream(w http.ResponseWriter, r *http.Request) error {
	var req gitRefRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "upstream", func(path string) (*gitx.Result, error) {
		return s.modules.git.SetUpstream(r.Context(), path, req.Name, req.Ref)
	})
}

func (s *Server) handleGitConflict(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	conflict, err := s.modules.git.Conflict(r.Context(), path, r.URL.Query().Get("file"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, conflict)
	return nil
}

func (s *Server) handleGitConflictResolve(w http.ResponseWriter, r *http.Request) error {
	var req gitx.ResolveConflictRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Choice != "result" {
		return httpx.BadRequest("this route accepts an edited text result")
	}
	return s.gitAction(w, r, "conflict.resolve", func(path string) (*gitx.Result, error) {
		return s.modules.git.ResolveConflict(r.Context(), path, req)
	})
}

func (s *Server) handleGitConflictChoose(w http.ResponseWriter, r *http.Request) error {
	var req gitx.ResolveConflictRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Choice != "ours" && req.Choice != "theirs" && req.Choice != "delete" {
		return httpx.BadRequest("choose a side or deletion")
	}
	return s.gitAction(w, r, "conflict.choose", func(path string) (*gitx.Result, error) {
		return s.modules.git.ResolveConflict(r.Context(), path, req)
	})
}

func (s *Server) handleGitOperationStart(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Operation string `json:"operation"`
		Ref       string `json:"ref"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "operation.start", func(path string) (*gitx.Result, error) {
		return s.modules.git.StartOperation(r.Context(), path, req.Operation, req.Ref)
	})
}

func (s *Server) handleGitOperationContinue(w http.ResponseWriter, r *http.Request) error {
	return s.gitAction(w, r, "operation.continue", func(path string) (*gitx.Result, error) {
		return s.modules.git.ContinueOperation(r.Context(), path)
	})
}

func (s *Server) handleGitOperationAbort(w http.ResponseWriter, r *http.Request) error {
	return s.gitAction(w, r, "operation.abort", func(path string) (*gitx.Result, error) {
		return s.modules.git.AbortOperation(r.Context(), path)
	})
}

func (s *Server) handleGitPartialDiff(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	diff, err := s.modules.git.PartialDiff(r.Context(), path, q.Get("file"), q.Get("staged") == "true")
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, diff)
	return nil
}

func (s *Server) handleGitStagePartial(w http.ResponseWriter, r *http.Request) error {
	var req gitx.PartialStageRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "stage.partial", func(path string) (*gitx.Result, error) {
		return s.modules.git.StagePartial(r.Context(), path, req)
	})
}

func (s *Server) handleGitRebasePlan(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	plan, err := s.modules.git.RebasePlan(r.Context(), path, r.URL.Query().Get("base"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

func (s *Server) handleGitRebase(w http.ResponseWriter, r *http.Request) error {
	var req gitx.RebasePlan
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "rebase", func(path string) (*gitx.Result, error) { return s.modules.git.StartRebase(r.Context(), path, req) })
}
