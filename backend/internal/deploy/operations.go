package deploy

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type RuntimeObserver interface {
	ListContainersWithLabels(context.Context, map[string]string) ([]dockerx.Container, error)
}

// RuntimeServices carries observations, not a health verdict inferred from a
// completed deployment. An empty successful inventory and a failed read differ.
type RuntimeServices struct {
	Status     string           `json:"status"`
	Reason     string           `json:"reason,omitempty"`
	ObservedAt time.Time        `json:"observedAt"`
	Services   []RuntimeService `json:"services"`
}

type RuntimeService struct {
	Manager     string     `json:"manager,omitempty"`
	ResourceID  string     `json:"resourceId,omitempty"`
	LogSource   string     `json:"logSource,omitempty"`
	PID         int32      `json:"pid,omitempty"`
	ContainerID string     `json:"containerId"`
	Name        string     `json:"name"`
	ReleaseID   int64      `json:"releaseId"`
	LiveRelease bool       `json:"liveRelease"`
	State       string     `json:"state"`
	Health      string     `json:"health"`
	ImageID     string     `json:"imageId"`
	Stack       string     `json:"stack,omitempty"`
	Service     string     `json:"service,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	// Image is the reference the container was created from, as Docker
	// reports it, so a service can be drawn as the product it runs.
	Image string `json:"image,omitempty"`
	// What Docker recorded about the container's last run, from the
	// inspection the listing already makes. ExitCode is present only for a
	// container that is not running; all three are omitted when unknown.
	RestartCount int  `json:"restartCount,omitempty"`
	ExitCode     *int `json:"exitCode,omitempty"`
	OOMKilled    bool `json:"oomKilled,omitempty"`
}

func ObserveRuntimeServices(ctx context.Context, owner RuntimeObserver, environmentID, liveReleaseID int64) RuntimeServices {
	return observeRuntimeServices(ctx, owner, environmentID, liveReleaseID, 0)
}

// ReleaseRuntimeDown is true when Docker holds the release's containers and
// none of them is running. A runtime recorded live goes down without a stop
// run when its containers exit, are stopped in Docker, or are not brought back
// after a daemon restart; the project page reads that as stopped and offers
// Start, so start admission has to accept the same observation.
func ReleaseRuntimeDown(ctx context.Context, owner RuntimeObserver, runtime ReleaseRuntime) bool {
	observed := observeRuntimeServices(ctx, owner, runtime.EnvironmentID, runtime.ReleaseID, runtime.ReleaseID)
	if observed.Status != "available" || len(observed.Services) == 0 {
		return false
	}
	for _, service := range observed.Services {
		if service.State == "running" {
			return false
		}
	}
	return true
}

func observeRuntimeServices(ctx context.Context, owner RuntimeObserver, environmentID, liveReleaseID, onlyReleaseID int64) RuntimeServices {
	if recorded, ok := owner.(interface {
		RecordedRuntimeServices(context.Context, int64, int64, int64) RuntimeServices
	}); ok {
		return recorded.RecordedRuntimeServices(ctx, environmentID, liveReleaseID, onlyReleaseID)
	}
	result := RuntimeServices{Status: "unavailable", Services: []RuntimeService{}}
	if owner == nil {
		result.Reason = "Docker runtime evidence is unavailable. Open Docker to check the connection."
		return result
	}
	if environmentID <= 0 {
		result.Reason = "A deployment environment is required to observe runtime services."
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	labels := map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
	}
	if onlyReleaseID > 0 {
		labels["io.just-dashboard.release-id"] = strconv.FormatInt(onlyReleaseID, 10)
	}
	list := owner.ListContainersWithLabels
	// The Docker client can also read what a stopped container's last run left
	// behind. Only this read wants it, so it is asked for here and nowhere else.
	if lastRun, ok := owner.(interface {
		ListContainersWithLastRun(context.Context, map[string]string) ([]dockerx.Container, error)
	}); ok {
		list = lastRun.ListContainersWithLastRun
	}
	containers, err := list(ctx, labels)
	if err != nil {
		// Owner errors can contain daemon addresses and credentials; expose only
		// the availability result, never the transport error.
		result.Reason = "Docker runtime evidence is unavailable. Open Docker to check the connection."
		return result
	}
	result.Status = "available"
	result.ObservedAt = time.Now().UTC()
	for _, item := range containers {
		// A release task's one-shot container carries its release's labels
		// but is not one of the release's services.
		if item.Labels["io.just-dashboard.managed"] != "true" ||
			item.Labels["io.just-dashboard.environment-id"] != labels["io.just-dashboard.environment-id"] ||
			item.Labels[releaseTaskLabel] != "" {
			continue
		}
		releaseID, err := strconv.ParseInt(item.Labels["io.just-dashboard.release-id"], 10, 64)
		if err != nil || releaseID <= 0 || (onlyReleaseID > 0 && releaseID != onlyReleaseID) {
			continue
		}
		health := item.Health
		if health == "" {
			health = "unavailable"
		}
		result.Services = append(result.Services, RuntimeService{
			Manager: "docker", ResourceID: item.ID, LogSource: "docker:" + item.ID,
			ContainerID: item.ID, Name: item.Name, ReleaseID: releaseID,
			LiveRelease: releaseID == liveReleaseID, State: item.State,
			Health: health, ImageID: item.ImageID, Stack: item.ComposeStack,
			Service: item.ComposeSvc, StartedAt: item.StartedAt, Image: item.Image,
			RestartCount: item.Restarts, ExitCode: item.Exited, OOMKilled: item.WasOOMKilled,
		})
	}
	sort.Slice(result.Services, func(i, j int) bool {
		if result.Services[i].Name != result.Services[j].Name {
			return result.Services[i].Name < result.Services[j].Name
		}
		return result.Services[i].ContainerID < result.Services[j].ContainerID
	})
	return result
}
