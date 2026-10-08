package api

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkInvestigatorRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/investigate/sources", s.handle(s.handleNetworkInvestigatorSources))
		r.Method(http.MethodPost, "/investigate", s.handle(s.handleNetworkInvestigate))
	})
}

type investigatorSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleNetworkInvestigatorSources(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 10*time.Second)
	defer cancel()
	response := struct {
		Containers []investigatorSource `json:"containers"`
		Error      string               `json:"error,omitempty"`
	}{Containers: []investigatorSource{}}
	if s.modules.docker == nil {
		response.Error = "Docker inventory is unavailable."
	} else if values, err := s.modules.docker.ListContainers(ctx, false); err != nil {
		response.Error = "Docker inventory could not be read: " + err.Error()
	} else {
		for _, value := range values {
			if value.State == "running" && len(response.Containers) < 200 {
				response.Containers = append(response.Containers, investigatorSource{ID: value.ID, Name: value.Name})
			}
		}
	}
	httpx.JSON(w, http.StatusOK, response)
	return nil
}

func (s *Server) handleNetworkInvestigate(w http.ResponseWriter, r *http.Request) error {
	var request netpath.Request
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	req, err := netpath.Validate(request)
	if err != nil {
		return httpx.BadRequest("%s", err)
	}
	auditScope := map[string]any{"sourceKind": req.SourceKind, "containerId": req.ContainerID, "family": req.Family, "protocol": req.Protocol, "port": req.Port, "measure": req.Measure, "requestedSource": req.SourceAddress, "requestedAddress": req.Address, "mark": req.Mark}
	httpx.SetAudit(r, "network.path.investigate", req.Target, auditScope)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	providers, err := s.networkPathProviders(ctx, req)
	if err != nil {
		return httpx.Err(http.StatusConflict, "source_unavailable", err.Error())
	}
	result, err := netpath.Investigate(ctx, req, providers)
	if err != nil {
		return httpx.BadRequest("%s", err)
	}
	auditScope["pinnedAddress"], auditScope["selectedSource"] = result.Scope.Address, result.Scope.SourceAddress
	if len(result.Evidence) > 0 {
		probe := result.Evidence[len(result.Evidence)-1]
		auditScope["probeBasis"], auditScope["probeState"] = probe.Basis, probe.State
	}
	httpx.SetAudit(r, "network.path.investigate", req.Target, auditScope)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) executeNetworkInvestigation(ctx context.Context, request netpath.Request) (*netpath.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := netpath.Validate(request)
	if err != nil {
		return nil, err
	}
	providers, err := s.networkPathProviders(ctx, req)
	if err != nil {
		return nil, err
	}
	return netpath.Investigate(ctx, req, providers)
}

func (s *Server) networkPathProviders(ctx context.Context, req netpath.Request) (netpath.Providers, error) {
	var execute netx.TrafficExecutor
	var command netsec.SourceCommand
	name := "Dashboard host"
	var container *dockerx.NetworkSource
	if req.SourceKind == "container" {
		if s.modules.docker == nil {
			return netpath.Providers{}, fmt.Errorf("the Docker owner is unavailable; no host-source substitute will be used")
		}
		var err error
		container, err = s.modules.docker.NetworkSource(ctx, req.ContainerID)
		if err != nil {
			return netpath.Providers{}, err
		}
		name = container.Name
		command = func(ctx context.Context, tool string, args ...string) (*exec.Cmd, func(), error) {
			return s.modules.docker.NetworkSourceCommand(ctx, container, tool, args...)
		}
		execute = func(ctx context.Context, tool string, args ...string) (string, error) {
			output, _, err := netsec.RunSourceDiagnostic(ctx, command, tool, args...)
			return output, err
		}
	}
	p := netpath.Providers{SourceName: name}
	p.DNS = func(ctx context.Context, request netpath.Request) (netpath.DNSAnswer, error) {
		if container != nil {
			if container.DNSError != "" {
				return netpath.DNSAnswer{}, fmt.Errorf("%s No host/public fallback was queried.", container.DNSError)
			}
			result, err := netsec.SourceDNS(ctx, command, request.Target, request.Family, container.Nameservers)
			if err != nil {
				return netpath.DNSAnswer{}, err
			}
			if result.Error != "" {
				return netpath.DNSAnswer{}, fmt.Errorf("%s", result.Error)
			}
			return netpath.DNSAnswer{Addresses: result.Records, Owner: "Container native resolver", Summary: "The configured container resolver answered an absolute DNS wire query in its verified network namespace.", Limits: []string{"The query follows configured nameservers only; full NSS/hosts/search rules and encrypted resolver transport are not represented.", "No host resolver or public comparison preset was queried."}}, nil
		}
		if s.modules.network == nil {
			return netpath.DNSAnswer{}, fmt.Errorf("the host native DNS owner is unavailable; no fallback was queried")
		}
		record := "A"
		if request.Family == "inet6" {
			record = "AAAA"
		}
		answer, err := s.modules.network.LookupWithOptions(ctx, request.Target, record, netx.LookupOptions{Mode: "effective"})
		if err != nil {
			return netpath.DNSAnswer{}, err
		}
		var addresses []string
		var failures []string
		for _, row := range answer.Answers {
			addresses = append(addresses, row.Answers...)
			if row.Error != "" {
				failures = append(failures, row.Error)
			}
		}
		if len(addresses) == 0 && len(failures) > 0 {
			return netpath.DNSAnswer{}, fmt.Errorf("The native resolver returned no usable %s address: %s", record, strings.Join(failures, "; "))
		}
		return netpath.DNSAnswer{Addresses: addresses, Owner: "Host native resolver", Summary: answer.Route, Limits: []string{answer.Note}}, nil
	}
	if s.modules.network != nil {
		p.Rules = func(ctx context.Context, family string) ([]netx.TrafficRule, error) {
			return s.modules.network.TrafficRules(ctx, family, execute)
		}
		p.Route = func(ctx context.Context, request netpath.Request) (netx.RouteLookup, error) {
			return s.modules.network.LookupTrafficRoute(ctx, request.Address, request.SourceAddress, request.Mark, request.Protocol, request.Port, execute)
		}
		p.Gateway = s.modules.network.Gateway
		p.Links = func(ctx context.Context) ([]netx.Link, error) {
			return s.modules.network.ReadLinks(ctx, s.networkInventory(ctx), "")
		}
	}
	if s.modules.netsec != nil {
		p.Firewall = s.modules.netsec.Status
	}
	p.Owners = func(ctx context.Context) (netpath.OwnerSnapshot, error) {
		values, err := proxysvc.ListListeners(ctx)
		if err != nil {
			return netpath.OwnerSnapshot{}, err
		}
		values = proxysvc.AttributeOwners(values, s.ownerInput(ctx, true))
		result := netpath.OwnerSnapshot{Listeners: values, Limits: []string{"Process ownership is a current socket snapshot; request handling and remote owners are not measured."}}
		if s.modules.proxy == nil {
			result.Limits = append(result.Limits, "The native proxy owner is unavailable.")
			return result, nil
		}
		vhosts, err := s.modules.proxy.ListVHosts(ctx)
		if err != nil {
			result.Limits = append(result.Limits, "Native proxy sites could not be read: "+err.Error())
		}
		streams, err := s.modules.proxy.Streams(ctx)
		if err != nil {
			result.Limits = append(result.Limits, "Native stream configuration could not be read: "+err.Error())
		}
		proxysvc.AttachProxy(result.Listeners, vhosts, streams)
		return result, nil
	}
	p.Probe = func(ctx context.Context, request netpath.Request) (*netsec.ProbeResult, error) {
		if container != nil {
			return netsec.SourcePortCheck(ctx, command, request.Address, request.Port, request.SourceAddress)
		}
		if s.modules.netsec == nil {
			return nil, fmt.Errorf("the host TCP probe owner is unavailable")
		}
		return s.modules.netsec.PortCheckFromSource(ctx, request.Address, request.Port, request.SourceAddress)
	}
	return p, nil
}
