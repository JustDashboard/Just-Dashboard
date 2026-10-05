package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type reservationComposeInventory struct {
	containers []dockerx.Container
	details    map[string]*dockerx.ContainerDetail
}

func (f *reservationComposeInventory) ListContainersWithLabels(context.Context, map[string]string) ([]dockerx.Container, error) {
	return f.containers, nil
}
func (f *reservationComposeInventory) Inspect(_ context.Context, id string) (*dockerx.ContainerDetail, error) {
	if detail := f.details[id]; detail != nil {
		return detail, nil
	}
	return nil, errors.New("absent")
}

func TestRuntimeReservationVerifiesExactCompensatedComposeReplicaPopulation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{"restored-owned", true}, {"original-unlabeled", true}, {"oneoff-and-unrelated", true},
		{"missing", false}, {"foreign-environment", false}, {"foreign-release", false}, {"unowned", false},
		{"extra-owned-replica", false}, {"extra-unowned-replica", false}, {"ambiguous-replica", false}, {"wrong-project", false}, {"inspection-identity-changed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := []AdoptedContainer{{ID: strings.Repeat("a", 64), Service: "web", Number: 1, Running: true}, {ID: strings.Repeat("c", 64), Service: "worker", Number: 1}}
			runtime := ReleaseRuntime{EnvironmentID: 12, ReleaseID: 34, Kind: "compose", RuntimeID: "original-project", Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, Adopted: true, ContainerIDs: []string{baseline[0].ID, baseline[1].ID}, BaselineContainers: baseline})}
			makeContainer := func(id, service, number string, managed bool) dockerx.Container {
				labels := map[string]string{"com.docker.compose.project": runtime.RuntimeID, "com.docker.compose.service": service, "com.docker.compose.container-number": number}
				if managed {
					labels["io.just-dashboard.managed"] = "true"
					labels["io.just-dashboard.environment-id"] = "12"
					labels["io.just-dashboard.release-id"] = "34"
				}
				return dockerx.Container{ID: id, ComposeStack: runtime.RuntimeID, State: "exited", Labels: labels}
			}
			containers := []dockerx.Container{makeContainer(strings.Repeat("b", 64), "web", "1", true), makeContainer(strings.Repeat("d", 64), "worker", "1", true)}
			switch tc.name {
			case "original-unlabeled":
				containers = []dockerx.Container{makeContainer(baseline[0].ID, "web", "1", false), makeContainer(baseline[1].ID, "worker", "1", false)}
			case "oneoff-and-unrelated":
				oneoff := makeContainer(strings.Repeat("e", 64), "web", "2", false)
				oneoff.Labels["com.docker.compose.oneoff"] = "True"
				containers = append(containers, oneoff, makeContainer(strings.Repeat("f", 64), "external", "1", false))
			case "missing":
				containers = containers[:1]
			case "foreign-environment":
				containers[1].Labels["io.just-dashboard.environment-id"] = "99"
			case "foreign-release":
				containers[1].Labels["io.just-dashboard.release-id"] = "99"
			case "unowned":
				delete(containers[1].Labels, "io.just-dashboard.managed")
			case "extra-owned-replica":
				containers = append(containers, makeContainer(strings.Repeat("e", 64), "web", "2", true))
			case "extra-unowned-replica":
				containers = append(containers, makeContainer(strings.Repeat("e", 64), "web", "2", false))
			case "ambiguous-replica":
				containers = append(containers, makeContainer(strings.Repeat("e", 64), "worker", "1", true))
			case "wrong-project":
				containers[1].ComposeStack = "other-project"
			}
			inventory := &reservationComposeInventory{containers: containers, details: map[string]*dockerx.ContainerDetail{}}
			for _, container := range containers {
				inventory.details[container.ID] = &dockerx.ContainerDetail{Container: container}
			}
			if tc.name == "inspection-identity-changed" {
				inventory.details[containers[1].ID].ID = strings.Repeat("f", 64)
			}
			observer := NewRuntimeReservationObserver(nil, inventory, nil)
			if observer.reservedDockerRuntimeAvailable(t.Context(), runtime, false) != tc.allowed {
				t.Fatalf("reservation authority mismatch for %s", tc.name)
			}
		})
	}
}
