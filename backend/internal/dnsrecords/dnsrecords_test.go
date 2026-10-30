package dnsrecords

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The zone a name is written in is the longest one the credentials reach:
// a sub-zone delegated elsewhere is the one whose servers answer for it.
func TestZoneForChoosesTheLongestZoneThatHoldsTheName(t *testing.T) {
	zones := []Zone{
		{ID: "1", Name: "example.com"}, {ID: "2", Name: "preview.example.com."},
		{ID: "3", Name: "ample.com"}, {ID: "4", Name: "Other.ORG"},
	}
	for name, want := range map[string]string{
		"app.example.com":           "1",
		"example.com":               "1",
		"APP.Example.COM.":          "1",
		"pr-7.preview.example.com":  "2",
		"*.preview.example.com":     "2",
		"preview.example.com":       "2",
		"deep.a.b.example.com":      "1",
		"www.other.org":             "4",
		"notexample.com":            "",
		"example.co":                "",
		"previewexample.com":        "",
		"app.preview.example.com.x": "",
	} {
		zone, found := ZoneFor(name, zones)
		if got := map[bool]string{true: zone.ID, false: ""}[found]; got != want {
			t.Errorf("ZoneFor(%q) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[[2]string]string{
		{"example.com", "example.com"}:            "@",
		{"app.example.com", "example.com"}:        "app",
		{"*.preview.example.com", "example.com"}:  "*.preview",
		{"A.Preview.Example.com.", "example.com"}: "a.preview",
	} {
		if got := Relative(name[0], name[1]); got != want {
			t.Errorf("Relative(%q, %q) = %q, want %q", name[0], name[1], got, want)
		}
	}
	if got := absolute("@", "example.com"); got != "example.com" {
		t.Errorf("absolute(@) = %q", got)
	}
	if got := absolute("www", "example.com"); got != "www.example.com" {
		t.Errorf("absolute(www) = %q", got)
	}
}

// A provider's floor, the default, the day's cap, and Linode's steps.
func TestTTLDefaultsAndFloors(t *testing.T) {
	cases := []struct {
		provider  Provider
		requested int
		want      int
	}{
		{&cloudflare{}, 0, DefaultTTL},
		{&cloudflare{}, 10, 60},
		{&cloudflare{}, 3600, 3600},
		{&cloudflare{}, 999999, 86400},
		{&gandi{}, 0, 300},
		{&gandi{}, 60, 300},
		{&digitalOcean{}, 10, 30},
		{&route53{}, 5, 5},
		{&linode{}, 0, 300},
		{&linode{}, 400, 3600},
		{&linode{}, 31, 120},
		{&linode{}, 86400, 86400},
		{&ovh{}, 1, 60},
		{&rfc2136{}, -5, DefaultTTL},
	}
	for _, c := range cases {
		if got := TTLFor(c.provider, c.requested); got != c.want {
			t.Errorf("%s TTLFor(%d) = %d, want %d", c.provider.Key(), c.requested, got, c.want)
		}
	}
}

func actions(changes []Change) string {
	parts := []string{}
	for _, change := range changes {
		parts = append(parts, string(change.Type)+":"+string(change.Action))
	}
	return strings.Join(parts, " ")
}

// What a write would do, set by set, and what it refuses to.
func TestDiffSaysWhatAWriteDoesAndWhatItWillNot(t *testing.T) {
	name := "app.example.com"
	a := Set{Name: name, Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300}
	aaaa := Set{Name: name, Type: TypeAAAA, Values: []string{"2001:db8::7"}, TTL: 300}
	tunnel := Set{Name: name, Type: TypeCNAME, Values: []string{"abc.cfargotunnel.com"}, TTL: 300, Proxied: true}
	cases := []struct {
		label     string
		want      []Set
		current   []Set
		perRecord bool
		actions   string
		detail    string
	}{
		{"nothing there", []Set{a, aaaa}, nil, true, "A:create AAAA:create", "Create A app.example.com → 203.0.113.7."},
		{"already right, in another order and case", []Set{a}, []Set{{Name: name, Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 3600}}, true, "A:keep", "already right"},
		{"another address", []Set{a}, []Set{{Name: name, Type: TypeA, Values: []string{"198.51.100.4"}, TTL: 300, ids: []string{"r1"}}}, true, "A:update",
			"Change A app.example.com from 198.51.100.4 to 203.0.113.7."},
		{"a CNAME where an address is wanted", []Set{a, aaaa}, []Set{{Name: name, Type: TypeCNAME, Values: []string{"elsewhere.example.net"}}}, true,
			"A:conflict AAAA:conflict", "has a CNAME to elsewhere.example.net"},
		{"addresses where the tunnel's CNAME is wanted", []Set{tunnel}, []Set{{Name: name, Type: TypeA, Values: []string{"198.51.100.4"}}}, true,
			"CNAME:conflict", "a CNAME cannot sit beside another record"},
		{"two records where one value is written, one record per value", []Set{a}, []Set{{Name: name, Type: TypeA, Values: []string{"198.51.100.4", "198.51.100.5"}, ids: []string{"1", "2"}}}, true,
			"A:conflict", "deleting the rest"},
		{"two values replaced by one, a set provider", []Set{a}, []Set{{Name: name, Type: TypeA, Values: []string{"198.51.100.4", "198.51.100.5"}}}, false,
			"A:update", "from 198.51.100.4, 198.51.100.5 to 203.0.113.7"},
		{"an alias", []Set{a}, []Set{{Name: name, Type: TypeA, Values: []string{"alias to lb.example.net"}, Unwritable: "it is an alias"}}, false,
			"A:conflict", "cannot be replaced from here: it is an alias"},
		{"an AAAA elsewhere on a server with no IPv6", []Set{a}, []Set{{Name: name, Type: TypeAAAA, Values: []string{"2001:db8::99"}}}, true,
			"A:create AAAA:stale", "no public IPv6 address"},
		{"the tunnel's CNAME, not proxied yet", []Set{tunnel}, []Set{{Name: name, Type: TypeCNAME, Values: []string{"ABC.cfargotunnel.com."}, ids: []string{"r"}}}, true,
			"CNAME:update", "behind Cloudflare's edge"},
		{"the tunnel's CNAME, right", []Set{tunnel}, []Set{{Name: name, Type: TypeCNAME, Values: []string{"abc.cfargotunnel.com"}, Proxied: true}}, true,
			"CNAME:keep", "already right"},
	}
	for _, c := range cases {
		changes := Diff(c.want, c.current, c.perRecord)
		if got := actions(changes); got != c.actions {
			t.Errorf("%s: actions %q, want %q", c.label, got, c.actions)
		}
		joined := ""
		for _, change := range changes {
			joined += change.Detail + "\n"
		}
		if !strings.Contains(joined, c.detail) {
			t.Errorf("%s: detail %q does not say %q", c.label, joined, c.detail)
		}
	}
	// Blocked is the first conflict, and Apply writes nothing while one
	// stands.
	changes := Diff([]Set{a}, []Set{{Name: name, Type: TypeCNAME, Values: []string{"x.example.net"}}}, true)
	if _, blocked := Blocked(changes); !blocked {
		t.Fatal("a CNAME where an address is wanted did not block the plan")
	}
	recorder := &recordingProvider{}
	if written, err := Apply(context.Background(), recorder, Zone{Name: "example.com"}, changes); err == nil || len(written) != 0 || len(recorder.writes) != 0 {
		t.Fatalf("a blocked plan wrote %v (%v)", recorder.writes, err)
	}
	if len(Pending(Diff([]Set{a}, []Set{a}, true))) != 0 {
		t.Fatal("a set already right is pending")
	}
}

// Apply writes in order and stops at the provider's first refusal.
func TestApplyStopsAtTheFirstRefusal(t *testing.T) {
	name := "app.example.com"
	changes := Diff([]Set{
		{Name: name, Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300},
		{Name: name, Type: TypeAAAA, Values: []string{"2001:db8::7"}, TTL: 300},
	}, nil, false)
	recorder := &recordingProvider{refuse: TypeAAAA}
	written, err := Apply(context.Background(), recorder, Zone{Name: "example.com"}, changes)
	var refused *Error
	if !errors.As(err, &refused) || len(written) != 1 || written[0].Type != TypeA || !slices.Equal(recorder.writes, []string{"A create"}) {
		t.Fatalf("written %v, writes %v, err %v", written, recorder.writes, err)
	}
}

type recordingProvider struct {
	zones  []Zone
	err    error
	writes []string
	refuse Type
	key    string
}

func (r *recordingProvider) Key() string {
	if r.key == "" {
		return "fake"
	}
	return r.key
}
func (r *recordingProvider) Name() string    { return "Fake " + r.Key() }
func (r *recordingProvider) PerRecord() bool { return false }
func (r *recordingProvider) MinTTL() int     { return 1 }
func (r *recordingProvider) Zones(context.Context, string) ([]Zone, error) {
	return r.zones, r.err
}
func (r *recordingProvider) Read(context.Context, Zone, string) ([]Set, error) { return nil, nil }
func (r *recordingProvider) Write(_ context.Context, _ Zone, current *Set, want Set) error {
	if want.Type == r.refuse {
		return &Error{Provider: r.Name(), Operation: "write", Status: 400, Message: "no"}
	}
	verb := "create"
	if current != nil {
		verb = "update"
	}
	r.writes = append(r.writes, string(want.Type)+" "+verb)
	return nil
}

// Several providers asked at once: the longest zone wins, a tie goes to
// the provider the nameservers name, and otherwise each refusal says why.
func TestResolveChoosesTheProviderWhoseZoneHoldsTheName(t *testing.T) {
	ctx := context.Background()
	cloudflare := &recordingProvider{key: "cloudflare", zones: []Zone{{ID: "c1", Name: "example.com"}}}
	route53 := &recordingProvider{key: "route53", zones: []Zone{{ID: "r1", Name: "preview.example.com"}}}
	broken := &recordingProvider{key: "linode", err: errors.New("Linode refused to list the domains: Invalid Token")}

	target, candidates, err := Resolve(ctx, "pr-7.preview.example.com", []Provider{cloudflare, route53, broken}, nil)
	if err != nil || target.Provider.Key() != "route53" || target.Zone.ID != "r1" || len(candidates) != 3 || candidates[2].Error == "" {
		t.Fatalf("delegated sub-zone: %+v %+v %v", target, candidates, err)
	}
	if target, _, err := Resolve(ctx, "app.example.com", []Provider{cloudflare, route53}, nil); err != nil || target.Provider.Key() != "cloudflare" {
		t.Fatalf("apex zone: %+v %v", target, err)
	}

	moving := &recordingProvider{key: "digitalocean", zones: []Zone{{ID: "d1", Name: "example.com"}}}
	if target, _, err := Resolve(ctx, "app.example.com", []Provider{cloudflare, moving},
		func(context.Context, string) string { return "digitalocean" }); err != nil || target.Provider.Key() != "digitalocean" {
		t.Fatalf("a tie the nameservers settle: %+v %v", target, err)
	}
	_, _, err = Resolve(ctx, "app.example.com", []Provider{cloudflare, moving}, func(context.Context, string) string { return "" })
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusedAmbiguous || !strings.Contains(refusal.Message, "Fake cloudflare and Fake digitalocean") {
		t.Fatalf("a tie nothing settles: %v", err)
	}

	_, _, err = Resolve(ctx, "shop.example.org", []Provider{cloudflare, broken}, nil)
	if !errors.As(err, &refusal) || refusal.Code != RefusedNoZone || !strings.Contains(refusal.Message, "Invalid Token") ||
		!strings.Contains(refusal.Message, "Fake cloudflare reaches no zone holding it") || !strings.Contains(refusal.Message, CertificatesPage) {
		t.Fatalf("no zone: %v", err)
	}
	_, _, err = Resolve(ctx, "shop.example.org", nil, nil)
	if !errors.As(err, &refusal) || refusal.Code != RefusedNoCredentials {
		t.Fatalf("nothing saved: %v", err)
	}
}

// New refuses credentials it cannot write with, in words.
func TestNewRefusesCredentialsItCannotUse(t *testing.T) {
	for key, credentials := range map[string]string{
		"cloudflare":   "dns_cloudflare_email = a@example.com",
		"route53":      "[default]\n",
		"digitalocean": "# nothing",
		"linode":       "dns_linode_key = k\ndns_linode_version = 3",
		"ovh":          "dns_ovh_endpoint = ovh-mars\ndns_ovh_application_key = a\ndns_ovh_application_secret = b\ndns_ovh_consumer_key = c",
		"gandi":        "dns_gandi_sharing_id = s",
		"google":       `{"type":"authorized_user"}`,
		"rfc2136":      "dns_rfc2136_server = 192.0.2.1\ndns_rfc2136_name = k.\ndns_rfc2136_secret = c2VjcmV0\ndns_rfc2136_algorithm = HMAC-CRC32",
		"namecheap":    "token = x",
	} {
		if _, err := New(key, credentials, Options{}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", key, err)
		}
	}
}

// exercise is one provider against its fake, the way the route uses it:
// find the zone, read, create A and AAAA, read them back, change the A,
// create a CNAME on another name, and refuse an address where the CNAME is.
func exercise(t *testing.T, provider Provider, name, zone, cname string, proxied bool) {
	t.Helper()
	ctx := context.Background()
	target, _, err := Resolve(ctx, name, []Provider{provider}, nil)
	if err != nil || Canonical(target.Zone.Name) != zone {
		t.Fatalf("resolve %s: %+v %v", name, target.Zone, err)
	}
	current, err := provider.Read(ctx, target.Zone, name)
	if err != nil || len(current) != 0 {
		t.Fatalf("read before: %+v %v", current, err)
	}
	ttl := TTLFor(provider, 0)
	want := []Set{
		{Name: name, Type: TypeA, Values: []string{"203.0.113.7"}, TTL: ttl, Note: "Just Dashboard"},
		{Name: name, Type: TypeAAAA, Values: []string{"2001:db8::7"}, TTL: ttl, Note: "Just Dashboard"},
	}
	written, err := Apply(ctx, provider, target.Zone, Diff(want, current, provider.PerRecord()))
	if err != nil || len(written) != 2 {
		t.Fatalf("create: %v %v", written, err)
	}
	current, err = provider.Read(ctx, target.Zone, name)
	if err != nil || actions(Diff(want, current, provider.PerRecord())) != "A:keep AAAA:keep" {
		t.Fatalf("read after create: %+v %v", current, err)
	}
	for _, set := range current {
		if set.TTL != ttl {
			t.Errorf("%s was written with TTL %d, want %d", set.Type, set.TTL, ttl)
		}
	}
	want[0].Values = []string{"203.0.113.8"}
	changes := Diff(want, current, provider.PerRecord())
	if actions(changes) != "A:update AAAA:keep" {
		t.Fatalf("plan for a moved server: %s", actions(changes))
	}
	if _, err := Apply(ctx, provider, target.Zone, changes); err != nil {
		t.Fatalf("update: %v", err)
	}
	current, err = provider.Read(ctx, target.Zone, name)
	if err != nil || actions(Diff(want, current, provider.PerRecord())) != "A:keep AAAA:keep" {
		t.Fatalf("read after update: %+v %v", current, err)
	}

	tunnel := []Set{{Name: cname, Type: TypeCNAME, Values: []string{"abc.cfargotunnel.com"}, TTL: ttl, Proxied: proxied}}
	held, err := provider.Read(ctx, target.Zone, cname)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, provider, target.Zone, Diff(tunnel, held, provider.PerRecord())); err != nil {
		t.Fatalf("create the CNAME: %v", err)
	}
	held, err = provider.Read(ctx, target.Zone, cname)
	if err != nil || actions(Diff(tunnel, held, provider.PerRecord())) != "CNAME:keep" {
		t.Fatalf("read the CNAME: %+v %v", held, err)
	}
	address := Diff(want[:1], held, provider.PerRecord())
	if _, err := Apply(ctx, provider, target.Zone, address); err == nil || !strings.Contains(err.Error(), "has a CNAME to abc.cfargotunnel.com") {
		t.Fatalf("an address where the CNAME is: %v", err)
	}
}

// refusedInWords checks a provider's refusal carries its own words.
func refusedInWords(t *testing.T, err error, words ...string) {
	t.Helper()
	var refused *Error
	if !errors.As(err, &refused) {
		t.Fatalf("not the provider's refusal: %v", err)
	}
	for _, word := range words {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("%q does not say %q", err.Error(), word)
		}
	}
}
