package api

import (
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/forgex"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
)

func (s *Server) mountForgeRoutes(r chi.Router) {
	r.Route("/forge", func(r chi.Router) {
		r.Method("GET", "/", s.handle(s.handleForgeStatus))
		r.Method("GET", "/requests", s.handle(s.handleForgeRequests))
		r.Method("GET", "/requests/{number}", s.handle(s.handleForgeRequest))
		r.Method("GET", "/requests/{number}/files", s.handle(s.handleForgeFiles))
		r.Method("GET", "/requests/{number}/conversation", s.handle(s.handleForgeConversation))
		r.With(httpx.RequireCapability(auth.CapSystemAdmin)).Method("POST", "/account", s.handle(s.handleForgeAccount))
		r.With(httpx.RequireCapability(auth.CapSystemAdmin)).Method("POST", "/disconnect", s.handle(s.handleForgeDisconnect))
		r.With(httpx.RequireCapability(auth.CapServiceControl)).Method("POST", "/requests", s.handle(s.handleForgeCreate))
		r.With(httpx.RequireCapability(auth.CapServiceControl)).Method("POST", "/requests/{number}/action", s.handle(s.handleForgeAction))
	})
}
func (s *Server) handleForgeStatus(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	data, err := s.modules.forge.Status(r.Context(), path)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeAccount(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	var req forgex.Setup
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	data, err := s.modules.forge.Configure(r.Context(), path, req)
	httpx.SetAudit(r, "git.forge.account", path, map[string]any{"ok": err == nil, "provider": req.Kind})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeDisconnect(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	err = s.modules.forge.Disconnect(r.Context(), path)
	httpx.SetAudit(r, "git.forge.disconnect", path, map[string]any{"ok": err == nil})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, map[string]bool{"ok": true})
	return nil
}
func forgePage(r *http.Request) int {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page == 0 {
		return 1
	}
	return page
}
func (s *Server) handleForgeRequests(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}
	data, err := s.modules.forge.List(r.Context(), path, state, forgePage(r))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeRequest(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	data, err := s.modules.forge.View(r.Context(), path, number)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeFiles(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	data, err := s.modules.forge.Files(r.Context(), path, number, forgePage(r), r.URL.Query().Get("head"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeConversation(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	data, err := s.modules.forge.Conversation(r.Context(), path, number, forgePage(r))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeCreate(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	var req forgex.NewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	data, err := s.modules.forge.Create(r.Context(), path, req)
	httpx.SetAudit(r, "git.forge.create", path, map[string]any{"ok": err == nil, "head": req.Head, "base": req.Base})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, data)
	return nil
}
func (s *Server) handleForgeAction(w http.ResponseWriter, r *http.Request) error {
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	var req forgex.Action
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	err = s.modules.forge.Act(r.Context(), path, number, req)
	httpx.SetAudit(r, "git.forge.action", path, map[string]any{"ok": err == nil, "number": number, "action": req.Action, "head": req.SHA})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, 200, map[string]bool{"ok": true})
	return nil
}
