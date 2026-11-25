package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

var (
	scanReleaseImageID = "sha256:" + strings.Repeat("a", 64)
	scanNodeBase       = "sha256:" + strings.Repeat("b", 64)
	scanNodeBaseNow    = "sha256:" + strings.Repeat("c", 64)
	scanAlpineBase     = "sha256:" + strings.Repeat("d", 64)
)

// seedScanRelease records a release built by the Node recipe on two pinned
// bases, with its image's local id, as the build step records one.
func seedScanRelease(t *testing.T, f *releaseStoreFixture, number int64, live bool) int64 {
	t.Helper()
	provenance := mustJSON(map[string]any{"builder": PreparedBuild{
		Method: BuildRecipe, Recipe: "node",
		BaseImages: []ResolvedImage{
			{Reference: "node:22-alpine", Digest: scanNodeBase},
			{Reference: "alpine:3.22", Digest: scanAlpineBase},
		},
	}})
	return seedScanReleaseWith(t, f, number, live, string(provenance), `{"kind":"git","revision":"`+strings.Repeat("e", 40)+`"}`)
}

func seedScanReleaseWith(t *testing.T, f *releaseStoreFixture, number int64, live bool, provenance, identity string) int64 {
	t.Helper()
	state := "retained"
	if live {
		state = "live"
	}
	result, err := f.base.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, state, source_revision,
		source_identity_json, provenance_json, created_at, activated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.projectID, f.envID, number, state, strings.Repeat("e", 40), identity, provenance, f.now.Unix(), f.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	if _, err := f.base.DB.Exec(`INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, created_at)
		VALUES(?, 'image', ?, ?, ?, ?)`, id, fmt.Sprintf("jd-release-e%d-r%d:latest", f.envID, number), scanReleaseImageID,
		`{"configDigest":"`+scanReleaseImageID+`"}`, f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if live {
		if _, err := f.base.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, id, f.envID); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestScanningIsOnFromHighByDefaultAndTheFloorIsValidated(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	policy, err := f.runs.ImageScanPolicy(ctx, f.envID)
	if err != nil || !policy.Enabled || policy.SeverityFloor != imagescan.SeverityHigh {
		t.Fatalf("an unsaved environment's terms = %+v, %v", policy, err)
	}
	for _, floor := range []string{"UNKNOWN", "SEVERE", "none"} {
		if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: true, SeverityFloor: floor}, "admin"); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("floor %q was accepted: %v", floor, err)
		}
	}
	if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID+99, ImageScanPolicy{}, "admin"); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("another environment = %v", err)
	}
	saved, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: false, SeverityFloor: "critical"}, "admin")
	if err != nil || saved.Enabled || saved.SeverityFloor != imagescan.SeverityCritical || saved.UpdatedBy != "admin" || saved.UpdatedAt == nil {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
}

func TestScansAreClaimedAskedForFirstThenTheLiveReleaseDaily(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	live := seedScanRelease(t, f, 2, true)
	older := seedScanRelease(t, f, 1, false)

	// Nothing asked for: the live release, never scanned, is due.
	id, trigger, err := f.runs.ClaimImageScan(ctx)
	if err != nil || id != live || trigger != ImageScanTriggerSchedule {
		t.Fatalf("claimed %d %q %v", id, trigger, err)
	}
	if err := f.runs.FinishImageScan(ctx, live, ImageScanResult{State: ImageScanScanned}); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := f.runs.ClaimImageScan(ctx); id != 0 {
		t.Fatalf("a release scanned a minute ago was claimed again: %d", id)
	}

	// A scan asked for comes first, and asking twice keeps one in the queue.
	if queued, err := f.runs.QueueImageScan(ctx, older, ImageScanTriggerManual); err != nil || !queued {
		t.Fatalf("queue = %v %v", queued, err)
	}
	if queued, _ := f.runs.QueueImageScan(ctx, older, ImageScanTriggerManual); queued {
		t.Fatal("a queued scan was queued again")
	}
	if id, trigger, _ := f.runs.ClaimImageScan(ctx); id != older || trigger != ImageScanTriggerManual {
		t.Fatalf("claimed %d %q", id, trigger)
	}
	// A scan in flight is not queued under itself, and a restart puts it back.
	if queued, _ := f.runs.QueueImageScan(ctx, older, ImageScanTriggerManual); queued {
		t.Fatal("a scan in flight was queued again")
	}
	if err := f.runs.RequeueInterruptedImageScans(ctx); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := f.runs.ClaimImageScan(ctx); id != older {
		t.Fatalf("the interrupted scan was not run again: %d", id)
	}
	if err := f.runs.FinishImageScan(ctx, older, ImageScanResult{State: ImageScanFailed, Reason: "Docker went away"}); err != nil {
		t.Fatal(err)
	}

	// A day on, the live release is due again.
	f.now = f.now.Add(25 * time.Hour)
	if id, trigger, _ := f.runs.ClaimImageScan(ctx); id != live || trigger != ImageScanTriggerSchedule {
		t.Fatalf("a day later claimed %d %q", id, trigger)
	}
	if err := f.runs.FinishImageScan(ctx, live, ImageScanResult{State: ImageScanScanned}); err != nil {
		t.Fatal(err)
	}

	// Turned off, nothing is claimed — not the daily scan, not one asked for.
	if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: false}, "admin"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(25 * time.Hour)
	_, _ = f.runs.QueueImageScan(ctx, older, ImageScanTriggerManual)
	if id, _, _ := f.runs.ClaimImageScan(ctx); id != 0 {
		t.Fatalf("an environment that turned scanning off had %d claimed", id)
	}
	// Turned back on, the live release is asked for at once.
	if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: true}, "admin"); err != nil {
		t.Fatal(err)
	}
	if scan, _ := f.runs.ImageScan(ctx, live); scan == nil || scan.State != ImageScanQueued {
		t.Fatalf("turning scanning on did not ask for the live release: %+v", scan)
	}
}

func TestABaseIsScannedByItsRepositoryAndDigest(t *testing.T) {
	t.Parallel()
	for reference, want := range map[string]string{
		"node:22-alpine":                       "node",
		"registry.example:5000/team/app:1.2":   "registry.example:5000/team/app",
		"registry.example:5000/team/app":       "registry.example:5000/team/app",
		"ghcr.io/gleam-lang/gleam:v1@sha256:1": "ghcr.io/gleam-lang/gleam",
		"mcr.microsoft.com/dotnet/aspnet:8.0":  "mcr.microsoft.com/dotnet/aspnet",
	} {
		if got := imageRepository(reference); got != want {
			t.Errorf("%s = %s, want %s", reference, got, want)
		}
	}
}

func TestAPreviewIsNeverScanned(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 1, true)
	if _, err := f.base.DB.Exec(`UPDATE deploy_environments SET kind = 'preview' WHERE id = ?`, f.envID); err != nil {
		t.Fatal(err)
	}
	_, _ = f.runs.QueueImageScan(ctx, release, ImageScanTriggerBuild)
	if id, _, _ := f.runs.ClaimImageScan(ctx); id != 0 {
		t.Fatalf("a preview's release was claimed: %d", id)
	}
	if scans, _ := f.runs.LiveImageScans(ctx); len(scans) != 0 {
		t.Fatalf("a preview is followed by the alert: %+v", scans)
	}
}

func TestAScanIsReadAtItsEnvironmentsFloorAndAFailedOneKeepsTheLastFindings(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 3, true)
	findings := []imagescan.Finding{
		{ID: "CVE-1", Package: "openssl", Installed: "3.0.1", Fixed: "3.0.2", Severity: imagescan.SeverityCritical, FromBase: true},
		{ID: "CVE-2", Package: "lodash", Installed: "4.17.20", Fixed: "4.17.21", Severity: imagescan.SeverityMedium},
		{ID: "CVE-3", Package: "bash", Installed: "5.1", Severity: imagescan.SeverityHigh},
	}
	database := imagescan.DatabaseState{Installed: true, UpdatedAt: f.now.Add(-9 * time.Hour)}
	if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{
		State: ImageScanScanned, Scanner: imagescan.Version, Database: database, Findings: findings,
		Targets:     []ImageScanTarget{{Kind: "release", Reference: "jd-release", State: ImageScanScanned}},
		BaseUpdates: []ReleaseBaseUpdate{{Reference: "node:22-alpine", Pinned: scanNodeBase, Current: scanNodeBaseNow, State: "outdated"}},
	}); err != nil {
		t.Fatal(err)
	}
	scan, err := f.runs.ImageScan(ctx, release)
	if err != nil || scan == nil {
		t.Fatalf("scan = %+v, %v", scan, err)
	}
	if scan.ReleaseNumber != 3 || scan.Floor != imagescan.SeverityHigh || scan.DatabaseAgeHours != 9 || scan.Scanner != imagescan.Version {
		t.Fatalf("scan = %+v", scan)
	}
	if scan.Summary.Counts != (imagescan.Counts{Critical: 1, High: 1, Medium: 1}) || len(scan.Summary.Fixable) != 1 || len(scan.Summary.Unfixed) != 1 {
		t.Fatalf("summary at HIGH = %+v", scan.Summary)
	}
	if !scan.NewerBase || !scan.Rebuildable || !scan.Vulnerable() {
		t.Fatalf("a fixable critical on an outdated recipe base is the alert's condition: %+v", scan)
	}
	badges, err := f.runs.ImageScanBadges(ctx, f.envID)
	if err != nil || len(badges) != 1 || badges[0].AtFloor != 2 || badges[0].Fixable != 1 || !badges[0].NewerBase {
		t.Fatalf("badges = %+v, %v", badges, err)
	}

	// From MEDIUM up, the lodash finding is listed too.
	if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: true, SeverityFloor: "MEDIUM"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if scan, _ := f.runs.ImageScan(ctx, release); len(scan.Summary.Fixable) != 2 || scan.Floor != "MEDIUM" {
		t.Fatalf("at MEDIUM = %+v", scan.Summary.Fixable)
	}
	// From CRITICAL up with no fixable critical, nothing fires.
	if _, err := f.runs.PutImageScanPolicy(ctx, f.projectID, f.envID, ImageScanPolicy{Enabled: true, SeverityFloor: "CRITICAL"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{State: ImageScanScanned, Findings: findings[1:], BaseUpdates: []ReleaseBaseUpdate{{State: "outdated"}}}); err != nil {
		t.Fatal(err)
	}
	if scan, _ := f.runs.ImageScan(ctx, release); scan.Vulnerable() {
		t.Fatalf("no fixable critical, yet vulnerable at CRITICAL: %+v", scan.Summary)
	}

	// A scan that fails keeps what the last one found.
	if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{State: ImageScanFailed, Reason: "the scanner exited with 1"}); err != nil {
		t.Fatal(err)
	}
	failed, _ := f.runs.ImageScan(ctx, release)
	if failed.State != ImageScanFailed || failed.Reason != "the scanner exited with 1" || failed.Summary.Counts.High != 1 {
		t.Fatalf("failed = %+v", failed)
	}
	if failed.Vulnerable() {
		t.Fatal("a failed scan is not a verdict")
	}
}

func TestAFixableFindingWithoutANewerBaseIsNotTheAlert(t *testing.T) {
	t.Parallel()
	scan := ImageScan{State: ImageScanScanned, Floor: imagescan.SeverityHigh, Summary: imagescan.Summarize([]imagescan.Finding{
		{ID: "CVE-1", Package: "openssl", Installed: "1", Fixed: "2", Severity: imagescan.SeverityHigh, FromBase: true},
	}, imagescan.SeverityHigh)}
	if scan.Vulnerable() {
		t.Fatal("no newer base: a rebuild would pin the same digest, so there is nothing to tell")
	}
	scan.NewerBase = true
	if !scan.Vulnerable() {
		t.Fatal("a fixable high in the base and a newer base is the alert")
	}
	scan.State = ImageScanScanning
	if scan.Vulnerable() {
		t.Fatal("a scan in flight is not a verdict")
	}
	// The same finding in what the application or a tool brought is not
	// the base's to fix, however new the base.
	scan.State = ImageScanScanned
	scan.Summary = imagescan.Summarize([]imagescan.Finding{
		{ID: "CVE-1", Package: "stdlib", Installed: "v1.20.7", Fixed: "1.21.11", Severity: imagescan.SeverityCritical,
			Class: "lang-pkgs", Dependency: imagescan.DependencyDevelopment},
		{ID: "CVE-2", Package: "next", Installed: "16.4.6", Fixed: "16.4.7", Severity: imagescan.SeverityHigh,
			Class: "lang-pkgs", Dependency: imagescan.DependencyDirect},
	}, imagescan.SeverityHigh)
	if scan.Vulnerable() {
		t.Fatal("a newer base was the alert for findings it cannot fix")
	}
}

// Each finding says whose it is and what the reader can do: the
// application's package an upgrade, a base's a rebuild while its tag names a
// newer image, a package the build installed a rebuild, a development tool's
// nothing. Only what can be done is counted on the release's row.
func TestAScanSaysWhoseEachFindingIsAndWhatCanBeDone(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 5, true)
	findings := []imagescan.Finding{
		{ID: "CVE-1", Package: "openssl", Installed: "3.0.1", Fixed: "3.0.2", Severity: imagescan.SeverityHigh,
			Class: "os-pkgs", Target: "alpine 3.22.1", FromBase: true},
		{ID: "CVE-2024-24790", Package: "stdlib", Installed: "v1.20.7", Fixed: "1.21.11", Severity: imagescan.SeverityCritical,
			Class: "lang-pkgs", Path: "app/node_modules/@esbuild/linux-x64/bin/esbuild",
			Dependency: imagescan.DependencyDevelopment, Via: "drizzle-kit"},
		{ID: "CVE-3", Package: "next", Installed: "16.4.6", Fixed: "16.4.7", Severity: imagescan.SeverityHigh,
			Class: "lang-pkgs", Path: "app/node_modules/next/package.json", Dependency: imagescan.DependencyDirect, Via: "next"},
		{ID: "CVE-4", Package: "curl", Installed: "8.1.0-r0", Fixed: "8.1.2-r0", Severity: imagescan.SeverityHigh,
			Class: "os-pkgs", Target: "alpine 3.22.1"},
		{ID: "CVE-5", Package: "postcss", Installed: "8.4.30", Severity: imagescan.SeverityHigh,
			Class: "lang-pkgs", Path: "app/node_modules/postcss/package.json", Dependency: imagescan.DependencyIndirect, Via: "next"},
	}
	finish := func(state string) {
		t.Helper()
		if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{
			State: ImageScanScanned, Findings: findings,
			BaseUpdates: []ReleaseBaseUpdate{{Reference: "node:22-alpine", Pinned: scanNodeBase, Current: scanNodeBaseNow, State: state}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	finish("outdated")
	scan, err := f.runs.ImageScan(ctx, release)
	if err != nil || scan == nil {
		t.Fatalf("scan = %+v, %v", scan, err)
	}
	actions := map[string]string{}
	origins := map[string]string{}
	for _, finding := range append(append([]imagescan.Finding{}, scan.Summary.Fixable...), scan.Summary.Unfixed...) {
		actions[finding.Package], origins[finding.Package] = finding.Action, finding.Origin
	}
	wantActions := map[string]string{"openssl": "rebuild", "stdlib": "", "next": "upgrade", "curl": "rebuild", "postcss": ""}
	wantOrigins := map[string]string{"openssl": "base", "stdlib": "development", "next": "app", "curl": "app", "postcss": "app"}
	for name := range wantActions {
		if actions[name] != wantActions[name] || origins[name] != wantOrigins[name] {
			t.Errorf("%s = %q from %q, want %q from %q", name, actions[name], origins[name], wantActions[name], wantOrigins[name])
		}
	}
	if scan.Origins["app"] != (imagescan.Counts{High: 3}) || scan.Origins["development"] != (imagescan.Counts{Critical: 1}) ||
		scan.Origins["base"] != (imagescan.Counts{High: 1}) {
		t.Fatalf("origins = %+v", scan.Origins)
	}
	if scan.Actionable != (imagescan.Counts{High: 3}) {
		t.Fatalf("actionable = %+v", scan.Actionable)
	}
	badges, _ := f.runs.ImageScanBadges(ctx, f.envID)
	if len(badges) != 1 || badges[0].AtFloor != 5 || badges[0].Fixable != 4 || badges[0].Actionable != 3 {
		t.Fatalf("badges = %+v", badges)
	}

	// With the base current, its finding waits on whoever publishes it.
	finish("current")
	scan, _ = f.runs.ImageScan(ctx, release)
	for _, finding := range scan.Summary.Fixable {
		if finding.Package == "openssl" && finding.Action != "" {
			t.Fatalf("a current base's finding = %q", finding.Action)
		}
	}
	if scan.Actionable != (imagescan.Counts{High: 2}) || scan.Vulnerable() {
		t.Fatalf("with a current base: actionable %+v, vulnerable %v", scan.Actionable, scan.Vulnerable())
	}
}

// A scan read before paths were kept is due again at once, bases and all,
// and until then a binary under node_modules reads as a development tool's.
func TestAScanReadBeforePathsWereKeptIsDueAgain(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 6, true)
	esbuild := imagescan.Finding{ID: "CVE-2024-24790", Package: "stdlib", Installed: "v1.20.7", Fixed: "1.21.11",
		Severity: imagescan.SeverityCritical, Class: "lang-pkgs", Target: "app/node_modules/@esbuild/linux-x64/bin/esbuild"}
	if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{State: ImageScanScanned, Findings: []imagescan.Finding{esbuild}}); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := f.runs.ClaimImageScan(ctx); id != 0 {
		t.Fatalf("a scan read a minute ago was claimed again: %d", id)
	}
	// As 0.9.0 left it: no reading, and counts without origins.
	if _, err := f.base.DB.Exec(`UPDATE deploy_image_scans SET reading = 0, counts_json = '{"all":{"critical":1},"fixable":{"critical":1}}' WHERE release_id = ?`, release); err != nil {
		t.Fatal(err)
	}
	scan, _ := f.runs.ImageScan(ctx, release)
	if len(scan.Summary.Fixable) != 1 || scan.Summary.Fixable[0].Origin != imagescan.OriginDevelopment || scan.Actionable.Total() != 0 ||
		scan.Origins["development"] != (imagescan.Counts{Critical: 1}) {
		t.Fatalf("an old scan's binary = %+v, origins %+v", scan.Summary.Fixable, scan.Origins)
	}
	if id, trigger, _ := f.runs.ClaimImageScan(ctx); id != release || trigger != ImageScanTriggerSchedule {
		t.Fatalf("an old reading was not due again: %d %q", id, trigger)
	}
}

type digestResolver map[string]string

func (r digestResolver) ResolveImage(_ context.Context, reference, _ string) (ResolvedImage, error) {
	if digest, ok := r[reference]; ok {
		return ResolvedImage{Reference: reference, Digest: digest}, nil
	}
	return ResolvedImage{}, errors.New("manifest unknown")
}

func TestABaseIsComparedWithWhatItsTagNamesInTheRegistryNow(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 1, true)
	updates, err := f.runs.ReleaseBaseUpdates(ctx, digestResolver{"node:22-alpine": scanNodeBaseNow}, release)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Reference != "node:22-alpine" || updates[0].State != "outdated" ||
		updates[0].Pinned != scanNodeBase || updates[0].Current != scanNodeBaseNow {
		t.Fatalf("node = %+v", updates)
	}
	if updates[1].State != "unknown" || !strings.Contains(updates[1].Reason, "manifest unknown") {
		t.Fatalf("an unreadable registry = %+v", updates[1])
	}
	same, _ := f.runs.ReleaseBaseUpdates(ctx, digestResolver{"node:22-alpine": scanNodeBase, "alpine:3.22": scanAlpineBase}, release)
	if same[0].State != "current" || same[1].State != "current" {
		t.Fatalf("unmoved tags = %+v", same)
	}
	none, _ := f.runs.ReleaseBaseUpdates(ctx, nil, release)
	if none[0].State != "unknown" {
		t.Fatalf("without Docker = %+v", none)
	}

	// A release that is an upstream image is compared by its own tag.
	upstream := seedScanReleaseWith(t, f, 2, false, `{"builder":{"method":"image"}}`,
		`{"kind":"image","repository":"alpine:3.12","digest":"`+scanAlpineBase+`"}`)
	image, _ := f.runs.ReleaseBaseUpdates(ctx, digestResolver{"alpine:3.12": scanNodeBaseNow}, upstream)
	if len(image) != 1 || image[0].Reference != "alpine:3.12" || image[0].State != "outdated" {
		t.Fatalf("an image release = %+v", image)
	}
}

// fakeImageScanner answers each reference with its findings, and records
// what it was asked to pull.
type fakeImageScanner struct {
	mu       sync.Mutex
	findings map[string][]imagescan.Finding
	fail     map[string]error
	asked    []string
}

func (s *fakeImageScanner) Scan(_ context.Context, ref string, pull bool) (*imagescan.Scanned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, fmt.Sprintf("%s pull=%v", ref, pull))
	if err := s.fail[ref]; err != nil {
		return nil, err
	}
	return &imagescan.Scanned{
		Report:   &imagescan.Report{Scanner: imagescan.Version, OS: "alpine 3.20.1", Findings: append([]imagescan.Finding(nil), s.findings[ref]...)},
		Database: imagescan.DatabaseState{Installed: true, UpdatedAt: time.Date(2026, 9, 4, 6, 0, 0, 0, time.UTC)},
	}, nil
}

func TestTheWatchScansTheReleaseAndItsBasesAndCarriesTheBasesForwardDaily(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 4, true)
	openssl := imagescan.Finding{ID: "CVE-1", Package: "openssl", Installed: "3.0.1", Fixed: "3.0.2", Severity: imagescan.SeverityHigh, Target: "alpine 3.20.1"}
	lodash := imagescan.Finding{ID: "CVE-2", Package: "lodash", Installed: "4.17.20", Fixed: "4.17.21", Severity: imagescan.SeverityHigh, Target: "app/package-lock.json"}
	scanner := &fakeImageScanner{findings: map[string][]imagescan.Finding{
		scanReleaseImageID:         {openssl, lodash},
		"node@" + scanNodeBase:     {openssl},
		"alpine@" + scanAlpineBase: {},
	}, fail: map[string]error{}}
	watch := NewImageScanWatcher(f.runs, scanner, digestResolver{"node:22-alpine": scanNodeBaseNow, "alpine:3.22": scanAlpineBase}, nil)

	if err := watch.ScanRelease(ctx, release, ImageScanTriggerBuild); err != nil {
		t.Fatal(err)
	}
	want := []string{
		scanReleaseImageID + " pull=false",
		"node@" + scanNodeBase + " pull=true",
		"alpine@" + scanAlpineBase + " pull=true",
	}
	if !slices.Equal(scanner.asked, want) {
		t.Fatalf("asked %v", scanner.asked)
	}
	scan, _ := f.runs.ImageScan(ctx, release)
	if scan.State != ImageScanScanned || len(scan.Targets) != 3 || scan.Targets[1].Kind != "base" || scan.Targets[1].Counts.High != 1 {
		t.Fatalf("scan = %+v", scan)
	}
	if len(scan.Summary.Fixable) != 2 || !scan.Summary.Fixable[1].FromBase && !scan.Summary.Fixable[0].FromBase {
		t.Fatalf("openssl is the base's: %+v", scan.Summary.Fixable)
	}
	for _, finding := range scan.Summary.Fixable {
		if finding.FromBase != (finding.Package == "openssl") {
			t.Fatalf("%s from base = %v", finding.Package, finding.FromBase)
		}
	}
	if !scan.Vulnerable() || scan.BaseUpdates[0].State != "outdated" || scan.BaseUpdates[1].State != "current" {
		t.Fatalf("bases = %+v", scan.BaseUpdates)
	}

	// The daily scan reads the release image again and carries the pinned
	// bases' findings forward rather than pulling them again.
	scanner.asked = nil
	if err := watch.ScanRelease(ctx, release, ImageScanTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scanner.asked, []string{scanReleaseImageID + " pull=false"}) {
		t.Fatalf("the daily scan asked %v", scanner.asked)
	}
	daily, _ := f.runs.ImageScan(ctx, release)
	if len(daily.Targets) != 3 || daily.Targets[1].Counts.High != 1 {
		t.Fatalf("the carried bases = %+v", daily.Targets)
	}
	for _, finding := range daily.Summary.Fixable {
		if finding.FromBase != (finding.Package == "openssl") {
			t.Fatalf("after the daily scan %s from base = %v", finding.Package, finding.FromBase)
		}
	}

	// A scan read before paths were kept has bases with nothing to match
	// paths against: its next daily scan reads them again.
	if _, err := f.base.DB.Exec(`UPDATE deploy_image_scans SET reading = 0 WHERE release_id = ?`, release); err != nil {
		t.Fatal(err)
	}
	scanner.asked = nil
	if err := watch.ScanRelease(ctx, release, ImageScanTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scanner.asked, want) {
		t.Fatalf("after an old reading the daily scan asked %v", scanner.asked)
	}

	// An image that is gone fails the scan and keeps the last findings.
	scanner.fail[scanReleaseImageID] = fmt.Errorf("%w: it is no longer on this server", imagescan.ErrUnavailable)
	if err := watch.ScanRelease(ctx, release, ImageScanTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	failed, _ := f.runs.ImageScan(ctx, release)
	if failed.State != ImageScanFailed || !strings.Contains(failed.Reason, "no longer on this server") || failed.Summary.Counts.High != 2 {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestABuildsReleaseIsQueuedOnceWhenItsRunSucceeds(t *testing.T) {
	t.Parallel()
	f := newReleaseStoreFixture(t)
	ctx := context.Background()
	release := seedScanRelease(t, f, 1, true)
	watch := NewImageScanWatcher(f.runs, &fakeImageScanner{}, nil, nil)
	watch.RunFinished(ctx, EngineRun{State: RunFailed, ReleaseID: release})
	if scan, _ := f.runs.ImageScan(ctx, release); scan != nil {
		t.Fatalf("a failed run queued a scan: %+v", scan)
	}
	watch.RunFinished(ctx, EngineRun{State: RunSucceeded, ReleaseID: release})
	scan, _ := f.runs.ImageScan(ctx, release)
	if scan == nil || scan.State != ImageScanQueued || scan.Trigger != ImageScanTriggerBuild {
		t.Fatalf("scan = %+v", scan)
	}
	if err := f.runs.FinishImageScan(ctx, release, ImageScanResult{State: ImageScanScanned}); err != nil {
		t.Fatal(err)
	}
	// A restart of a release already scanned asks for nothing.
	watch.RunFinished(ctx, EngineRun{State: RunSucceeded, ReleaseID: release})
	if scan, _ := f.runs.ImageScan(ctx, release); scan.State != ImageScanScanned {
		t.Fatalf("a restart queued a second scan: %+v", scan)
	}
	// Without a scanner the observer does nothing at all.
	idle := NewImageScanWatcher(f.runs, nil, nil, nil)
	idle.RunFinished(ctx, EngineRun{State: RunSucceeded, ReleaseID: release})
	if idle.Available() {
		t.Fatal("a watch without a scanner says it is available")
	}
}
