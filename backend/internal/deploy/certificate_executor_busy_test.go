package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// certbot held by somebody else for longer than the step's bound fails the
// step in the step's own words — what it waited for, for how long, and that
// the run is to be deployed again — and not in certbot's, whose "another
// instance is already running" says nothing about whose or for how long.
func TestCertificateStepReportsATimedOutWaitForCertbotInItsOwnWords(t *testing.T) {
	busy := &proxysvc.CertbotBusyError{
		Holder: "Renewing app.example.com (started by alice from the Certificates page)",
		Waited: 5*time.Minute + 300*time.Millisecond,
	}
	logged := []string{}
	result := (&NormalizedStepExecutor{certificates: &certificateIssuerFake{
		err: fmt.Errorf("ordering app.example.com: %w", busy),
	}}).certificateStep(context.Background(), []string{"app.example.com"},
		func(_, text string) { logged = append(logged, text) })
	if result.State != StepFailed || result.ErrorCode != "certificate_certbot_busy" {
		t.Fatalf("a wait that ran out = %#v, want failed as certificate_certbot_busy", result)
	}
	want := "the certificate step waited 5m0s for certbot, which was busy with " +
		"Renewing app.example.com (started by alice from the Certificates page), and its time ran out; deploy again once that has finished"
	if result.ErrorMessage != want {
		t.Fatalf("the step says %q\nwant %q", result.ErrorMessage, want)
	}
	if !strings.Contains(string(result.Evidence), `"outcome":"failed"`) {
		t.Fatalf("evidence = %s", result.Evidence)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "Resolving a certificate for app.example.com") {
		t.Fatalf("transcript = %#v", logged)
	}
}
