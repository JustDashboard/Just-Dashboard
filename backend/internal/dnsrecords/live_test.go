package dnsrecords

import (
	"context"
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc/acmetest"
)

// What the RFC 2136 provider does to a real BIND, the one the DNS-01 live
// tests prove wildcards against (proxysvc/acmetest), granted the address
// types as well as TXT: the zone found from a name two labels under it, an
// A and an AAAA created and read back by BIND's own dig, the A changed in
// place, a CNAME on another name, a set that appeared since it was read
// refused by the UPDATE's prerequisite, a name outside the zone refused, and
// a wrong secret refused by BIND's signature check with nothing written.
func TestLiveRFC2136WritesRecordsIntoBIND(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to write records into an isolated BIND")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bind := acmetest.StartBIND(ctx, t, "jd-records.test", "A", "AAAA", "CNAME", "TXT")
	provider := mustProvider(t, "rfc2136", bind.Credentials, Options{})

	zones, err := provider.Zones(ctx, "deep.app.jd-records.test")
	if err != nil || len(zones) != 1 || zones[0].Name != "jd-records.test" {
		t.Fatalf("zone discovery = %+v %v", zones, err)
	}
	if _, err := provider.Zones(ctx, "app.elsewhere.test"); err == nil {
		t.Fatal("BIND answered for a zone it does not have")
	}
	zone := zones[0]
	name := "app.jd-records.test"
	want := []Set{
		{Name: name, Type: TypeA, Values: []string{"203.0.113.7"}, TTL: TTLFor(provider, 0)},
		{Name: name, Type: TypeAAAA, Values: []string{"2001:db8::7"}, TTL: TTLFor(provider, 0)},
	}
	held, err := provider.Read(ctx, zone, name)
	if err != nil || len(held) != 0 {
		t.Fatalf("before: %+v %v", held, err)
	}
	if written, err := Apply(ctx, provider, zone, Diff(want, held, provider.PerRecord())); err != nil || len(written) != 2 {
		t.Fatalf("create: %v %v", written, err)
	}
	if got := bind.Dig(t, name, "A"); !slices.Equal(got, []string{"203.0.113.7"}) {
		t.Fatalf("BIND serves A %v", got)
	}
	if got := bind.Dig(t, name, "AAAA"); !slices.Equal(got, []string{"2001:db8::7"}) {
		t.Fatalf("BIND serves AAAA %v", got)
	}
	held, err = provider.Read(ctx, zone, name)
	if err != nil || actions(Diff(want, held, false)) != "A:keep AAAA:keep" || held[0].TTL != DefaultTTL {
		t.Fatalf("read back: %+v %v", held, err)
	}

	want[0].Values = []string{"203.0.113.8"}
	if _, err := Apply(ctx, provider, zone, Diff(want, held, false)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := bind.Dig(t, name, "A"); !slices.Equal(got, []string{"203.0.113.8"}) {
		t.Fatalf("BIND serves A %v after the change", got)
	}

	tunnel := []Set{{Name: "tunnel.jd-records.test", Type: TypeCNAME, Values: []string{"abc.cfargotunnel.com"}, TTL: 60}}
	if _, err := Apply(ctx, provider, zone, Diff(tunnel, nil, false)); err != nil {
		t.Fatalf("CNAME: %v", err)
	}
	if got := bind.Dig(t, "tunnel.jd-records.test", "CNAME"); !slices.Equal(got, []string{"abc.cfargotunnel.com."}) {
		t.Fatalf("BIND serves CNAME %v", got)
	}
	held, err = provider.Read(ctx, zone, "tunnel.jd-records.test")
	if err != nil || actions(Diff(tunnel, held, false)) != "CNAME:keep" {
		t.Fatalf("the CNAME read back: %+v %v", held, err)
	}

	// Created by someone else since it was read: the prerequisite refuses.
	err = provider.Write(ctx, zone, nil, Set{Name: name, Type: TypeA, Values: []string{"203.0.113.9"}, TTL: 300})
	refusedInWords(t, err, "YXRRSET")
	if got := bind.Dig(t, name, "A"); !slices.Equal(got, []string{"203.0.113.8"}) {
		t.Fatalf("a refused create changed the A to %v", got)
	}
	err = provider.Write(ctx, Zone{ID: "jd-records.test", Name: "jd-records.test"}, nil, Set{Name: "app.elsewhere.test", Type: TypeA, Values: []string{"203.0.113.9"}, TTL: 300})
	refusedInWords(t, err, "NOTZONE")

	wrong := strings.Replace(bind.Credentials, bind.Secret, base64.StdEncoding.EncodeToString([]byte("not the secret BIND was given!!")), 1)
	impostor := mustProvider(t, "rfc2136", wrong, Options{})
	err = impostor.Write(ctx, zone, nil, Set{Name: "other.jd-records.test", Type: TypeA, Values: []string{"203.0.113.9"}, TTL: 300})
	if err == nil || !strings.Contains(err.Error(), "NOTAUTH") {
		t.Fatalf("a wrong secret = %v", err)
	}
	if got := bind.Dig(t, "other.jd-records.test", "A"); len(got) != 0 {
		t.Fatalf("a wrong secret wrote %v", got)
	}
}
