package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
)

func TestNetworkReportMountedObserverStopPrerequisite(t *testing.T) {
	c, s := newClient(t)
	c.h = s.Routes()
	assertFlowObserverExplicitStop(t, c, s)
}

func TestNetworkReportMountedPrivateRoutesAndIndependentMachineAuthentication(t *testing.T) {
	c, s := newClient(t)
	for _, path := range []string{"/api/v1/network/external/vantages", "/api/v1/network/external/checks", "/api/v1/network/dns/evidence/"} {
		if w := c.do("GET", path, "", nil); w.Code != 200 {
			t.Fatalf("mounted admin %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		actor := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "report-mounted-"+string(role), role)}
		for _, path := range []string{"/api/v1/network/external/vantages", "/api/v1/network/external/checks", "/api/v1/network/ipam/", "/api/v1/network/captures/", "/api/v1/network/flows/", "/api/v1/network/dns/evidence/"} {
			if w := actor.do("GET", path, "", nil); w.Code != 403 {
				t.Errorf("mounted %s %s=%d", role, path, w.Code)
			}
		}
	}
	w := c.do("POST", "/api/v1/network/external/enrollments", externalEnrollment, nil)
	var enrollment netvantage.Enrollment
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &enrollment) != nil {
		t.Fatalf("mounted enrollment=%d %s", w.Code, w.Body.String())
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claim := netvantage.SignClaim(key, netvantage.Claim{ServerKey: enrollment.ServerKey, ID: enrollment.Vantage.ID, Token: enrollment.Token, PublicKey: base64.RawStdEncoding.EncodeToString(public)})
	body := string(netvantage.Marshal(claim))
	outside := httptest.NewRequest("POST", "/api/v1/probe-agent/enroll", strings.NewReader(body))
	outside.RemoteAddr = "203.0.113.8:54321"
	outside.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.Routes().ServeHTTP(response, outside)
	if response.Code != 403 {
		t.Fatalf("machine allowlist=%d %s", response.Code, response.Body.String())
	}
	// The failed off-network request cannot consume the one-time proof.
	anonymous := &client{t: t, h: s.Routes()}
	if w := anonymous.do("POST", "/api/v1/probe-agent/enroll", body, nil); w.Code != 200 {
		t.Fatalf("anonymous proof=%d %s", w.Code, w.Body.String())
	}
	queued := c.do("POST", "/api/v1/network/external/checks", `{"vantageId":"`+enrollment.Vantage.ID+`","scopeId":"service","family":"inet","port":443,"tls":true}`, nil)
	if queued.Code != http.StatusAccepted {
		t.Fatalf("mounted queue=%d %s", queued.Code, queued.Body.String())
	}
	path := "/api/v1/probe-agent/poll"
	signature := netvantage.SignRequest(key, "POST", path, nil, netvantage.Signature{ServerKey: enrollment.ServerKey, ID: enrollment.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
	headers := map[string]string{
		"X-JD-Vantage": signature.ID, "X-JD-Probe-Server": signature.ServerKey,
		"X-JD-Probe-Sequence": strconv.FormatInt(signature.Sequence, 10),
		"X-JD-Probe-Time":     strconv.FormatInt(signature.Timestamp, 10), "X-JD-Probe-Signature": signature.Value,
	}
	polled := anonymous.do("POST", path, "", headers)
	var delivery struct {
		Job *netvantage.SignedJob `json:"job"`
	}
	if polled.Code != http.StatusOK || json.Unmarshal(polled.Body.Bytes(), &delivery) != nil || delivery.Job == nil {
		t.Fatalf("mounted signed poll=%d %s", polled.Code, polled.Body.String())
	}
	if err := netvantage.VerifyJob(*delivery.Job, enrollment.ServerKey); err != nil || delivery.Job.Job.VantageID != enrollment.Vantage.ID {
		t.Fatalf("mounted job identity or signature invalid: %v", err)
	}
	if replay := anonymous.do("POST", path, "", headers); replay.Code != http.StatusUnauthorized {
		t.Fatalf("mounted signed request replay=%d %s", replay.Code, replay.Body.String())
	}
	if w := c.do("POST", "/api/v1/probe-agent/poll", `{}`, nil); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "vantage_authentication_failed") {
		t.Fatalf("human cookie replaced machine proof=%d %s", w.Code, w.Body.String())
	}
	if w := anonymous.do("GET", "/api/v1/network/external/checks", "", map[string]string{"X-JD-Vantage": enrollment.Vantage.ID, "X-JD-Probe-Signature": "machine credential"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("machine identity widened human access=%d", w.Code)
	}
}
