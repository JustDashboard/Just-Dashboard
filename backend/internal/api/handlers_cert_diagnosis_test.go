package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A failed certbot job's own output is read into its problems by stage, for
// administrators; a job that is not certbot's is not read.
func TestCertJobDiagnosisReadsTheFailedRun(t *testing.T) {
	s := testServer(t)
	run := func(kind string) string {
		job := s.modules.jobs.Start(jobs.Spec{Kind: kind, Title: "Issuing a certificate for www.example.com", Target: "www.example.com", Timeout: time.Minute},
			func(ctx context.Context, out jobs.Emitter) error {
				out.Status("$ certbot certonly")
				for _, line := range []string{
					"Certbot failed to authenticate some domains (authenticator: webroot). The Certificate Authority reported these problems:",
					"  Domain: www.example.com",
					"  Type:   dns",
					"  Detail: DNS problem: NXDOMAIN looking up A for www.example.com - check that a DNS record exists for this domain",
				} {
					out.Line("stdout", line)
				}
				return errors.New("certbot exited 1 — the last lines above say why")
			})
		waitForJob(t, s, job.ID)
		return job.ID
	}
	id := run("certbot.issue")
	h := s.Routes()
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := reader.do(http.MethodGet, "/api/v1/certificates/jobs/"+id+"/diagnosis", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("reader: %d", w.Code)
	}
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	w := admin.do(http.MethodGet, "/api/v1/certificates/jobs/"+id+"/diagnosis", "", nil)
	var out struct {
		Status   jobs.Status                  `json:"status"`
		Problems []proxysvc.IssuanceDiagnosis `json:"problems"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}
	if out.Status != jobs.StatusFailed || len(out.Problems) != 1 || out.Problems[0].Stage != proxysvc.StageDNS || out.Problems[0].Domain != "www.example.com" {
		t.Fatalf("diagnosis = %+v", out)
	}
	if w := admin.do(http.MethodGet, "/api/v1/certificates/jobs/"+run("docker.pull")+"/diagnosis", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("another kind of job: %d", w.Code)
	}
}
