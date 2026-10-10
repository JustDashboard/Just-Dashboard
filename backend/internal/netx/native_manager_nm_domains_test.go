package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func nativeNMActiveDomainTranscript(t *testing.T, p *nativeProfile, family int, domains, searches []string, corrupt string) func(context.Context, []byte, string, ...string) (string, error) {
	t.Helper()
	return func(_ context.Context, _ []byte, tool string, args ...string) (string, error) {
		if tool != "busctl" || len(args) != 10 || args[5] != "get-property" || args[6] != p.OwnerBus {
			t.Fatal("active DNS read escaped its pinned unique owner", tool, args)
		}
		property := args[9]
		kind := "as"
		var value any
		switch property {
		case "Ip4Config", "Ip6Config":
			kind, value = "o", fmt.Sprintf("/org/freedesktop/NetworkManager/IP%dConfig/7", 4+2*family)
			if args[7] != p.DeviceObject || args[8] != nmService+".Device" {
				t.Fatal("active config read escaped its selected device", args)
			}
		case "NameserverData":
			kind, value = "aa{sv}", []any{}
		case "Nameservers":
			kind, value = "aay", []any{}
		case "Domains":
			value = domains
		case "Searches":
			value = searches
		default:
			t.Fatal("unexpected active domain property", property)
		}
		if property == corrupt {
			kind, value = "s", "wrong typed property"
		}
		encoded, err := json.Marshal(map[string]any{"type": kind, "data": value})
		return string(encoded), err
	}
}

func TestNativeNMDomainsIncludeDHCPDomainNameAndSearchLists(t *testing.T) {
	prior := nativeExecute
	t.Cleanup(func() { nativeExecute = prior })
	p := &nativeProfile{OwnerBus: ":1.17", DeviceObject: nmObject + "/Devices/1"}
	for _, family := range []int{0, 1} {
		nativeExecute = nativeNMActiveDomainTranscript(t, p, family, []string{"auto.test"}, []string{"auto.test", "~corp.test"}, "")
		_, domains, err := nativeNMDNS(nativePinnedBus(context.Background(), strings.Repeat("1", 32)), p, family)
		if err != nil || !slices.Equal(domains, []string{"auto.test", "~corp.test"}) {
			t.Fatal("DHCP domain-name/search-list union lost or duplicated a suffix", family, domains, err)
		}
	}
	for _, corrupt := range []string{"Domains", "Searches", "Ip4Config"} {
		nativeExecute = nativeNMActiveDomainTranscript(t, p, 0, []string{"auto.test"}, []string{}, corrupt)
		if _, _, err := nativeNMDNS(context.Background(), p, 0); err == nil {
			t.Fatal("wrong typed active domain/config property was admitted", corrupt)
		}
	}
	for _, domains := range [][]string{{"foreign\n.test"}, {""}, {"UPPER.test"}, make([]string, 129)} {
		nativeExecute = nativeNMActiveDomainTranscript(t, p, 0, domains, []string{}, "")
		if _, _, err := nativeNMDNS(context.Background(), p, 0); err == nil {
			t.Fatal("unreadable/unbounded acquired domains admitted", domains)
		}
	}
	nativeExecute = nativeNMActiveDomainTranscript(t, p, 1, []string{}, []string{}, "")
	if _, _, err := nativeNMDNS(context.Background(), p, 0); err == nil {
		t.Fatal("IPv6 config object admitted for an IPv4 domain read")
	}
	foreign := *p
	foreign.OwnerBus = nmService
	if _, _, err := nativeNMDNS(context.Background(), &foreign, 0); err == nil {
		t.Fatal("a replaceable well-known owner admitted for active domain reads")
	}
}

func TestNativeNMRuntimeRefusesUnconfiguredDomainsForManualFamily(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	p := &nativeProfile{View: NativeProfileView{Device: "d0", Kind: "dummy", Renderer: "NetworkManager", Contract: f.u.Contract}, Device: ipLink{IfIndex: f.u.IfIndex, Address: f.u.MAC}, OwnerBus: f.u.OwnerBus, DeviceObject: nmObject + "/Devices/1"}
	transcript := nativeExecute
	active := nativeNMActiveDomainTranscript(t, p, 0, []string{"foreign.test"}, []string{}, "")
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "busctl" && len(args) > 9 && args[5] == "get-property" {
			return active(ctx, input, tool, args...)
		}
		return transcript(ctx, input, tool, args...)
	}
	if got := readNativeRuntime(context.Background(), p, f.u.CandidateIntent); got.Status != "drift" || !strings.Contains(got.Reason, "extra domains") {
		t.Fatal("unconfigured DHCP/native domain passed a manual family", got)
	}
}
