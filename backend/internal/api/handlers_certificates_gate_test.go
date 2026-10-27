package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// One certbot at a time across the dashboard: the Certificates page's jobs
// and a deployment run's own order take the same turn, each waiting for the
// other and saying so, instead of the loser failing on certbot's lock.

func awaitCondition(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A page job started while a deployment run has certbot is accepted as
// always, says in its console whom it waits for, keeps the page's own
// exclusivity meanwhile, and runs certbot once the run has let go.
func TestCertbotJobWaitsForADeploymentRunsCertbot(t *testing.T) {
	host := useFakeCertbot(t, "webroot")
	c, s := newClient(t)
	const run = "a deployment run issuing a certificate for app.example.com"
	release, err := proxysvc.HoldCertbot(context.Background(), run, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("renew = %d: %s", w.Code, w.Body.String())
	}
	job := decodeJob(t, w.Body.Bytes())
	waiting := "status: Waiting for certbot, which is busy with " + run + "; this job continues when it finishes."
	awaitCondition(t, "the job to say whom it waits for", func() bool { return strings.Contains(jobText(t, s, job.ID), waiting) })
	if j, _, _ := s.modules.jobs.Get(job.ID); j.Status != jobs.StatusRunning {
		t.Fatalf("a waiting job reads %s", j.Status)
	}
	if strings.Contains(host.argv(t), "renew") {
		t.Fatalf("certbot ran while the run had it:\n%s", host.argv(t))
	}
	// The page is still one job at a time, from any tab.
	if w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"","dryRun":true}`, nil); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "certbot_busy") {
		t.Fatalf("a second job while one waits = %d: %s", w.Code, w.Body.String())
	}

	release()
	awaitCondition(t, "the job to end", func() bool {
		j, _, _ := s.modules.jobs.Get(job.ID)
		return j.Status != jobs.StatusRunning
	})
	if j, _, _ := s.modules.jobs.Get(job.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("after the run let go: %+v\n%s", j, jobText(t, s, job.ID))
	}
	if !strings.Contains(host.argv(t), "renew --non-interactive --cert-name app.example.com") {
		t.Fatalf("certbot did not renew once the run let go:\n%s", host.argv(t))
	}
}

// A deployment run whose certificate step finds the page's job running
// waits for it, says so in its transcript, and orders once the job is over;
// the job's certbot ran first, the run's second, never both at once.
func TestDeploymentCertificateWaitsForThePagesCertbotJob(t *testing.T) {
	host := useFakeCertbot(t, "webroot", "dns-cloudflare")
	c, s := newClient(t)
	root := t.TempDir()
	s.modules.proxy = proxysvc.New(root, filepath.Join(root, "Caddyfile"))
	t.Cleanup(proxysvc.UseDeploymentNSLookupForTest(func(_ context.Context, name string) ([]*net.NS, error) {
		if name == "example.test" {
			return []*net.NS{{Host: "ada.ns.cloudflare.com."}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}))
	if _, err := proxysvc.WriteDNSCredentials("cloudflare", "dns_cloudflare_api_token = secret-zone-token"); err != nil {
		t.Fatal(err)
	}
	// What the run's certonly leaves: a certificate covering the wildcard,
	// under its lineage.
	stage := t.TempDir()
	certPEM, keyPEM := testCertificate(t, []string{"*.preview.example.test", "preview.example.test"})
	for name, content := range map[string]string{"fullchain.pem": certPEM, "privkey.pem": keyPEM} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("JD_TEST_CERTBOT_STAGE", stage)
	t.Setenv("JD_TEST_CERTBOT_ISSUE", filepath.Join(host.letsencrypt, "live", "preview.example.test"))

	// The page's job holds certbot until the file appears.
	releaseFile := filepath.Join(t.TempDir(), "release")
	t.Setenv("JD_TEST_CERTBOT_WAIT", releaseFile)
	w := c.do(http.MethodPost, "/api/v1/certificates/renew", `{"name":"app.example.com"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("renew = %d: %s", w.Code, w.Body.String())
	}
	job := decodeJob(t, w.Body.Bytes())
	awaitCondition(t, "the job to run certbot", func() bool { return strings.Contains(host.argv(t), "renew") })

	var mu sync.Mutex
	var logged []string
	transcript := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(logged, "\n")
	}
	type outcome struct {
		result proxysvc.DeploymentCertificate
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.modules.proxy.EnsureDeploymentCertificate(context.Background(), []string{"*.preview.example.test"},
			func(stream, line string) {
				mu.Lock()
				defer mu.Unlock()
				logged = append(logged, stream+": "+line)
			})
		done <- outcome{result, err}
	}()
	waiting := "status: Waiting for certbot, which is busy with " + job.Title + " (started by " + job.StartedBy +
		" from the Certificates page); the certificate step continues when it finishes."
	awaitCondition(t, "the run to say whom it waits for", func() bool { return strings.Contains(transcript(), waiting) })
	select {
	case o := <-done:
		t.Fatalf("the run went ahead while the page's job had certbot: %+v, %v", o.result, o.err)
	case <-time.After(150 * time.Millisecond):
	}
	if strings.Contains(host.argv(t), "certonly") {
		t.Fatalf("the run ordered while the page's job had certbot:\n%s", host.argv(t))
	}

	if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var o outcome
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the run did not go ahead once the job was over:\n%s", jobText(t, s, job.ID))
	}
	if o.err != nil || o.result.Outcome != proxysvc.CertificateIssued || o.result.Method != "dns-cloudflare" {
		t.Fatalf("after the wait: %+v, %v\n%s", o.result, o.err, transcript())
	}
	if j, _, _ := s.modules.jobs.Get(job.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("the page's job: %+v\n%s", j, jobText(t, s, job.ID))
	}
	argv := host.argv(t)
	if renewed, ordered := strings.Index(argv, "renew"), strings.Index(argv, "certonly"); renewed < 0 || ordered < renewed {
		t.Fatalf("certbot ran in the wrong order:\n%s", argv)
	}
}
