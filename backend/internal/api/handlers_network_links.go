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
	r.Method(http.MethodGet, "/namespaces/{name}", s.handle(s.handleNetworkNamespaceDetail))
	r.Method(http.MethodGet, "/namespaces/{name}/lookup", s.handle(s.handleNetworkNamespaceLookup))
	r.Method(http.MethodGet, "/links/{name}/detail", s.handle(s.handleNetworkLinkDetail))
	r.Method(http.MethodGet, "/links/{name}/bridge", s.handle(s.handleNetworkBridge))
	r.Method(http.MethodGet, "/links/{name}/readiness", s.handle(s.handleNetworkLinkReadiness))
	r.Method(http.MethodGet, "/links/{name}/master/preview", s.handle(s.handleNetworkLinkMasterPreview))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/namespaces", s.handle(s.handleNetworkNamespaceCreate))
		r.Method(http.MethodPost, "/links", s.handle(s.handleNetworkLinkCreate))
		r.Method(http.MethodPost, "/links/{name}/up", s.handle(s.handleNetworkLinkUp))
		r.Method(http.MethodPost, "/links/{name}/mtu", s.handle(s.handleNetworkLinkMTU))
		r.Method(http.MethodPost, "/links/{name}/master", s.handle(s.handleNetworkLinkMaster))
		r.Method(http.MethodPost, "/links/{name}/addresses", s.handle(s.handleNetworkAddressAdd))
		// Replacing a port's VLANs or a VXLAN's flood ends is destructive
		// only when it takes one away; the handlers decide by content.
		r.Method(http.MethodPut, "/links/{name}/vlans", s.handle(s.handleNetworkPortVLANs))
		r.Method(http.MethodPut, "/links/{name}/remotes", s.handle(s.handleNetworkVXLANRemotes))
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
	ns, err := s.modules.network.CreateNamespace(ctx, req, s.networkClient(r), actor(r))
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
	if err := s.modules.network.DeleteNamespace(ctx, name, s.networkClient(r), actor(r)); err != nil {
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
	link, err := s.modules.network.CreateLink(ctx, req, s.networkClient(r), actor(r))
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
	if err := s.modules.network.DeleteLink(ctx, name, s.networkClient(r), actor(r)); err != nil {
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
	change, err := s.modules.network.SetLinkState(ctx, name, up, s.networkClient(r), actor(r))
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
	change, err := s.modules.network.SetLinkMTU(ctx, name, req.MTU, s.networkClient(r), actor(r))
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
	change, err := s.modules.network.SetLinkMaster(ctx, name, req.Master, s.networkClient(r), actor(r))
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
	change, err := s.modules.network.AddAddress(ctx, name, req.CIDR, s.networkClient(r), actor(r))
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
	if _, err := s.modules.network.RemoveAddress(ctx, name, cidr, s.networkClient(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

// handleNetworkLinkDetail is one device's driver, offloads and every error
// counter, read when its sheet opens.
func (s *Server) handleNetworkLinkDetail(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	detail, err := s.modules.network.LinkDetail(ctx, chi.URLParam(r, "name"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleNetworkBridge is a bridge's settings, port VLANs and learned entries.
func (s *Server) handleNetworkBridge(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	view, err := s.modules.network.Bridge(ctx, chi.URLParam(r, "name"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// handleNetworkLinkReadiness is what this host can establish about whether a
// tunnel or virtual device can carry traffic. It sends nothing.
func (s *Server) handleNetworkLinkReadiness(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	readiness, err := s.modules.network.Readiness(ctx, chi.URLParam(r, "name"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, readiness)
	return nil
}

// handleNetworkLinkMasterPreview previews a bridge membership change; a
// read, so it changes nothing and needs no administrator.
func (s *Server) handleNetworkLinkMasterPreview(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	preview, err := s.modules.network.PreviewMaster(ctx, chi.URLParam(r, "name"), r.URL.Query().Get("master"), s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

type portVLANsRequest struct {
	VLANs []netx.PortVLAN `json:"vlans"`
}

func (s *Server) handleNetworkPortVLANs(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req portVLANsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.link.vlans", name, req)
	if sp, err := s.modules.network.Spec(); err == nil && netx.RemovesPortVLAN(sp, name, req.VLANs) {
		if err := s.requireDestructive(r, "netvlan", "taking a VLAN off a port"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.SetPortVLANs(ctx, name, req.VLANs, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

type vxlanRemotesRequest struct {
	Remotes []string `json:"remotes"`
}

func (s *Server) handleNetworkVXLANRemotes(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req vxlanRemotesRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.link.remotes", name, req)
	if sp, err := s.modules.network.Spec(); err == nil && netx.RemovesVXLANRemote(sp, name, req.Remotes) {
		if err := s.requireDestructive(r, "netvxlan", "removing a VXLAN's other end"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	change, err := s.modules.network.SetVXLANRemotes(ctx, name, req.Remotes, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

// handleNetworkNamespaceDetail reads one namespace as its own network.
func (s *Server) handleNetworkNamespaceDetail(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail, err := s.modules.network.NamespaceDetail(ctx, r.URL.Query().Get("kind"), chi.URLParam(r, "name"), s.networkInventory(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleNetworkNamespaceLookup asks a namespace's kernel how it routes to a
// literal address; it sends nothing.
func (s *Server) handleNetworkNamespaceLookup(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if q.Get("target") == "" {
		return httpx.BadRequest("target is a literal IPv4 or IPv6 address")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	path, err := s.modules.network.NamespaceLookup(ctx, q.Get("kind"), chi.URLParam(r, "name"), q.Get("target"), q.Get("source"), s.networkInventory(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, path)
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
