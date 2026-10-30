package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/cftunnel"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsrecords"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// A deployment domain's DNS record, written from here (dnsrecords).
//
// A plan whose domain does not point at this server used to stop at the
// public-DNS precheck and send the operator to their provider. The DNS
// credentials the Certificates page saved for certbot's DNS-01 plugins can
// write an address record the same way they write a challenge, so the
// Domains settings ask GET …/dns-record what the name needs — A and AAAA to
// this server's public addresses, as preflight's comparison reads them, or
// for a name served through Cloudflare Tunnel a proxied CNAME to the tunnel
// — what its zone holds, and through which saved credential and zone it
// would be written; POST writes it. Nothing is deleted: a CNAME standing
// where an address is wanted, or a set that could only shrink by removing a
// record, is refused in words. A record this dashboard did not write is
// replaced only when the request says replace, which the sheet sends after
// showing what it replaces. What was written is kept in deploy_dns_records,
// which is how the sheet says "managed here, created then, by them".
//
// Both routes are an administrator's, in a session, as the Domains
// settings are; a deploy key is refused (deploy_keys.go): this writes to a
// zone outside the server with a credential the key never sees.

// dnsRecordHooks are what a test replaces: the saved credentials as
// providers, this server's public addresses, and whose nameservers a zone
// names. The zero value is the server's own.
type dnsRecordHooks struct {
	providers func() ([]dnsrecords.Provider, []string)
	addresses func() []string
	hostedBy  func(context.Context, string) string
}

func (s *Server) dnsRecordProviders() ([]dnsrecords.Provider, []string) {
	if s.dnsRecords.providers != nil {
		return s.dnsRecords.providers()
	}
	providers, problems := []dnsrecords.Provider{}, []string{}
	for _, key := range proxysvc.SavedDNSProviderKeys() {
		content, err := proxysvc.SavedDNSCredentials(key)
		if err == nil {
			var provider dnsrecords.Provider
			if provider, err = dnsrecords.New(key, content, dnsrecords.Options{}); err == nil {
				providers = append(providers, provider)
				continue
			}
		}
		name := key
		if known, ok := proxysvc.DNSProviderFor(key); ok {
			name = known.Name
		}
		problems = append(problems, name+": "+err.Error())
	}
	return providers, problems
}

func (s *Server) dnsRecordAddresses() []string {
	if s.dnsRecords.addresses != nil {
		return s.dnsRecords.addresses()
	}
	return proxysvc.PublicAddresses()
}

func (s *Server) dnsRecordHostedBy(ctx context.Context, name string) string {
	if s.dnsRecords.hostedBy != nil {
		return s.dnsRecords.hostedBy(ctx, name)
	}
	return proxysvc.ZoneHostedBy(ctx, name)
}

// dnsRecordPlan is the dry run: what the name should hold, what its zone
// holds, through whose credentials it would be written, and what a write
// does. POST answers it too, with what it wrote.
type dnsRecordPlan struct {
	Hostname      string `json:"hostname"`
	EnvironmentID int64  `json:"environmentId"`
	Environment   string `json:"environment"`
	// Tunnel says the name is served through Cloudflare Tunnel, so it
	// wants a proxied CNAME to the tunnel rather than this server's
	// addresses.
	Tunnel bool `json:"tunnel"`
	// Want are the record sets the name should hold.
	Want []dnsrecords.Set `json:"want"`
	// Provider, ProviderKey, Zone and Account say whose saved credentials
	// would write it and where; absent when none can.
	Provider    string `json:"provider,omitempty"`
	ProviderKey string `json:"providerKey,omitempty"`
	Zone        string `json:"zone,omitempty"`
	Account     string `json:"account,omitempty"`
	// Candidates are every saved provider as it was asked, with the zone it
	// reaches or why it could not answer.
	Candidates []dnsrecords.Candidate `json:"candidates"`
	// Problems are saved credentials that could not be used at all.
	Problems []string `json:"problems"`
	// Changes are, set by set, what the zone holds and what a write does.
	Changes []dnsRecordChange `json:"changes"`
	// Writable says a write would go through; Ready that nothing is left
	// to write.
	Writable bool `json:"writable"`
	Ready    bool `json:"ready"`
	// Foreign says the write would change a record this dashboard did not
	// write, which POST does only with replace.
	Foreign bool `json:"foreign"`
	// Refusal is why nothing can be written, RefusalCode a word for it:
	// no_public_address, tunnel_not_connected, no_credentials, no_zone,
	// ambiguous, tunnel_zone, provider_error, conflict.
	Refusal     string `json:"refusal,omitempty"`
	RefusalCode string `json:"refusalCode,omitempty"`
	// Summary is the dry run in a sentence.
	Summary string `json:"summary"`
	// Managed are the sets this dashboard wrote for the name.
	Managed []dnsRecordManaged `json:"managed"`
	// Written are the sets a POST changed.
	Written []dnsrecords.Change `json:"written,omitempty"`
}

// dnsRecordChange is a set's line of the plan, and whether the set as the
// zone holds it now is the one this dashboard wrote.
type dnsRecordChange struct {
	Type       dnsrecords.Type   `json:"type"`
	Name       string            `json:"name"`
	Action     dnsrecords.Action `json:"action"`
	Current    []string          `json:"current"`
	Want       []string          `json:"want"`
	CurrentTTL int               `json:"currentTtl,omitempty"`
	TTL        int               `json:"ttl,omitempty"`
	Proxied    bool              `json:"proxied,omitempty"`
	Detail     string            `json:"detail"`
	Ours       bool              `json:"ours"`
}

// dnsRecordManaged is one set as this dashboard wrote it.
type dnsRecordManaged struct {
	Type      dnsrecords.Type `json:"type"`
	Values    []string        `json:"values"`
	TTL       int             `json:"ttl"`
	Provider  string          `json:"provider"`
	Zone      string          `json:"zone"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedBy string          `json:"updatedBy"`
	UpdatedAt time.Time       `json:"updatedAt"`
	// Current says the zone still holds what was written.
	Current bool `json:"current"`
}

// dnsRecordWriteRequest is POST's body, all of it optional.
type dnsRecordWriteRequest struct {
	// TTL is the time to live to write; the provider's floor and a day's
	// cap apply, and 300 seconds when absent.
	TTL int `json:"ttl,omitempty"`
	// Replace consents to changing a record this dashboard did not write,
	// which the dry run said it would.
	Replace bool `json:"replace,omitempty"`
}

// dnsRecordAnswer is a plan's working state beside what the client is
// shown: the target and the leaf's changes, which POST writes.
type dnsRecordAnswer struct {
	plan    dnsRecordPlan
	target  *dnsrecords.Target
	changes []dnsrecords.Change
}

// wantedRecords are the sets a name should hold: the tunnel's CNAME, or an
// A and an AAAA to the first public address of each family, as preflight
// compares them.
func (s *Server) wantedRecords(ctx context.Context, domain deploy.PlannedDomain) ([]dnsrecords.Set, string, string) {
	hostname := dnsrecords.Canonical(domain.Hostname)
	if domain.Tunnel {
		connection, _ := s.tunnelConnection(ctx)
		if connection == nil {
			return nil, "tunnel_not_connected", hostname + " is served through Cloudflare Tunnel, and no tunnel is connected: connect one under Proxy › Tunnel first, since its CNAME names the tunnel."
		}
		return []dnsrecords.Set{{
			Name: hostname, Type: dnsrecords.TypeCNAME, Values: []string{cftunnel.TunnelTarget(connection.TunnelID)},
			Proxied: true, Note: cftunnel.RecordComment,
		}}, "", ""
	}
	var v4, v6 string
	addresses := append([]string(nil), s.dnsRecordAddresses()...)
	sort.Strings(addresses)
	for _, address := range addresses {
		ip := net.ParseIP(address)
		switch {
		case ip == nil || !proxysvc.IsPublicAddress(ip):
		case ip.To4() != nil && v4 == "":
			v4 = ip.String()
		case ip.To4() == nil && v6 == "":
			v6 = ip.String()
		}
	}
	if v4 == "" && v6 == "" {
		return nil, "no_public_address", "This server has no public address of its own — on a provider that maps one in front of it (AWS, Google Cloud, Azure, Oracle) the record names the address the provider shows for this instance, which cannot be read from here. Create the record where the zone is kept."
	}
	note := "Just Dashboard: " + hostname + " served from this server"
	sets := []dnsrecords.Set{}
	if v4 != "" {
		sets = append(sets, dnsrecords.Set{Name: hostname, Type: dnsrecords.TypeA, Values: []string{v4}, Note: note})
	}
	if v6 != "" {
		sets = append(sets, dnsrecords.Set{Name: hostname, Type: dnsrecords.TypeAAAA, Values: []string{v6}, Note: note})
	}
	return sets, "", ""
}

// tunnelConnection is the connected tunnel, if any.
func (s *Server) tunnelConnection(ctx context.Context) (*cftunnel.Connection, bool) {
	if s.modules.tunnel == nil {
		return nil, false
	}
	return s.modules.tunnel.Connected(ctx)
}

// planDNSRecord is the one decision the dry run, the write and preflight
// share.
func (s *Server) planDNSRecord(ctx context.Context, domain deploy.PlannedDomain, ttl int) dnsRecordAnswer {
	hostname := dnsrecords.Canonical(domain.Hostname)
	answer := dnsRecordAnswer{plan: dnsRecordPlan{
		Hostname: hostname, Tunnel: domain.Tunnel, Want: []dnsrecords.Set{}, Candidates: []dnsrecords.Candidate{},
		Problems: []string{}, Changes: []dnsRecordChange{}, Managed: []dnsRecordManaged{},
	}}
	plan := &answer.plan
	refuse := func(code, message string) dnsRecordAnswer {
		plan.RefusalCode, plan.Refusal, plan.Summary = code, message, message
		plan.Writable = false
		return answer
	}
	plan.Managed = s.managedDNSRecords(ctx, hostname)
	want, code, message := s.wantedRecords(ctx, domain)
	if code != "" {
		return refuse(code, message)
	}
	// The TTL before a provider is known, as a record to make by hand
	// shows it; the provider's floor is applied once it is.
	for index := range want {
		switch {
		case want[index].Proxied:
			want[index].TTL = 1
		case ttl > 0:
			want[index].TTL = min(ttl, 86400)
		default:
			want[index].TTL = dnsrecords.DefaultTTL
		}
	}
	plan.Want = want
	providers, problems := s.dnsRecordProviders()
	plan.Problems = problems
	target, candidates, err := dnsrecords.Resolve(ctx, hostname, providers, s.dnsRecordHostedBy)
	plan.Candidates = candidates
	if err != nil {
		var refusal *dnsrecords.Refusal
		if errors.As(err, &refusal) {
			message := refusal.Message
			if len(problems) > 0 {
				message += " (" + strings.Join(problems, "; ") + ")"
			}
			return refuse(refusal.Code, message)
		}
		return refuse("provider_error", err.Error())
	}
	answer.target = &target
	plan.Provider, plan.ProviderKey = target.Provider.Name(), target.Provider.Key()
	plan.Zone, plan.Account = dnsrecords.Canonical(target.Zone.Name), target.Zone.Account
	if domain.Tunnel && target.Provider.Key() != "cloudflare" {
		return refuse("tunnel_zone", fmt.Sprintf(
			"%s is served through Cloudflare Tunnel, which only a proxied CNAME in a zone on Cloudflare reaches, and its zone %s is %s's here. Move the zone to Cloudflare, or serve the name at this server's own address.",
			hostname, plan.Zone, plan.Provider))
	}
	for index := range plan.Want {
		if plan.Want[index].Proxied {
			// Cloudflare's automatic TTL: its edge answers for the name.
			plan.Want[index].TTL = 1
		} else {
			plan.Want[index].TTL = dnsrecords.TTLFor(target.Provider, ttl)
		}
	}
	current, err := target.Provider.Read(ctx, target.Zone, hostname)
	if err != nil {
		return refuse("provider_error", err.Error())
	}
	answer.changes = dnsrecords.Diff(plan.Want, current, target.Provider.PerRecord())
	comments := map[dnsrecords.Type]string{}
	for _, set := range current {
		comments[set.Type] = set.Comment
	}
	managed := map[dnsrecords.Type]dnsRecordManaged{}
	for index, row := range plan.Managed {
		managed[row.Type] = row
		for _, set := range current {
			if set.Type == row.Type && dnsrecords.SameValues(row.Type, set.Values, row.Values) {
				plan.Managed[index].Current = true
			}
		}
	}
	for _, change := range answer.changes {
		row, wrote := managed[change.Type]
		ours := (wrote && len(change.Current) > 0 && dnsrecords.SameValues(change.Type, change.Current, row.Values)) ||
			(change.Type == dnsrecords.TypeCNAME && comments[change.Type] == cftunnel.RecordComment)
		plan.Changes = append(plan.Changes, dnsRecordChange{
			Type: change.Type, Name: change.Name, Action: change.Action, Current: change.Current, Want: change.Want,
			CurrentTTL: change.CurrentTTL, TTL: change.TTL, Proxied: change.Proxied, Detail: change.Detail, Ours: ours,
		})
		if change.Action == dnsrecords.ActionUpdate && !ours {
			plan.Foreign = true
		}
	}
	if blocked, ok := dnsrecords.Blocked(answer.changes); ok {
		return refuse("conflict", blocked.Detail)
	}
	pending := dnsrecords.Pending(answer.changes)
	plan.Ready = len(pending) == 0
	plan.Writable = !plan.Ready
	plan.Summary = dnsRecordSummary(*plan, pending)
	return answer
}

// dnsRecordSummary is the dry run's sentence.
func dnsRecordSummary(plan dnsRecordPlan, pending []dnsrecords.Change) string {
	where := plan.Zone + " at " + plan.Provider
	if plan.Account != "" && plan.Account != plan.Zone {
		where += " (" + plan.Account + ")"
	}
	var sentence string
	if len(pending) == 0 {
		target := "this server"
		if plan.Tunnel {
			target = "the tunnel"
		}
		sentence = fmt.Sprintf("%s's records in %s already point at %s.", plan.Hostname, where, target)
	} else {
		lines := make([]string, 0, len(pending))
		for _, change := range pending {
			lines = append(lines, strings.TrimSuffix(change.Detail, "."))
		}
		sentence = fmt.Sprintf("Writes into %s with the %s credentials saved under Certificates: %s.", where, plan.Provider, strings.Join(lines, "; "))
		if plan.Foreign {
			sentence += " It replaces a record this dashboard did not write."
		}
	}
	for _, change := range plan.Changes {
		if change.Action == dnsrecords.ActionStale {
			sentence += " " + change.Detail
		}
	}
	return sentence
}

// managedDNSRecords are the rows this dashboard keeps for a name.
func (s *Server) managedDNSRecords(ctx context.Context, hostname string) []dnsRecordManaged {
	out := []dnsRecordManaged{}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT record_type, record_values, ttl, provider, zone, created_by, created_at, updated_by, updated_at
		FROM deploy_dns_records WHERE hostname = ? ORDER BY record_type`, hostname)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var row dnsRecordManaged
		var values string
		var created, updated int64
		if rows.Scan(&row.Type, &values, &row.TTL, &row.Provider, &row.Zone, &row.CreatedBy, &created, &row.UpdatedBy, &updated) != nil {
			continue
		}
		_ = json.Unmarshal([]byte(values), &row.Values)
		if row.Values == nil {
			row.Values = []string{}
		}
		row.CreatedAt, row.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		out = append(out, row)
	}
	return out
}

// recordDNSWrite keeps what a write set.
func (s *Server) recordDNSWrite(ctx context.Context, projectID, environmentID int64, plan dnsRecordPlan, change dnsrecords.Change, actor string) error {
	values, _ := json.Marshal(change.Want)
	now := time.Now().UTC().Unix()
	proxied := 0
	if change.Proxied {
		proxied = 1
	}
	_, err := s.Store.DB.ExecContext(ctx, `INSERT INTO deploy_dns_records(hostname, record_type, project_id, environment_id, record_values, ttl, proxied, provider, zone, created_by, created_at, updated_by, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hostname, record_type) DO UPDATE SET project_id = excluded.project_id, environment_id = excluded.environment_id,
		  record_values = excluded.record_values, ttl = excluded.ttl, proxied = excluded.proxied, provider = excluded.provider,
		  zone = excluded.zone, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		plan.Hostname, string(change.Type), projectID, environmentID, string(values), change.TTL, proxied,
		plan.ProviderKey, plan.Zone, actor, now, actor, now)
	return err
}

// deploymentDomain finds the environment of a deployment whose saved
// configuration lists the name, production first.
func (s *Server) deploymentDomain(ctx context.Context, projectID int64, hostname string) (deploy.EnvironmentSummary, deploy.PlannedDomain, error) {
	environments, err := s.modules.deployRuns.ProjectEnvironments(ctx, projectID)
	if err != nil {
		return deploy.EnvironmentSummary{}, deploy.PlannedDomain{}, mapDeployError(err)
	}
	for _, environment := range environments {
		configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(ctx, projectID, environment.ID)
		if err != nil {
			continue
		}
		for _, domain := range configuration.Domains {
			if dnsrecords.Canonical(domain.Hostname) == hostname {
				return environment, domain, nil
			}
		}
	}
	return deploy.EnvironmentSummary{}, deploy.PlannedDomain{}, httpx.Err(http.StatusNotFound, "domain_not_found",
		hostname+" is not a saved domain of this deployment's environments: add it under Domains and save first")
}

func dnsRecordHostname(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "domain")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", httpx.BadRequest("%q is not a domain name", raw)
	}
	hostname := dnsrecords.Canonical(decoded)
	if hostname == "" || len(hostname) > 253 || strings.ContainsAny(hostname, " /\\") {
		return "", httpx.BadRequest("%q is not a domain name", raw)
	}
	return hostname, nil
}

func (s *Server) mountDeploymentDNSRecordRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/domains/{domain}/dns-record", s.handle(s.handleDeploymentDNSRecord))
	r.Method(http.MethodPost, "/{id}/domains/{domain}/dns-record", s.handle(s.handleDeploymentDNSRecordWrite))
}

func (s *Server) handleDeploymentDNSRecord(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	hostname, err := dnsRecordHostname(r)
	if err != nil {
		return err
	}
	ttl := 0
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if ttl, err = strconv.Atoi(raw); err != nil || ttl < 0 {
			return httpx.BadRequest("ttl is a number of seconds")
		}
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	environment, domain, err := s.deploymentDomain(ctx, projectID, hostname)
	if err != nil {
		return err
	}
	answer := s.planDNSRecord(ctx, domain, ttl)
	answer.plan.EnvironmentID, answer.plan.Environment = environment.ID, environment.Name
	httpx.JSON(w, http.StatusOK, answer.plan)
	return nil
}

// dnsRecordRefusals are the plan's refusals as a POST answers them.
var dnsRecordRefusals = map[string]struct {
	status int
	code   string
}{
	"no_public_address":    {http.StatusConflict, "no_public_address"},
	"tunnel_not_connected": {http.StatusConflict, "tunnel_not_connected"},
	"no_credentials":       {http.StatusConflict, "dns_record_unwritable"},
	"no_zone":              {http.StatusConflict, "dns_record_unwritable"},
	"ambiguous":            {http.StatusConflict, "dns_record_unwritable"},
	"tunnel_zone":          {http.StatusConflict, "dns_record_tunnel_zone"},
	"conflict":             {http.StatusConflict, "dns_record_conflict"},
	"provider_error":       {http.StatusBadGateway, "dns_provider_error"},
}

func (s *Server) handleDeploymentDNSRecordWrite(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	hostname, err := dnsRecordHostname(r)
	if err != nil {
		return err
	}
	var req dnsRecordWriteRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			return err
		}
	}
	if req.TTL < 0 {
		return httpx.BadRequest("ttl is a number of seconds")
	}
	detail := map[string]any{"deploymentId": projectID, "hostname": hostname}
	httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	environment, domain, err := s.deploymentDomain(ctx, projectID, hostname)
	if err != nil {
		return err
	}
	detail["environmentId"] = environment.ID
	answer := s.planDNSRecord(ctx, domain, req.TTL)
	plan := &answer.plan
	plan.EnvironmentID, plan.Environment = environment.ID, environment.Name
	if plan.ProviderKey != "" {
		detail["provider"], detail["zone"] = plan.ProviderKey, plan.Zone
	}
	httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
	if plan.RefusalCode != "" {
		detail["result"] = plan.RefusalCode
		httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
		refusal := dnsRecordRefusals[plan.RefusalCode]
		if refusal.code == "" {
			refusal.status, refusal.code = http.StatusConflict, "dns_record_unwritable"
		}
		return httpx.Err(refusal.status, refusal.code, plan.Refusal)
	}
	if plan.Ready {
		detail["result"] = "present"
		httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
		httpx.JSON(w, http.StatusOK, plan)
		return nil
	}
	if plan.Foreign && !req.Replace {
		detail["result"] = "foreign"
		httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
		return httpx.Err(http.StatusConflict, "dns_record_foreign",
			plan.Summary+" Send replace: true to replace it, as the dry run says.")
	}
	written, applyErr := dnsrecords.Apply(ctx, answer.target.Provider, answer.target.Zone, answer.changes)
	actor := httpx.MustPrincipal(r).Username()
	records := []string{}
	for _, change := range written {
		verb := "created "
		if change.Action == dnsrecords.ActionUpdate {
			verb = "changed "
		}
		records = append(records, verb+dnsrecords.Describe(dnsrecords.Set{Name: change.Name, Type: change.Type, Values: change.Want, Proxied: change.Proxied}))
		if err := s.recordDNSWrite(ctx, projectID, environment.ID, *plan, change, actor); err != nil {
			s.Log.Warn("a written DNS record could not be kept", "hostname", hostname, "type", change.Type, "err", err)
		}
	}
	detail["records"], detail["replace"] = records, req.Replace
	if applyErr != nil {
		detail["result"], detail["error"] = "failed", applyErr.Error()
		httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
		message := applyErr.Error()
		if len(records) > 0 {
			message += " Written before it: " + strings.Join(records, "; ") + "."
		}
		return httpx.Err(http.StatusBadGateway, "dns_provider_error", message)
	}
	detail["result"] = "written"
	httpx.SetAudit(r, "deploy.domain.record", hostname, detail)
	plan.Written = written
	for index := range plan.Changes {
		for _, change := range written {
			if plan.Changes[index].Type == change.Type {
				plan.Changes[index].Current, plan.Changes[index].CurrentTTL = change.Want, change.TTL
				plan.Changes[index].Action, plan.Changes[index].Ours = dnsrecords.ActionKeep, true
			}
		}
	}
	plan.Managed = s.managedDNSRecords(ctx, hostname)
	for index := range plan.Managed {
		plan.Managed[index].Current = true
	}
	plan.Ready, plan.Writable, plan.Foreign = true, false, false
	plan.Summary = fmt.Sprintf("Written into %s at %s: %s.", plan.Zone, plan.Provider, strings.Join(records, "; "))
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

// dnsRecordObserver is preflight's half: for a planned name whose lookup
// does not reach where it should, the records it needs and whether the
// saved credentials can write them. A server with no public address, or a
// tunnel's name with no tunnel, has nothing to add to what preflight
// already says.
func (s *Server) dnsRecordObserver() deploy.DNSRecordObserver {
	return func(ctx context.Context, domain deploy.PlannedDomain) (deploy.DNSRecordObservation, bool) {
		answer := s.planDNSRecord(ctx, domain, 0)
		plan := answer.plan
		if plan.RefusalCode == "no_public_address" || plan.RefusalCode == "tunnel_not_connected" || len(plan.Want) == 0 {
			return deploy.DNSRecordObservation{}, false
		}
		observed := deploy.DNSRecordObservation{
			Records: []string{}, Missing: true, Writable: plan.Writable, Provider: plan.Provider, Zone: plan.Zone,
			Present: plan.Ready, Detail: plan.Summary,
		}
		for _, set := range plan.Want {
			observed.Records = append(observed.Records, dnsrecords.Describe(set))
		}
		for _, change := range plan.Changes {
			if len(change.Current) > 0 {
				observed.Missing = false
			}
		}
		if plan.RefusalCode == "conflict" {
			observed.Missing = false
		}
		return observed, true
	}
}
