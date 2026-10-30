package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/cftunnel"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dnsrecords"
)

// fakeZoneProvider is a DNS provider kept in memory: the routes' half of
// dnsrecords is what is tested here, each provider's API is tested against
// its own fake in that package.
type fakeZoneProvider struct {
	mu       sync.Mutex
	key      string
	zones    []dnsrecords.Zone
	sets     map[string]dnsrecords.Set // name + " " + type
	refuse   bool
	writes   int
	zonesErr error
}

func newFakeZoneProvider(key string, zones ...string) *fakeZoneProvider {
	provider := &fakeZoneProvider{key: key, sets: map[string]dnsrecords.Set{}}
	for _, zone := range zones {
		provider.zones = append(provider.zones, dnsrecords.Zone{ID: "id-" + zone, Name: zone})
	}
	return provider
}

func (f *fakeZoneProvider) Key() string     { return f.key }
func (f *fakeZoneProvider) Name() string    { return "Fake " + f.key }
func (f *fakeZoneProvider) PerRecord() bool { return false }
func (f *fakeZoneProvider) MinTTL() int     { return 60 }
func (f *fakeZoneProvider) Zones(context.Context, string) ([]dnsrecords.Zone, error) {
	return f.zones, f.zonesErr
}

func (f *fakeZoneProvider) Read(_ context.Context, _ dnsrecords.Zone, name string) ([]dnsrecords.Set, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []dnsrecords.Set{}
	for _, kind := range dnsrecords.Types {
		if set, ok := f.sets[name+" "+string(kind)]; ok {
			out = append(out, set)
		}
	}
	return out, nil
}

func (f *fakeZoneProvider) Write(_ context.Context, _ dnsrecords.Zone, _ *dnsrecords.Set, want dnsrecords.Set) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return &dnsrecords.Error{Provider: f.Name(), Operation: "create " + string(want.Type) + " " + want.Name, Status: 403, Message: "the token may not edit this zone"}
	}
	f.writes++
	want.Comment = want.Note
	f.sets[want.Name+" "+string(want.Type)] = want
	return nil
}

func (f *fakeZoneProvider) put(name string, kind dnsrecords.Type, values ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets[name+" "+string(kind)] = dnsrecords.Set{Name: name, Type: kind, Values: values, TTL: 3600}
}

func (f *fakeZoneProvider) values(name string, kind dnsrecords.Type) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets[name+" "+string(kind)].Values
}

// dnsRecordBench is a key bench with a deployment whose saved
// configuration lists domains, and the DNS record hooks pointed at fakes.
type dnsRecordBench struct {
	*keyBench
	projectID, environmentID int64
	providers                []dnsrecords.Provider
	addresses                []string
}

func newDNSRecordBench(t *testing.T, domains ...deploy.PlannedDomain) *dnsRecordBench {
	t.Helper()
	b := &dnsRecordBench{keyBench: newKeyBench(t), addresses: []string{"203.0.113.7", "2001:db8::7", "10.0.0.4"}}
	b.projectID, b.environmentID, _ = insertDeploymentConfigurationAPI(t, b.s)
	for _, domain := range domains {
		config, _ := json.Marshal(domain)
		if _, err := b.s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
			VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, ?, ?)`, b.environmentID, domain.Hostname, string(config), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	b.s.dnsRecords = dnsRecordHooks{
		providers: func() ([]dnsrecords.Provider, []string) { return b.providers, nil },
		addresses: func() []string { return b.addresses },
		hostedBy:  func(context.Context, string) string { return "" },
	}
	return b
}

func (b *dnsRecordBench) path(hostname string) string {
	return fmt.Sprintf("/api/v1/deploy/%d/domains/%s/dns-record", b.projectID, hostname)
}

func (b *dnsRecordBench) plan(method, hostname string, body any) (dnsRecordPlan, int, string) {
	b.t.Helper()
	w := b.inSession(method, b.path(hostname), body)
	var plan dnsRecordPlan
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
			b.t.Fatal(err)
		}
	}
	return plan, w.Code, w.Body.String()
}

func changeActions(plan dnsRecordPlan) string {
	parts := []string{}
	for _, change := range plan.Changes {
		part := string(change.Type) + ":" + string(change.Action)
		if change.Ours {
			part += "(ours)"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// The dry run says what would be written, where and through whom; the
// write creates it, keeps who did it, and a second dry run tells the record
// as this dashboard's. A record changed by someone else is replaced only
// when asked, and every write is audited.
func TestDeploymentDNSRecordIsPlannedWrittenAndRemembered(t *testing.T) {
	b := newDNSRecordBench(t,
		deploy.PlannedDomain{Hostname: "app.example.com", HTTPS: true},
		deploy.PlannedDomain{Hostname: "www.example.com", HTTPS: true})
	cloudflare := newFakeZoneProvider("cloudflare", "example.com", "example.net")
	route53 := newFakeZoneProvider("route53", "other.org")
	b.providers = []dnsrecords.Provider{route53, cloudflare}

	plan, code, body := b.plan(http.MethodGet, "app.example.com", nil)
	if code != http.StatusOK || plan.Provider != "Fake cloudflare" || plan.ProviderKey != "cloudflare" || plan.Zone != "example.com" ||
		!plan.Writable || plan.Ready || plan.Foreign || changeActions(plan) != "A:create AAAA:create" || plan.EnvironmentID != b.environmentID ||
		!strings.Contains(plan.Summary, "Writes into example.com at Fake cloudflare with the Fake cloudflare credentials saved under Certificates: Create A app.example.com → 203.0.113.7") {
		t.Fatalf("dry run = %d %s", code, body)
	}
	if len(plan.Candidates) != 2 || plan.Want[0].TTL != dnsrecords.DefaultTTL || len(plan.Want) != 2 || plan.Want[1].Values[0] != "2001:db8::7" {
		t.Fatalf("dry run's candidates and records = %+v %+v", plan.Candidates, plan.Want)
	}
	if cloudflare.writes != 0 {
		t.Fatal("the dry run wrote")
	}

	plan, code, body = b.plan(http.MethodPost, "app.example.com", nil)
	if code != http.StatusOK || !plan.Ready || len(plan.Written) != 2 || changeActions(plan) != "A:keep(ours) AAAA:keep(ours)" ||
		len(plan.Managed) != 2 || plan.Managed[0].CreatedBy != "tester" || !plan.Managed[0].Current {
		t.Fatalf("write = %d %s", code, body)
	}
	if got := cloudflare.values("app.example.com", dnsrecords.TypeA); !slices.Equal(got, []string{"203.0.113.7"}) {
		t.Fatalf("the zone holds A %v", got)
	}
	entry := b.lastAudit("deploy.domain.record")
	if entry.Target != "app.example.com" || !entry.Success || !strings.Contains(entry.Detail, `"result":"written"`) ||
		!strings.Contains(entry.Detail, `"provider":"cloudflare"`) || !strings.Contains(entry.Detail, "created A app.example.com → 203.0.113.7") {
		t.Fatalf("audited as %+v", entry)
	}

	plan, _, body = b.plan(http.MethodGet, "app.example.com", nil)
	if !plan.Ready || plan.Writable || changeActions(plan) != "A:keep(ours) AAAA:keep(ours)" || !strings.Contains(plan.Summary, "already point at this server") {
		t.Fatalf("after the write = %s", body)
	}
	if _, code, body = b.plan(http.MethodPost, "app.example.com", nil); code != http.StatusOK || cloudflare.writes != 2 {
		t.Fatalf("a second write = %d %s after %d writes", code, body, cloudflare.writes)
	}
	if entry := b.lastAudit("deploy.domain.record"); !strings.Contains(entry.Detail, `"result":"present"`) {
		t.Fatalf("nothing to write was audited as %+v", entry)
	}

	// Someone moves the A elsewhere: not this dashboard's any more, so it
	// is replaced only with replace.
	cloudflare.put("app.example.com", dnsrecords.TypeA, "198.51.100.9")
	plan, _, body = b.plan(http.MethodGet, "app.example.com", nil)
	if !plan.Foreign || changeActions(plan) != "A:update AAAA:keep(ours)" || !strings.Contains(plan.Summary, "It replaces a record this dashboard did not write.") ||
		plan.Managed[0].Current {
		t.Fatalf("a record changed elsewhere = %s", body)
	}
	w := b.inSession(http.MethodPost, b.path("app.example.com"), nil)
	if w.Code != http.StatusConflict || keyErrorCode(w) != "dns_record_foreign" || !slices.Equal(cloudflare.values("app.example.com", dnsrecords.TypeA), []string{"198.51.100.9"}) {
		t.Fatalf("a write without replace = %d %s", w.Code, w.Body.String())
	}
	if entry := b.lastAudit("deploy.domain.record"); entry.Success || !strings.Contains(entry.Detail, `"result":"foreign"`) {
		t.Fatalf("a refused write was audited as %+v", entry)
	}
	if plan, code, body = b.plan(http.MethodPost, "app.example.com", map[string]any{"replace": true, "ttl": 600}); code != http.StatusOK ||
		!slices.Equal(cloudflare.values("app.example.com", dnsrecords.TypeA), []string{"203.0.113.7"}) || plan.Managed[0].TTL != 600 {
		t.Fatalf("a write with replace = %d %s", code, body)
	}

	// A CNAME where the address is wanted is the operator's to remove.
	cloudflare.put("www.example.com", dnsrecords.TypeCNAME, "shop.example.net")
	plan, _, body = b.plan(http.MethodGet, "www.example.com", nil)
	if plan.Writable || plan.RefusalCode != "conflict" || !strings.Contains(plan.Refusal, "has a CNAME to shop.example.net") {
		t.Fatalf("a CNAME in the way = %s", body)
	}
	writes := cloudflare.writes
	if w := b.inSession(http.MethodPost, b.path("www.example.com"), nil); w.Code != http.StatusConflict || keyErrorCode(w) != "dns_record_conflict" || cloudflare.writes != writes {
		t.Fatalf("a write past a CNAME = %d %s", w.Code, w.Body.String())
	}

	// The provider's refusal, in its words.
	cloudflare.refuse = true
	cloudflare.mu.Lock()
	delete(cloudflare.sets, "www.example.com CNAME")
	cloudflare.mu.Unlock()
	if w := b.inSession(http.MethodPost, b.path("www.example.com"), nil); w.Code != http.StatusBadGateway || keyErrorCode(w) != "dns_provider_error" ||
		!strings.Contains(w.Body.String(), "the token may not edit this zone") {
		t.Fatalf("a refused write = %d %s", w.Code, w.Body.String())
	}
	if entry := b.lastAudit("deploy.domain.record"); entry.Success || !strings.Contains(entry.Detail, "the token may not edit this zone") {
		t.Fatalf("a provider's refusal was audited as %+v", entry)
	}

	// Only a saved domain of this deployment.
	if w := b.inSession(http.MethodGet, b.path("shop.example.com"), nil); w.Code != http.StatusNotFound || keyErrorCode(w) != "domain_not_found" {
		t.Fatalf("another name = %d %s", w.Code, w.Body.String())
	}
	if w := b.inSession(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/domains/app.example.com/dns-record", b.projectID+100), nil); w.Code != http.StatusNotFound {
		t.Fatalf("another deployment = %d %s", w.Code, w.Body.String())
	}
}

// What refuses a write: no saved credentials, none that reach the zone, a
// server with no public address, a tunnel's name whose zone is not on
// Cloudflare; and who is refused: a deploy key, and any role short of
// administrator.
func TestDeploymentDNSRecordRefusals(t *testing.T) {
	b := newDNSRecordBench(t,
		deploy.PlannedDomain{Hostname: "app.example.com", HTTPS: true},
		deploy.PlannedDomain{Hostname: "edge.example.com", HTTPS: true, Tunnel: true})

	plan, code, body := b.plan(http.MethodGet, "app.example.com", nil)
	if code != http.StatusOK || plan.Writable || plan.RefusalCode != dnsrecords.RefusedNoCredentials || !strings.Contains(plan.Refusal, dnsrecords.CertificatesPage) ||
		len(plan.Want) != 2 || plan.Want[0].TTL != dnsrecords.DefaultTTL {
		t.Fatalf("nothing saved = %d %s", code, body)
	}
	if w := b.inSession(http.MethodPost, b.path("app.example.com"), nil); w.Code != http.StatusConflict || keyErrorCode(w) != "dns_record_unwritable" {
		t.Fatalf("a write with nothing saved = %d %s", w.Code, w.Body.String())
	}

	elsewhere := newFakeZoneProvider("digitalocean", "example.org")
	broken := newFakeZoneProvider("linode")
	broken.zonesErr = fmt.Errorf("Linode refused to list the domains: Invalid Token")
	b.providers = []dnsrecords.Provider{elsewhere, broken}
	plan, _, body = b.plan(http.MethodGet, "app.example.com", nil)
	if plan.RefusalCode != dnsrecords.RefusedNoZone || !strings.Contains(plan.Refusal, "Fake digitalocean reaches no zone holding it") || !strings.Contains(plan.Refusal, "Invalid Token") {
		t.Fatalf("no saved credential reaches the zone = %s", body)
	}
	if w := b.inSession(http.MethodPost, b.path("app.example.com"), nil); w.Code != http.StatusConflict || keyErrorCode(w) != "dns_record_unwritable" || elsewhere.writes != 0 {
		t.Fatalf("a write no credential reaches = %d %s", w.Code, w.Body.String())
	}

	b.addresses = []string{"10.0.0.4", "100.89.1.2"}
	plan, _, body = b.plan(http.MethodGet, "app.example.com", nil)
	if plan.RefusalCode != "no_public_address" || len(plan.Want) != 0 {
		t.Fatalf("no public address = %s", body)
	}
	if w := b.inSession(http.MethodPost, b.path("app.example.com"), nil); w.Code != http.StatusConflict || keyErrorCode(w) != "no_public_address" {
		t.Fatalf("a write with no public address = %d %s", w.Code, w.Body.String())
	}

	// A tunnel's name wants the tunnel's CNAME, and only a zone on
	// Cloudflare can hold it proxied.
	plan, _, body = b.plan(http.MethodGet, "edge.example.com", nil)
	if plan.RefusalCode != "tunnel_not_connected" || !plan.Tunnel {
		t.Fatalf("a tunnel's name with no tunnel = %s", body)
	}
	sealed, err := b.s.Sealer.Seal("tunnel-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.s.Store.DB.Exec(`INSERT INTO proxy_cloudflare_tunnel(id, mode, tunnel_id, tunnel_name, account_id, token_enc, created_at, updated_at)
		VALUES(1, 'token', 'c0ffee00-1111-2222-3333-444455556666', 'production', 'acct', ?, 1, 1)`, sealed); err != nil {
		t.Fatal(err)
	}
	route53 := newFakeZoneProvider("route53", "example.com")
	b.providers = []dnsrecords.Provider{route53}
	plan, _, body = b.plan(http.MethodGet, "edge.example.com", nil)
	if plan.RefusalCode != "tunnel_zone" || len(plan.Want) != 1 || plan.Want[0].Type != dnsrecords.TypeCNAME ||
		plan.Want[0].Values[0] != "c0ffee00-1111-2222-3333-444455556666.cfargotunnel.com" || !plan.Want[0].Proxied ||
		!strings.Contains(plan.Refusal, "its zone example.com is Fake route53's here") {
		t.Fatalf("a tunnel's name in a zone not on Cloudflare = %s", body)
	}
	if w := b.inSession(http.MethodPost, b.path("edge.example.com"), nil); w.Code != http.StatusConflict || keyErrorCode(w) != "dns_record_tunnel_zone" || route53.writes != 0 {
		t.Fatalf("a tunnel's CNAME written outside Cloudflare = %d %s", w.Code, w.Body.String())
	}
	cloudflare := newFakeZoneProvider("cloudflare", "example.com")
	b.providers = []dnsrecords.Provider{cloudflare}
	plan, code, body = b.plan(http.MethodPost, "edge.example.com", nil)
	written := cloudflare.sets["edge.example.com CNAME"]
	if code != http.StatusOK || !written.Proxied || written.TTL != 1 || written.Comment != cftunnel.RecordComment ||
		!slices.Equal(written.Values, []string{"c0ffee00-1111-2222-3333-444455556666.cfargotunnel.com"}) {
		t.Fatalf("the tunnel's CNAME = %d %s %+v", code, body, written)
	}
	plan, _, _ = b.plan(http.MethodGet, "edge.example.com", nil)
	if !plan.Ready || changeActions(plan) != "CNAME:keep(ours)" || !strings.Contains(plan.Summary, "already point at the tunnel") {
		t.Fatalf("the tunnel's CNAME read back = %+v", plan)
	}

	// Who may: an administrator in a session, nobody else.
	for name, w := range map[string]interface {
		Bytes() []byte
	}{
		"a deploy key's dry run": b.call(b.deploy, http.MethodGet, b.path("app.example.com"), nil).Body,
		"a deploy key's write":   b.call(b.deploy, http.MethodPost, b.path("app.example.com"), "{}").Body,
	} {
		if !strings.Contains(string(w.Bytes()), `"session_required"`) || !strings.Contains(string(w.Bytes()), whyDNSRecord) {
			t.Errorf("%s = %s", name, w.Bytes())
		}
	}
	if w := b.call(b.ordinary, http.MethodPost, b.path("app.example.com"), "{}"); keyErrorCode(w) != "session_required" {
		t.Errorf("an ordinary key's write = %d %s", w.Code, w.Body.String())
	}
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		other := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "dns-"+string(role), role)}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if w := other.do(method, b.path("app.example.com"), "", nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d %s", role, method, w.Code, w.Body.String())
			}
		}
	}
}

// Preflight's half asks the same plan: the records, whether they can be
// written and by whom, nothing for a server it cannot name.
func TestDNSRecordObserverAnswersPreflightFromThePlan(t *testing.T) {
	b := newDNSRecordBench(t)
	observe := b.s.dnsRecordObserver()
	domain := deploy.PlannedDomain{Hostname: "app.example.com"}
	cloudflare := newFakeZoneProvider("cloudflare", "example.com")
	b.providers = []dnsrecords.Provider{cloudflare}
	observed, ok := observe(context.Background(), domain)
	if !ok || !observed.Writable || !observed.Missing || observed.Provider != "Fake cloudflare" || observed.Zone != "example.com" ||
		!slices.Equal(observed.Records, []string{"A app.example.com → 203.0.113.7", "AAAA app.example.com → 2001:db8::7"}) {
		t.Fatalf("writable = %+v %v", observed, ok)
	}
	cloudflare.put("app.example.com", dnsrecords.TypeA, "198.51.100.9")
	if observed, _ = observe(context.Background(), domain); observed.Missing || !observed.Writable {
		t.Fatalf("another server's address = %+v", observed)
	}
	b.providers = nil
	if observed, ok = observe(context.Background(), domain); !ok || observed.Writable || !strings.Contains(observed.Detail, "No DNS provider has credentials saved") {
		t.Fatalf("nothing saved = %+v %v", observed, ok)
	}
	b.addresses = nil
	if _, ok = observe(context.Background(), domain); ok {
		t.Fatal("a server with no public address named a record")
	}
	if _, ok = observe(context.Background(), deploy.PlannedDomain{Hostname: "edge.example.com", Tunnel: true}); ok {
		t.Fatal("a tunnel's name with no tunnel named a record")
	}
}
