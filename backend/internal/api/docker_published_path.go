package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleContainerPublishedPath is GET /docker/containers/{id}/published/{port}:
// the inbound path to one published port — Docker's publication and NAT,
// the forwarded leg's filters, the dashboard's gateway, the proxy, provider
// policy and retained external measurements — joined in one report. It reads
// only; nothing is sent to the port.
func (s *Server) handleContainerPublishedPath(w http.ResponseWriter, r *http.Request) error {
	port, err := strconv.Atoi(httpx.URLParam(r, "port"))
	if err != nil {
		return httpx.BadRequest("port must be a number")
	}
	q := r.URL.Query()
	protocol, family := q.Get("protocol"), q.Get("family")
	if protocol == "" {
		protocol = "tcp"
	}
	if family == "" {
		family = "inet"
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail, err := s.modules.docker.Inspect(ctx, httpx.URLParam(r, "id"))
	if err != nil {
		return s.dockerErr(err)
	}
	binding, ok := publishedBinding(detail, port, protocol, family)
	if !ok {
		return httpx.Err(http.StatusNotFound, "not_published", "That container does not publish this host port in that protocol and family.")
	}
	request := netpath.PublishedRequest{ContainerID: detail.ID, HostPort: port, Protocol: protocol, Family: family}
	if _, err := netpath.ValidatePublished(request); err != nil {
		return httpx.BadRequest("%s", err)
	}
	result, err := netpath.InvestigatePublished(ctx, request, s.publishedPathProviders(detail, binding))
	if err != nil {
		return httpx.BadRequest("%s", err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// publishedBinding finds the binding a request names: Docker publishes a
// port on both families as two bindings, told apart by their address.
func publishedBinding(detail *dockerx.ContainerDetail, port int, protocol, family string) (dockerx.PortExposure, bool) {
	for _, p := range detail.Exposure {
		if p.HostPort != port || p.Protocol != protocol {
			continue
		}
		six := p.IPv6 || strings.Contains(p.HostIP, ":")
		if (family == "inet6") == six {
			return p, true
		}
	}
	return dockerx.PortExposure{}, false
}

func (s *Server) publishedPathProviders(detail *dockerx.ContainerDetail, binding dockerx.PortExposure) netpath.PublishedProviders {
	p := netpath.PublishedProviders{}
	p.Publication = func(context.Context) (netpath.Publication, error) {
		out := netpath.Publication{Container: detail.Name, HostIP: binding.HostIP, HostPort: binding.HostPort, ContainerPort: binding.ContainerPort, Protocol: binding.Protocol}
		for _, n := range detail.NetworkList {
			out.Addresses = append(out.Addresses, netpath.PublishedAddress{Network: n.Name, IPv4: n.IPAddress, IPv6: n.IPv6})
		}
		return out, nil
	}
	p.Chains = readDockerChains
	if s.modules.netsec != nil {
		p.Firewall = s.modules.netsec.Status
	}
	if s.modules.network != nil {
		p.Gateway = s.modules.network.Gateway
	}
	p.Owners = func(ctx context.Context) (netpath.OwnerSnapshot, error) {
		values, err := proxysvc.ListListeners(ctx)
		if err != nil {
			return netpath.OwnerSnapshot{}, err
		}
		values = proxysvc.AttributeOwners(values, s.ownerInput(ctx, false))
		placeListeners(values, netsec.ReadHostNetwork(ctx), nil)
		snapshot := netpath.OwnerSnapshot{Listeners: values, Limits: []string{"Process ownership is a current socket snapshot."}}
		if s.modules.proxy != nil {
			vhosts, err := s.modules.proxy.ListVHosts(ctx)
			if err != nil {
				snapshot.Limits = append(snapshot.Limits, "Native proxy sites could not be read: "+err.Error())
			}
			streams, _ := s.modules.proxy.Streams(ctx)
			proxysvc.AttachProxy(snapshot.Listeners, vhosts, streams)
		}
		return snapshot, nil
	}
	p.External = func(ctx context.Context, port int) ([]netpath.ExternalMeasurement, error) {
		family := "inet"
		if binding.IPv6 || strings.Contains(binding.HostIP, ":") {
			family = "inet6"
		}
		return s.externalMeasurements(ctx, binding.Protocol, family, binding.HostIP, port)
	}
	p.PublicAddress = func(context.Context) (string, error) { return publicHostAddress() }
	return p
}

// readDockerChains lists Docker's iptables chains on the host; a variable so
// a test does not run the host's iptables.
var readDockerChains = netsec.ReadDockerChains

// publicHostAddress is a global unicast address on one of this host's
// interfaces that is not in a private, shared or tailnet range.
func publicHostAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	shared := netip.MustParsePrefix("100.64.0.0/10")
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := prefix.Addr().Unmap()
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && !shared.Contains(ip) {
			return ip.String(), nil
		}
	}
	return "", nil
}
