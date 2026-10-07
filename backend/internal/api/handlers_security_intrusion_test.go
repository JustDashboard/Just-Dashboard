package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// CrowdSec and Suricata are absent from most hosts, and an absent tool is
// information: both readings answer 200 and say so.
func TestIntrusionReadsSurviveABareHost(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/security/crowdsec/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("crowdsec = %d: %s", w.Code, w.Body.String())
	}
	var cs netsec.CrowdSecView
	decodeNetworkBody(t, w.Body.Bytes(), &cs)
	if cs.Decisions == nil || cs.Alerts == nil || cs.Bouncers == nil {
		t.Fatalf("crowdsec lists must be arrays: %+v", cs)
	}

	w = c.do(http.MethodGet, "/api/v1/security/suricata/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("suricata = %d: %s", w.Code, w.Body.String())
	}
	var su netsec.SuricataView
	decodeNetworkBody(t, w.Body.Bytes(), &su)
	if su.Alerts == nil || su.BySeverity == nil || su.TopSignatures == nil || su.LogPath == "" {
		t.Fatalf("suricata lists must be arrays: %+v", su)
	}
}

func TestIntrusionCapabilities(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	viewer := &client{t: t, h: h, cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}
	if w := viewer.do(http.MethodGet, "/api/v1/security/crowdsec/", "", nil); w.Code != http.StatusOK {
		t.Errorf("crowdsec is readable by every role: %d", w.Code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/security/suricata/", ""},
		{http.MethodPost, "/api/v1/security/crowdsec/decisions", `{"value":"203.0.113.9","duration":"4h","reason":"x"}`},
		{http.MethodDelete, "/api/v1/security/crowdsec/decisions/7", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if w := viewer.do(tc.method, tc.path, tc.body, nil); w.Code != http.StatusForbidden {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// A decision that cannot be valid is refused before cscli is asked.
func TestCrowdSecDecisionValidation(t *testing.T) {
	c, _ := newClient(t)
	for name, body := range map[string]string{
		"not an address": `{"value":"example.com","duration":"4h","reason":"x"}`,
		"every address":  `{"value":"0.0.0.0/0","duration":"4h","reason":"x"}`,
		"loopback":       `{"value":"127.0.0.1","duration":"4h","reason":"x"}`,
		"a bad duration": `{"value":"203.0.113.9","duration":"forever","reason":"x"}`,
		"a long reason":  `{"value":"203.0.113.9","duration":"4h","reason":"` + strings.Repeat("a", 200) + `"}`,
		"an extra field": `{"value":"203.0.113.9","duration":"4h","reason":"x","type":"captcha"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := c.do(http.MethodPost, "/api/v1/security/crowdsec/decisions", body, nil); w.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, id := range []string{"abc", "0", "-4", "1.5"} {
		cc, _ := newClient(t)
		if w := cc.do(http.MethodDelete, "/api/v1/security/crowdsec/decisions/"+id, "", nil); w.Code != http.StatusBadRequest {
			t.Errorf("decision id %q = %d", id, w.Code)
		}
	}
}
