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
}

func ObserveRuntimeServices(ctx context.Context, owner RuntimeObserver, environmentID, liveReleaseID int64) RuntimeServices {
	return observeRuntimeServices(ctx, owner, environmentID, liveReleaseID, 0)
}

func observeRuntimeServices(ctx context.Context, owner RuntimeObserver, environmentID, liveReleaseID, onlyReleaseID int64) RuntimeServices {
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
	containers, err := owner.ListContainersWithLabels(ctx, labels)
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
			ContainerID: item.ID, Name: item.Name, ReleaseID: releaseID,
			LiveRelease: releaseID == liveReleaseID, State: item.State,
			Health: health, ImageID: item.ImageID, Stack: item.ComposeStack,
			Service: item.ComposeSvc, StartedAt: item.StartedAt, Image: item.Image,
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
