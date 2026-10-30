package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

// The base-image vulnerability watch: every release's image, and the base
// images the dashboard pinned it on, read by a scanner for known
// vulnerabilities after the build and again every day while it is live.
//
// A deployment is rebuilt when its code changes. Its base image changes
// without it: the distribution fixes OpenSSL, the tag the recipe names moves
// to an image with the fix, and the release keeps serving from the digest it
// was pinned to the day it was built. The watch reads two facts and puts
// them side by side — a finding that has a fixed version, and a base image
// whose tag now names a newer digest — because together they say a rebuild
// is likely to fix something, which is the only thing the reader can do.
//
// The scanning is internal/imagescan's; this file is what is kept of it and
// when: one row per release (its latest scan) and one environment's terms.

const (
	ImageScanQueued   = "queued"
	ImageScanScanning = "scanning"
	ImageScanScanned  = "scanned"
	ImageScanFailed   = "failed"

	ImageScanTriggerBuild    = "build"
	ImageScanTriggerSchedule = "schedule"
	ImageScanTriggerManual   = "manual"

	// imageScanInterval is how often a live release is scanned again: the
	// database it is read against is rebuilt every few hours, and a finding
	// published today is one the reader should hear about tomorrow at the
	// latest.
	imageScanInterval = 24 * time.Hour
	// imageScanRetry is how soon a scan that failed — Docker away, the
	// database not yet downloaded — is tried again.
	imageScanRetry = time.Hour
	// maxStoredFindings bounds what one row keeps, worst first.
	maxStoredFindings = 1000
)

// ImageScanPolicy is one environment's terms. Scanning is on unless an
// environment turns it off; the floor is the lowest severity the panel
// lists, the releases list counts and the alert is raised at.
type ImageScanPolicy struct {
	Enabled       bool       `json:"enabled"`
	SeverityFloor string     `json:"severityFloor"`
	UpdatedBy     string     `json:"updatedBy,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

// DefaultImageScanPolicy scans and counts from HIGH up.
func DefaultImageScanPolicy() ImageScanPolicy {
	return ImageScanPolicy{Enabled: true, SeverityFloor: imagescan.SeverityHigh}
}

func validateImageScanPolicy(policy ImageScanPolicy) (ImageScanPolicy, error) {
	policy.SeverityFloor = strings.ToUpper(strings.TrimSpace(policy.SeverityFloor))
	if policy.SeverityFloor == "" {
		policy.SeverityFloor = imagescan.SeverityHigh
	}
	if !imagescan.ValidFloor(policy.SeverityFloor) {
		return policy, fmt.Errorf("%w: the severity floor is CRITICAL, HIGH, MEDIUM or LOW", ErrInvalidPlan)
	}
	return policy, nil
}

func (s *OrchestrationStore) ImageScanPolicy(ctx context.Context, environmentID int64) (ImageScanPolicy, error) {
	policy := DefaultImageScanPolicy()
	var enabled int
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT enabled, severity_floor, updated_by, updated_at
		  FROM deploy_image_scan_policies WHERE environment_id = ?`, environmentID).
		Scan(&enabled, &policy.SeverityFloor, &policy.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	policy.Enabled, policy.UpdatedAt = enabled != 0, unixTimePtr(updatedAt)
	return policy, nil
}

// PutImageScanPolicy saves an environment's terms. Turning scanning back on
// asks for a scan of the live release at once rather than at the next day's.
func (s *OrchestrationStore) PutImageScanPolicy(ctx context.Context, projectID, environmentID int64, policy ImageScanPolicy, actor string) (ImageScanPolicy, error) {
	policy, err := validateImageScanPolicy(policy)
	if err != nil {
		return policy, err
	}
	var liveRelease int64
	err = s.db.QueryRowContext(ctx, `SELECT live_release_id FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		environmentID, projectID).Scan(&liveRelease)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, ErrEnvironmentNotFound
	}
	if err != nil {
		return policy, err
	}
	before, err := s.ImageScanPolicy(ctx, environmentID)
	if err != nil {
		return policy, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_image_scan_policies(environment_id, enabled, severity_floor, updated_by, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(environment_id) DO UPDATE SET enabled = excluded.enabled, severity_floor = excluded.severity_floor,
		  updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		environmentID, boolInt(policy.Enabled), policy.SeverityFloor, actor, s.now().UTC().Unix()); err != nil {
		return policy, err
	}
	if policy.Enabled && !before.Enabled && liveRelease != 0 {
		if _, err := s.QueueImageScan(ctx, liveRelease, ImageScanTriggerManual); err != nil {
			return policy, err
		}
	}
	return s.ImageScanPolicy(ctx, environmentID)
}

// ImageScanTarget is one image a scan read: the release's own, or a base it
// was built on.
type ImageScanTarget struct {
	// Kind is "release" for the image that runs, "base" for one it was
	// built from.
	Kind string `json:"kind"`
	// Service names a Compose service's image.
	Service   string           `json:"service,omitempty"`
	Reference string           `json:"reference"`
	Digest    string           `json:"digest,omitempty"`
	State     string           `json:"state"`
	Reason    string           `json:"reason,omitempty"`
	OS        string           `json:"os,omitempty"`
	EndOfLife bool             `json:"endOfLife,omitempty"`
	Counts    imagescan.Counts `json:"counts"`
	// ScannedAt is when this target was read; a base carried over from the
	// scan after the build keeps that scan's time.
	ScannedAt *time.Time `json:"scannedAt,omitempty"`
	// findings is what the target's scan found, kept only to mark the
	// release's own findings that come from a base.
	findings []imagescan.Finding
}

// ReleaseBaseUpdate is one image a release was pinned on, against what its
// tag names in the registry now. State is "current", "outdated" (the tag
// names a newer digest), "unknown" (the registry could not say) or "local"
// (an image built here, with no registry tag to move).
type ReleaseBaseUpdate struct {
	Reference string     `json:"reference"`
	Pinned    string     `json:"pinned,omitempty"`
	Current   string     `json:"current,omitempty"`
	State     string     `json:"state"`
	Reason    string     `json:"reason,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}

// ImageScan is a release's latest scan as the panel, the releases list and
// the alert read it.
type ImageScan struct {
	ReleaseID     int64  `json:"releaseId"`
	ReleaseNumber int64  `json:"releaseNumber"`
	EnvironmentID int64  `json:"environmentId"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	Trigger       string `json:"trigger,omitempty"`
	Scanner       string `json:"scanner,omitempty"`
	// DatabaseUpdatedAt is when the database the scan read was built, and
	// DatabaseAgeHours how old it was when the scan read it.
	DatabaseUpdatedAt *time.Time `json:"databaseUpdatedAt,omitempty"`
	DatabaseAgeHours  float64    `json:"databaseAgeHours,omitempty"`
	// Floor is the environment's floor the summary was read at.
	Floor       string              `json:"floor"`
	Summary     imagescan.Summary   `json:"summary"`
	Targets     []ImageScanTarget   `json:"targets"`
	BaseUpdates []ReleaseBaseUpdate `json:"baseUpdates"`
	// NewerBase says a base the release was pinned on has a newer digest
	// under its tag; Rebuildable that a rebuild re-pins it (a recipe or
	// static build, or a Dockerfile whose bases a build pulls again).
	NewerBase   bool       `json:"newerBase"`
	Rebuildable bool       `json:"rebuildable"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`

	findings []imagescan.Finding
	method   BuildMethod
}

// Vulnerable reports the alert's condition: a fixable finding at or above
// the floor, and a newer digest of a base the release was pinned on.
func (scan ImageScan) Vulnerable() bool {
	return scan.State == ImageScanScanned && scan.NewerBase && scan.Summary.FixableAtLeast(scan.Floor)
}

// QueueImageScan asks for a release to be scanned. A scan already in flight
// is left to finish; one that is queued keeps its place.
func (s *OrchestrationStore) QueueImageScan(ctx context.Context, releaseID int64, trigger string) (bool, error) {
	var environmentID int64
	err := s.db.QueryRowContext(ctx, `SELECT environment_id FROM deploy_releases WHERE id = ?`, releaseID).Scan(&environmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrArtifactMissing
	}
	if err != nil {
		return false, err
	}
	now := s.now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_image_scans(release_id, environment_id, state, trigger, requested_at)
		VALUES(?, ?, 'queued', ?, ?)
		ON CONFLICT(release_id) DO UPDATE SET state = 'queued', trigger = excluded.trigger, requested_at = excluded.requested_at
		 WHERE deploy_image_scans.state NOT IN ('queued', 'scanning')`,
		releaseID, environmentID, trigger, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClaimImageScan takes the next scan to run: the oldest one asked for, then
// the live release scanned longest ago (or never) once a day has passed. An
// environment whose terms turned scanning off, an archived one and a
// preview are skipped — a preview is production's code at a pull request's
// head, and lives for an afternoon. Zero is nothing to do.
func (s *OrchestrationStore) ClaimImageScan(ctx context.Context) (int64, string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := s.now().UTC()
	var releaseID int64
	var trigger string
	err := s.db.QueryRowContext(ctx, `
		SELECT q.release_id, q.trigger FROM deploy_image_scans q
		  JOIN deploy_environments e ON e.id = q.environment_id
		  LEFT JOIN deploy_image_scan_policies p ON p.environment_id = e.id
		 WHERE q.state = 'queued' AND e.archived_at = 0 AND e.kind <> 'preview' AND COALESCE(p.enabled, 1) = 1
		 ORDER BY q.requested_at, q.release_id LIMIT 1`).Scan(&releaseID, &trigger)
	if errors.Is(err, sql.ErrNoRows) {
		trigger = ImageScanTriggerSchedule
		err = s.db.QueryRowContext(ctx, `
			SELECT e.live_release_id FROM deploy_environments e
			  LEFT JOIN deploy_image_scan_policies p ON p.environment_id = e.id
			  LEFT JOIN deploy_image_scans q ON q.release_id = e.live_release_id
			 WHERE e.live_release_id <> 0 AND e.archived_at = 0 AND e.kind <> 'preview' AND COALESCE(p.enabled, 1) = 1
			   AND (q.release_id IS NULL OR (q.state = 'scanned' AND q.finished_at <= ?)
			        OR (q.state = 'failed' AND q.finished_at <= ?))
			 ORDER BY COALESCE(q.finished_at, 0), e.id LIMIT 1`,
			now.Add(-imageScanInterval).Unix(), now.Add(-imageScanRetry).Unix()).Scan(&releaseID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	var environmentID int64
	if err := s.db.QueryRowContext(ctx, `SELECT environment_id FROM deploy_releases WHERE id = ?`, releaseID).Scan(&environmentID); err != nil {
		return 0, "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_image_scans(release_id, environment_id, state, trigger, requested_at, started_at)
		VALUES(?, ?, 'scanning', ?, ?, ?)
		ON CONFLICT(release_id) DO UPDATE SET state = 'scanning', trigger = excluded.trigger, started_at = excluded.started_at`,
		releaseID, environmentID, trigger, now.Unix(), now.Unix()); err != nil {
		return 0, "", err
	}
	return releaseID, trigger, nil
}

// RequeueInterruptedImageScans puts back a scan a stopped process was
// running, so it is run again rather than left reading as in flight.
func (s *OrchestrationStore) RequeueInterruptedImageScans(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_image_scans SET state = 'queued' WHERE state = 'scanning'`)
	return err
}

// ImageScanImage is one image of a release to scan.
type ImageScanImage struct {
	Kind      string
	Service   string
	Reference string
	Digest    string
	// Ref is what the daemon is asked for: the local image id when the
	// release recorded one, its reference otherwise; a base's is its
	// reference pinned at its digest.
	Ref string
}

// ImageScanTargets are a release's images — every image artifact, one per
// Compose service — and the base images its recipe was pinned on.
func (s *OrchestrationStore) ImageScanTargets(ctx context.Context, releaseID int64) ([]ImageScanImage, []ImageScanImage, error) {
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, nil, err
	}
	images := []ImageScanImage{}
	seen := map[string]bool{}
	for _, artifact := range release.Artifacts {
		if artifact.Kind != ArtifactImage || artifact.State != "available" {
			continue
		}
		var metadata struct {
			ConfigDigest string `json:"configDigest"`
			Service      string `json:"service"`
		}
		_ = json.Unmarshal(artifact.Metadata, &metadata)
		ref := artifact.Reference
		if contentDigestRE.MatchString(metadata.ConfigDigest) {
			ref = metadata.ConfigDigest
		}
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		images = append(images, ImageScanImage{
			Kind: "release", Service: metadata.Service, Reference: artifact.Reference, Digest: artifact.Digest, Ref: ref,
		})
	}
	bases := []ImageScanImage{}
	for _, base := range releaseBaseImages(release) {
		// By its repository and digest alone: the tag has moved since, which
		// is the point, and the daemon finds the image by its digest.
		ref := imageRepository(base.Reference) + "@" + base.Digest
		if seen[ref] {
			continue
		}
		seen[ref] = true
		bases = append(bases, ImageScanImage{Kind: "base", Reference: base.Reference, Digest: base.Digest, Ref: ref})
	}
	return images, bases, nil
}

// releaseBaseImages are the bases the release's build pinned, as its
// provenance recorded the prepared build.
func releaseBaseImages(release *ReleaseWithArtifacts) []ResolvedImage {
	var provenance struct {
		Builder PreparedBuild `json:"builder"`
	}
	if json.Unmarshal(release.Release.Provenance, &provenance) != nil {
		return nil
	}
	out := []ResolvedImage{}
	for _, base := range provenance.Builder.BaseImages {
		if base.Reference != "" && contentDigestRE.MatchString(base.Digest) {
			out = append(out, base)
		}
	}
	return out
}

func releaseBuildMethod(release *ReleaseWithArtifacts) BuildMethod {
	var provenance struct {
		Builder PreparedBuild `json:"builder"`
	}
	_ = json.Unmarshal(release.Release.Provenance, &provenance)
	return provenance.Builder.Method
}

// ImageDigestResolver is the registry read the base check makes: what a
// tag names now. The artifact builder's backend is one.
type ImageDigestResolver interface {
	ResolveImage(ctx context.Context, reference, registryAuth string) (ResolvedImage, error)
}

// ReleaseBaseUpdates asks the registry what each base the release was pinned
// on is now, and for a release that is an upstream image, what its tag is
// now. It is the read ReleaseUpdate makes for the release comparison,
// compared with the digest the release itself pinned rather than with
// whatever the tag names on this server today.
func (s *OrchestrationStore) ReleaseBaseUpdates(ctx context.Context, resolver ImageDigestResolver, releaseID int64) ([]ReleaseBaseUpdate, error) {
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	type pinned struct{ reference, digest string }
	var checks []pinned
	for _, base := range releaseBaseImages(release) {
		checks = append(checks, pinned{strings.Split(base.Reference, "@")[0], base.Digest})
	}
	var identity SourceIdentity
	_ = json.Unmarshal(release.Release.SourceIdentity, &identity)
	if identity.Kind == SourceImage && identity.Repository != "" && contentDigestRE.MatchString(identity.Digest) {
		if identity.Local {
			return []ReleaseBaseUpdate{{Reference: identity.Repository, Pinned: identity.Digest, State: "local",
				Reason: "This image exists only on this server, so there is no registry tag to have moved."}}, nil
		}
		checks = append(checks, pinned{strings.Split(identity.Repository, "@")[0], identity.Digest})
	}
	out := make([]ReleaseBaseUpdate, 0, len(checks))
	for _, check := range checks {
		update := ReleaseBaseUpdate{Reference: check.reference, Pinned: check.digest, State: "unknown"}
		if resolver == nil {
			update.Reason = "Docker is unavailable, so no registry comparison can be made."
			out = append(out, update)
			continue
		}
		lookup, cancel := context.WithTimeout(ctx, 20*time.Second)
		current, err := resolver.ResolveImage(lookup, check.reference, "")
		cancel()
		checked := s.now().UTC()
		update.CheckedAt = &checked
		switch {
		case err != nil:
			update.Reason = "The registry did not say what " + check.reference + " names now: " + clipText(err.Error(), 200)
		case !contentDigestRE.MatchString(current.Digest):
			update.Reason = "The registry did not report a digest for " + check.reference + "."
		case current.Digest == check.digest:
			update.State, update.Current = "current", current.Digest
		default:
			update.State, update.Current = "outdated", current.Digest
		}
		out = append(out, update)
	}
	return out, nil
}

// imageRepository is a reference without its tag or digest:
// "registry:5000/team/app:1.2@sha256:…" is "registry:5000/team/app".
func imageRepository(reference string) string {
	name, _, _ := strings.Cut(reference, "@")
	if colon := strings.LastIndex(name, ":"); colon > strings.LastIndex(name, "/") {
		name = name[:colon]
	}
	return name
}

func clipText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// ImageScanResult is what a finished scan writes.
type ImageScanResult struct {
	State    string
	Reason   string
	Scanner  string
	Database imagescan.DatabaseState
	// Findings are the release's own images' findings, marked where a base
	// carries them too.
	Findings    []imagescan.Finding
	Targets     []ImageScanTarget
	BaseUpdates []ReleaseBaseUpdate
}

// FinishImageScan keeps a release's scan. A failed scan keeps the findings
// of the last one that read the image, so a registry or Docker that was down
// for a minute does not read as an image with nothing wrong with it.
func (s *OrchestrationStore) FinishImageScan(ctx context.Context, releaseID int64, result ImageScanResult) error {
	now := s.now().UTC().Unix()
	// A scan run directly, without being asked for first, has no row yet.
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO deploy_image_scans(release_id, environment_id, state, trigger, requested_at, started_at)
		SELECT id, environment_id, 'scanning', ?, ?, ? FROM deploy_releases WHERE id = ?`,
		ImageScanTriggerManual, now, now, releaseID); err != nil {
		return err
	}
	if result.State != ImageScanScanned {
		_, err := s.db.ExecContext(ctx, `UPDATE deploy_image_scans SET state = 'failed', reason = ?, finished_at = ? WHERE release_id = ?`,
			clipText(result.Reason, 1000), now, releaseID)
		return err
	}
	findings := append([]imagescan.Finding(nil), result.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		return imagescan.SeverityRank(findings[i].Severity) > imagescan.SeverityRank(findings[j].Severity)
	})
	truncated := len(findings) > maxStoredFindings
	if truncated {
		findings = findings[:maxStoredFindings]
	}
	counts := imagescan.Summarize(result.Findings, imagescan.SeverityLow)
	var databaseUpdated int64
	if !result.Database.UpdatedAt.IsZero() {
		databaseUpdated = result.Database.UpdatedAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_image_scans SET state = 'scanned', reason = ?, scanner = ?, db_updated_at = ?,
		  counts_json = ?, findings_json = ?, targets_json = ?, bases_json = ?, truncated = ?, finished_at = ?
		 WHERE release_id = ?`,
		clipText(result.Reason, 1000), result.Scanner, databaseUpdated,
		string(mustJSON(map[string]imagescan.Counts{"all": counts.Counts, "fixable": counts.FixableCounts})),
		string(mustJSON(findings)), string(mustJSON(storedTargets(result.Targets))), string(mustJSON(result.BaseUpdates)),
		boolInt(truncated), now, releaseID)
	return err
}

// storedTarget is ImageScanTarget with its findings, as the row keeps it, so
// a daily scan can carry a base's findings forward without reading it again.
type storedTarget struct {
	ImageScanTarget
	Findings []imagescan.Finding `json:"findings,omitempty"`
}

func storedTargets(targets []ImageScanTarget) []storedTarget {
	out := make([]storedTarget, 0, len(targets))
	for _, target := range targets {
		kept := storedTarget{ImageScanTarget: target}
		if target.Kind == "base" {
			kept.Findings = target.findings
			if len(kept.Findings) > maxStoredFindings {
				kept.Findings = kept.Findings[:maxStoredFindings]
			}
		}
		out = append(out, kept)
	}
	return out
}

// WithFindings is a target carrying what its scan found, for the marks a
// release's own findings are given and the next scan's carry-over.
func (t ImageScanTarget) WithFindings(findings []imagescan.Finding) ImageScanTarget {
	t.findings = findings
	return t
}

// Findings is what the target's scan found, when it was kept.
func (t ImageScanTarget) Findings() []imagescan.Finding { return t.findings }

const imageScanColumns = `q.release_id, r.release_number, q.environment_id, q.state, q.reason, q.trigger, q.scanner,
	q.db_updated_at, q.counts_json, q.findings_json, q.targets_json, q.bases_json, q.truncated,
	q.requested_at, q.started_at, q.finished_at, r.provenance_json`

func scanImageScan(row interface{ Scan(...any) error }) (*ImageScan, error) {
	var scan ImageScan
	var databaseUpdated, requested, started, finished int64
	var counts, findings, targets, bases, provenance string
	var truncated int
	if err := row.Scan(&scan.ReleaseID, &scan.ReleaseNumber, &scan.EnvironmentID, &scan.State, &scan.Reason,
		&scan.Trigger, &scan.Scanner, &databaseUpdated, &counts, &findings, &targets, &bases, &truncated,
		&requested, &started, &finished, &provenance); err != nil {
		return nil, err
	}
	scan.DatabaseUpdatedAt = unixTimePtr(databaseUpdated)
	scan.RequestedAt, scan.StartedAt, scan.FinishedAt = unixTimePtr(requested), unixTimePtr(started), unixTimePtr(finished)
	if scan.DatabaseUpdatedAt != nil && scan.FinishedAt != nil {
		scan.DatabaseAgeHours = float64(int(scan.FinishedAt.Sub(*scan.DatabaseUpdatedAt).Hours()*10)) / 10
	}
	_ = json.Unmarshal([]byte(findings), &scan.findings)
	var kept []storedTarget
	_ = json.Unmarshal([]byte(targets), &kept)
	scan.Targets = make([]ImageScanTarget, 0, len(kept))
	for _, target := range kept {
		scan.Targets = append(scan.Targets, target.ImageScanTarget.WithFindings(target.Findings))
	}
	scan.BaseUpdates = []ReleaseBaseUpdate{}
	_ = json.Unmarshal([]byte(bases), &scan.BaseUpdates)
	var recorded map[string]imagescan.Counts
	_ = json.Unmarshal([]byte(counts), &recorded)
	scan.Summary = imagescan.Summary{Counts: recorded["all"], FixableCounts: recorded["fixable"]}
	scan.Summary.Truncated = truncated != 0
	var record struct {
		Builder PreparedBuild `json:"builder"`
	}
	_ = json.Unmarshal([]byte(provenance), &record)
	scan.method = record.Builder.Method
	for _, update := range scan.BaseUpdates {
		if update.State == "outdated" {
			scan.NewerBase = true
		}
	}
	switch scan.method {
	case BuildRecipe, BuildStatic, BuildDockerfile, BuildCompose, BuildRailpack:
		scan.Rebuildable = true
	}
	return &scan, nil
}

// read summarizes the kept findings at the environment's floor.
func (scan *ImageScan) read(floor string) {
	scan.Floor = floor
	summary := imagescan.Summarize(scan.findings, floor)
	// The counts are of every finding the scan made, which the kept list
	// may have been cut short of.
	summary.Counts, summary.FixableCounts = scan.Summary.Counts, scan.Summary.FixableCounts
	summary.Truncated = summary.Truncated || scan.Summary.Truncated
	scan.Summary = summary
}

// ImageScan is a release's latest scan, read at its environment's floor; nil
// when it was never scanned.
func (s *OrchestrationStore) ImageScan(ctx context.Context, releaseID int64) (*ImageScan, error) {
	scan, err := scanImageScan(s.db.QueryRowContext(ctx, `SELECT `+imageScanColumns+`
		  FROM deploy_image_scans q JOIN deploy_releases r ON r.id = q.release_id WHERE q.release_id = ?`, releaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	policy, err := s.ImageScanPolicy(ctx, scan.EnvironmentID)
	if err != nil {
		return nil, err
	}
	scan.read(policy.SeverityFloor)
	return scan, nil
}

// ImageScanBadge is a release's scan as its row in the releases list says
// it: how many findings at or above the floor, how many of those are
// fixable, and whether a newer base would be picked up by a rebuild.
type ImageScanBadge struct {
	ReleaseID int64            `json:"releaseId"`
	State     string           `json:"state"`
	Floor     string           `json:"floor"`
	Counts    imagescan.Counts `json:"counts"`
	AtFloor   int              `json:"atFloor"`
	Fixable   int              `json:"fixable"`
	NewerBase bool             `json:"newerBase"`
	ScannedAt *time.Time       `json:"scannedAt,omitempty"`
}

// ImageScanBadges are the badges of an environment's scanned releases.
func (s *OrchestrationStore) ImageScanBadges(ctx context.Context, environmentID int64) ([]ImageScanBadge, error) {
	policy, err := s.ImageScanPolicy(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+imageScanColumns+`
		  FROM deploy_image_scans q JOIN deploy_releases r ON r.id = q.release_id
		 WHERE q.environment_id = ? ORDER BY q.release_id DESC LIMIT 200`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageScanBadge{}
	for rows.Next() {
		scan, err := scanImageScan(rows)
		if err != nil {
			return nil, err
		}
		scan.read(policy.SeverityFloor)
		out = append(out, ImageScanBadge{
			ReleaseID: scan.ReleaseID, State: scan.State, Floor: scan.Floor, Counts: scan.Summary.Counts,
			AtFloor: scan.Summary.Counts.AtLeast(scan.Floor), Fixable: scan.Summary.FixableCounts.AtLeast(scan.Floor),
			NewerBase: scan.NewerBase, ScannedAt: scan.FinishedAt,
		})
	}
	return out, rows.Err()
}

// LiveImageScan is one environment's live release and its scan, for the
// alert.
type LiveImageScan struct {
	ProjectID       int64
	ProjectName     string
	EnvironmentID   int64
	EnvironmentName string
	Policy          ImageScanPolicy
	Scan            *ImageScan
}

// LiveImageScans are the live releases of every environment that scans,
// each with its latest scan, if any.
func (s *OrchestrationStore) LiveImageScans(ctx context.Context) ([]LiveImageScan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.name, e.live_release_id FROM deploy_environments e
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE e.live_release_id <> 0 AND e.archived_at = 0 AND e.kind <> 'preview'
		 ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	type environment struct {
		LiveImageScan
		release int64
	}
	var environments []environment
	for rows.Next() {
		var row environment
		if err := rows.Scan(&row.ProjectID, &row.ProjectName, &row.EnvironmentID, &row.EnvironmentName, &row.release); err != nil {
			rows.Close()
			return nil, err
		}
		environments = append(environments, row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := []LiveImageScan{}
	for _, row := range environments {
		policy, err := s.ImageScanPolicy(ctx, row.EnvironmentID)
		if err != nil {
			return nil, err
		}
		if !policy.Enabled {
			continue
		}
		row.Policy = policy
		if row.Scan, err = s.ImageScan(ctx, row.release); err != nil {
			return nil, err
		}
		out = append(out, row.LiveImageScan)
	}
	return out, nil
}
