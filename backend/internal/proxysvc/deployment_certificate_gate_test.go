package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A run that finds certbot the Certificates page's waits for it, saying whom
// for in its transcript, and orders once it is free; one whose own bound
// runs out first gives up naming the holder, never in certbot's words.
func TestDeploymentIssuanceWaitsForThePagesCertbot(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	t.Setenv("JD_ACME_DIRECTORY", "")
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(map[string][]string{"example.test": {"ada.ns.cloudflare.com"}}, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	argv := fakeDNSCertbot(t, letsencrypt, "webroot", "dns-cloudflare")
	if _, err := WriteDNSCredentials("cloudflare", "dns_cloudflare_api_token = secret-zone-token"); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	ordered := func() bool {
		raw, _ := os.ReadFile(argv)
		return strings.Contains(string(raw), "certonly")
	}

	release, err := HoldCertbot(context.Background(), pageRenewal, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logged []string
	log := func(_, line string) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, line)
	}
	transcript := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(logged, "\n")
	}
	type outcome struct {
		result DeploymentCertificate
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := service.EnsureDeploymentCertificate(context.Background(), []string{"*.preview.example.test"}, log)
		done <- outcome{result, err}
	}()
	waiting := "Waiting for certbot, which is busy with " + pageRenewal + "; the certificate step continues when it finishes."
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(transcript(), waiting); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the run never said whom it waits for:\n%s", transcript())
		}
	}
	select {
	case o := <-done:
		t.Fatalf("the run went ahead while the page had certbot: %+v, %v", o.result, o.err)
	case <-time.After(150 * time.Millisecond):
	}
	if ordered() {
		t.Fatal("certbot was asked to order while the page had it")
	}
	release()
	var o outcome
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not go ahead once certbot was free")
	}
	if o.err != nil || o.result.Outcome != CertificateIssued || o.result.Method != "dns-cloudflare" || !ordered() {
		t.Fatalf("after the wait: %+v, %v\n%s", o.result, o.err, transcript())
	}
	if strings.Count(transcript(), waiting) != 1 {
		t.Fatalf("the wait was said more than once:\n%s", transcript())
	}

	// A certificate the page's job left is reused once the turn comes
	// round, rather than ordered again.
	raw, _ := os.ReadFile(argv)
	release, err = HoldCertbot(context.Background(), pageRenewal, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()
	if again, err := service.EnsureDeploymentCertificate(context.Background(), []string{"pr-7.preview.example.test"}, nil); err != nil || again.Outcome != CertificateReused {
		t.Fatalf("a name under the wildcard = %+v, %v", again, err)
	}
	if after, _ := os.ReadFile(argv); string(after) != string(raw) {
		t.Fatalf("certbot was asked again for a name the wildcard covers:\n%s", after)
	}

	// The step's own bound runs out first: the refusal names the holder.
	release, err = HoldCertbot(context.Background(), pageRenewal, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	logged = nil
	_, err = service.EnsureDeploymentCertificate(ctx, []string{"*.other.example.test"}, log)
	var busy *CertbotBusyError
	if !errors.As(err, &busy) || busy.Holder != pageRenewal || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a wait that ran out = %v", err)
	}
	if strings.Contains(err.Error(), "Another instance of Certbot") || !strings.Contains(transcript(), waiting) {
		t.Fatalf("a wait that ran out reads %q, transcript:\n%s", err, transcript())
	}
}
