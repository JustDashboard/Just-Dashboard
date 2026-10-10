package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func phases(h PackageHandoff) string {
	var out []string
	for _, p := range h.Phases {
		out = append(out, p.Key+"="+p.Status)
	}
	return strings.Join(out, " ")
}

// A package on disk is not a working service: each phase is read from the
// module that owns the software, and working is only every phase done.
func TestHandoffsSayWhatFollowedTheInstall(t *testing.T) {
	cases := []struct {
		name    string
		got     PackageHandoff
		phases  string
		working bool
		detail  string
	}{
		{"wireguard not installed", wireguardHandoff(&netx.WireGuardView{}), "installed=pending configured=pending active=pending verified=pending", false, ""},
		{"wireguard installed, nothing configured", wireguardHandoff(&netx.WireGuardView{Installed: true, Kernel: true}),
			"installed=done configured=pending active=pending verified=done", false, "create one"},
		{"wireguard without a kernel module", wireguardHandoff(&netx.WireGuardView{Installed: true, Interfaces: []netx.WGInterface{{Configured: true, Up: true}}}),
			"installed=done configured=done active=done verified=failed", false, "no WireGuard module"},
		{"a working tunnel", wireguardHandoff(&netx.WireGuardView{Installed: true, Kernel: true, Interfaces: []netx.WGInterface{{Configured: true, Up: true}}}),
			"installed=done configured=done active=done verified=done", true, ""},
		{"bpftool not allowed to list", bpftoolHandoff(&netx.EBPFView{Installed: true, Error: "bpftool: can't get next program: Operation not permitted"}),
			"installed=done configured=not_applicable active=not_applicable verified=failed", false, "Operation not permitted"},
		{"bpftool listing", bpftoolHandoff(&netx.EBPFView{Installed: true, Total: 47}),
			"installed=done configured=not_applicable active=not_applicable verified=done", true, "47"},
		{"crowdsec without a bouncer", crowdsecHandoff(&netsec.CrowdSecView{Installed: true, Active: true}),
			"installed=done configured=done active=done verified=pending", false, "block nothing"},
		{"crowdsec stopped", crowdsecHandoff(&netsec.CrowdSecView{Installed: true, Bouncers: []netsec.CrowdSecBouncer{{Valid: true}}}),
			"installed=done configured=done active=pending verified=pending", false, ""},
		{"crowdsec enforcing", crowdsecHandoff(&netsec.CrowdSecView{Installed: true, Active: true, Bouncers: []netsec.CrowdSecBouncer{{Valid: true}, {Valid: false}}}),
			"installed=done configured=done active=done verified=done", true, "1 bouncer"},
		{"suricata with no rules", suricataHandoff(&netsec.SuricataView{Installed: true, Active: true, Mode: "ids", RulesLoaded: ptr(0), LogPath: "/var/log/suricata/eve.json"}),
			"installed=done configured=pending active=done verified=done", false, "suricata-update"},
		{"suricata log outside the roots", suricataHandoff(&netsec.SuricataView{Installed: true, Active: true, Mode: "ids", RulesLoaded: ptr(40000), LogRefused: "outside JD_LOG_ROOTS"}),
			"installed=done configured=done active=done verified=unknown", false, "JD_LOG_ROOTS"},
		{"suricata working", suricataHandoff(&netsec.SuricataView{Installed: true, Active: true, Mode: "ips", RulesLoaded: ptr(40000), LogPath: "/var/log/suricata/eve.json"}),
			"installed=done configured=done active=done verified=done", true, "eve.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := phases(c.got); got != c.phases || c.got.Working != c.working {
				t.Fatalf("phases %q working %v", got, c.got.Working)
			}
			if c.detail != "" {
				found := false
				for _, p := range c.got.Phases {
					if strings.Contains(p.Detail, c.detail) {
						found = true
					}
				}
				if !found {
					t.Fatalf("no phase says %q: %+v", c.detail, c.got.Phases)
				}
			}
		})
	}
}

func TestHandoffRouteReadsOnlyThePackagesItOffers(t *testing.T) {
	admin, _, _ := networkClients(t)
	if w := admin.do(http.MethodGet, "/api/v1/network/handoffs/nginx", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown package = %d %s", w.Code, w.Body)
	}
}

// On the machine running the tests, read-only: the phases follow what is
// actually there, and a package on disk is never reported working by itself.
func TestHandoffRouteReadsThisHost(t *testing.T) {
	_, viewer, _ := networkClients(t)
	for _, pkg := range []string{"bpftool", "wireguard-tools"} {
		w := viewer.do(http.MethodGet, "/api/v1/network/handoffs/"+pkg, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", pkg, w.Code, w.Body)
		}
		var h PackageHandoff
		decodeNetworkBody(t, w.Body.Bytes(), &h)
		if h.Package != pkg || len(h.Phases) != 4 || h.Phases[0].Key != "installed" || h.Phases[3].Key != "verified" {
			t.Fatalf("%s = %+v", pkg, h)
		}
		installed := h.Phases[0].Status == "done"
		if !installed && h.Working {
			t.Fatalf("%s works without being installed: %+v", pkg, h)
		}
		for _, p := range h.Phases {
			if p.Detail == "" {
				t.Fatalf("%s phase without a sentence: %+v", pkg, p)
			}
		}
	}
}
