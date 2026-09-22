package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGitHubPullFiles(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page == 0 {
		page = 1
	}
	data, err := s.modules.github.PullFiles(r.Context(), path, number, page, r.URL.Query().Get("head"))
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, data)
	return nil
}
func (s *Server) handleGitHubConversation(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page == 0 {
		page = 1
	}
	data, err := s.modules.github.Conversation(r.Context(), path, number, page)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, data)
	return nil
}
func (s *Server) handleGitHubReview(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	var req ghx.ReviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	err = s.modules.github.ReviewPull(r.Context(), path, number, req)
	httpx.SetAudit(r, "github.pull.review", path, map[string]any{"ok": err == nil, "number": number, "event": req.Event, "head": req.HeadSHA})
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func (s *Server) handleGitHubRun(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	data, err := s.modules.github.ViewRun(r.Context(), path, id)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, data)
	return nil
}
func (s *Server) handleGitHubRunLog(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	q := r.URL.Query()
	job, _ := strconv.ParseInt(q.Get("job"), 10, 64)
	data, err := s.modules.github.RunLog(r.Context(), path, id, job, q.Get("step"), q.Get("failed") == "true")
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"body": data})
	return nil
}
