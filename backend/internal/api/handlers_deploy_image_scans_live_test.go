package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

// The watch end to end on a Docker host: the real scanner image, the real
// vulnerability database fetched from its registry into a volume of the
// test's own, two releases built "on" alpine — one on 3.12, end of life and
// carrying zlib's CVE-2022-37434 with a published fix, recorded as pinned on
// alpine:3.22 at a digest the tag no longer names; one on today's 3.22 — and
// the host alert's reading over what was stored. The first is found
// vulnerable and told; the second has nothing at the floor and is not.
//
// It pulls alpine:3.12, alpine:3.22 and the scanner's image when they are
// not here (and removes those it pulled), downloads the database (about 140
// MB) into a volume it removes, and leaves no container behind.
func TestLiveImageScanStoresFindingsAndRaisesTheAlert(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	h := newHostAlertHarness(t)
	s := h.s
	ctx := context.Background()
	docker := dockerx.New("unix:///var/run/docker.sock")
	s.modules.docker = docker

	for _, image := range []string{"alpine:3.12", "alpine:3.22", imagescan.Image} {
		if exec.Command("docker", "image", "inspect", image).Run() == nil {
			continue
		}
		run(t, "docker", "pull", "-q", image)
		t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", image).Run() })
	}
	volume := fmt.Sprintf("jd-image-scan-db-live-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", volume).Run() })
	scanner := imagescan.New(imagescan.DockerDaemon{Client: docker}, filepath.Join(t.TempDir(), "image-scans")).WithVolume(volume)
	s.modules.imageScanner = scanner
	watch := deploy.NewImageScanWatcher(s.modules.deployRuns, scanner, deploy.NewDockerArtifactBackend(docker), s.Log)
	s.modules.imageScans = watch

	type local struct{ id, digest string }
	inspect := func(image string) local {
		t.Helper()
		out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}} {{index .RepoDigests 0}}", image).Output()
		if err != nil {
			t.Fatalf("inspect %s: %v", image, err)
		}
		id, repoDigest, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		_, digest, _ := strings.Cut(repoDigest, "@")
		return local{id: id, digest: digest}
	}
	old, fresh := inspect("alpine:3.12"), inspect("alpine:3.22")

	release := func(name string, image, base local) (int64, int64, int64) {
		projectID, environmentID := seedRecipeDeployment(t, s, name)
		provenance := `{"builder":{"method":"recipe","recipe":"static","baseImages":[{"reference":"alpine:3.22","digest":"` + base.digest + `","platforms":[]}]}}`
		now := time.Now().Unix()
		result, err := s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, state, source_revision,
			source_identity_json, provenance_json, created_at, activated_at) VALUES(?, ?, 1, 'live', ?, '{"kind":"git"}', ?, ?, ?)`,
			projectID, environmentID, strings.Repeat("e", 40), provenance, now, now)
		if err != nil {
			t.Fatal(err)
		}
		releaseID, _ := result.LastInsertId()
		if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, created_at)
			VALUES(?, 'image', 'alpine', ?, ?, ?)`, releaseID, image.digest, `{"configDigest":"`+image.id+`"}`, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
			t.Fatal(err)
		}
		return projectID, environmentID, releaseID
	}
	// Pinned on alpine:3.22 at 3.12's digest: the tag has moved on since,
	// which is what a base looks like a while after the build.
	oldProject, oldEnvironment, oldRelease := release("live-old-alpine", old, old)
	_, _, freshRelease := release("live-new-alpine", fresh, fresh)

	started := time.Now()
	if err := watch.ScanRelease(ctx, oldRelease, deploy.ImageScanTriggerBuild); err != nil {
		t.Fatal(err)
	}
	t.Logf("the first scan, with the database download, took %s", time.Since(started).Round(time.Second))
	if err := watch.ScanRelease(ctx, freshRelease, deploy.ImageScanTriggerBuild); err != nil {
		t.Fatal(err)
	}

	scan, err := s.modules.deployRuns.ImageScan(ctx, oldRelease)
	if err != nil || scan == nil {
		t.Fatalf("the old release's scan = %+v, %v", scan, err)
	}
	if scan.State != deploy.ImageScanScanned {
		t.Fatalf("the old release was not scanned: %s (%s); targets %+v", scan.State, scan.Reason, scan.Targets)
	}
	var zlib *imagescan.Finding
	for i, finding := range scan.Summary.Fixable {
		if finding.ID == "CVE-2022-37434" {
			zlib = &scan.Summary.Fixable[i]
		}
	}
	if zlib == nil || zlib.Package != "zlib" || zlib.Severity != imagescan.SeverityCritical || zlib.Fixed == "" || !zlib.FromBase {
		t.Fatalf("zlib's CVE was not stored as a fixable critical from the base: %+v", scan.Summary.Fixable)
	}
	if len(scan.Targets) != 2 || scan.Targets[0].OS != "alpine 3.12.12" || !scan.Targets[0].EndOfLife || scan.Targets[1].Kind != "base" {
		t.Fatalf("targets = %+v", scan.Targets)
	}
	if scan.Scanner != imagescan.Version || scan.DatabaseUpdatedAt == nil || time.Since(*scan.DatabaseUpdatedAt) > 72*time.Hour {
		t.Fatalf("scanner %q, database built %v", scan.Scanner, scan.DatabaseUpdatedAt)
	}
	if len(scan.BaseUpdates) != 1 || scan.BaseUpdates[0].State != "outdated" || scan.BaseUpdates[0].Current != fresh.digest {
		t.Fatalf("the registry's alpine:3.22 = %+v (local 3.22 is %s)", scan.BaseUpdates, fresh.digest)
	}
	if !scan.Vulnerable() {
		t.Fatalf("a fixable critical on a moved base is the alert's condition: %+v", scan)
	}

	clean, err := s.modules.deployRuns.ImageScan(ctx, freshRelease)
	if err != nil || clean == nil || clean.State != deploy.ImageScanScanned {
		t.Fatalf("the new release's scan = %+v, %v", clean, err)
	}
	if n := clean.Summary.Counts.AtLeast(imagescan.SeverityHigh); n != 0 || len(clean.Summary.Fixable) != 0 || clean.NewerBase || clean.Vulnerable() {
		t.Fatalf("a current alpine:3.22 read as vulnerable: %d at HIGH, %+v", n, clean)
	}

	if _, err := h.a.CreateRule(ctx, hostAlertWrite{Kind: hostAlertImageVulnerable}); err != nil {
		t.Fatal(err)
	}
	h.evaluate(t)
	if len(h.sent) != 1 || h.sent[0].Host.Label != "live-old-alpine · production" ||
		h.sent[0].Host.Subject != imageAlertSubject(oldProject, oldEnvironment) ||
		!strings.Contains(h.sent[0].Host.Detail, "CVE-2022-37434") {
		t.Fatalf("the alert = %+v", h.sent)
	}

	// The route reads the same, with the database's age.
	w := h.admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/vulnerabilities", oldProject, oldEnvironment), "", nil)
	var reading imageScanReading
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &reading) != nil || !reading.Database.Installed || reading.Scan == nil || !reading.Scan.Vulnerable() {
		t.Fatalf("the route: %d %s", w.Code, w.Body.String())
	}

	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+imagescan.RoleLabel).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the scans left containers behind: %s", out)
	}
}
