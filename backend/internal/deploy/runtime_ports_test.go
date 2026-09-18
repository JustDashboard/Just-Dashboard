package deploy

import (
	"errors"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The routed port rides the leased host port; every additional published port
// keeps the number and interface the plan pinned, and host networking
// publishes nothing because the container already owns the host's ports.
func TestRuntimePortMappingsPublishTheRoutedAndPinnedPorts(t *testing.T) {
	t.Parallel()
	plan := RuntimePlanConfig{InternalPort: 3000, Ports: []PublishedPort{
		{HostPort: 2222, ContainerPort: 2222},
		{HostPort: 19132, ContainerPort: 19132, Protocol: "udp", BindAddress: "127.0.0.1"},
	}}
	mappings := runtimePortMappings(plan, "127.0.0.1", 41000)
	want := []dockerx.PortMapping{
		{HostIP: "127.0.0.1", HostPort: 41000, ContainerPort: 3000, Protocol: "tcp"},
		{HostIP: "", HostPort: 2222, ContainerPort: 2222, Protocol: "tcp"},
		{HostIP: "127.0.0.1", HostPort: 19132, ContainerPort: 19132, Protocol: "udp"},
	}
	if len(mappings) != len(want) {
		t.Fatalf("mappings = %#v", mappings)
	}
	for index := range want {
		if mappings[index] != want[index] {
			t.Fatalf("mapping %d = %#v, want %#v", index, mappings[index], want[index])
		}
	}
	if unleased := runtimePortMappings(plan, "127.0.0.1", 0); len(unleased) != 2 || unleased[0].HostPort != 2222 {
		t.Fatalf("without a lease = %#v", unleased)
	}
	plan.HostNetwork = true
	if mappings := runtimePortMappings(plan, "127.0.0.1", 41000); len(mappings) != 0 {
		t.Fatalf("host network published %#v", mappings)
	}
}

// A pinned publication is a fixed host binding, so two candidates cannot hold
// it at once: the plan is not eligible for blue/green activation.
func TestPublishedPortsMakeAPlanStopFirstOnly(t *testing.T) {
	t.Parallel()
	snapshot := runtimeReleaseSnapshot{Plan: RuntimePlanConfig{Strategy: StrategyBlueGreen, InternalPort: 3000,
		Ports: []PublishedPort{{HostPort: 2222, ContainerPort: 2222}}}}
	if err := validateRuntimeActivationStrategy(snapshot); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("blue/green accepted a published port: %v", err)
	}
	snapshot.Plan.Strategy = StrategyStopFirst
	if err := validateRuntimeActivationStrategy(snapshot); err != nil {
		t.Fatal(err)
	}
	if label := publishedPortsLabel(snapshot.Plan.Ports); label != "2222/tcp → 2222" {
		t.Fatalf("label = %q", label)
	}
	if label := publishedPortsLabel(nil); label != "none" {
		t.Fatalf("empty label = %q", label)
	}
}
