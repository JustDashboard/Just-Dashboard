package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

var (
	scanTestImage = "sha256:" + strings.Repeat("a", 64)
	scanTestBase  = "sha256:" + strings.Repeat("b", 64)
	scanTestNewer = "sha256:" + strings.Repeat("c", 64)
)

// seedScannedRelease records a live release built by a recipe on one pinned
// base, scanned with the findings given and the base's tag in the state
// given.
func seedScannedRelease(t *testing.T, s *Server, projectID, environmentID, number int64, findings []imagescan.Finding, baseState string) int64 {
	t.Helper()
	provenance := `{"builder":{"method":"recipe","recipe":"node","baseImages":[{"reference":"node:22-alpine","digest":"` + scanTestBase + `","platforms":[]}]}}`
	now := time.Now().Unix()
	result, err := s.Store.DB.ExecContext(t.Context(), `INSERT INTO deploy_releases(project_id, environment_id, release_number, state,
		source_revision, source_identity_json, provenance_json, created_at, activated_at) VALUES(?, ?, ?, 'live', ?, '{"kind":"git"}', ?, ?, ?)`,
		projectID, environmentID, number, strings.Repeat("e", 40), provenance, now, now)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := s.Store.DB.ExecContext(t.Context(), `UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	current := scanTestBase
	if baseState == "outdated" {
		current = scanTestNewer
	}
	if err := s.modules.deployRuns.FinishImageScan(t.Context(), releaseID, deploy.ImageScanResult{
		State: deploy.ImageScanScanned, Scanner: imagescan.Version, Findings: findings,
		Database:    imagescan.DatabaseState{Installed: true, UpdatedAt: time.Now().Add(-5 * time.Hour)},
		Targets:     []deploy.ImageScanTarget{{Kind: "release", Reference: "jd-release", State: deploy.ImageScanScanned}},
		BaseUpdates: []deploy.ReleaseBaseUpdate{{Reference: "node:22-alpine", Pinned: scanTestBase, Current: current, State: baseState}},
	}); err != nil {
		t.Fatal(err)
	}
	return releaseID
}

// seedRecipeDeployment is a committed deployment of a remote Git repository
// built by a recipe: what pins a base, and what a rebuild re-pins.
func seedRecipeDeployment(t *testing.T, s *Server, name string) (int64, int64) {
	t.Helper()
	source, _ := json.Marshal(deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/shop.git", Ref: "main"})
	identity, _ := json.Marshal(deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("e", 40)})
	result, err := s.Store.DB.Exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'web', '', 'main', '', '', ?, 1, 0, 0)`, name, name+"-hook")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'blue_green', 0, 1, 0, 0)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at) VALUES(?, 1, 'git', '` + string(source) + `', 0, '` + string(identity) + `', 'sha256:seed', 0)`,
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'recipe', '{"method":"recipe"}', '{}', '{}', 'sha256:build-seed', 0)`,
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, '{"strategy":"blue_green"}', '{}', 'sha256:runtime-seed', 0)`,
	} {
		if _, err := s.Store.DB.Exec(statement, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

var zlibFinding = imagescan.Finding{
	ID: "CVE-2022-37434", Package: "zlib", Installed: "1.2.12-r0", Fixed: "1.2.12-r2",
	Severity: imagescan.SeverityCritical, Target: "alpine 3.12.12", FromBase: true,
}

func TestImageVulnerableIsAHostAlertKindWithNothingToSet(t *testing.T) {
	in, err := validateHostAlert(hostAlertWrite{Kind: "IMAGE_VULNERABLE", Params: hostAlertParams{Threshold: 50, Minutes: 3}})
	if err != nil || in.Kind != hostAlertImageVulnerable || in.Params != (hostAlertParams{}) {
		t.Fatalf("image_vulnerable = %+v, %v", in, err)
	}
	if page := hostAlertPage(hostAlertImageVulnerable, "env:7:12"); page != "/deploy/7#vulnerabilities" {
		t.Fatalf("page = %s", page)
	}
	if page := hostAlertPage(hostAlertImageVulnerable, "env:x"); page != "/deploy" {
		t.Fatalf("a malformed subject's page = %s", page)
	}
	if hostAlertTestDetail[hostAlertImageVulnerable] == "" || hostAlertTestLabel[hostAlertImageVulnerable] == "" {
		t.Fatal("the kind has no test message")
	}
}

// The reading is the alert's condition: fixable at the floor and a newer
// base fires; either half missing is a recovery; a scan not read yet, in
// flight or failed keeps the subject where it was.
func TestImageAlertReadingFiresOnAFixableFindingAndANewerBase(t *testing.T) {
	scan := func(state string, findings []imagescan.Finding, newer bool) *deploy.ImageScan {
		return &deploy.ImageScan{
			ReleaseNumber: 4, State: state, Floor: imagescan.SeverityHigh, NewerBase: newer, Rebuildable: true,
			Summary:     imagescan.Summarize(findings, imagescan.SeverityHigh),
			BaseUpdates: []deploy.ReleaseBaseUpdate{{Reference: "node:22-alpine", State: map[bool]string{true: "outdated", false: "current"}[newer]}},
		}
	}
	medium := imagescan.Finding{ID: "CVE-9", Package: "x", Installed: "1", Fixed: "2", Severity: imagescan.SeverityMedium}
	unfixed := imagescan.Finding{ID: "CVE-8", Package: "y", Installed: "1", Severity: imagescan.SeverityCritical}
	live := func(environment int64, s *deploy.ImageScan) deploy.LiveImageScan {
		return deploy.LiveImageScan{ProjectID: 7, ProjectName: "shop", EnvironmentID: environment, EnvironmentName: "production", Scan: s}
	}
	reading := imageAlertReading([]deploy.LiveImageScan{
		live(1, scan(deploy.ImageScanScanned, []imagescan.Finding{zlibFinding}, true)),
		live(2, scan(deploy.ImageScanScanned, []imagescan.Finding{zlibFinding}, false)),
		live(3, scan(deploy.ImageScanScanned, []imagescan.Finding{medium, unfixed}, true)),
		live(4, nil),
		live(5, scan(deploy.ImageScanScanning, []imagescan.Finding{zlibFinding}, true)),
		live(6, scan(deploy.ImageScanFailed, nil, true)),
	})
	if !reading.Judged || len(reading.Firing) != 1 || reading.Firing[0].Subject != "env:7:1" || reading.Firing[0].Label != "shop · production" {
		t.Fatalf("firing = %+v", reading.Firing)
	}
	detail := reading.Firing[0].Detail
	for _, want := range []string{"Release 4 has 1 critical finding with a fix", "CVE-2022-37434 in zlib 1.2.12-r0, fixed in 1.2.12-r2", "node:22-alpine now names a newer image", "Rebuild it"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the message does not say %q: %s", want, detail)
		}
	}
	if !strings.Contains(reading.Now["env:7:2"], "built on the image each of its bases names now") {
		t.Fatalf("no newer base = %q", reading.Now["env:7:2"])
	}
	if !strings.Contains(reading.Now["env:7:3"], "no high or worse finding with a fix") {
		t.Fatalf("only an unfixed critical and a medium = %q", reading.Now["env:7:3"])
	}
	for _, subject := range []string{"env:7:4", "env:7:5", "env:7:6"} {
		if !reading.Unknown[subject] {
			t.Fatalf("%s is not kept where it was: %+v", subject, reading)
		}
	}
}

// Through the server's own observer and the store: told once when the live
// release is found vulnerable, never again while it stays so, and once when
// a rebuilt release on a newer base is scanned clean.
func TestTheImageVulnerableRuleTellsOnceOnTheWayInAndOnceOnTheWayOut(t *testing.T) {
	h := newHostAlertHarness(t)
	ctx := context.Background()
	projectID, environmentID := seedDeployment(t, h.s, "shop")
	seedScannedRelease(t, h.s, projectID, environmentID, 1, []imagescan.Finding{zlibFinding}, "outdated")
	if _, err := h.a.CreateRule(ctx, hostAlertWrite{Kind: hostAlertImageVulnerable}); err != nil {
		t.Fatal(err)
	}
	h.evaluate(t)
	if len(h.sent) != 1 || h.sent[0].Event != deploy.NotificationEventHostFiring || h.sent[0].Host.Kind != hostAlertImageVulnerable ||
		h.sent[0].Host.Label != "shop · production" || h.sent[0].URL != fmt.Sprintf("https://dash.example.test/deploy/%d#vulnerabilities", projectID) {
		t.Fatalf("firing = %+v", h.sent)
	}
	h.now = h.now.Add(time.Minute)
	h.evaluate(t)
	if len(h.sent) != 1 {
		t.Fatalf("told again while it stayed vulnerable: %d", len(h.sent))
	}
	// The rebuild: release 2, on the newer base, without the finding.
	seedScannedRelease(t, h.s, projectID, environmentID, 2, nil, "current")
	h.now = h.now.Add(time.Minute)
	h.evaluate(t)
	if len(h.sent) != 2 || h.sent[1].Event != deploy.NotificationEventHostRecovered || !strings.Contains(h.sent[1].Host.Detail, "Release 2 has no high or worse finding") {
		t.Fatalf("recovery = %+v", h.sent)
	}
	// Scanning turned off: the environment leaves the rule's reading.
	if _, err := h.s.modules.deployRuns.PutImageScanPolicy(ctx, projectID, environmentID, deploy.ImageScanPolicy{Enabled: false}, "admin"); err != nil {
		t.Fatal(err)
	}
	h.evaluate(t)
	if len(h.sent) != 2 {
		t.Fatalf("a quiet environment that stopped scanning was told about: %+v", h.sent[2:])
	}
}

func TestTheVulnerabilitiesRoutesReadSaveAndAskForAScan(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedRecipeDeployment(t, s, "routes-shop")
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/vulnerabilities", projectID, environmentID)

	var empty imageScanReading
	w := admin.do(http.MethodGet, base, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &empty) != nil || !empty.Policy.Enabled || empty.Scan != nil || empty.Scanner != "Trivy "+imagescan.Version {
		t.Fatalf("an environment with no release: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodPost, base+"/scan", `{}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no_release") {
		t.Fatalf("a scan with no release: %d %s", w.Code, w.Body.String())
	}

	release := seedScannedRelease(t, s, projectID, environmentID, 1, []imagescan.Finding{zlibFinding}, "outdated")
	var reading imageScanReading
	w = admin.do(http.MethodGet, base, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &reading) != nil {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	if reading.Scan == nil || reading.ReleaseID != release || len(reading.Scan.Summary.Fixable) != 1 || !reading.Scan.Vulnerable() {
		t.Fatalf("scan = %+v", reading.Scan)
	}
	if len(reading.Releases) != 1 || reading.Releases[0].Fixable != 1 {
		t.Fatalf("badges = %+v", reading.Releases)
	}
	// The rebuild is the runs route's force_build, of the commit the live
	// release was built from, so only its bases move.
	if reading.Rebuild == nil || reading.Rebuild.Operation != deploy.OperationForceBuild || reading.Rebuild.SourceRevision != strings.Repeat("e", 40) {
		t.Fatalf("rebuild = %+v (%s)", reading.Rebuild, reading.RebuildReason)
	}

	if w := admin.do(http.MethodPut, base, `{"enabled":true,"severityFloor":"SEVERE"}`, nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "severity floor") {
		t.Fatalf("an invalid floor: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodPut, base, `{"enabled":true,"severityFloor":"critical"}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"severityFloor":"CRITICAL"`) {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	assertAudited(t, s, "deploy.vulnerabilities.update")

	w = admin.do(http.MethodPost, base+"/scan", `{}`, nil)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"state":"queued"`) {
		t.Fatalf("scan now: %d %s", w.Code, w.Body.String())
	}
	assertAudited(t, s, "deploy.vulnerabilities.scan")

	if w := admin.do(http.MethodPut, base, `{"enabled":false,"severityFloor":"HIGH"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("turn off: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodPost, base+"/scan", `{}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "scanning_off") {
		t.Fatalf("a scan with scanning off: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodGet, base+"?release=99999", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("another environment's release: %d", w.Code)
	}
}

func assertAudited(t *testing.T, s *Server, action string) {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND success = 1`, action).Scan(&count); err != nil || count == 0 {
		t.Fatalf("%s was not audited: %d %v", action, count, err)
	}
}
