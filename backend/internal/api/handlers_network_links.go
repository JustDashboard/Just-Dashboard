package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkLinkRoutes mounts the devices, their addresses and the namespaces under /network.
//
// The device list itself (GET /links) is mounted with the section's other
// reads in handlers_network.go; what is here changes it. Up and down are two
// paths rather than one with a body because s.destructive decides by route,
// and only taking a device down can cost the operator access.
func (s *Server) mountNetworkLinkRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/namespaces", s.handle(s.handleNetworkNamespaces))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/namespaces", s.handle(s.handleNetworkNamespaceCreate))
		r.Method(http.MethodPost, "/links", s.handle(s.handleNetworkLinkCreate))
		r.Method(http.MethodPost, "/links/{name}/up", s.handle(s.handleNetworkLinkUp))
		r.Method(http.MethodPost, "/links/{name}/mtu", s.handle(s.handleNetworkLinkMTU))
		r.Method(http.MethodPost, "/links/{name}/master", s.handle(s.handleNetworkLinkMaster))
		r.Method(http.MethodPost, "/links/{name}/addresses", s.handle(s.handleNetworkAddressAdd))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/namespaces/{name}", s.handle(s.handleNetworkNamespaceDelete))
			r.Method(http.MethodDelete, "/links/{name}", s.handle(s.handleNetworkLinkDelete))
			r.Method(http.MethodPost, "/links/{name}/down", s.handle(s.handleNetworkLinkDown))
			r.Method(http.MethodDelete, "/links/{name}/addresses", s.handle(s.handleNetworkAddressRemove))
		})
	})
}

func (s *Server) handleNetworkNamespaces(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	namespaces, err := s.modules.network.Namespaces(ctx, s.networkInventory(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, namespaces)
	return nil
}

func (s *Server) handleNetworkNamespaceCreate(w http.ResponseWriter, r *http.Request) error {
	var req netx.NamespaceRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.namespace.create", req.Name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	ns, err := s.modules.network.CreateNamespace(ctx, req, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusCreated, ns)
	return nil
}

func (s *Server) handleNetworkNamespaceDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	httpx.SetAudit(r, "network.namespace.delete", name, nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteNamespace(ctx, name, httpx.ClientIP(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleNetworkLinkCreate(w http.ResponseWriter, r *http.Request) error {
	var req netx.LinkRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.link.create", req.Name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	link, err := s.modules.network.CreateLink(ctx, req, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusCreated, link)
	return nil
}

func (s *Server) handleNetworkLinkDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	httpx.SetAudit(r, "network.link.delete", name, nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteLink(ctx, name, httpx.ClientIP(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleNetworkLinkUp(w http.ResponseWriter, r *http.Request) error {
	return s.setLinkState(w, r, true)
}

func (s *Server) handleNetworkLinkDown(w http.ResponseWriter, r *http.Request) error {
	return s.setLinkState(w, r, false)
}

func (s *Server) setLinkState(w http.ResponseWriter, r *http.Request, up bool) error {
	name := chi.URLParam(r, "name")
	action := "network.link.down"
	if up {
		action = "network.link.up"
	}
	httpx.SetAudit(r, action, name, nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.SetLinkState(ctx, name, up, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

type linkMTURequest struct {
	MTU int `json:"mtu"`
}

func (s *Server) handleNetworkLinkMTU(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req linkMTURequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.link.mtu", name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.SetLinkMTU(ctx, name, req.MTU, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

type linkMasterRequest struct {
	// Master is the bridge to join; empty takes the device out of its bridge.
	Master string `json:"master"`
}

func (s *Server) handleNetworkLinkMaster(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req linkMasterRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.link.master", name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.SetLinkMaster(ctx, name, req.Master, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

type linkAddressRequest struct {
	CIDR string `json:"cidr"`
}

func (s *Server) handleNetworkAddressAdd(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req linkAddressRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.address.add", name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.AddAddress(ctx, name, req.CIDR, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

// handleNetworkAddressRemove takes the address as a query parameter: a CIDR
// has a slash in it, and a path segment holding one is a route that does not
// match.
func (s *Server) handleNetworkAddressRemove(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	cidr := r.URL.Query().Get("cidr")
	if cidr == "" {
		return httpx.BadRequest("cidr is the address to remove, such as 10.8.0.1/24")
	}
	httpx.SetAudit(r, "network.address.remove", name, map[string]string{"cidr": cidr})
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if _, err := s.modules.network.RemoveAddress(ctx, name, cidr, httpx.ClientIP(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

// pathID reads a numeric id from the route.
func pathID(r *http.Request, param string) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, param))
	if err != nil || id < 1 {
		return 0, httpx.BadRequest("%s is the number the entry was made with", param)
	}
	return id, nil
}
