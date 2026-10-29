package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

// The capacity forecast: when each filesystem fills at the rate the record
// shows, and which deployments' volumes are growing fastest. Both are lines
// fitted through what the recorder already keeps (metrics/forecast.go); the
// volumes' sizes are sampled for this every quarter of an hour by
// sampleDeploymentVolumes, and read from Docker at the moment of asking too,
// so a volume is sized before its first sample.

type capacityForecast struct {
	// Status is "available", or "unavailable" with the reason when the host
	// records no history.
	Status                string                  `json:"status"`
	Reason                string                  `json:"reason,omitempty"`
	RetentionSeconds      int                     `json:"retentionSeconds"`
	VolumeIntervalSeconds int                     `json:"volumeIntervalSeconds"`
	Mounts                []metrics.MountForecast `json:"mounts"`
	Apps                  []appGrowth             `json:"apps"`
}

// appGrowth is one environment's volumes: how big they are now and how fast
// they grow together.
type appGrowth struct {
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	SizeBytes       uint64 `json:"sizeBytes"`
	// Growth is the sum of its volumes' fitted slopes, nil while none of
	// them has history enough for one.
	GrowthBytesPerDay *float64               `json:"growthBytesPerDay"`
	Status            string                 `json:"status"`
	Volumes           []metrics.VolumeGrowth `json:"volumes"`
}

func (s *Server) handleCapacityForecast(w http.ResponseWriter, r *http.Request) error {
	recorder := s.modules.metrics
	result := capacityForecast{Status: "available", Mounts: []metrics.MountForecast{}, Apps: []appGrowth{}}
	if recorder == nil || !recorder.Enabled() {
		result.Status, result.Reason = "unavailable", metricsOff
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	now := time.Now()
	result.RetentionSeconds = int(recorder.Retention() / time.Second)
	result.VolumeIntervalSeconds = int(recorder.VolumeSampling() / time.Second)
	mounts, err := recorder.MountForecasts(r.Context(), now)
	if err != nil {
		return httpx.Internal(err)
	}
	result.Mounts = mounts
	if s.modules.deployPlanning == nil {
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	targets, err := s.modules.deployPlanning.SizingTargets(r.Context(), 0, 0)
	if err != nil {
		return httpx.Internal(err)
	}
	// Docker's sizes now, when its cached walk answers within a few
	// seconds; the newest sample otherwise.
	volumes, listed := s.dockerVolumes(r.Context(), 5*time.Second)
	owned := ownedVolumes(targets, volumes)
	names := []string{}
	for _, list := range owned {
		names = append(names, list...)
	}
	growths, err := recorder.VolumeGrowths(r.Context(), names, now)
	if err != nil {
		return httpx.Internal(err)
	}
	result.Apps = appGrowths(targets, owned, volumes, listed, growths)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// dockerVolumes lists Docker's volumes with their sizes, and whether Docker
// answered the listing at all.
func (s *Server) dockerVolumes(ctx context.Context, wait time.Duration) (map[string]dockerx.Volume, bool) {
	out := map[string]dockerx.Volume{}
	if s.modules.docker == nil {
		return out, false
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	list, err := s.modules.docker.ListVolumes(ctx)
	if err != nil {
		return out, false
	}
	for _, volume := range list {
		out[volume.Name] = volume
	}
	return out, true
}

// volumeMeasured: a volume's size is a measurement only when the disk walk
// reached it; ListVolumes leaves RefCount at -1 otherwise.
func volumeMeasured(volume dockerx.Volume) bool { return volume.RefCount >= 0 && volume.Size >= 0 }

// ownedVolumes names, per environment, the volumes it owns: those its saved
// plan manages, and the volumes Compose labelled with its live stack's
// project.
func ownedVolumes(targets []deploy.SizingTarget, docker map[string]dockerx.Volume) map[int64][]string {
	projects := map[string]int64{}
	out := map[int64][]string{}
	claimed := map[string]bool{}
	for _, target := range targets {
		for _, name := range target.Volumes {
			if !claimed[name] {
				claimed[name] = true
				out[target.EnvironmentID] = append(out[target.EnvironmentID], name)
			}
		}
		if target.ComposeProject != "" {
			projects[target.ComposeProject] = target.EnvironmentID
		}
	}
	names := make([]string, 0, len(docker))
	for name := range docker {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment, ok := projects[docker[name].Labels["com.docker.compose.project"]]
		if ok && !claimed[name] {
			claimed[name] = true
			out[environment] = append(out[environment], name)
		}
	}
	return out
}

// appGrowths joins each environment's volumes to their sizes and lines, the
// fastest-growing first. A volume Docker measured now takes that size; one a
// listing Docker answered does not have is not on this host (a plan naming a
// volume no release has made yet, or one removed) and is left out, as is an
// environment left with no volume.
func appGrowths(targets []deploy.SizingTarget, owned map[int64][]string, docker map[string]dockerx.Volume,
	listed bool, growths map[string]metrics.VolumeGrowth) []appGrowth {
	out := []appGrowth{}
	for _, target := range targets {
		names := owned[target.EnvironmentID]
		if len(names) == 0 {
			continue
		}
		app := appGrowth{
			ProjectID: target.ProjectID, ProjectName: target.ProjectName,
			EnvironmentID: target.EnvironmentID, EnvironmentName: target.EnvironmentName,
			Status: metrics.ForecastInsufficient, Volumes: []metrics.VolumeGrowth{},
		}
		var growth float64
		fitted := false
		for _, name := range names {
			volume := growths[name]
			volume.Name = name
			current, present := docker[name]
			switch {
			case present && volumeMeasured(current):
				volume.SizeBytes, volume.Measured = uint64(current.Size), true
			case listed && !present:
				continue
			}
			if volume.Status == "" {
				volume.Status = metrics.ForecastInsufficient
			}
			app.SizeBytes += volume.SizeBytes
			if volume.GrowthBytesPerDay != nil {
				growth += *volume.GrowthBytesPerDay
				fitted = true
			}
			app.Volumes = append(app.Volumes, volume)
		}
		if len(app.Volumes) == 0 {
			continue
		}
		if fitted {
			app.GrowthBytesPerDay = &growth
			app.Status = metrics.ForecastSteady
			switch {
			case growth >= 1<<20:
				app.Status = metrics.ForecastFilling
			case growth <= -(1 << 20):
				app.Status = metrics.ForecastShrinking
			}
		}
		sort.SliceStable(app.Volumes, func(i, j int) bool {
			return growthOf(app.Volumes[i].GrowthBytesPerDay) > growthOf(app.Volumes[j].GrowthBytesPerDay)
		})
		out = append(out, app)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.GrowthBytesPerDay == nil) != (b.GrowthBytesPerDay == nil) {
			return a.GrowthBytesPerDay != nil
		}
		if ga, gb := growthOf(a.GrowthBytesPerDay), growthOf(b.GrowthBytesPerDay); ga != gb {
			return ga > gb
		}
		return a.SizeBytes > b.SizeBytes
	})
	return out
}

func growthOf(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// sampleDeploymentVolumes is the recorder's volume sampler: the sizes of the
// volumes deployments own, from Docker's disk walk (cached and shared with the
// Docker pages). A size the walk did not reach is left out, not kept as zero.
func (s *Server) sampleDeploymentVolumes(ctx context.Context) ([]metrics.VolumeSize, error) {
	if s.modules.deployPlanning == nil || s.modules.docker == nil {
		return nil, nil
	}
	targets, err := s.modules.deployPlanning.SizingTargets(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	list, err := s.modules.docker.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	docker := make(map[string]dockerx.Volume, len(list))
	for _, volume := range list {
		docker[volume.Name] = volume
	}
	sizes := []metrics.VolumeSize{}
	for _, names := range ownedVolumes(targets, docker) {
		for _, name := range names {
			if volume, ok := docker[name]; ok && volumeMeasured(volume) {
				sizes = append(sizes, metrics.VolumeSize{Name: name, Size: volume.Size})
			}
		}
	}
	return sizes, nil
}
