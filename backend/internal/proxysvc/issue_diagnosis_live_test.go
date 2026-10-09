package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeACME is a certificate authority on loopback that takes an account and
// an order, hands out one http-01 challenge, and fails it with the problem
// a real authority sends when nothing answers port 80. Nothing leaves this
// host and no certificate exists.
func fakeACME(t *testing.T, problem map[string]any) string {
	t.Helper()
	var triggered atomic.Bool
	var base string
	nonce := atomic.Int64{}
	reply := func(w http.ResponseWriter, status int, location string, body any) {
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce%d", nonce.Add(1)))
		if location != "" {
			w.Header().Set("Location", location)
		}
		if body == nil {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	identifier := map[string]string{"type": "dns", "value": "app.example.com"}
	challenge := func() map[string]any {
		c := map[string]any{"type": "http-01", "url": base + "/chall/1", "token": "tok123tok123tok123tok123tok123AB", "status": "pending"}
		if triggered.Load() {
			c["status"], c["error"] = "invalid", problem
		}
		return c
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dir", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, "", map[string]any{"newNonce": base + "/nonce", "newAccount": base + "/acct", "newOrder": base + "/order",
			"revokeCert": base + "/revoke", "keyChange": base + "/key", "meta": map[string]any{"termsOfService": base + "/tos"}})
	})
	mux.HandleFunc("/nonce", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, "", nil) })
	mux.HandleFunc("/acct", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 201, base+"/acct/1", map[string]any{"status": "valid", "orders": base + "/acct/1/orders"})
	})
	mux.HandleFunc("/acct/1", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, "", map[string]any{"status": "valid", "orders": base + "/acct/1/orders"})
	})
	mux.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 201, base+"/order/1", map[string]any{"status": "pending", "expires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"identifiers": []any{identifier}, "authorizations": []string{base + "/authz/1"}, "finalize": base + "/order/1/finalize"})
	})
	mux.HandleFunc("/authz/1", func(w http.ResponseWriter, r *http.Request) {
		status := "pending"
		if triggered.Load() {
			status = "invalid"
		}
		reply(w, 200, "", map[string]any{"status": status, "identifier": identifier, "expires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"challenges": []any{challenge()}})
	})
	mux.HandleFunc("/chall/1", func(w http.ResponseWriter, r *http.Request) {
		triggered.Store(true)
		w.Header().Set("Link", "<"+base+"/authz/1>;rel=\"up\"")
		reply(w, 200, base+"/chall/1", challenge())
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	base = server.URL
	return base + "/dir"
}

// The host's certbot ordering from the loopback authority above, over the
// webroot method, into private directories: its real output for each kind of
// failed challenge is read into the stage that failed, the address the
// authority reached where it names one, and the owner to fix it.
func TestLiveCertbotFailureIsReadByStage(t *testing.T) {
	certbot, err := exec.LookPath("certbot")
	if err != nil {
		t.Skip("certbot is not installed")
	}
	const fetch = "203.0.113.5: Fetching http://app.example.com/.well-known/acme-challenge/tok123tok123tok123tok123tok123AB: "
	for _, tc := range []struct {
		kind, detail, stage, owner string
	}{
		{"connection", fetch + "Timeout during connect (likely firewall problem)", StageConnect, "A firewall in front of port 80: this host's or the provider's"},
		{"dns", "DNS problem: NXDOMAIN looking up A for app.example.com - check that a DNS record exists for this domain", StageDNS, "The DNS provider or registrar"},
		{"unauthorized", "203.0.113.5: Invalid response from http://app.example.com/.well-known/acme-challenge/tok123tok123tok123tok123tok123AB: 404", StageChallenge, "The web server answering the name on port 80"},
		{"caa", "CAA record for app.example.com prevents issuance", StageCAA, "The domain's DNS zone (its CAA records)"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"www", "conf", "work", "logs"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			directory := fakeACME(t, map[string]any{"type": "urn:ietf:params:acme:error:" + tc.kind, "detail": tc.detail, "status": 400})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, certbot, "certonly", "--non-interactive", "--agree-tos", "--register-unsafely-without-email",
				"--webroot", "-w", filepath.Join(root, "www"), "-d", "app.example.com", "--server", directory,
				"--config-dir", filepath.Join(root, "conf"), "--work-dir", filepath.Join(root, "work"), "--logs-dir", filepath.Join(root, "logs"))
			cmd.Env = append(os.Environ(), "HOME="+root)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("certbot succeeded against an authority that fails every challenge:\n%s", out)
			}
			problems := DiagnoseIssuance(strings.Split(string(out), "\n"), func(ip net.IP) (bool, bool) {
				return ip.String() == "203.0.113.5", true
			})
			if len(problems) != 1 {
				t.Fatalf("problems = %+v from:\n%s", problems, out)
			}
			p := problems[0]
			if p.Domain != "app.example.com" || p.Stage != tc.stage || p.Owner != tc.owner || p.Detail != tc.detail {
				t.Fatalf("problem = %+v from:\n%s", p, out)
			}
			t.Logf("certbot said:\n%s", out)
		})
	}
}
