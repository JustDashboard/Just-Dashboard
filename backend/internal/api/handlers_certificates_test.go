package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The certificate routes, driven through the real router against a certbot
// the test owns: it lists the plugins it is given, logs every argv, and does
// whatever $JD_TEST_CERTBOT_DO says for anything else — wait for a file,
// replace a certificate, or nothing.

type fakeCertbotHost struct {
	letsencrypt string
	imported    string
	log         string
}

func useFakeCertbot(t *testing.T, plugins ...string) fakeCertbotHost {
	t.Helper()
	host := fakeCertbotHost{letsencrypt: t.TempDir(), imported: t.TempDir()}
	bin := t.TempDir()
	host.log = filepath.Join(bin, "argv.log")
	var listing strings.Builder
	for _, p := range plugins {
		fmt.Fprintf(&listing, "* %s\nInterfaces: Authenticator, Plugin\n\n", p)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins) cat <<'LIST'
%sLIST
exit 0 ;;
esac
if [ -n "$JD_TEST_CERTBOT_WAIT" ]; then
  while [ ! -f "$JD_TEST_CERTBOT_WAIT" ]; do sleep 0.02; done
fi
if [ -n "$JD_TEST_CERTBOT_REPLACE" ]; then cp "$JD_TEST_CERTBOT_REPLACE" "$JD_TEST_CERTBOT_TARGET"; fi
echo "Certificate not yet due for renewal; no action taken."
exit 0
`, host.log, listing.String())
	if err := os.WriteFile(filepath.Join(bin, "certbot"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_CERTBOT_WAIT", "")
	t.Setenv("JD_TEST_CERTBOT_REPLACE", "")
	t.Setenv("JD_ACME_DIRECTORY", "")
	t.Cleanup(proxysvc.UseCertificateDirsForTest(host.letsencrypt, host.imported))
	return host
}

// lineage writes a certbot lineage for names, as certbot lays one out, and
// returns the path of its cert.pem.
func (h fakeCertbotHost) lineage(t *testing.T, name string, names ...string) string {
	t.Helper()
	live := filepath.Join(h.letsencrypt, "live", name)
	for _, dir := range []string{live, filepath.Join(h.letsencrypt, "renewal")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	certPEM, _ := testCertificate(t, names)
	for _, file := range []string{"cert.pem", "fullchain.pem"} {
		if err := os.WriteFile(filepath.Join(live, file), []byte(certPEM), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	conf := fmt.Sprintf("cert = %[1]s/cert.pem\nprivkey = %[1]s/privkey.pem\nfullchain = %[1]s/fullchain.pem\n\n[renewalparams]\nauthenticator = webroot\nserver = https://acme-v02.api.letsencrypt.org/directory\n", live)
	if err := os.WriteFile(filepath.Join(h.letsencrypt, "renewal", name+".conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(live, "cert.pem")
}

func (h fakeCertbotHost) argv(t *testing.T) string {
	t.Helper()
	raw, _ := os.ReadFile(h.log)
	return string(raw)
}

func testCertificate(t *testing.T, names []string) (string, string) {
	t.Helper()
	return testCertificateUntil(t, names, time.Now().Add(80*24*time.Hour))
}

func testCertificateUntil(t *testing.T, names []string, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func decodeJob(t *testing.T, body []byte) jobs.Job {
	t.Helper()
	var job jobs.Job
	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatalf("not a job: %s", body)
	}
	return job
}

func jobText(t *testing.T, s *Server, id string) string {
	t.Helper()
	_, lines, _ := s.modules.jobs.Get(id)
	var text []string
	for _, l := range lines {
		text = append(text, l.Stream+": "+l.Text)
	}
	return strings.Join(text, "\n")
}

// A second certbot fails on the lock the first holds, and the page's console
// swapped the running job for the one about to fail. While one runs, every
// certbot route answers 409 with what is running, from any tab.
func TestCertbotJobsRefuseToRunTwice(t *testing.T) {
	useFakeCertbot(t, "webroot")
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("JD_TEST_CERTBOT_WAIT", release)
	c, s := newClient(t)

	w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("renew = %d: %s", w.Code, w.Body.String())
	}
	running := decodeJob(t, w.Body.Bytes())

	for _, call := range []struct{ path, body string }{
		{"/api/v1/certificates/issue", `{"domains":["other.example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html"}`},
		{"/api/v1/certificates/renew", `{"name":"","dryRun":true}`},
	} {
		w := c.do(http.MethodPost, call.path, call.body, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "certbot_busy") ||
			!strings.Contains(w.Body.String(), "Renewing app.example.com") {
			t.Fatalf("%s while certbot runs = %d: %s", call.path, w.Code, w.Body.String())
		}
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/revoke", `{"name":"app.example.com"}`,
		map[string]string{"X-Confirm": "revoke app.example.com"}); w.Code != http.StatusConflict {
		t.Fatalf("revoke while certbot runs = %d: %s", w.Code, w.Body.String())
	}
	if n := len(s.modules.jobs.List()); n != 1 {
		t.Fatalf("%d jobs exist; the refused ones must not start", n)
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if final := waitForJob(t, s, running.ID); final.Status != jobs.StatusSucceeded {
		t.Fatalf("the first job = %+v", final)
	}
	if w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com","dryRun":true}`, nil); w.Code != http.StatusAccepted {
		t.Fatalf("renew after the first finished = %d: %s", w.Code, w.Body.String())
	}
}

// The token used to be saved by a separate request before /issue, so a
// refused issuance left it on disk anyway. It travels with the issuance now
// and the job writes it first — never for a request that was refused.
func TestIssueSavesDNSCredentialsOnlyOnceTheJobStarts(t *testing.T) {
	host := useFakeCertbot(t, "webroot", "dns-cloudflare")
	c, s := newClient(t)
	saved := filepath.Join(host.letsencrypt, "jd-dns", "cloudflare.ini")
	issue := func(body string) (int, string) {
		w := c.do(http.MethodPost, "/api/v1/certificates/issue", body, nil)
		return w.Code, w.Body.String()
	}
	const token = "dns_cloudflare_api_token = s3cr3t-token"
	for name, body := range map[string]string{
		"a bad email":          `{"domains":["*.example.com"],"email":"nope","method":"dns","dnsProvider":"cloudflare","credentials":"` + token + `"}`,
		"a missing plugin":     `{"domains":["*.example.com"],"email":"ops@example.com","method":"dns","dnsProvider":"digitalocean","credentials":"dns_digitalocean_token = x"}`,
		"a wait over an hour":  `{"domains":["*.example.com"],"email":"ops@example.com","method":"dns","dnsProvider":"cloudflare","dnsWait":7200,"credentials":"` + token + `"}`,
		"credentials of blank": `{"domains":["*.example.com"],"email":"ops@example.com","method":"dns","dnsProvider":"cloudflare","credentials":"   "}`,
	} {
		if code, body := issue(body); code != http.StatusBadRequest {
			t.Fatalf("%s = %d: %s", name, code, body)
		}
		if _, err := os.Stat(filepath.Join(host.letsencrypt, "jd-dns")); !os.IsNotExist(err) {
			t.Fatalf("%s left credentials on disk", name)
		}
	}

	// Refused because certbot is busy: nothing written either.
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("JD_TEST_CERTBOT_WAIT", release)
	w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":""}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("renew = %d", w.Code)
	}
	busy := decodeJob(t, w.Body.Bytes())
	valid := `{"domains":["*.example.com"],"email":"ops@example.com","method":"dns","dnsProvider":"cloudflare","dnsWait":120,"credentials":"` + token + `"}`
	if code, body := issue(valid); code != http.StatusConflict {
		t.Fatalf("issue while busy = %d: %s", code, body)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Fatal("a request refused as busy saved its credentials")
	}
	os.WriteFile(release, nil, 0o600)
	waitForJob(t, s, busy.ID)
	t.Setenv("JD_TEST_CERTBOT_WAIT", "")

	code, body := issue(valid)
	if code != http.StatusAccepted {
		t.Fatalf("issue = %d: %s", code, body)
	}
	job := decodeJob(t, []byte(body))
	if final := waitForJob(t, s, job.ID); final.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v\n%s", final, jobText(t, s, job.ID))
	}
	st, err := os.Stat(saved)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials after the job: %v %v", st, err)
	}
	text := jobText(t, s, job.ID)
	if !strings.Contains(text, "Saved the Cloudflare credentials") || !strings.Contains(text, "--dns-cloudflare-propagation-seconds 120") {
		t.Fatalf("job output:\n%s", text)
	}
	if strings.Contains(text, "s3cr3t") || strings.Contains(host.argv(t), "s3cr3t") {
		t.Fatal("the token reached the job output or certbot's argv")
	}
}

// A test run is certbot's --dry-run, and says what it is.
func TestIssueTestRunIsADryRun(t *testing.T) {
	host := useFakeCertbot(t, "webroot")
	c, s := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/certificates/issue",
		`{"domains":["app.example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html","staging":true}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("issue = %d: %s", w.Code, w.Body.String())
	}
	job := decodeJob(t, w.Body.Bytes())
	if job.Title != "Test issuance for app.example.com" {
		t.Fatalf("title = %q", job.Title)
	}
	waitForJob(t, s, job.ID)
	argv := host.argv(t)
	if !strings.Contains(argv, "certonly --webroot -w /var/www/html") || !strings.Contains(argv, "--dry-run") || strings.Contains(argv, "--staging") {
		t.Fatalf("certbot ran as:\n%s", argv)
	}
	text := jobText(t, s, job.ID)
	if !strings.Contains(text, "A test run") || strings.Contains(text, "did not issue") {
		t.Fatalf("job output:\n%s", text)
	}
}

// certbot exits 0 whether it issued or kept what it had. A certificate it
// kept is said so in the job; one it replaced is not.
func TestIssueSaysWhenCertbotKeptTheCertificate(t *testing.T) {
	host := useFakeCertbot(t, "webroot")
	c, s := newClient(t)
	certPath := host.lineage(t, "app.example.com", "app.example.com")
	body := `{"domains":["app.example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html"}`

	w := c.do(http.MethodPost, "/api/v1/certificates/issue", body, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("issue = %d: %s", w.Code, w.Body.String())
	}
	kept := decodeJob(t, w.Body.Bytes())
	if final := waitForJob(t, s, kept.ID); final.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v", final)
	}
	if text := jobText(t, s, kept.ID); !strings.Contains(text, "certbot did not issue a new certificate") {
		t.Fatalf("a kept certificate read as issued:\n%s", text)
	}

	replacement := filepath.Join(t.TempDir(), "new.pem")
	newPEM, _ := testCertificate(t, []string{"app.example.com"})
	os.WriteFile(replacement, []byte(newPEM), 0o644)
	t.Setenv("JD_TEST_CERTBOT_REPLACE", replacement)
	t.Setenv("JD_TEST_CERTBOT_TARGET", certPath)
	w = c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com"}`, nil)
	renewed := decodeJob(t, w.Body.Bytes())
	waitForJob(t, s, renewed.ID)
	if text := jobText(t, s, renewed.ID); strings.Contains(text, "not due") {
		t.Fatalf("a renewed certificate read as kept:\n%s", text)
	}

	t.Setenv("JD_TEST_CERTBOT_REPLACE", "")
	w = c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com"}`, nil)
	again := decodeJob(t, w.Body.Bytes())
	waitForJob(t, s, again.ID)
	if text := jobText(t, s, again.ID); !strings.Contains(text, "app.example.com is not due for renewal yet") {
		t.Fatalf("a renewal that changed nothing read as done:\n%s", text)
	}
}

// A real issuance over a lineage renewed from a staging authority replaces
// it, says so in its title, and says what nginx is still serving: certonly
// writes the files and reloads nothing.
func TestIssueOverATestCertificateReplacesItAndSaysNginxHasNotSeenIt(t *testing.T) {
	host := useFakeCertbot(t, "webroot")
	c, s := newClient(t)
	certPath := host.lineage(t, "app.example.com", "app.example.com")
	conf := filepath.Join(host.letsencrypt, "renewal", "app.example.com.conf")
	raw, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	staged := strings.Replace(string(raw), "https://acme-v02.", "https://acme-staging-v02.", 1)
	if err := os.WriteFile(conf, []byte(staged), 0o644); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(t.TempDir(), "real.pem")
	realPEM, _ := testCertificate(t, []string{"app.example.com"})
	if err := os.WriteFile(replacement, []byte(realPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_TEST_CERTBOT_REPLACE", replacement)
	t.Setenv("JD_TEST_CERTBOT_TARGET", certPath)

	w := c.do(http.MethodPost, "/api/v1/certificates/issue",
		`{"domains":["app.example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("issue = %d: %s", w.Code, w.Body.String())
	}
	job := decodeJob(t, w.Body.Bytes())
	if job.Title != "Replacing the test certificate for app.example.com" {
		t.Fatalf("title = %q", job.Title)
	}
	if final := waitForJob(t, s, job.ID); final.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v", final)
	}
	if argv := host.argv(t); !strings.Contains(argv, "--force-renewal") || strings.Contains(argv, "--dry-run") {
		t.Fatalf("certbot ran as:\n%s", argv)
	}
	text := jobText(t, s, job.ID)
	if !strings.Contains(text, "certbot replaces it with a real one") ||
		!strings.Contains(text, "keeps serving the test one until nginx reloads") ||
		strings.Contains(text, "did not issue") {
		t.Fatalf("job output:\n%s", text)
	}
}

// With JD_ACME_DIRECTORY set, certbot's --dry-run rehearses with that
// authority rather than Let's Encrypt's staging one, and the job says which.
func TestIssueTestRunNamesTheAuthorityItRehearsesWith(t *testing.T) {
	host := useFakeCertbot(t, "webroot")
	t.Setenv("JD_ACME_DIRECTORY", "https://ca.internal:9000/acme/acme/directory")
	c, s := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/certificates/issue",
		`{"domains":["app.example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html","staging":true}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("issue = %d: %s", w.Code, w.Body.String())
	}
	job := decodeJob(t, w.Body.Bytes())
	waitForJob(t, s, job.ID)
	if argv := host.argv(t); !strings.Contains(argv, "--dry-run --server https://ca.internal:9000/acme/acme/directory") {
		t.Fatalf("certbot ran as:\n%s", argv)
	}
	text := jobText(t, s, job.ID)
	if !strings.Contains(text, "with https://ca.internal:9000/acme/acme/directory and saves nothing") ||
		strings.Contains(text, "Let's Encrypt") {
		t.Fatalf("job output:\n%s", text)
	}
}

// Importing under a name already in use is a 409 that says what is there;
// replacing it is a separate, explicit request.
func TestCertImportRefusesAnExistingNameUnlessReplacing(t *testing.T) {
	host := useFakeCertbot(t)
	c, _ := newClient(t)
	certPEM, keyPEM := testCertificate(t, []string{"bought.example.com"})
	body, _ := json.Marshal(map[string]any{"name": "bought", "certificate": certPEM, "key": keyPEM})

	if w := c.do(http.MethodPost, "/api/v1/certificates/import", string(body), nil); w.Code != http.StatusOK {
		t.Fatalf("first import = %d: %s", w.Code, w.Body.String())
	}
	w := c.do(http.MethodPost, "/api/v1/certificates/import", string(body), nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "certificate_exists") ||
		!strings.Contains(w.Body.String(), "bought.example.com") {
		t.Fatalf("second import = %d: %s", w.Code, w.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"name": "bought", "certificate": certPEM, "key": keyPEM, "replace": true})
	w = c.do(http.MethodPost, "/api/v1/certificates/import", string(body), nil)
	var res proxysvc.ImportResult
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil || !res.Replaced {
		t.Fatalf("replace = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(host.imported, "bought", "privkey.pem.bak")); err != nil {
		t.Fatalf("the replaced key was not kept: %v", err)
	}
}

// Caddy's release copies are out of what the operator reads as an inventory,
// the list and the security posture, and in what deployments read: on a
// Docker Caddy host a copy is the only certificate covering a release's
// domains, and hidden from them every release failed its activation and read
// as having no certificate.
func TestCaddyEvidenceIsOutOfTheInventoryAndInTheDeploymentsView(t *testing.T) {
	host := useFakeCertbot(t)
	c, _ := newClient(t)
	evidence := "caddy-0da2f3126af1d760c968313b"
	for dir, domain := range map[string]string{evidence: "app.jd.test", "bought": "bought.example.com"} {
		certPEM, keyPEM := testCertificateUntil(t, []string{domain}, time.Now().Add(10*24*time.Hour))
		if err := os.MkdirAll(filepath.Join(host.imported, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for file, content := range map[string]string{"fullchain.pem": certPEM, "privkey.pem": keyPEM} {
			if err := os.WriteFile(filepath.Join(host.imported, dir, file), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	w := c.do(http.MethodGet, "/api/v1/certificates/", "", nil)
	var listed []proxysvc.Certificate
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &listed) != nil {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	if len(listed) != 1 || listed[0].Name != "bought" {
		t.Fatalf("the inventory is %+v, want the import alone", listed)
	}

	w = c.do(http.MethodGet, "/api/v1/security/posture", "", nil)
	var posture netsec.Posture
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &posture) != nil {
		t.Fatalf("posture = %d: %s", w.Code, w.Body.String())
	}
	var ids []string
	for _, f := range posture.Findings {
		ids = append(ids, f.ID)
	}
	joined := strings.Join(ids, " ")
	if !strings.Contains(joined, "tls.expiring.bought") || strings.Contains(joined, evidence) {
		t.Fatalf("posture findings %v: want the import's expiry and not the copy's", ids)
	}

	w = c.do(http.MethodGet, "/api/v1/deploy/hostname?hostname=app.jd.test", "", nil)
	var suggestion struct {
		Covered         bool   `json:"covered"`
		CertificateName string `json:"certificateName"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &suggestion) != nil {
		t.Fatalf("hostname = %d: %s", w.Code, w.Body.String())
	}
	if !suggestion.Covered || suggestion.CertificateName != evidence {
		t.Fatalf("app.jd.test reads %+v, want covered by %s", suggestion, evidence)
	}
}
