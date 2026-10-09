package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleRouteResolve answers which server block and location nginx picks for
// a URL, from the configuration nginx -T prints. Nothing is sent to the URL:
// the answer is worked out from the files alone.
func (s *Server) handleRouteResolve(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.proxy.EffectiveConfig(r.Context())
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", "nginx could not print its configuration: "+err.Error())
	}
	tree, err := proxysvc.NginxTree(files)
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", err.Error())
	}
	out, err := proxysvc.ResolveRoute(tree, r.URL.Query().Get("url"))
	if errors.Is(err, proxysvc.ErrRouteURL) {
		return httpx.BadRequest("%s", err.Error())
	}
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleRouteAccess answers who may reach a URL from one address: the route
// nginx picks, then each layer of access in the order nginx runs them, with
// the host firewall in front of the port judged for the same address. It is
// worked out from the files and the firewall's rules; nothing is sent.
func (s *Server) handleRouteAccess(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.proxy.EffectiveConfig(r.Context())
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", "nginx could not print its configuration: "+err.Error())
	}
	tree, err := proxysvc.NginxTree(files)
	if err != nil {
		return httpx.Err(http.StatusConflict, "config_unreadable", err.Error())
	}
	q := r.URL.Query()
	out, err := proxysvc.ExplainAccess(tree, q.Get("url"), q.Get("source"))
	if errors.Is(err, proxysvc.ErrRouteURL) || errors.Is(err, proxysvc.ErrAccessSource) {
		return httpx.BadRequest("%s", err.Error())
	}
	if err != nil {
		return httpx.Internal(err)
	}
	out.AddLayer(1, s.firewallAccessLayer(r, out))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// firewallAccessLayer judges the host firewall's inbound rules for the
// route's port and the source as the connecting address.
func (s *Server) firewallAccessLayer(r *http.Request, out *proxysvc.AccessExplanation) proxysvc.AccessLayer {
	layer := proxysvc.AccessLayer{ID: "firewall", Title: "Host firewall", Owner: "Firewall", OwnerPath: "/network/firewall", Verdict: proxysvc.LayerUnknown}
	if s.modules.netsec == nil {
		layer.Detail = "The firewall owner is unavailable, so its rules were not judged."
		return layer
	}
	status, err := s.modules.netsec.Status(r.Context())
	if err != nil {
		layer.Detail = "The firewall could not be read: " + err.Error()
		return layer
	}
	address, port := out.ListenPort()
	source := netip.MustParseAddr(out.Source)
	if address == "" {
		address = "0.0.0.0"
		if source.Is6() {
			address = "::"
		}
	}
	verdict, skipped := netsec.JudgeFirewallFrom(netsec.ExposedPort{Port: uint32(port), Protocol: "tcp", Address: address},
		netsec.ReadHostNetwork(r.Context()), status, source)
	decided := "its inbound default (" + verdict.Default + ")"
	if verdict.Rule > 0 {
		decided = fmt.Sprintf("rule %d (%s from %s)", verdict.Rule, verdict.Action, verdict.From)
	}
	switch verdict.Verdict {
	case netsec.VerdictAllowed:
		layer.Verdict, layer.Detail = proxysvc.LayerAdmits, fmt.Sprintf("%s lets %s reach port %d by %s.", verdict.Backend, source, port, decided)
	case netsec.VerdictBlocked:
		layer.Verdict, layer.Detail = proxysvc.LayerRefuses, fmt.Sprintf("%s refuses %s on port %d by %s.", verdict.Backend, source, port, decided)
	case netsec.VerdictOff:
		layer.Verdict, layer.Detail = proxysvc.LayerAdmits, fmt.Sprintf("%s is installed but not active, so it refuses nothing.", verdict.Backend)
	default:
		layer.Detail = "The firewall's verdict for this port could not be read from its rules."
	}
	if skipped > 0 && layer.Verdict != proxysvc.LayerUnknown {
		layer.Detail += fmt.Sprintf(" %d rule(s) ahead of it name an interface or a set rather than an address and were not judged.", skipped)
	}
	layer.Detail += " Judged for " + source.String() + " as the address the connection comes from."
	return layer
}
