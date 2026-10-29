package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/cftunnel"
)

// The tunnel's routes: read by anyone signed in, a token refused in words
// and audited, and the host alert unjudged while nothing is connected.
func TestTunnelRoutesRefuseATokenThatIsNotOne(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "tunnel-admin", auth.RoleAdmin)}
	res := admin.do(http.MethodGet, "/api/v1/proxy/tunnel", "", nil)
	var status cftunnel.Status
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil || res.Code != http.StatusOK || status.Connected {
		t.Fatalf("status = %d %s", res.Code, res.Body.String())
	}
	res = admin.do(http.MethodPut, "/api/v1/proxy/tunnel", `{"token":"not a token"}`, nil)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "tunnel token is base64") {
		t.Fatalf("a token that is not one = %d %s", res.Code, res.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.tunnel.connect"); !strings.Contains(entry.Detail, `"result":"refused"`) || strings.Contains(entry.Detail, "not a token") {
		t.Fatalf("audit = %+v", entry)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "tunnel-viewer", auth.RoleLimited)}
	if res := viewer.do(http.MethodPut, "/api/v1/proxy/tunnel", `{"token":"x"}`, nil); res.Code != http.StatusForbidden {
		t.Fatalf("a viewer connecting = %d", res.Code)
	}
	if reading := s.tunnelAlertReading(t.Context()); reading.Judged || !strings.Contains(reading.Why, "No Cloudflare Tunnel is connected") {
		t.Fatalf("alert reading = %+v", reading)
	}
}
