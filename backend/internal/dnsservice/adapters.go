package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func inspectNative(ctx context.Context, req ConnectionRequest) (*Snapshot, error) {
	return inspectNativeSelection(ctx, req, nil)
}

func inspectNativeSelection(ctx context.Context, req ConnectionRequest, selection *ChangeRequest) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	c, err := newNativeClient(req)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if err = c.login(ctx); err != nil {
		return nil, err
	}
	defer c.logout()
	s := &Snapshot{ObservedAt: time.Now().UTC(), Engine: req.Engine, Roles: []string{}, Listeners: []Listener{}, AllowedClients: []string{}, DeniedClients: []string{}, Upstreams: []string{}, Clients: []ClientPolicy{}, Zones: []Zone{}, Queries: []Query{}, Limitations: []string{
		"Management identity and configured listeners do not establish a client's DNS path, upstream encryption, authority correctness or provider reachability.",
		"Native configuration reads are sequential, not an atomic snapshot or a packet trace.",
	}}
	s.Views = Reading{"unsupported", "native_contract", "This engine adapter does not infer DNS views from local overrides or client filter groups."}
	s.ViewGroups, s.FilterGroups, s.LocalOverrides = []NativeGroup{}, []NativeGroup{}, []LocalOverride{}
	s.NamedNetworks = map[string][]string{}
	s.AppClientEvidence = Reading{"unsupported", "native_contract", "App-specific client groups are a separate native capability."}
	s.OverrideEvidence = Reading{"unknown", "unavailable", "Native local overrides have not been established."}
	s.QueryEvidence = Reading{"unknown", "unavailable", "Native query history has not been established."}
	s.ZoneEvidence = Reading{"unsupported", "native_contract", "This engine supplies local overrides; they are not native authoritative zones."}
	s.ClientEvidence = Reading{"unknown", "unavailable", "Native client policy has not been established."}
	s.Runtime = Reading{"unknown", "unavailable", "Native process readiness has not been established; configured listeners do not prove open sockets."}
	switch req.Engine {
	case AdGuard:
		err = c.inspectAdGuard(ctx, s)
	case PiHole:
		err = c.inspectPiHole(ctx, s)
	case Technitium:
		err = c.inspectTechnitium(ctx, s)
	default:
		err = errors.New("unsupported native DNS engine")
	}
	if err != nil {
		return nil, err
	}
	if selection != nil && policyAction(selection.Action) {
		if err = c.inspectPolicySelection(ctx, s, *selection); err != nil {
			return nil, err
		}
		if err = c.retainPolicy(s.SelectionFingerprint); err != nil {
			return nil, err
		}
	}
	s.Transport = c.transport
	s.PolicyFingerprint = c.fingerprint()
	encoded, _ := json.Marshal(s)
	if len(encoded) > 192<<10 {
		return nil, errors.New("native DNS snapshot exceeds its retained bound")
	}
	return s, nil
}

func (c *nativeClient) strings(values []string, bound int) ([]string, error) {
	if len(values) > bound {
		return nil, errors.New("native DNS inventory exceeds its supported bound")
	}
	out := make([]string, 0, len(values))
	for _, s := range values {
		out = append(out, c.text(s))
	}
	return out, nil
}

func versionSupported(version, prefix string) bool {
	version = strings.TrimPrefix(version, "v")
	if !strings.HasPrefix(version, prefix) || len(version) > 64 {
		return false
	}
	for _, ch := range version {
		if !(ch >= '0' && ch <= '9' || ch == '.') {
			return false
		}
	}
	return true
}

func (c *nativeClient) inspectAdGuard(ctx context.Context, s *Snapshot) error {
	var status struct {
		Version    string   `json:"version"`
		Addresses  []string `json:"dns_addresses"`
		Port       int      `json:"dns_port"`
		Protection *bool    `json:"protection_enabled"`
		Running    *bool    `json:"running"`
	}
	if err := c.request(ctx, http.MethodGet, "/control/status", nil, &status); err != nil {
		return err
	}
	if !versionSupported(status.Version, "0.107.") || status.Protection == nil || status.Running == nil || status.Port < 1 || status.Port > 65535 || len(status.Addresses) == 0 || len(status.Addresses) > 64 {
		return errors.New("AdGuard status does not match the supported 0.107 API contract")
	}
	s.Version, s.Protection = status.Version, *status.Protection
	s.Runtime = Reading{"native_stopped", "native_status", "The authenticated native status reports DNS is stopped; configured listeners are retained separately."}
	if *status.Running {
		s.Runtime = Reading{"native_running", "native_status", "The authenticated native status reports DNS is running; actual socket binding and client reachability remain unmeasured."}
	}
	s.Roles = []string{"recursive_filtering", "local_overrides"}
	s.UpstreamProtocol = "per_endpoint_configuration"
	for _, address := range status.Addresses {
		if _, err := netip.ParseAddr(address); err != nil {
			return errors.New("native AdGuard listener address is unreadable")
		}
		for _, protocol := range []string{"udp", "tcp"} {
			s.Listeners = append(s.Listeners, Listener{address, status.Port, protocol, "native_configuration"})
		}
	}
	var config map[string]json.RawMessage
	if err := c.request(ctx, http.MethodGet, "/control/dns_info", nil, &config); err != nil {
		return err
	}
	var upstreams []string
	if json.Unmarshal(config["upstream_dns"], &upstreams) != nil {
		return errors.New("native AdGuard upstream policy is unreadable")
	}
	var err error
	if s.Upstreams, err = c.strings(upstreams, 128); err != nil {
		return err
	}
	if err = c.retainPolicy(config); err != nil {
		return err
	}
	if err = c.retainPolicy(struct {
		Version    string
		Addresses  []string
		Port       int
		Protection bool
		Running    bool
	}{status.Version, status.Addresses, status.Port, *status.Protection, *status.Running}); err != nil {
		return err
	}
	var access struct {
		Allowed []string `json:"allowed_clients"`
		Denied  []string `json:"disallowed_clients"`
		Blocked []string `json:"blocked_hosts"`
	}
	if err = c.request(ctx, http.MethodGet, "/control/access/list", nil, &access); err != nil {
		return err
	}
	if access.Allowed == nil || access.Denied == nil || access.Blocked == nil {
		return errors.New("native AdGuard access policy did not declare all inventories")
	}
	if s.AllowedClients, err = c.strings(access.Allowed, 128); err != nil {
		return err
	}
	if s.DeniedClients, err = c.strings(access.Denied, 128); err != nil {
		return err
	}
	if err = c.retainPolicy(access); err != nil {
		return err
	}
	s.Access = Reading{"configured", "native_configuration", "Native allowed/denied clients are configured policy; listener publication and client reachability require separate evidence."}
	var clients struct {
		Clients []struct {
			Name      string   `json:"name"`
			IDs       []string `json:"ids"`
			Inherited bool     `json:"use_global_settings"`
			Filtering *bool    `json:"filtering_enabled"`
		} `json:"clients"`
	}
	if err = c.request(ctx, http.MethodGet, "/control/clients", nil, &clients); err != nil || clients.Clients == nil {
		if err == nil {
			err = errors.New("native client response did not declare its inventory")
		}
		s.ClientEvidence.Summary = err.Error()
	} else {
		if len(clients.Clients) > 128 {
			return errors.New("native AdGuard client inventory exceeds its bound")
		}
		for _, client := range clients.Clients {
			addresses, e := c.strings(client.IDs, 64)
			if e != nil {
				return e
			}
			s.Clients = append(s.Clients, ClientPolicy{ID: c.text(client.Name), Name: c.text(client.Name), Addresses: addresses, Filtering: client.Filtering, Inherited: client.Inherited})
		}
		s.ClientEvidence = Reading{"configured", "native_configuration", "Persistent native client filter settings are listed; inherited settings remain explicit."}
		if err = c.retainPolicy(clients); err != nil {
			return err
		}
	}
	if err = c.inspectAdGuardOverrides(ctx, s); err != nil {
		return err
	}
	var logs struct {
		Data []struct {
			Time     string `json:"time"`
			Client   string `json:"client"`
			Reason   string `json:"reason"`
			Protocol string `json:"client_proto"`
			Question struct {
				Host string `json:"host"`
				Type string `json:"type"`
			} `json:"question"`
		} `json:"data"`
	}
	if err = c.request(ctx, http.MethodGet, "/control/querylog?limit=100", nil, &logs); err != nil || logs.Data == nil {
		if err == nil {
			err = errors.New("native query log did not declare its entry inventory")
		}
		s.QueryEvidence.Summary = err.Error()
	} else {
		if len(logs.Data) > 100 {
			return errors.New("native AdGuard query history exceeds its requested bound")
		}
		for _, q := range logs.Data {
			s.Queries = append(s.Queries, Query{c.text(q.Time), c.text(q.Client), c.text(q.Question.Host), c.text(q.Question.Type), c.text(q.Reason), c.text(q.Protocol)})
		}
		s.QueryEvidence = Reading{"native_history", "native_api", "At most 100 recent native query-log entries; absent entries do not prove an absence of DNS traffic."}
	}
	return nil
}

func (c *nativeClient) inspectPiHole(ctx context.Context, s *Snapshot) error {
	var version struct {
		Version struct {
			FTL struct {
				Local struct {
					Version string `json:"version"`
				} `json:"local"`
			} `json:"ftl"`
		} `json:"version"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/info/version", nil, &version); err != nil {
		return err
	}
	s.Version = version.Version.FTL.Local.Version
	if !versionSupported(s.Version, "6.") {
		return errors.New("Pi-hole requires the supported native FTL 6 API")
	}
	var runtime struct {
		FTL struct {
			PID    int      `json:"pid"`
			Uptime *float64 `json:"uptime"`
		} `json:"ftl"`
	}
	requestStarted := time.Now().UTC()
	if err := c.request(ctx, http.MethodGet, "/api/info/ftl", nil, &runtime); err == nil && runtime.FTL.PID > 0 && runtime.FTL.Uptime != nil && *runtime.FTL.Uptime >= 0 && *runtime.FTL.Uptime < float64((100*365*24*time.Hour)/time.Millisecond) {
		requestEnded := time.Now().UTC()
		uptime := time.Duration(*runtime.FTL.Uptime * float64(time.Millisecond))
		s.Process = &NativeProcess{PID: runtime.FTL.PID, StartedAt: requestStarted.Add(-uptime - time.Millisecond), StartedBefore: requestEnded.Add(-uptime + time.Millisecond)}
		s.Runtime = Reading{"native_process", "native_runtime", "The authenticated FTL API reports process identity and uptime. Start-time bounds include request latency; exact DNS socket bindings remain unmeasured."}
	}
	var config struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/config", nil, &config); err != nil {
		return err
	}
	var dns struct {
		Upstreams []string `json:"upstreams"`
		Mode      string   `json:"listeningMode"`
		Interface string   `json:"interface"`
		Port      int      `json:"port"`
		Hosts     []string `json:"hosts"`
		CNAME     []string `json:"cnameRecords"`
	}
	if json.Unmarshal(config.Config["dns"], &dns) != nil || dns.Upstreams == nil || dns.Port < 1 || dns.Port > 65535 || dns.Mode == "" {
		return errors.New("native Pi-hole DNS policy is unreadable")
	}
	var err error
	if s.Upstreams, err = c.strings(dns.Upstreams, 128); err != nil {
		return err
	}
	if err = c.retainPolicy(config.Config); err != nil {
		return err
	}
	if err = c.retainPolicy(s.Version); err != nil {
		return err
	}
	s.Roles = []string{"recursive_filtering", "local_overrides"}
	s.UpstreamProtocol = "classic_dns_to_configured_forwarders"
	address := "available interfaces"
	if dns.Interface != "" {
		address = "interface " + c.text(dns.Interface)
	}
	for _, protocol := range []string{"udp", "tcp"} {
		s.Listeners = append(s.Listeners, Listener{address, dns.Port, protocol, "native_configuration"})
	}
	s.Access = Reading{"configured", "native_configuration", "Native listening mode: " + c.text(dns.Mode) + ". Exact socket bindings, firewall exposure and client reachability are unmeasured."}
	if dns.Hosts != nil && dns.CNAME != nil {
		if len(dns.Hosts)+len(dns.CNAME) > 256 {
			return errors.New("native Pi-hole override inventory exceeds its bound")
		}
		for _, value := range dns.Hosts {
			s.LocalOverrides = append(s.LocalOverrides, LocalOverride{Value: c.text(value), Type: "hosts_entry"})
		}
		for _, value := range dns.CNAME {
			s.LocalOverrides = append(s.LocalOverrides, LocalOverride{Value: c.text(value), Type: "cname_entry"})
		}
		s.OverrideEvidence = Reading{"configured", "native_configuration", "Native hosts and CNAME configuration are local overrides; they do not establish a delegated authoritative zone."}
	}
	var blocking struct {
		Blocking string   `json:"blocking"`
		Timer    *float64 `json:"timer"`
	}
	if err = c.request(ctx, http.MethodGet, "/api/dns/blocking", nil, &blocking); err != nil {
		return err
	}
	if blocking.Blocking != "enabled" && blocking.Blocking != "disabled" {
		return errors.New("native Pi-hole blocking state is unreadable")
	}
	s.Protection = blocking.Blocking == "enabled"
	s.ProtectionTemporary = blocking.Timer != nil
	if err = c.retainPolicy(blocking); err != nil {
		return err
	}
	var clients struct {
		Clients []struct {
			Client  string `json:"client"`
			Comment string `json:"comment"`
			Groups  []int  `json:"groups"`
		} `json:"clients"`
	}
	if err = c.request(ctx, http.MethodGet, "/api/clients", nil, &clients); err != nil || clients.Clients == nil {
		if err == nil {
			err = errors.New("native client response did not declare its inventory")
		}
		s.ClientEvidence.Summary = err.Error()
	} else {
		if len(clients.Clients) > 128 {
			return errors.New("native Pi-hole client inventory exceeds its bound")
		}
		for _, client := range clients.Clients {
			if len(client.Groups) > 64 {
				return errors.New("native Pi-hole client groups exceed their bound")
			}
			s.Clients = append(s.Clients, ClientPolicy{ID: c.text(client.Client), Name: c.text(client.Comment), Addresses: []string{c.text(client.Client)}, Groups: client.Groups})
		}
		s.ClientEvidence = Reading{"configured", "native_configuration", "Native persistent clients and their group assignments; group filtering rules are not inferred."}
		if err = c.retainPolicy(clients); err != nil {
			return err
		}
	}
	if err = c.inspectPiHoleGroups(ctx, s); err != nil {
		return err
	}
	var logs struct {
		Queries []struct {
			Time   float64 `json:"time"`
			Type   string  `json:"type"`
			Domain string  `json:"domain"`
			Status string  `json:"status"`
			Client struct {
				IP string `json:"ip"`
			} `json:"client"`
		} `json:"queries"`
	}
	if err = c.request(ctx, http.MethodGet, "/api/queries?length=100", nil, &logs); err != nil || logs.Queries == nil {
		if err == nil {
			err = errors.New("native query history did not declare its entry inventory")
		}
		s.QueryEvidence.Summary = err.Error()
	} else {
		if len(logs.Queries) > 100 {
			return errors.New("native Pi-hole query history exceeds its requested bound")
		}
		for _, q := range logs.Queries {
			s.Queries = append(s.Queries, Query{strconv.FormatFloat(q.Time, 'f', 3, 64), c.text(q.Client.IP), c.text(q.Domain), c.text(q.Type), c.text(q.Status), ""})
		}
		s.QueryEvidence = Reading{"native_history", "native_api", "At most 100 recent native query entries; runtime history and configured client policy are distinct."}
	}
	return nil
}

func (c *nativeClient) technitium(ctx context.Context, path string, form url.Values, result any) error {
	var envelope struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if form == nil {
		form = url.Values{}
	}
	if err := c.request(ctx, http.MethodPost, path, form, &envelope); err != nil {
		return err
	}
	if envelope.Status != "ok" {
		return errors.New("native Technitium refused the operation or its scoped token")
	}
	if result != nil && json.Unmarshal(envelope.Response, result) != nil {
		return errors.New("native Technitium response does not match its supported contract")
	}
	return nil
}

func (c *nativeClient) inspectTechnitium(ctx context.Context, s *Snapshot) error {
	var config map[string]json.RawMessage
	if err := c.technitium(ctx, "/api/settings/get", nil, &config); err != nil {
		return err
	}
	var policy struct {
		Version    string   `json:"version"`
		Endpoints  []string `json:"dnsServerLocalEndPoints"`
		Recursion  string   `json:"recursion"`
		ACL        []string `json:"recursionNetworkACL"`
		Protection *bool    `json:"enableBlocking"`
		Forwarders []string `json:"forwarders"`
		Protocol   string   `json:"forwarderProtocol"`
	}
	encoded, _ := json.Marshal(config)
	if json.Unmarshal(encoded, &policy) != nil || !versionSupported(policy.Version, "15.") || policy.Protection == nil || len(policy.Endpoints) == 0 || len(policy.Endpoints) > 64 || policy.Recursion == "" {
		return errors.New("Technitium requires readable settings from the supported native version 15 API")
	}
	s.Version, s.Protection = policy.Version, *policy.Protection
	c.version = s.Version
	s.OverrideEvidence = Reading{"unsupported", "native_contract", "Native authoritative and forwarder zones are inspected separately; they are not host-file overrides."}
	s.UpstreamProtocol = c.text(policy.Protocol)
	s.Roles = []string{"authoritative", "recursive"}
	if policy.Recursion == "Deny" {
		s.Roles = []string{"authoritative"}
	}
	for _, endpoint := range policy.Endpoints {
		ap, err := netip.ParseAddrPort(endpoint)
		if err != nil || ap.Port() == 0 {
			return errors.New("native Technitium listener endpoint is unreadable")
		}
		for _, protocol := range []string{"udp", "tcp"} {
			s.Listeners = append(s.Listeners, Listener{ap.Addr().String(), int(ap.Port()), protocol, "native_configuration"})
		}
	}
	var err error
	if s.Upstreams, err = c.strings(policy.Forwarders, 128); err != nil {
		return err
	}
	if len(policy.ACL) > 128 {
		return errors.New("native Technitium recursion ACL exceeds its bound")
	}
	for _, entry := range policy.ACL {
		if strings.HasPrefix(entry, "!") {
			s.DeniedClients = append(s.DeniedClients, c.text(strings.TrimPrefix(entry, "!")))
		} else {
			s.AllowedClients = append(s.AllowedClients, c.text(entry))
		}
	}
	s.Access = Reading{"configured", "native_configuration", "Native recursion mode: " + c.text(policy.Recursion) + "; ACL order and unmatched policy are owned by Technitium. This does not establish listener reachability."}
	s.ClientEvidence = Reading{"configured", "native_configuration", "The recursion ACL describes client network access. App-specific filtering and DNS views remain separate capabilities."}
	if err = c.retainPolicy(config); err != nil {
		return err
	}
	var zones struct {
		Zones []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Disabled bool   `json:"disabled"`
			DNSSEC   string `json:"dnssecStatus"`
		} `json:"zones"`
		Total int `json:"totalZones"`
		Pages int `json:"totalPages"`
	}
	if err = c.technitium(ctx, "/api/zones/list", url.Values{"pageNumber": {"1"}, "zonesPerPage": {"256"}}, &zones); err != nil || zones.Zones == nil {
		if err == nil {
			err = errors.New("native zone response did not declare its inventory")
		}
		s.ZoneEvidence = Reading{"unknown", "unavailable", err.Error()}
	} else {
		if len(zones.Zones) > 256 {
			return errors.New("native Technitium zone inventory exceeds its requested bound")
		}
		for _, zone := range zones.Zones {
			s.Zones = append(s.Zones, Zone{c.text(zone.Name), c.text(zone.Type), zone.Disabled, c.text(zone.DNSSEC)})
		}
		s.ZoneEvidence = Reading{"native_authority_configuration", "native_api", "Native authoritative/forwarder zone types are listed separately from local host overrides; publication and delegation are unmeasured."}
		if zones.Pages > 1 || zones.Total > len(zones.Zones) {
			s.ZoneEvidence.State = "partial"
			s.ZoneEvidence.Summary += " Only the first 256 visible zones are retained."
		}
		if err = c.retainPolicy(zones); err != nil {
			return err
		}
	}
	var apps struct {
		Apps []struct {
			Name    string `json:"name"`
			DNSApps []struct {
				Class       string `json:"classPath"`
				QueryLogger bool   `json:"isQueryLogger"`
			} `json:"dnsApps"`
		} `json:"apps"`
	}
	if err = c.technitium(ctx, "/api/apps/list", nil, &apps); err != nil || apps.Apps == nil {
		if err == nil {
			err = errors.New("native app response did not declare its inventory")
		}
		s.QueryEvidence.Summary = err.Error()
		return nil
	}
	if len(apps.Apps) > 64 {
		return errors.New("native Technitium app inventory exceeds its bound")
	}
	if err = c.retainPolicy(apps); err != nil {
		return err
	}
	for _, app := range apps.Apps {
		if len(app.DNSApps) > 64 {
			return errors.New("native Technitium app-class inventory exceeds its bound")
		}
		for _, class := range app.DNSApps {
			if class.Class == "AdvancedBlocking.App" {
				if err = c.inspectNativeGroups(ctx, s, app.Name, false); err != nil {
					return err
				}
				break
			}
			if strings.HasPrefix(class.Class, "SplitHorizon.") {
				if err = c.inspectNativeGroups(ctx, s, app.Name, true); err != nil {
					return err
				}
				break
			}
		}
	}
	for _, app := range apps.Apps {
		for _, class := range app.DNSApps {
			if !class.QueryLogger {
				continue
			}
			if len(app.Name) > 128 || len(class.Class) > 256 {
				return errors.New("native query logger identity exceeds its bound")
			}
			var logs struct {
				Entries []struct {
					At       string `json:"timestamp"`
					Client   string `json:"clientIpAddress"`
					Name     string `json:"qname"`
					Type     string `json:"qtype"`
					Status   string `json:"responseType"`
					Protocol string `json:"protocol"`
				} `json:"entries"`
			}
			form := url.Values{"name": {app.Name}, "classPath": {class.Class}, "pageNumber": {"1"}, "entriesPerPage": {"100"}, "descendingOrder": {"true"}}
			if err = c.technitium(ctx, "/api/logs/query", form, &logs); err != nil || logs.Entries == nil {
				if err == nil {
					err = errors.New("native query logger did not declare its entry inventory")
				}
				s.QueryEvidence.Summary = err.Error()
				return nil
			}
			if len(logs.Entries) > 100 {
				return errors.New("native Technitium query history exceeds its requested bound")
			}
			for _, q := range logs.Entries {
				s.Queries = append(s.Queries, Query{c.text(q.At), c.text(q.Client), c.text(q.Name), c.text(q.Type), c.text(q.Status), c.text(q.Protocol)})
			}
			s.QueryEvidence = Reading{"native_history", "native_api", "At most 100 entries from the first installed native query-logger app. The dashboard installs no logging app."}
			return nil
		}
	}
	s.QueryEvidence = Reading{"unsupported", "native_contract", "No installed native query-logger app is visible. No app is installed or private DNS question rerun to manufacture history."}
	return nil
}

func applyNative(ctx context.Context, req ConnectionRequest, change ChangeRequest, before *Snapshot) error {
	if err := validateChange(change, req.Engine); err != nil {
		return err
	}
	c, err := newNativeClient(req)
	if err != nil {
		return err
	}
	defer c.close()
	if err = c.login(ctx); err != nil {
		return err
	}
	defer c.logout()
	if policyAction(change.Action) {
		return applyPolicyNative(ctx, c, change, before)
	}
	switch change.Action {
	case "protection":
		switch req.Engine {
		case AdGuard:
			return c.request(ctx, http.MethodPost, "/control/protection", map[string]any{"enabled": *change.Protection, "duration": 0}, nil)
		case PiHole:
			return c.request(ctx, http.MethodPost, "/api/dns/blocking", map[string]any{"blocking": *change.Protection, "timer": nil}, nil)
		case Technitium:
			return c.technitium(ctx, "/api/settings/set", url.Values{"enableBlocking": {strconv.FormatBool(*change.Protection)}}, nil)
		}
	case "upstreams":
		values := make([]string, len(change.Upstreams))
		for i, endpoint := range change.Upstreams {
			ap, _ := netip.ParseAddrPort(endpoint)
			switch req.Engine {
			case PiHole:
				values[i] = ap.Addr().String() + "#" + strconv.Itoa(int(ap.Port()))
			case Technitium:
				values[i] = ap.String()
			default:
				values[i] = ap.String()
			}
		}
		switch req.Engine {
		case AdGuard:
			return c.request(ctx, http.MethodPost, "/control/dns_config", map[string]any{"upstream_dns": values}, nil)
		case PiHole:
			return c.request(ctx, http.MethodPatch, "/api/config", map[string]any{"config": map[string]any{"dns": map[string]any{"upstreams": values}}}, nil)
		case Technitium:
			return c.technitium(ctx, "/api/settings/set", url.Values{"forwarders": {strings.Join(values, ",")}, "forwarderProtocol": {"Udp"}}, nil)
		}
	case "access":
		var current struct {
			Blocked []string `json:"blocked_hosts"`
		}
		if err = c.request(ctx, http.MethodGet, "/control/access/list", nil, &current); err != nil {
			return err
		}
		if len(current.Blocked) > 2048 {
			return errors.New("native blocked-host policy exceeds its supported bound")
		}
		if current.Blocked == nil {
			return errors.New("native blocked-host policy did not declare its inventory")
		}
		denied := change.DeniedClients
		if denied == nil {
			denied = []string{}
		}
		return c.request(ctx, http.MethodPost, "/control/access/set", map[string]any{"allowed_clients": change.AllowedClients, "disallowed_clients": denied, "blocked_hosts": current.Blocked}, nil)
	case "zone_create":
		return c.technitium(ctx, "/api/zones/create", url.Values{"zone": {change.Zone}, "type": {"Primary"}}, nil)
	}
	return errors.New("unsupported native DNS action")
}

func canonicalUpstream(engine Engine, entry string) (netip.AddrPort, bool) {
	if ap, err := netip.ParseAddrPort(entry); err == nil && ap.Port() != 0 && ap.Addr().Zone() == "" {
		return ap, true
	}
	if engine == PiHole && strings.Contains(entry, "#") {
		parts := strings.Split(entry, "#")
		if len(parts) != 2 {
			return netip.AddrPort{}, false
		}
		ip, err := netip.ParseAddr(strings.Trim(parts[0], "[]"))
		port, e := strconv.Atoi(parts[1])
		if err != nil || e != nil || port < 1 || port > 65535 || ip.Zone() != "" {
			return netip.AddrPort{}, false
		}
		return netip.AddrPortFrom(ip, uint16(port)), true
	}
	if ip, err := netip.ParseAddr(strings.Trim(entry, "[]")); err == nil && ip.Zone() == "" {
		return netip.AddrPortFrom(ip, 53), true
	}
	return netip.AddrPort{}, false
}
