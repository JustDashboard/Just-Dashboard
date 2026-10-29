package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/cftunnel"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// Cloudflare Tunnel as an ingress (cftunnel): the Proxy page's connection,
// its connector's health, the hostnames routed through it, and the verbs.
// Connecting takes a credential — a tunnel token, or the saved Cloudflare
// API token spent on creating a tunnel — so it is an administrator's, in a
// session; disconnecting takes every name routed through it off the
// internet, so it is destructive.

func (s *Server) newTunnelManager() *cftunnel.Manager {
	return cftunnel.NewManager(cftunnel.Options{
		DB: s.Store.DB, Sealer: s.Sealer, Connector: cftunnel.NewDockerConnector(),
		API: func() (cftunnel.API, error) {
			credentials, err := proxysvc.SavedCloudflareCredentials()
			if err != nil {
				return cftunnel.API{}, err
			}
			return cftunnel.API{Token: credentials.Token, Email: credentials.Email, Key: credentials.Key}, nil
		},
		Origin: func(ctx context.Context, network string) (cftunnel.Origin, error) {
			if s.modules.proxy == nil {
				return cftunnel.Origin{}, errors.New("the proxy is unavailable on this host")
			}
			origin, err := s.modules.proxy.TunnelOrigin(ctx, network)
			if err != nil {
				return cftunnel.Origin{}, err
			}
			return cftunnel.Origin{Service: origin.Service, Network: network, HostGateway: origin.HostGateway, Proxy: origin.Proxy}, nil
		},
		Hostnames: func(ctx context.Context) ([]cftunnel.Hostname, error) {
			domains, err := s.modules.deployRuns.TunnelDomains(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]cftunnel.Hostname, 0, len(domains))
			for _, domain := range domains {
				out = append(out, cftunnel.Hostname{Hostname: domain.Hostname, ProjectID: domain.ProjectID, EnvironmentID: domain.EnvironmentID, Project: domain.Project})
			}
			return out, nil
		},
		Audit: func(ctx context.Context, action string, detail map[string]any, success bool) {
			encoded, _ := json.Marshal(detail)
			s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: action, Target: "cloudflare-tunnel", Success: success, Detail: string(encoded)})
		},
		Log: s.Log,
	})
}

// tunnelRouter is the executor's half: a release's tunnel names routed as it
// goes live.
func (s *Server) tunnelRouter() deploy.TunnelRouter {
	return func(ctx context.Context, extra []string) error {
		if s.modules.tunnel == nil {
			return cftunnel.ErrNotConnected
		}
		return s.modules.tunnel.Route(ctx, extra)
	}
}

// tunnelObserver is preflight's.
func (s *Server) tunnelObserver() deploy.TunnelObserver {
	return func(ctx context.Context) (string, bool) {
		if s.modules.tunnel == nil {
			return "", false
		}
		connection, _ := s.modules.tunnel.Connected(ctx)
		if connection == nil {
			return "", false
		}
		name := connection.TunnelName
		if name == "" {
			name = connection.TunnelID
		}
		return name, true
	}
}

func (s *Server) mountTunnelRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleTunnel))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/sync", s.handle(s.handleTunnelSync))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodPut, "/", s.handle(s.handleTunnelConnect))
			r.Method(http.MethodPost, "/create", s.handle(s.handleTunnelCreate))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/", s.handle(s.handleTunnelDisconnect))
		})
	})
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.modules.tunnel.Status(ctx))
	return nil
}

type tunnelConnectRequest struct {
	// Token is the tunnel's token as Cloudflare shows it; sealed at once and
	// never returned.
	Token string `json:"token"`
}

type tunnelCreateRequest struct {
	Name string `json:"name"`
	// AccountID names the account when the saved credentials reach more
	// than one.
	AccountID string `json:"accountId,omitempty"`
}

func tunnelRefusal(err error) error {
	var cloudflare *cftunnel.Error
	if errors.As(err, &cloudflare) {
		return httpx.Err(http.StatusBadGateway, "cloudflare_refused", err.Error())
	}
	return httpx.Err(http.StatusUnprocessableEntity, "tunnel_refused", err.Error())
}

func (s *Server) handleTunnelConnect(w http.ResponseWriter, r *http.Request) error {
	var req tunnelConnectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	status, err := s.modules.tunnel.Connect(ctx, req.Token, httpx.MustPrincipal(r).Username())
	detail := map[string]any{"mode": "token"}
	if status.Connection != nil {
		detail["tunnelId"] = status.Connection.TunnelID
	}
	if err != nil {
		detail["result"] = "refused"
		httpx.SetAudit(r, "proxy.tunnel.connect", "cloudflare-tunnel", detail)
		return tunnelRefusal(err)
	}
	httpx.SetAudit(r, "proxy.tunnel.connect", "cloudflare-tunnel", detail)
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) handleTunnelCreate(w http.ResponseWriter, r *http.Request) error {
	var req tunnelCreateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	status, err := s.modules.tunnel.Create(ctx, req.Name, req.AccountID, httpx.MustPrincipal(r).Username())
	detail := map[string]any{"mode": "api", "name": req.Name}
	if status.Connection != nil && status.Connection.Mode == "api" {
		detail["tunnelId"] = status.Connection.TunnelID
	}
	if err != nil {
		detail["result"] = "refused"
		httpx.SetAudit(r, "proxy.tunnel.create", "cloudflare-tunnel", detail)
		return tunnelRefusal(err)
	}
	httpx.SetAudit(r, "proxy.tunnel.create", "cloudflare-tunnel", detail)
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) handleTunnelSync(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	status := s.modules.tunnel.Sync(ctx)
	httpx.SetAudit(r, "proxy.tunnel.sync", "cloudflare-tunnel", map[string]any{"ready": status.Ready, "hostnames": len(status.Hostnames)})
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

func (s *Server) handleTunnelDisconnect(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, time.Minute)
	defer cancel()
	before := s.modules.tunnel.Status(ctx)
	detail := map[string]any{}
	if before.Connection != nil {
		detail["tunnelId"] = before.Connection.TunnelID
	}
	httpx.SetAudit(r, "proxy.tunnel.disconnect", "cloudflare-tunnel", detail)
	if err := s.modules.tunnel.Disconnect(ctx); err != nil {
		return tunnelRefusal(err)
	}
	httpx.JSON(w, http.StatusOK, s.modules.tunnel.Status(ctx))
	return nil
}

// tunnelAlertReading is the host's tunnel_down rule's reading: the connector
// not running, or running with no connection to Cloudflare's edge, in
// cloudflared's own words where it said why.
func (s *Server) tunnelAlertReading(ctx context.Context) hostAlertReading {
	if s.modules.tunnel == nil {
		return unjudgedHostAlert("This dashboard runs no Cloudflare Tunnel manager.")
	}
	status := s.modules.tunnel.Status(ctx)
	if !status.Connected || status.Connection == nil {
		return unjudgedHostAlert("No Cloudflare Tunnel is connected, so there is nothing to watch.")
	}
	reading := newHostAlertReading()
	subject := "tunnel:" + status.Connection.TunnelID
	label := status.Connection.TunnelName
	if label == "" {
		label = status.Connection.TunnelID
	}
	switch {
	case status.Connector.State != "running":
		detail := "Its connector container is " + status.Connector.State + ", so nothing routed through the tunnel is reachable."
		if status.Error != "" {
			detail += " cloudflared said: " + status.Error
		}
		reading.Firing = append(reading.Firing, hostAlertFinding{subject, label, detail})
	case status.CheckedAt == nil:
		reading.Unknown[subject] = true
	case !status.Ready:
		detail := "The connector holds no connection to Cloudflare's edge, so nothing routed through the tunnel is reachable."
		if status.Error != "" {
			detail += " cloudflared said: " + status.Error
		}
		reading.Firing = append(reading.Firing, hostAlertFinding{subject, label, detail})
	default:
		reading.Now[subject], reading.Labels[subject] = fmt.Sprintf("Its connector holds %d %s to Cloudflare's edge.", status.ReadyConnections, pluralWord(status.ReadyConnections, "connection", "connections")), label
	}
	return reading
}
