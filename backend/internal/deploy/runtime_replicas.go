package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// MaxRuntimeReplicas is the most containers one release runs side by side on
// this host. Replicas here are for a release that one process cannot carry,
// or that should not go down with one container; past a handful they buy
// nothing on a single machine the processor count does not already bound.
const MaxRuntimeReplicas = 8

// Labels a replicated release's containers carry beside the release's own,
// so the inventory, the lifecycle feed and the metrics read them as one
// release and can still tell them apart. A release of one container carries
// neither, as before.
const (
	replicaLabel  = "io.just-dashboard.replica"
	replicasLabel = "io.just-dashboard.replicas"
)

// EffectiveReplicas is how many containers each release of the plan runs.
func (c RuntimePlanConfig) EffectiveReplicas() int {
	if c.Replicas <= 1 {
		return 1
	}
	return c.Replicas
}

// replicaRefusal says why a plan cannot run more than one container per
// release, or nothing when it can. Replicas share one route and one host, so
// each of these would have them fight over one thing only one of them can
// hold: a Compose stack runs its services once each, a host port or a
// published port is one listener, the host network is every port at once,
// and a writable mount would be written by all of them at the same time. A
// read-only mount is shared without harm.
func replicaRefusal(method BuildMethod, runtime RuntimePlanConfig) string {
	switch {
	case method == BuildCompose || method == BuildLegacyCompose:
		return "a Compose stack runs one copy of each of its services"
	case runtime.HostNetwork:
		return "on the host network every replica would claim the same ports"
	case runtime.HostPort > 0:
		return fmt.Sprintf("host port %d can be held by one container at a time", runtime.HostPort)
	case len(runtime.Ports) > 0:
		return fmt.Sprintf("published port %d can be held by one container at a time", runtime.Ports[0].HostPort)
	}
	for _, mount := range runtime.Mounts {
		if !mount.ReadOnly {
			return fmt.Sprintf("%s is writable, and replicas would write to it at the same time", mount.Target)
		}
	}
	return ""
}

// validateReplicas is the plan's own rule, so a plan that saves is one the
// runtime owner can start.
func validateReplicas(method BuildMethod, runtime RuntimePlanConfig) error {
	if runtime.Replicas < 0 || runtime.Replicas > MaxRuntimeReplicas {
		return invalidField("runtime.replicas", "replicas must be between 1 and %d", MaxRuntimeReplicas)
	}
	if runtime.Replicas > 1 {
		if why := replicaRefusal(method, runtime); why != "" {
			return invalidField("runtime.replicas", "%d replicas need a stateless release: %s", runtime.Replicas, why)
		}
	}
	return nil
}

// replicaContainerName is the deterministic name of one replica of a
// release. The first keeps the name a release has always had, so a release
// of one container, cancellation cleanup and every reader of the name see
// what they did; the others follow it with their number.
func replicaContainerName(environmentID, releaseNumber int64, replica int) string {
	if replica <= 1 {
		return fmt.Sprintf("jd-e%d-r%d", environmentID, releaseNumber)
	}
	return fmt.Sprintf("jd-e%d-r%d-%d", environmentID, releaseNumber, replica)
}

// runtimeReplica is one container of a release as its runtime record keeps
// it: where it was published, for the route and the checks.
type runtimeReplica struct {
	Index       int    `json:"index"`
	ContainerID string `json:"containerId"`
	Name        string `json:"name,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
}

// runtimeReplicas are a release's containers, first replica first. A record
// written before replicas, or of one container, is its one container.
func runtimeReplicas(runtime ReleaseRuntime) []runtimeReplica {
	if runtime.Kind == "container" {
		var metadata dockerReleaseRuntimeMetadata
		if json.Unmarshal(runtime.Metadata, &metadata) == nil && len(metadata.Replicas) > 1 {
			return append([]runtimeReplica(nil), metadata.Replicas...)
		}
	}
	return []runtimeReplica{{Index: 1, ContainerID: runtime.RuntimeID, Name: runtime.Name, Host: runtime.Host, Port: runtime.Port}}
}

// runtimeUpstreams are the addresses the route shares the release's requests
// between, one per replica.
func runtimeUpstreams(runtime ReleaseRuntime) []string {
	replicas := runtimeReplicas(runtime)
	upstreams := make([]string, 0, len(replicas))
	for _, replica := range replicas {
		upstreams = append(upstreams, "http://"+net.JoinHostPort(runtimeCheckHost(replica.Host), strconv.Itoa(replica.Port)))
	}
	return upstreams
}

// replicaTargets are what a release's own checks ask: every replica, since
// traffic is shared between all of them the moment the route moves.
func replicaTargets(runtime ReleaseRuntime, snapshot runtimeReleaseSnapshot) []CheckTarget {
	replicas := runtimeReplicas(runtime)
	if len(replicas) == 1 {
		return []CheckTarget{targetForRuntime(runtime, snapshot)}
	}
	targets := make([]CheckTarget, 0, len(replicas))
	for _, replica := range replicas {
		target := targetForRuntime(runtime, snapshot)
		target.ContainerID, target.Host, target.Port = replica.ContainerID, runtimeCheckHost(replica.Host), replica.Port
		targets = append(targets, target)
	}
	return targets
}

// replicaLabels are the labels one replica carries beyond its release's.
func replicaLabels(replica, replicas int) []dockerx.LabelSpec {
	if replicas <= 1 {
		return nil
	}
	return []dockerx.LabelSpec{
		{Name: replicaLabel, Value: strconv.Itoa(replica)},
		{Name: replicasLabel, Value: strconv.Itoa(replicas)},
	}
}

// orphanReplicas names the containers a crash between Docker create and the
// runtime record could have left: every replica the snapshot asked for, by
// its deterministic name, which Docker takes in place of an id.
func orphanReplicas(environmentID, releaseNumber int64, replicas int) json.RawMessage {
	if replicas <= 1 {
		return nil
	}
	names := make([]string, 0, replicas-1)
	for replica := 2; replica <= replicas; replica++ {
		names = append(names, replicaContainerName(environmentID, releaseNumber, replica))
	}
	return mustJSON(dockerReleaseRuntimeMetadata{Version: 1, ContainerIDs: names})
}

// replicasFinding is preflight's word on a plan that runs more than one
// container per release: refused with the reason, or what the replicas cost.
// The plan's own validation refuses the same plans first; this is the rule
// as the review reads it, beside the strategy's.
func replicasFinding(method BuildMethod, runtime RuntimePlanConfig) (PreflightFinding, bool) {
	replicas := runtime.EffectiveReplicas()
	if replicas <= 1 {
		return PreflightFinding{}, false
	}
	if why := replicaRefusal(method, runtime); why != "" {
		return finding("replicas_ineligible", PreflightBlocked,
			"Replicas need a stateless release", fmt.Sprintf("%d replicas", replicas),
			"Replicas run side by side on one host: "+why+".",
			"Run one replica, or remove what only one container can hold.", "deploy", "runtime.replicas"), true
	}
	means := fmt.Sprintf("The route shares requests between %d containers of each release, and a cutover waits until every one of them passes its readiness check.", replicas)
	if runtime.MemoryMB > 0 {
		means += fmt.Sprintf(" Each has its own %d MiB memory limit: %d MiB for the release, and twice that while a blue/green cutover runs both releases.",
			runtime.MemoryMB, int64(replicas)*runtime.MemoryMB)
	}
	return finding("replicas", PreflightPass,
		fmt.Sprintf("Runs %d replicas of each release", replicas), fmt.Sprintf("%d replicas", replicas),
		means, "", "deploy", "runtime.replicas"), true
}
