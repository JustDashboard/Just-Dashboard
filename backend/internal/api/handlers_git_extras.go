package api

import (
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"net/http"
)

func (s *Server) handleGitSubmodules(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	rows, err := s.modules.git.Submodules(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, rows)
	return nil
}
func (s *Server) handleGitSubmodule(w http.ResponseWriter, r *http.Request) error {
	var req gitx.SubmoduleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Action != "add" && req.Action != "update" && req.Action != "sync" {
		return httpx.BadRequest("choose add, update or sync")
	}
	return s.gitAction(w, r, "submodule."+req.Action, func(path string) (*gitx.Result, error) { return s.modules.git.ManageSubmodule(r.Context(), path, req) })
}
func (s *Server) handleGitSubmoduleRemove(w http.ResponseWriter, r *http.Request) error {
	var req gitx.SubmoduleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Action != "deinit" && req.Action != "remove" {
		return httpx.BadRequest("choose deinit or remove")
	}
	return s.gitAction(w, r, "submodule."+req.Action, func(path string) (*gitx.Result, error) { return s.modules.git.ManageSubmodule(r.Context(), path, req) })
}
func (s *Server) handleGitLFS(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	status, err := s.modules.git.LFS(r.Context(), path)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}
func (s *Server) handleGitLFSAction(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Action  string `json:"action"`
		Pattern string `json:"pattern"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "lfs."+req.Action, func(path string) (*gitx.Result, error) {
		return s.modules.git.ManageLFS(r.Context(), path, req.Action, req.Pattern)
	})
}
func (s *Server) handleGitPatchExport(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	body, err := s.modules.git.ExportPatch(r.Context(), path, q.Get("mode"), q.Get("ref"))
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"body": body})
	return nil
}
func (s *Server) handleGitPatchCheck(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	var req gitx.PatchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	data, err := s.modules.git.CheckPatch(r.Context(), path, req)
	if err != nil {
		return gitErr(err)
	}
	httpx.JSON(w, http.StatusOK, data)
	return nil
}
func (s *Server) handleGitPatchImport(w http.ResponseWriter, r *http.Request) error {
	var req gitx.PatchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.gitAction(w, r, "patch.import", func(path string) (*gitx.Result, error) { return s.modules.git.ImportPatch(r.Context(), path, req) })
}
