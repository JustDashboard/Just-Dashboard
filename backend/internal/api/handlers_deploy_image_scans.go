package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
)

// The base-image vulnerability watch (deploy/image_scan.go): read by anyone
// who may read the environment, its terms saved by an administrator in a
// session, a scan asked for with the control a deploy takes. A rebuild is not
// a route of its own: the panel offers the run the runs route already takes
// (force_build at the live release's commit), so it is queued, gated and
// audited as every other run is.

// newImageScanWatcher is the watch over this server's releases, with the
// scanner when Docker is here and none when it is not.
func (s *Server) newImageScanWatcher() *deploy.ImageScanWatcher {
	if s.modules.docker == nil {
		return deploy.NewImageScanWatcher(s.modules.deployRuns, nil, nil, s.Log)
	}
	s.modules.imageScanner = imagescan.New(imagescan.DockerDaemon{Client: s.modules.docker}, filepath.Join(s.Cfg.DataDir, "image-scans"))
	return deploy.NewImageScanWatcher(s.modules.deployRuns, s.modules.imageScanner,
		deploy.NewDockerArtifactBackend(s.modules.docker), s.Log)
}

// startImageScans removes what a stopped process's scan left behind, then
// starts the watch.
func (s *Server) startImageScans(ctx context.Context) {
	if s.modules.imageScans == nil || !s.modules.imageScans.Available() {
		return
	}
	sweep, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := (imagescan.DockerDaemon{Client: s.modules.docker}).Sweep(sweep); err != nil && s.Log != nil {
		s.Log.Warn("image scan containers left by an earlier process could not be removed", "err", err)
	}
	cancel()
	s.modules.imageScans.Start(ctx)
}

// imageScanDatabase is the scanner's database as the panel states it.
type imageScanDatabase struct {
	Installed  bool       `json:"installed"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
	NextUpdate *time.Time `json:"nextUpdate,omitempty"`
	AgeHours   float64    `json:"ageHours,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	Source     string     `json:"source,omitempty"`
}

// imageScanRebuild is the run the panel's Rebuild button asks for, through
// POST /deploy/{id}/environments/{env}/runs.
type imageScanRebuild struct {
	Operation      deploy.Operation `json:"operation"`
	SourceRevision string           `json:"sourceRevision,omitempty"`
}

type imageScanReading struct {
	Policy    deploy.ImageScanPolicy `json:"policy"`
	Available bool                   `json:"available"`
	// Reason says why there is no scanner on this host.
	Reason   string            `json:"reason,omitempty"`
	Scanner  string            `json:"scanner"`
	Database imageScanDatabase `json:"database"`
	// ReleaseID is the release the scan is of: the live one unless the
	// request named another.
	ReleaseID int64             `json:"releaseId,omitempty"`
	Scan      *deploy.ImageScan `json:"scan"`
	// Rebuild is offered when the release's base can be re-pinned by a
	// build; RebuildReason says why it cannot otherwise.
	Rebuild       *imageScanRebuild       `json:"rebuild,omitempty"`
	RebuildReason string                  `json:"rebuildReason,omitempty"`
	Releases      []deploy.ImageScanBadge `json:"releases"`
}

func (s *Server) handleDeploymentImageScans(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	policy, err := s.modules.deployRuns.ImageScanPolicy(r.Context(), environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	reading := imageScanReading{
		Policy: policy, Available: s.modules.imageScans.Available(), Scanner: "Trivy " + imagescan.Version,
		ReleaseID: target.LiveReleaseID, Releases: []deploy.ImageScanBadge{},
	}
	if !reading.Available {
		reading.Reason = "Docker is unavailable on this host, so there is nothing to run the scanner in."
	}
	if requested := r.URL.Query().Get("release"); requested != "" {
		id, parseErr := strconv.ParseInt(requested, 10, 64)
		if parseErr != nil || id <= 0 {
			return httpx.BadRequest("release must be a positive release id")
		}
		release, releaseErr := s.modules.deployRuns.Release(r.Context(), id)
		if releaseErr != nil || release.Release.EnvironmentID != environmentID || release.Release.ProjectID != projectID {
			return httpx.ErrNotFound
		}
		reading.ReleaseID = id
	}
	if s.modules.imageScanner != nil {
		state := s.modules.imageScanner.Database()
		reading.Database = imageScanDatabase{Installed: state.Installed, LastError: state.LastError, Source: state.Source}
		if state.Installed {
			updated, next := state.UpdatedAt, state.NextUpdate
			reading.Database.UpdatedAt, reading.Database.NextUpdate = &updated, &next
			reading.Database.AgeHours = float64(int(state.Age(time.Now()).Hours()*10)) / 10
		}
	}
	if reading.ReleaseID != 0 {
		if reading.Scan, err = s.modules.deployRuns.ImageScan(r.Context(), reading.ReleaseID); err != nil {
			return httpx.Internal(err)
		}
		reading.Rebuild, reading.RebuildReason = s.imageScanRebuild(r.Context(), target, reading.ReleaseID, reading.Scan)
	}
	if reading.Releases, err = s.modules.deployRuns.ImageScanBadges(r.Context(), environmentID); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, reading)
	return nil
}

// imageScanRebuild is the run that re-pins the live release's bases: a
// forced build of the commit it was built from, with no cache, whose
// preparation resolves each base's tag to the digest the registry names now.
func (s *Server) imageScanRebuild(ctx context.Context, target *deploy.EnvironmentExecutionTarget, releaseID int64, scan *deploy.ImageScan) (*imageScanRebuild, string) {
	if releaseID != target.LiveReleaseID {
		return nil, "Only the live release is rebuilt; deploy this one again from the releases list first."
	}
	switch target.BuildMethod {
	case deploy.BuildImage:
		return nil, "This deployment runs an image from a registry: check the source again in Settings to pin what its tag names now, then deploy."
	case deploy.BuildNone, deploy.BuildLegacyCompose:
		return nil, "This deployment is not built here, so there is no base to re-pin."
	}
	if scan != nil && !scan.Rebuildable {
		return nil, "This release was not built on bases this dashboard pinned."
	}
	rebuild := &imageScanRebuild{Operation: deploy.OperationForceBuild}
	release, err := s.modules.deployRuns.Release(ctx, releaseID)
	if err != nil {
		return nil, "The live release could not be read."
	}
	source, err := s.modules.deployRuns.SourceConfiguration(ctx, target.EnvironmentID, target.DesiredRevision)
	if err == nil && source.Kind == deploy.SourceGit && deploy.IsRemoteGitSource(source) && release.Release.SourceRevision != "" {
		// The same commit: only the bases move.
		rebuild.SourceRevision = release.Release.SourceRevision
	}
	return rebuild, ""
}

func (s *Server) handleDeploymentImageScanPolicyPut(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var policy deploy.ImageScanPolicy
	if err := httpx.DecodeJSON(r, &policy); err != nil {
		return err
	}
	httpx.SetAudit(r, "deploy.vulnerabilities.update", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "enabled": policy.Enabled, "severityFloor": policy.SeverityFloor,
	})
	saved, err := s.modules.deployRuns.PutImageScanPolicy(r.Context(), projectID, environmentID, policy, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDeployError(err)
	}
	s.modules.imageScans.Notify()
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}

// handleDeploymentImageScanRequest asks for the live release (or the one
// named) to be scanned now, its bases with it.
func (s *Server) handleDeploymentImageScanRequest(w http.ResponseWriter, r *http.Request) error {
	projectID, environmentID, err := deploymentEnvironmentIDs(r)
	if err != nil {
		return err
	}
	var request struct {
		ReleaseID int64 `json:"releaseId"`
	}
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	target, err := s.modules.deployRuns.EnvironmentExecutionTarget(r.Context(), projectID, environmentID)
	if err != nil {
		return mapDeployError(err)
	}
	if !s.modules.imageScans.Available() {
		return httpx.Err(http.StatusConflict, "scanner_unavailable", "Docker is unavailable on this host, so there is nothing to run the scanner in.")
	}
	releaseID := request.ReleaseID
	if releaseID == 0 {
		releaseID = target.LiveReleaseID
	}
	if releaseID == 0 {
		return httpx.Err(http.StatusConflict, "no_release", "This environment has no live release to scan yet.")
	}
	release, err := s.modules.deployRuns.Release(r.Context(), releaseID)
	if err != nil || release.Release.EnvironmentID != environmentID || release.Release.ProjectID != projectID {
		return httpx.ErrNotFound
	}
	policy, err := s.modules.deployRuns.ImageScanPolicy(r.Context(), environmentID)
	if err != nil {
		return httpx.Internal(err)
	}
	if !policy.Enabled {
		return httpx.Err(http.StatusConflict, "scanning_off", "Scanning is turned off for this environment.")
	}
	if target.Kind == deploy.EnvironmentPreview {
		return httpx.Err(http.StatusConflict, "scanning_off", "Previews are not scanned; their production is.")
	}
	httpx.SetAudit(r, "deploy.vulnerabilities.scan", fmt.Sprint(projectID), map[string]any{
		"environmentId": environmentID, "releaseId": releaseID,
	})
	if _, err := s.modules.deployRuns.QueueImageScan(r.Context(), releaseID, deploy.ImageScanTriggerManual); err != nil {
		return mapDeployError(err)
	}
	s.modules.imageScans.Notify()
	scan, err := s.modules.deployRuns.ImageScan(r.Context(), releaseID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusAccepted, scan)
	return nil
}
