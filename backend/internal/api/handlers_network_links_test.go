package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// These cover what the device and routing routes decide before any command
// reaches the host: who may call them, and what is refused as malformed. The
// mutations themselves are tested in netx against recorded transcripts and,
// behind JD_NETNS_LIVE, a throwaway namespace; a test here that got past
// validation would be changing the machine the tests run on, so none does.

// networkMutations is every route in the section's devices and routing areas
// that changes something, each with a body that passes the decoder.
var networkMutations = []struct {
	method, path, body string
	destructive        bool
}{
	{http.MethodPost, "/api/v1/network/namespaces", `{"name":"lab"}`, false},
	{http.MethodDelete, "/api/v1/network/namespaces/lab", "", true},
	{http.MethodPost, "/api/v1/network/links", `{"name":"d0","kind":"dummy","up":true}`, false},
	{http.MethodDelete, "/api/v1/network/links/d0", "", true},
	{http.MethodPost, "/api/v1/network/links/d0/up", "", false},
	{http.MethodPost, "/api/v1/network/links/d0/down", "", true},
	{http.MethodPost, "/api/v1/network/links/d0/mtu", `{"mtu":1400}`, false},
	{http.MethodPost, "/api/v1/network/links/d0/master", `{"master":"br0"}`, false},
	{http.MethodPost, "/api/v1/network/links/d0/addresses", `{"cidr":"10.0.0.1/24"}`, false},
	{http.MethodDelete, "/api/v1/network/links/d0/addresses?cidr=10.0.0.1/24", "", true},
	{http.MethodPost, "/api/v1/network/routing/routes", `{"destination":"10.0.0.0/24","device":"d0"}`, false},
	{http.MethodDelete, "/api/v1/network/routing/routes/1", "", true},
	{http.MethodPost, "/api/v1/network/routing/rules", `{"from":"10.0.0.0/24","table":100}`, false},
	{http.MethodDelete, "/api/v1/network/routing/rules/1", "", true},
	{http.MethodPost, "/api/v1/network/forwarding/ipv4/on", "", false},
	{http.MethodPost, "/api/v1/network/forwarding/ipv4/off", "", true},
}

func TestNetworkDeviceAndRoutingMutationsNeedSystemAdmin(t *testing.T) {
	s := testServer(t)
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	for _, m := range networkMutations {
		t.Run(m.method+" "+m.path, func(t *testing.T) {
			w := limited.do(m.method, m.path, m.body, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("a limited account got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

func TestNetworkDeviceAndRoutingReadsAnswerOnAnyHost(t *testing.T) {
	c, _ := newClient(t)
	for _, path := range []string{"/api/v1/network/namespaces", "/api/v1/network/routing", "/api/v1/network/bgp"} {
		t.Run(path, func(t *testing.T) {
			w := c.do(http.MethodGet, path, "", nil)
			// A host without iproute2 answers 503 tool_unavailable, which is
			// information; anything else must be a real answer.
			if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
				t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
			}
			var any any
			if err := json.Unmarshal(w.Body.Bytes(), &any); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
		})
	}
}

func TestNetworkBGPIsInformationWhereFRRIsAbsent(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/network/bgp", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Installed bool              `json:"installed"`
		Running   bool              `json:"running"`
		Families  []json.RawMessage `json:"families"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Families == nil {
		t.Fatalf("families serialises as null: %s", w.Body.String())
	}
}

func TestNetworkDeviceAndRoutingRefuseMalformedRequestsBeforeTouchingTheHost(t *testing.T) {
	c, _ := newClient(t)
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"an unknown device kind", http.MethodPost, "/api/v1/network/links", `{"name":"x0","kind":"wireguard"}`, 400, "bad_request"},
		{"an unknown field", http.MethodPost, "/api/v1/network/links", `{"name":"x0","kind":"dummy","mystery":1}`, 400, ""},
		{"a name with a space", http.MethodPost, "/api/v1/network/links", `{"name":"a b","kind":"dummy"}`, 400, "bad_request"},
		{"an MTU out of range", http.MethodPost, "/api/v1/network/links/x0/mtu", `{"mtu":5}`, 400, "bad_request"},
		{"an address that is not one", http.MethodPost, "/api/v1/network/links/x0/addresses", `{"cidr":"nope"}`, 400, ""},
		{"a removal with no address", http.MethodDelete, "/api/v1/network/links/x0/addresses", "", 400, "bad_request"},
		{"a bad namespace name", http.MethodPost, "/api/v1/network/namespaces", `{"name":"a/b"}`, 400, "bad_request"},
		{"a route with no destination", http.MethodPost, "/api/v1/network/routing/routes", `{"gateway":"10.0.0.1"}`, 400, "bad_request"},
		{"a route in the local table", http.MethodPost, "/api/v1/network/routing/routes", `{"destination":"10.0.0.0/24","device":"d0","table":255}`, 409, "would_lock_you_out"},
		{"a route in Tailscale's table", http.MethodPost, "/api/v1/network/routing/routes", `{"destination":"10.0.0.0/24","device":"d0","table":52}`, 409, "would_lock_you_out"},
		{"a rule with no selector", http.MethodPost, "/api/v1/network/routing/rules", `{"table":100}`, 409, "would_lock_you_out"},
		{"a mixed-family rule", http.MethodPost, "/api/v1/network/routing/rules", `{"from":"2001:db8::/32","to":"192.0.2.0/24","table":100}`, 400, "bad_request"},
		{"a route id that is not a number", http.MethodDelete, "/api/v1/network/routing/routes/abc", "", 400, "bad_request"},
		{"a rule id of zero", http.MethodDelete, "/api/v1/network/routing/rules/0", "", 400, "bad_request"},
		{"a family nobody has", http.MethodPost, "/api/v1/network/forwarding/ipx/on", "", 400, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := c.do(tc.method, tc.path, tc.body, nil)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
			}
			if tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("body = %s, want code %s", w.Body.String(), tc.code)
			}
		})
	}
}
