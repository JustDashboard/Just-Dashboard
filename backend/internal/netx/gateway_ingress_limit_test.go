package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The Docker Caddy ingress cannot limit a deployment's requests (caddy:2-alpine
// has no rate-limit module), so the Domains page offers the route's limit as
// the host's: one per-source limit on each of 80 and 443, posted to this
// gateway's own route (frontend edge-rules-model.ts, ingressLimitRequests).
// These are those requests as the page spells them, and what the gateway
// makes of them.
func TestTheIngressRateLimitIsMeteredPerSourceOnPublishedWebPorts(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	for _, port := range []string{"443", "80"} {
		body := fmt.Sprintf(`{"name":"Ingress rate limit %s: shop","protocol":"tcp","ports":%q,`+
			`"rate":20,"per":"second","burst":40,"perSource":true,"maxConnections":0,`+
			`"globalConnections":0,"action":"reject"}`, port, port)
		var req LimitRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := h.AddLimit(ctx, req, gwClient, "ops"); err != nil {
			t.Fatalf("port %s: %v", port, err)
		}
	}
	ruleset := h.loaded[len(h.loaded)-1]
	for id, port := range map[int]string{1: "443", 2: "80"} {
		for _, fam := range []string{"4", "6"} {
			saddr := gwFamSaddr(fam)
			// Docker publishes the ingress's ports by destination translation,
			// so its visitors are met in the forward chain, by the port they
			// asked for, each address on its own meter.
			want := fmt.Sprintf("meta l4proto tcp ct original proto-dst %s ct status dnat ct state new "+
				"update @lim_%d_%s { %s limit rate over 20/second burst 40 packets } "+
				"counter reject with tcp reset comment \"limit:%d\"", port, id, fam, saddr, id)
			if !strings.Contains(ruleset, want) {
				t.Errorf("the ruleset lacks\n  %s\nin:\n%s", want, ruleset)
			}
			if !strings.Contains(ruleset, fmt.Sprintf("set lim_%d_%s {", id, fam)) {
				t.Errorf("no per-source meter lim_%d_%s", id, fam)
			}
		}
	}
	// One limit is one port or one range: the two web ports in one would be
	// refused, and a range from 80 to 443 would meter every port between.
	_, err := h.AddLimit(ctx, LimitRequest{Name: "both", Protocol: "tcp", Ports: "80,443", Rate: 20, Per: "second"}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), "not a port") {
		t.Fatalf("80,443 in one limit: %v", err)
	}
}
