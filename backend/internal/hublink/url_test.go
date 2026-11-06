package hublink

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// ruleService is a Service with only what the URL rule reads: a tailnet, a
// whois, a resolver that knows nothing, and an optional test origin.
func ruleService(t *testing.T, testOrigin string) (*Service, *fakeTailnet, *fakeWhoIs) {
	t.Helper()
	tn, who := newFakeTailnet(), &fakeWhoIs{node: "hub.tailed39ba.ts.net."}
	s := New(Options{Tailnet: tn, WhoIs: who, TestOrigin: testOrigin})
	s.dial.lookup = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
	return s, tn, who
}

func codeOf(err error) string {
	var u *URLError
	if errors.As(err, &u) {
		return u.Code
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

func TestTheHubURLRule(t *testing.T) {
	for _, tc := range []struct{ in, origin, code string }{
		{"hub.tailed39ba.ts.net", "https://hub.tailed39ba.ts.net", ""},
		{" https://HUB.tailed39ba.ts.net./ ", "https://hub.tailed39ba.ts.net", ""},
		// The trailing dot goes before accountlink's spelling would keep it
		// (protocol 2.2 step 3, HL-24c).
		{"https://HUB.tailed39ba.ts.net.:443", "https://hub.tailed39ba.ts.net", ""},
		{"https://HUB.tailed39ba.ts.net.:8443", "https://hub.tailed39ba.ts.net:8443", ""},
		{"https://hub.tailed39ba.ts.net:443", "https://hub.tailed39ba.ts.net", ""},
		{"https://hub.tailed39ba.ts.net:8443", "https://hub.tailed39ba.ts.net:8443", ""},
		{"hub-1.tailed39ba.ts.net", "https://hub-1.tailed39ba.ts.net", ""},
		{"", "", CodeURLInvalid},
		{"http://hub.tailed39ba.ts.net", "", CodeURLInvalid},
		{"https://user:pw@hub.tailed39ba.ts.net", "", CodeURLInvalid},
		{"https://hub.tailed39ba.ts.net/path", "", CodeURLInvalid},
		{"https://hub.tailed39ba.ts.net/?q=1", "", CodeURLInvalid},
		{"https://hub.tailed39ba.ts.net#frag", "", CodeURLInvalid},
		{"https://hub.tailed39ba.ts.net:99999", "", CodeURLInvalid},
		{"https://100.101.102.103", "", CodeURLInvalid},
		{"https://[fd7a:115c:a1e0::1]", "", CodeURLInvalid},
		{"ftp://hub.tailed39ba.ts.net", "", CodeURLInvalid},
		{"hub.example.com", "", CodeNotTailnet},
		{"a.hub.tailed39ba.ts.net", "", CodeNotTailnet},
		{"hub.other.ts.net", "", CodeNotTailnet},
		{"-hub.tailed39ba.ts.net", "", CodeNotTailnet},
		{"box.tailed39ba.ts.net", "", CodeSameHost},
		{"https://box.tailed39ba.ts.net:9443", "", CodeSameHost},
	} {
		t.Run(tc.in, func(t *testing.T) {
			s, tn, who := ruleService(t, "")
			who.node = hostOf(tc.origin) + "."
			if tc.origin != "" {
				tn.set(func(s *TailnetStatus) { s.Peers[hostOf(tc.origin)] = netip.MustParseAddr("100.101.102.103") })
			}
			origin, _, err := s.checkURL(t.Context(), tc.in)
			if codeOf(err) != tc.code || origin != tc.origin {
				t.Fatalf("checkURL(%q) = %q, %v; want %q, %s", tc.in, origin, err, tc.origin, tc.code)
			}
		})
	}
}

func TestTheRuleNeedsATailnetWithMagicDNS(t *testing.T) {
	for name, set := range map[string]func(*fakeTailnet){
		"not running":    func(f *fakeTailnet) { f.set(func(s *TailnetStatus) { s.Running = false }) },
		"no suffix":      func(f *fakeTailnet) { f.set(func(s *TailnetStatus) { s.Suffix = "" }) },
		"not a ts.net":   func(f *fakeTailnet) { f.set(func(s *TailnetStatus) { s.Suffix = "corp.example" }) },
		"cannot be read": func(f *fakeTailnet) { f.err = errors.New("tailscale is not installed") },
	} {
		t.Run(name, func(t *testing.T) {
			s, tn, _ := ruleService(t, "")
			set(tn)
			if _, _, err := s.checkURL(t.Context(), "hub.tailed39ba.ts.net"); codeOf(err) != CodeNotTailnet {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestWhoIsConfirmsTheHubsNodeWhenItCanBeAsked(t *testing.T) {
	s, _, who := ruleService(t, "")
	if _, _, err := s.checkURL(t.Context(), "hub.tailed39ba.ts.net"); err != nil {
		t.Fatal(err)
	}
	// It asked about the address the dashboard would dial: from tailscale
	// status, since the resolver knows nothing.
	if len(who.asks) != 1 || who.asks[0] != netip.MustParseAddr("100.101.102.103") {
		t.Fatalf("asked = %v", who.asks)
	}
	who.node = "impostor.tailed39ba.ts.net."
	if _, _, err := s.checkURL(t.Context(), "hub.tailed39ba.ts.net"); codeOf(err) != CodeNotOnTailnet {
		t.Fatalf("another node = %v", err)
	}
	who.err = tailnetgate.ErrNotAPeer
	if _, _, err := s.checkURL(t.Context(), "hub.tailed39ba.ts.net"); codeOf(err) != CodeNotOnTailnet {
		t.Fatalf("not a peer = %v", err)
	}
	who.err = tailnetgate.ErrUnavailable
	if _, _, err := s.checkURL(t.Context(), "hub.tailed39ba.ts.net"); err != nil {
		t.Fatalf("tailscaled unavailable should skip the check: %v", err)
	}
	// A name with no address on the tailnet: nothing to ask about and
	// nothing to dial. The origin comes back with an unreachable error.
	who.err = nil
	asked := len(who.asks)
	var unreachable *UnreachableError
	origin, node, err := s.checkURL(t.Context(), "hub-2.tailed39ba.ts.net")
	if origin != "https://hub-2.tailed39ba.ts.net" || node != nil || !errors.As(err, &unreachable) || len(who.asks) != asked {
		t.Fatalf("no address = %q %+v %v, asked %d more", origin, node, err, len(who.asks)-asked)
	}
}

func TestTheTestOriginIsOneLoopbackOriginAndWidensNothingElse(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"http://127.0.0.1:43180", "http://127.0.0.1:43180"},
		{"http://LOCALHOST:43180/", "http://localhost:43180"},
		{"https://[::1]:8443", "https://[::1]:8443"},
		{"http://127.0.0.2", "http://127.0.0.2"},
	} {
		if got, err := ParseTestOrigin(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseTestOrigin(%q) = %q, %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"http://hub.tailed39ba.ts.net", "http://10.0.0.1:80", "ftp://127.0.0.1", "http://127.0.0.1/path", "http://u@127.0.0.1"} {
		if got, err := ParseTestOrigin(bad); err == nil {
			t.Errorf("ParseTestOrigin(%q) = %q, want an error", bad, got)
		}
	}

	s, _, _ := ruleService(t, "http://127.0.0.1:43180")
	if origin, node, err := s.checkURL(t.Context(), "http://127.0.0.1:43180/"); err != nil || origin != "http://127.0.0.1:43180" || node != nil {
		t.Fatalf("the test origin = %q, %v", origin, err)
	}
	if _, _, err := s.checkURL(t.Context(), "http://127.0.0.1:43181"); codeOf(err) != CodeURLInvalid {
		t.Fatalf("another port = %v", err)
	}
	if _, _, err := s.checkURL(t.Context(), "hub.example.com"); codeOf(err) != CodeNotTailnet {
		t.Fatalf("a real name still goes through the rule: %v", err)
	}
}

// Steps 1 to 4 need no tailnet and always run on a stored link; steps 5 to 7
// run when this server's suffix can be read (protocol 2.2, "When the rule
// runs").
func TestAStoredLinkIsCheckedOnlyWhenTheTailnetCanBeRead(t *testing.T) {
	s, tn, _ := ruleService(t, "")
	if err := s.admitStored(t.Context(), "https://hub.tailed39ba.ts.net"); err != nil {
		t.Fatal(err)
	}
	tn.set(func(st *TailnetStatus) { st.Suffix = "other.ts.net" })
	if err := s.admitStored(t.Context(), "https://hub.tailed39ba.ts.net"); err == nil {
		t.Fatal("another tailnet passed")
	}
	tn.err = errors.New("tailscaled is restarting")
	if err := s.admitStored(t.Context(), "https://hub.tailed39ba.ts.net"); err != nil {
		t.Fatalf("an unreadable tailnet decides steps 5 to 7 nothing: %v", err)
	}
	// With no tailnet at all, steps 1 to 4 still refuse.
	for _, stored := range []string{"http://127.0.0.1:43180", "https://127.0.0.1:43180", "https://[::1]", "https://localhost:9443", "http://hub.tailed39ba.ts.net"} {
		if err := s.admitStored(t.Context(), stored); err == nil {
			t.Errorf("%s passed with the tailnet unreadable", stored)
		}
	}
	// The test origin itself passes while it is set.
	s2, _, _ := ruleService(t, "http://127.0.0.1:43180")
	if err := s2.admitStored(t.Context(), "http://127.0.0.1:43180"); err != nil {
		t.Fatalf("the current test origin = %v", err)
	}
}

func TestFleetEndpointsAndNoticeLinksStayOnTheHubsTailnet(t *testing.T) {
	const hub = "https://hub.tailed39ba.ts.net"
	for raw, want := range map[string]string{
		"https://box.tailed39ba.ts.net:8443":   "https://box.tailed39ba.ts.net:8443",
		"https://BOX.tailed39ba.ts.net:8443/":  "https://box.tailed39ba.ts.net:8443",
		"https://box.other.ts.net:8443":        "",
		"https://a.box.tailed39ba.ts.net":      "",
		"http://box.tailed39ba.ts.net":         "",
		"https://box.tailed39ba.ts.net/x":      "",
		"https://100.64.0.1":                   "",
		"box.tailed39ba.ts.net":                "",
		"javascript:alert(1)":                  "",
		"https://box.tailed39ba.ts.net?next=x": "",
	} {
		if got := fleetEndpoint(raw, hub, "", nil); got != want {
			t.Errorf("fleetEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
	// At the test origin exactly one thing relaxes (2.3, 6.1): a loopback
	// http or https origin passes, as does one under this server's own
	// MagicDNS suffix. Any other https origin is still removed.
	const test = "http://127.0.0.1:43180"
	own := func() string { return "tailed39ba.ts.net" }
	for raw, want := range map[string]string{
		"https://example.com":                "",
		"https://anything.example.com":       "",
		"http://127.0.0.1:18443":             "http://127.0.0.1:18443",
		"http://127.0.0.2:18453":             "http://127.0.0.2:18453",
		"https://localhost:8443":             "https://localhost:8443",
		"http://[::1]:8443":                  "http://[::1]:8443",
		"https://box.tailed39ba.ts.net:8443": "https://box.tailed39ba.ts.net:8443",
		"http://box.tailed39ba.ts.net:8443":  "",
		"https://box.other.ts.net:8443":      "",
		"http://example.com":                 "",
		"http://127.0.0.1:18443/path":        "",
		"ftp://127.0.0.1:21":                 "",
	} {
		if got := fleetEndpoint(raw, test, test, own); got != want {
			t.Errorf("at the test origin, fleetEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
	// With this server's suffix unreadable, only loopback passes there.
	if got := fleetEndpoint("https://box.tailed39ba.ts.net:8443", test, test, func() string { return "" }); got != "" {
		t.Errorf("an unreadable own suffix still passed %q", got)
	}
	at := int64(1)
	n := func(href string) wireNotice {
		id, level, title, body := "x", "info", "t", "b"
		return wireNotice{ID: &id, Level: &level, Title: &title, Body: &body, Href: &href, At: &at}
	}
	for href, kept := range map[string]bool{
		hub + "/updates":                        true,
		"https://hub.tailed39ba.ts.net:443/x":   true,
		"https://hub.tailed39ba.ts.net:8443/x":  false,
		"https://evil.example.com/updates":      false,
		"http://hub.tailed39ba.ts.net/updates":  false,
		"https://user@hub.tailed39ba.ts.net/up": false,
	} {
		got, ok := notice(n(href), hub, "")
		if !ok || (got.Href != "") != kept {
			t.Errorf("href %q kept = %v", href, got.Href != "")
		}
	}
}

func TestTailscaleStatusIsReadForTheSuffixTheSelfNameAndPeers(t *testing.T) {
	st, err := parseTailnetStatus([]byte(`{
		"BackendState": "Running",
		"CurrentTailnet": {"Name": "x@example.com", "MagicDNSSuffix": "tailed39ba.ts.net"},
		"MagicDNSSuffix": "ignored.ts.net",
		"Self": {"DNSName": "vmi3690758.tailed39ba.ts.net.", "TailscaleIPs": ["100.80.1.1"]},
		"Peer": {
			"nodekey:a": {"DNSName": "hub.tailed39ba.ts.net.", "TailscaleIPs": ["fd7a:115c:a1e0::5", "100.101.102.103"]},
			"nodekey:b": {"DNSName": "odd.tailed39ba.ts.net.", "TailscaleIPs": ["10.0.0.5"]}
		}
	}`))
	if err != nil || !st.Running || st.Suffix != "tailed39ba.ts.net" || st.Self != "vmi3690758.tailed39ba.ts.net" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if st.Peers["hub.tailed39ba.ts.net"] != netip.MustParseAddr("100.101.102.103") {
		t.Fatalf("peers = %v", st.Peers)
	}
	if _, ok := st.Peers["odd.tailed39ba.ts.net"]; ok {
		t.Fatal("a peer address outside 100.64.0.0/10 was kept")
	}
	// Older clients: no CurrentTailnet, and then not even the top-level field.
	st, _ = parseTailnetStatus([]byte(`{"BackendState":"Running","MagicDNSSuffix":"tailed39ba.ts.net","Self":{"DNSName":"box.tailed39ba.ts.net."}}`))
	if st.Suffix != "tailed39ba.ts.net" {
		t.Fatalf("top-level suffix = %q", st.Suffix)
	}
	st, _ = parseTailnetStatus([]byte(`{"BackendState":"Stopped","Self":{"DNSName":"box.tailed39ba.ts.net."}}`))
	if st.Suffix != "tailed39ba.ts.net" || st.Running {
		t.Fatalf("suffix from the self name = %+v", st)
	}
}

func TestTheDialerReachesAHubOnlyOverTheTailnet(t *testing.T) {
	tn := newFakeTailnet()
	d := newDialer(tn, "")
	d.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "dns.tailed39ba.ts.net":
			return []netip.Addr{netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("100.70.0.9")}, nil
		case "public.tailed39ba.ts.net":
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
		return nil, errors.New("no such host")
	}
	if a, err := d.resolve(t.Context(), "dns.tailed39ba.ts.net"); err != nil || a != netip.MustParseAddr("100.70.0.9") {
		t.Fatalf("resolver answer = %v, %v", a, err)
	}
	if a, err := d.resolve(t.Context(), "HUB.tailed39ba.ts.net."); err != nil || a != netip.MustParseAddr("100.101.102.103") {
		t.Fatalf("tailscale status answer = %v, %v", a, err)
	}
	if _, err := d.resolve(t.Context(), "public.tailed39ba.ts.net"); err == nil {
		t.Fatal("a name that resolves only off the tailnet was given an address")
	}
	if _, err := d.dial(t.Context(), "tcp", "public.tailed39ba.ts.net:443"); err == nil {
		t.Fatal("dialled a hub off the tailnet")
	}
	// The cache is used for a minute and dropped after a failed dial.
	calls := tn.calls()
	_, _ = d.resolve(t.Context(), "hub.tailed39ba.ts.net")
	if tn.calls() != calls {
		t.Fatal("the cached peer address was not reused")
	}
	d.forget("hub.tailed39ba.ts.net")
	_, _ = d.resolve(t.Context(), "hub.tailed39ba.ts.net")
	if tn.calls() != calls+1 {
		t.Fatal("a forgotten peer was not looked up again")
	}
	if hostPortOf("http://127.0.0.1:43180") != "127.0.0.1:43180" || hostPortOf("https://hub.x.ts.net") != "hub.x.ts.net:443" {
		t.Fatal("hostPortOf")
	}
}
