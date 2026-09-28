package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountAccessListRoutes is the named access lists sites include. Behind
// system.admin throughout, like the password files they name: the listing
// says who may reach which site, and a save changes it for every site at
// once. Deleting one is destructive, with an ordinary confirmation — it is
// refused while a site includes the list, and a copy is kept.
func (s *Server) mountAccessListRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodGet, "/", s.handle(s.handleAccessListList))
	r.Method(http.MethodPut, "/{name}", s.handle(s.handleAccessListSave))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{name}", s.handle(s.handleAccessListDelete))
	})
}

// accessListsAnswer is every list, and the address this request came from,
// which the page checks each list against: a list that refuses the operator
// locks them out of every site that includes it.
type accessListsAnswer struct {
	Dir           string                `json:"dir"`
	ClientAddress string                `json:"clientAddress"`
	Lists         []proxysvc.AccessList `json:"lists"`
}

func (s *Server) handleAccessListList(w http.ResponseWriter, r *http.Request) error {
	lists, err := s.modules.proxy.ListAccessLists()
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, accessListsAnswer{
		Dir: s.modules.proxy.AccessListDir(), ClientAddress: httpx.ClientIP(r), Lists: lists,
	})
	return nil
}

type accessListRequest struct {
	proxysvc.AccessListSpec
	// Overwrite is false for a new list, which must not replace one of the
	// same name that sites already include.
	Overwrite bool `json:"overwrite"`
}

// accessListSaved is what a save did. The list passed `nginx -t` with every
// site that includes it, or it would have been put back and refused;
// Reloaded says whether nginx is now running it, and ReloadError why not.
type accessListSaved struct {
	List        proxysvc.AccessList        `json:"list"`
	Validation  *proxysvc.ValidationResult `json:"validation"`
	Reloaded    bool                       `json:"reloaded"`
	ReloadError string                     `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult     `json:"reload,omitempty"`
}

func (s *Server) handleAccessListSave(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	var req accessListRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	detail := map[string]any{
		"allow": len(req.Allow), "deny": len(req.Deny), "authFile": req.AuthFile,
		"satisfy": req.Satisfy, "created": !req.Overwrite,
	}
	// Named before the outcome is known, so a refused save is recorded as
	// what it tried to be.
	httpx.SetAudit(r, "proxy.accesslist.save", name, detail)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.SaveAccessList(ctx, name, req.AccessListSpec, req.Overwrite)
	if err != nil {
		if errors.Is(err, proxysvc.ErrAccessListExists) {
			return httpx.Err(http.StatusConflict, "exists", err.Error())
		}
		return refusedLinkChange(r, "proxy.accesslist.save", name, detail, err)
	}
	out := accessListSaved{List: res.List, Validation: res.Validation}
	if reload := res.Reload; reload != nil {
		out.Reload = reload.Result
		switch {
		case reload.Err == nil:
			out.Reloaded = true
		case errors.Is(reload.Err, proxysvc.ErrInvalidConf):
			out.ReloadError = "nginx -t failed: " + proxysvc.FailureHeadline(reload.Result.Validation)
		default:
			out.ReloadError = reload.Err.Error()
		}
	}
	sites := make([]string, 0, len(res.List.UsedBy))
	for _, use := range res.List.UsedBy {
		sites = append(sites, use.Site)
	}
	detail["usedBy"], detail["reloaded"] = sites, out.Reloaded
	if out.ReloadError != "" {
		detail["reloadError"] = out.ReloadError
	}
	httpx.SetAudit(r, "proxy.accesslist.save", name, detail)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleAccessListDelete(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	httpx.SetAudit(r, "proxy.accesslist.delete", name, nil)
	err := s.modules.proxy.DeleteAccessList(r.Context(), name)
	var inUse *proxysvc.AccessListInUseError
	switch {
	case errors.As(err, &inUse):
		return httpx.Err(http.StatusConflict, "in_use", err.Error())
	case errors.Is(err, proxysvc.ErrNoAccessList):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case err != nil:
		return refusedLinkChange(r, "proxy.accesslist.delete", name, nil, err)
	}
	httpx.NoContent(w)
	return nil
}
