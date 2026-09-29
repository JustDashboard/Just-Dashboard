package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountLinuxUserRoutes(r chi.Router) {
	r.Route("/system-users", func(r chi.Router) {
		// Creating a host account hands somebody a way onto the machine, so
		// the whole section requires system-admin, reads included: the list
		// exposes shells, groups and key counts.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleSysUserList))
		r.Method(http.MethodGet, "/groups", s.handle(s.handleSysGroupList))
		r.Method(http.MethodGet, "/{name}", s.handle(s.handleSysUserGet))
		r.Method(http.MethodPost, "/", s.handle(s.handleSysUserCreate))
		r.Method(http.MethodPatch, "/{name}", s.handle(s.handleSysUserUpdate))

		r.Method(http.MethodGet, "/{name}/keys", s.handle(s.handleSSHKeyList))
		r.Method(http.MethodPost, "/{name}/keys", s.handle(s.handleSSHKeyAdd))

		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{name}", s.handle(s.handleSysUserDelete))
			r.Method(http.MethodDelete, "/{name}/keys", s.handle(s.handleSSHKeyRemove))
		})
	})
}

func mapLinuxUserError(err error) error {
	switch {
	case errors.Is(err, linuxusers.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, linuxusers.ErrProtected):
		return httpx.Err(http.StatusForbidden, "protected_account", err.Error())
	case errors.Is(err, linuxusers.ErrInvalidUser):
		return httpx.Err(http.StatusBadRequest, "invalid_username", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func (s *Server) handleSysUserList(w http.ResponseWriter, r *http.Request) error {
	users, err := s.modules.linuxUsers.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	if r.URL.Query().Get("system") != "true" {
		filtered := users[:0]
		for _, u := range users {
			if !u.System {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	httpx.JSON(w, http.StatusOK, users)
	return nil
}

func (s *Server) handleSysGroupList(w http.ResponseWriter, r *http.Request) error {
	groups, err := s.modules.linuxUsers.ListGroups(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, groups)
	return nil
}

func (s *Server) handleSysUserGet(w http.ResponseWriter, r *http.Request) error {
	u, err := s.modules.linuxUsers.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, u)
	return nil
}

type createSysUserRequest struct {
	Username string   `json:"username"`
	Comment  string   `json:"comment"`
	Shell    string   `json:"shell"`
	Groups   []string `json:"groups"`
	System   bool     `json:"system"`
	NoHome   bool     `json:"noHome"`
	SSHKey   string   `json:"sshKey"`
}

func (s *Server) handleSysUserCreate(w http.ResponseWriter, r *http.Request) error {
	var req createSysUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The key is validated before the account exists, so a typo does not
	// leave a passwordless account with no way to log into it.
	if req.SSHKey != "" {
		if _, err := linuxusers.ValidatePublicKey(req.SSHKey); err != nil {
			return httpx.BadRequest("ssh key rejected: %v", err)
		}
	}
	if err := s.modules.linuxUsers.Create(r.Context(), linuxusers.CreateOptions{
		Username: req.Username, Comment: req.Comment, Shell: req.Shell,
		Groups: req.Groups, System: req.System, NoHome: req.NoHome,
	}); err != nil {
		return mapLinuxUserError(err)
	}
	if req.SSHKey != "" {
		if _, err := s.modules.linuxUsers.AddKey(req.Username, req.SSHKey); err != nil {
			httpx.SetAudit(r, "system.user.create", req.Username,
				map[string]any{"groups": req.Groups, "keyError": err.Error()})
			return httpx.BadRequest("account created but the ssh key could not be installed: %v", err)
		}
	}
	u, err := s.modules.linuxUsers.Get(r.Context(), req.Username)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.create", req.Username,
		map[string]any{"groups": req.Groups, "shell": req.Shell, "withKey": req.SSHKey != ""})
	httpx.JSON(w, http.StatusCreated, u)
	return nil
}

type updateSysUserRequest struct {
	Locked *bool     `json:"locked,omitempty"`
	Shell  *string   `json:"shell,omitempty"`
	Groups *[]string `json:"groups,omitempty"`
}

func (s *Server) handleSysUserUpdate(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req updateSysUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Locked != nil {
		if err := s.modules.linuxUsers.SetLocked(r.Context(), name, *req.Locked); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Shell != nil {
		if err := s.modules.linuxUsers.SetShell(r.Context(), name, *req.Shell); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Groups != nil {
		if err := s.modules.linuxUsers.SetGroups(r.Context(), name, *req.Groups); err != nil {
			return mapLinuxUserError(err)
		}
	}
	u, err := s.modules.linuxUsers.Get(r.Context(), name)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.update", name, req)
	httpx.JSON(w, http.StatusOK, u)
	return nil
}

func (s *Server) handleSysUserDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	removeHome := r.URL.Query().Get("removeHome") == "true"
	if err := s.modules.linuxUsers.Delete(r.Context(), name, removeHome); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.delete", name, map[string]any{"removeHome": removeHome})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleSSHKeyList(w http.ResponseWriter, r *http.Request) error {
	keys, path, err := s.modules.linuxUsers.ListKeys(chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "keys": keys})
	return nil
}

type sshKeyRequest struct {
	Key string `json:"key"`
}

func (s *Server) handleSSHKeyAdd(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req sshKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := s.modules.linuxUsers.AddKey(name, req.Key)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.add", name,
		map[string]any{"fingerprint": key.Fingerprint, "type": key.Type, "comment": key.Comment})
	httpx.JSON(w, http.StatusCreated, key)
	return nil
}

func (s *Server) handleSSHKeyRemove(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	fingerprint := r.URL.Query().Get("fingerprint")
	if fingerprint == "" {
		return httpx.BadRequest("fingerprint query parameter is required")
	}
	// No typed phrase: an authorised key is a public key, and putting one back
	// is a paste. Deleting the account it belongs to is the route above, and
	// that also uses ordinary confirmation.
	if err := s.modules.linuxUsers.RemoveKey(name, fingerprint); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.remove", name, map[string]any{"fingerprint": fingerprint})
	httpx.NoContent(w)
	return nil
}
