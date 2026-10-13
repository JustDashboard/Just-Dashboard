package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/remotedesk"
	"github.com/go-chi/chi/v5"
)

// mountRemoteDesktopStreamRoutes is Stream mode's half of the Remote Desktop
// API, inside its system.admin group: the bridge and each computer's pairing.
func (s *Server) mountRemoteDesktopStreamRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/stream", s.handle(s.handleRemoteDesktopBridge))
	r.Method(http.MethodPost, "/stream", s.handle(s.handleRemoteDesktopBridgeInstall))
	r.Method(http.MethodGet, "/computers/{id}/stream", s.handle(s.handleRemoteDesktopStream))
	r.Method(http.MethodPost, "/computers/{id}/stream/pair", s.handle(s.handleRemoteDesktopStreamPair))
	r.Method(http.MethodDelete, "/computers/{id}/stream", s.handle(s.handleRemoteDesktopStreamUnpair))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/stream", s.handle(s.handleRemoteDesktopBridgeRemove))
	})
}

// streamCSP is the bridge's page's whole allowance: its own files, its
// decoder's WebAssembly, the one inline script it ships (by hash, for the
// pinned image), video from a MediaSource, and a frame inside the
// dashboard only.
var streamCSP = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self' 'wasm-unsafe-eval' 'sha256-rIN45+SUBO4U012a5IK5piEITuk5/0Pm3JdMLlbiLSs='",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: blob:",
	"media-src 'self' blob:",
	"worker-src 'self' blob:",
	"connect-src 'self'",
	"object-src 'none'",
	"base-uri 'none'",
	"form-action 'self'",
	"frame-ancestors 'self'",
}, "; ")

// mountRemoteDesktopBridge proxies the stream bridge at its own prefix. The
// browser reaches it only through here, signed in to the dashboard with a
// session, and the bridge only ever sees the dashboard's one account: the
// cookie and any account header the browser sent are dropped on the way.
func (s *Server) mountRemoteDesktopBridge(r chi.Router) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := pr.In.Context().Value(bridgeTargetKey{}).(string)
			pr.SetURL(&url.URL{Scheme: "http", Host: target})
			pr.Out.Host = target
			for _, name := range []string{"Cookie", "Authorization", remotedesk.BridgeUserHeader, httpx.CSRFHeader} {
				pr.Out.Header.Del(name)
			}
			pr.Out.Header.Set(remotedesk.BridgeUserHeader, remotedesk.BridgeAccount)
		},
		// The pairing and host lists are streamed a line at a time.
		FlushInterval: -1,
		ModifyResponse: func(res *http.Response) error {
			for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "Set-Cookie"} {
				res.Header.Del(name)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.modules.remoteDesktop.ForgetBridgeAddress()
			httpx.WriteError(w, r, httpx.Err(http.StatusBadGateway, "stream_bridge_unreachable",
				"The stream bridge is not answering on this server."))
		},
	}
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin), httpx.RequireSession)
		r.Handle("/remote-desktop/bridge/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !bridgeSafeMethod(r.Method) || isUpgrade(r) {
				if !s.WS.SameSite(r) {
					httpx.WriteError(w, r, httpx.Err(http.StatusForbidden, "cross_site", "the stream bridge only answers this dashboard's own pages"))
					return
				}
			}
			target, err := s.modules.remoteDesktop.BridgeAddress(r.Context())
			if err != nil {
				httpx.WriteError(w, r, httpx.Err(http.StatusServiceUnavailable, "stream_bridge_stopped",
					"The stream bridge is not running on this server."))
				return
			}
			h := w.Header()
			h.Set("Content-Security-Policy", streamCSP)
			h.Set("X-Frame-Options", "SAMEORIGIN")
			// The bridge says how long each of its files may be kept.
			h.Del("Cache-Control")
			r = r.WithContext(context.WithValue(r.Context(), bridgeTargetKey{}, target))
			if isUpgrade(r) && strings.HasSuffix(r.URL.Path, "/api/host/stream") {
				s.proxyStream(w, r, proxy)
				return
			}
			proxy.ServeHTTP(w, r)
		}))
	})
}

type bridgeTargetKey struct{}

// proxyStream carries one stream's socket, recorded as a session is: who
// opened a stream and when it ended. The computer is named inside the
// socket, so the record says a stream rather than which computer.
func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request, proxy *httputil.ReverseProxy) {
	s.recordAudit(r, "remote_desktop.stream.open", "stream", nil)
	s.modules.remoteDesktop.StreamOpened()
	started := time.Now()
	defer func() {
		s.modules.remoteDesktop.StreamClosed()
		s.recordAudit(r, "remote_desktop.stream.close", "stream",
			map[string]any{"seconds": int(time.Since(started).Seconds())})
	}()
	proxy.ServeHTTP(w, r)
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func bridgeSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func mapRemoteDesktopStreamError(err error) error {
	var refused *remotedesk.StreamError
	switch {
	case errors.Is(err, remotedesk.ErrBridgeNotRunning):
		return httpx.Err(http.StatusConflict, "stream_bridge_stopped", "The stream bridge is not running on this server.")
	case errors.As(err, &refused):
		return httpx.Err(http.StatusBadGateway, "stream_refused", refused.Message)
	}
	return mapRemoteDesktopError(err)
}

func (s *Server) handleRemoteDesktopBridge(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.remoteDesktop.Bridge(r.Context()))
	return nil
}

func (s *Server) handleRemoteDesktopBridgeInstall(w http.ResponseWriter, r *http.Request) error {
	st := s.modules.remoteDesktop.Bridge(r.Context())
	switch st.State {
	case remotedesk.EngineUnavailable:
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "Docker is not reachable from this dashboard.").
			Because("Stream mode runs its bridge as a container, so it needs the Docker daemon.", st.Error).Retry()
	case remotedesk.EngineForeign:
		return httpx.Err(http.StatusConflict, "engine_foreign", st.Error)
	}
	httpx.SetAudit(r, "remote_desktop.stream.install", remotedesk.BridgeImage, nil)
	spec := jobs.Spec{
		Kind: "remote-desktop.stream.install", Title: "Install Stream mode", Target: "Moonlight Web " + remotedesk.BridgeVersion,
		StartedBy: httpx.MustPrincipal(r).Username(), Timeout: 15 * time.Minute,
	}
	job, _ := s.modules.jobs.StartExclusive("remote-desktop.", spec, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("Installing Moonlight Web %s", remotedesk.BridgeVersion)
		return s.modules.remoteDesktop.InstallBridge(ctx, func(line string) { out.Line("stdout", line) })
	})
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

func (s *Server) handleRemoteDesktopBridgeRemove(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "remote_desktop.stream.remove", remotedesk.BridgeImage, nil)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.remoteDesktop.RemoveBridge(ctx); err != nil {
		var busy *remotedesk.BusyError
		if errors.As(err, &busy) {
			return mapRemoteDesktopError(err)
		}
		return httpx.Err(http.StatusConflict, "engine_remove_failed", err.Error())
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleRemoteDesktopStream(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	v, err := s.modules.remoteDesktop.Stream(r.Context(), id)
	if err != nil {
		return mapRemoteDesktopStreamError(err)
	}
	v.Transport = streamTransport(v.Bridge, httpx.ClientIP(r))
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// streamTransport sends the picture over WebRTC only to a browser that can
// reach the bridge's UDP block: one on the tailnet the block is published
// on. Anything else would wait out a failed connection before falling back.
func streamTransport(bridge remotedesk.EngineStatus, client string) string {
	address, err := netip.ParseAddr(client)
	if bridge.Media == "" || err != nil {
		return remotedesk.TransportWebSocket
	}
	for _, prefix := range tailnetRanges {
		if prefix.Contains(address.Unmap()) {
			return remotedesk.TransportWebRTC
		}
	}
	return remotedesk.TransportWebSocket
}

func (s *Server) handleRemoteDesktopStreamPair(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "remote_desktop.stream.pair", id, nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	v, err := s.modules.remoteDesktop.Pair(ctx, id)
	if err != nil {
		return mapRemoteDesktopStreamError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleRemoteDesktopStreamUnpair(w http.ResponseWriter, r *http.Request) error {
	id, err := remoteDesktopID(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "remote_desktop.stream.unpair", id, nil)
	if err := s.modules.remoteDesktop.Unpair(r.Context(), id); err != nil {
		return mapRemoteDesktopStreamError(err)
	}
	httpx.NoContent(w)
	return nil
}
