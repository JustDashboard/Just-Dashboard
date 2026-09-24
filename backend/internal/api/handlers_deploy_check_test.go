package api

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// The advisory check is asked with the capability that starts a run and
// writes nothing; Detect again reads the source for Build settings and
// carries a settings write's capability. A source change answers with what
// detection read there, compared with the plan.
func TestDeploymentCheckAndDetectRoutes(t *testing.T) {
	s := testServer(t)
	checkout := localCheckoutFixture(t)
	wireDeployPlanningAndSources(t, s, []string{filepath.Dir(checkout)})
	s.modules.deployChecker = deploy.NewDeploymentChecker(
		s.modules.deployRuns, s.modules.deployPlanning, s.modules.deploySources, s.modules.deployPreflight,
	)
	head, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	projectID, environmentID := seedCommittedDeployment(t, s, "checked-app",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeLocalCheckout, LocalPath: checkout, Subdirectory: "api"},
		deploy.SourceIdentity{Kind: deploy.SourceGit, LocalPath: checkout, Revision: strings.TrimSpace(string(head))},
	)
	base := "/api/v1/deploy/" + strconv.FormatInt(projectID, 10) + "/environments/" + strconv.FormatInt(environmentID, 10)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "check-limited", auth.RoleLimited)}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "check-viewer", auth.RoleReadOnly)}

	if response := viewer.do(http.MethodPost, base+"/check", `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("read-only check = %d %s, want 403", response.Code, response.Body.String())
	}
	checked := limited.do(http.MethodPost, base+"/check", `{}`, nil)
	if checked.Code != http.StatusOK {
		t.Fatalf("check = %d %s", checked.Code, checked.Body.String())
	}
	var result deploy.DeploymentCheckResult
	decodeDeployResponse(t, checked.Body.Bytes(), &result)
	if result.PlanRevision != 1 || result.SourceRevision != strings.TrimSpace(string(head)) || result.Findings == nil {
		t.Fatalf("check result = %#v", result)
	}
	if response := limited.do(http.MethodPost, base+"/check", `{"ref":"main"}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "ref_not_applicable") {
		t.Fatalf("a ref for a local checkout = %d %s", response.Code, response.Body.String())
	}
	var audited int
	// A refused attempt is still recorded; an answered check is a read.
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE path LIKE '%/check' AND status < 400`).Scan(&audited); err == nil && audited != 0 {
		t.Fatalf("the advisory check wrote %d audit entries", audited)
	}

	if response := limited.do(http.MethodPost, base+"/detect", `{}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("limited detect = %d %s, want 403", response.Code, response.Body.String())
	}
	detected := admin.do(http.MethodPost, base+"/detect", `{}`, nil)
	if detected.Code != http.StatusOK {
		t.Fatalf("detect = %d %s", detected.Code, detected.Body.String())
	}
	var proposal deploy.DetectionProposal
	decodeDeployResponse(t, detected.Body.Bytes(), &proposal)
	if proposal.Revision != 1 || proposal.Candidate == nil || proposal.Candidate.Recipe != "go" {
		t.Fatalf("proposal = %#v", proposal)
	}

	changed := doDeployJSON(t, admin, http.MethodPut, base+"/source", map[string]any{
		"revision": 1, "kind": "git", "mode": "local_checkout", "localPath": checkout, "subdirectory": "worker",
	})
	if changed.Code != http.StatusOK {
		t.Fatalf("source change = %d %s", changed.Code, changed.Body.String())
	}
	var updated struct {
		Revision int                       `json:"revision"`
		Proposal *deploy.DetectionProposal `json:"proposal"`
	}
	decodeDeployResponse(t, changed.Body.Bytes(), &updated)
	if updated.Revision != 2 || updated.Proposal == nil || updated.Proposal.Revision != 2 || updated.Proposal.Candidate == nil {
		t.Fatalf("source change result = %#v", updated)
	}
	var evidence string
	if err := s.Store.DB.QueryRow(`SELECT evidence_json FROM deploy_build_plans WHERE environment_id = ? AND revision = 2`, environmentID).
		Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence, `"name":"Go library in ."`) {
		t.Fatalf("the new revision kept the old source's evidence: %s", evidence)
	}
}
