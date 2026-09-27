package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountAuthFileRoutes is the password files behind the site form's basic-auth
// option. Behind system.admin throughout: the listing names who may reach a
// protected site, and setting one is handing out access to it.
func (s *Server) mountAuthFileRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodGet, "/", s.handle(s.handleAuthFileList))
	r.Method(http.MethodPost, "/", s.handle(s.handleAuthUserSet))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{file}", s.handle(s.handleAuthFileDelete))
		r.Method(http.MethodDelete, "/{file}/users/{user}", s.handle(s.handleAuthUserRemove))
	})
}

func (s *Server) handleAuthFileList(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.ListAuthFiles())
	return nil
}

type authUserRequest struct {
	File     string `json:"file"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// handleAuthUserSet adds or replaces one entry. The password is hashed in
// process and never becomes an argument to anything — /proc/*/cmdline is
// world-readable, which is the same reason dbx keeps database passwords out of
// argv.
func (s *Server) handleAuthUserSet(w http.ResponseWriter, r *http.Request) error {
	var req authUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	file, err := s.modules.proxy.SetAuthUser(req.File, req.User, req.Password)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The password is deliberately absent from the audit detail.
	httpx.SetAudit(r, "proxy.auth.set", req.File, map[string]any{"user": req.User})
	httpx.JSON(w, http.StatusOK, file)
	return nil
}

func (s *Server) handleAuthUserRemove(w http.ResponseWriter, r *http.Request) error {
	file, user := chi.URLParam(r, "file"), chi.URLParam(r, "user")
	updated, err := s.modules.proxy.RemoveAuthUser(file, user)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "proxy.auth.remove", file, map[string]any{"user": user})
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleAuthFileDelete(w http.ResponseWriter, r *http.Request) error {
	file := chi.URLParam(r, "file")
	if err := s.modules.proxy.DeleteAuthFile(file); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "proxy.auth.delete", file, nil)
	httpx.NoContent(w)
	return nil
}
