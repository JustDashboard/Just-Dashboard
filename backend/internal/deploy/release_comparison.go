package deploy

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReleaseFieldChange is one named fact about a release and its counterpart in
// the release being compared against. Values are rendered strings, never raw
// variable content: a variable appears here by name and digest only.
type ReleaseFieldChange struct {
	Field   string `json:"field"`
	From    string `json:"from"`
	To      string `json:"to"`
	Changed bool   `json:"changed"`
}

type ReleaseListChange struct {
	Name   string `json:"name"`
	Change string `json:"change"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Detail string `json:"detail,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// ReleaseArtifactStatus explains why an artifact is still on disk. Rollback is
// only real while the artifact the older release names is retained.
type ReleaseArtifactStatus struct {
	Kind      ArtifactKind `json:"kind"`
	Reference string       `json:"reference"`
	Digest    string       `json:"digest"`
	SizeBytes int64        `json:"sizeBytes"`
	State     string       `json:"state"`
	Retained  bool         `json:"retained"`
	Reason    string       `json:"reason"`
}

type ReleaseDetailComparison struct {
	Status       string               `json:"status"`
	Reason       string               `json:"reason,omitempty"`
	Fields       []ReleaseFieldChange `json:"fields"`
	Variables    []ReleaseListChange  `json:"variables"`
	Dependencies []ReleaseListChange  `json:"dependencies"`
	Checks       []ReleaseListChange  `json:"checks"`
	Domains      []ReleaseListChange  `json:"domains"`
}

// ReleaseUpdateStatus reports whether the upstream image a release was built
// from has moved. A locally built image has nothing to compare against, which
// is an answer rather than a failure.
type ReleaseUpdateStatus struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	Reference string    `json:"reference,omitempty"`
	State     string    `json:"state,omitempty"`
	Local     string    `json:"localDigest,omitempty"`
	Remote    string    `json:"remoteDigest,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

// ImageUpdateObserver is Docker's registry update check. Deployments asks it
// about the reference a release recorded; it does not resolve tags itself.
type ImageUpdateObserver interface {
	CheckUpdate(context.Context, string, bool) ImageUpdateResult
}

type ImageUpdateResult struct {
	Ref          string
	State        string
	LocalDigest  string
	RemoteDigest string
	Reason       string
	CheckedAt    time.Time
}

func (s *OrchestrationStore) CompareReleaseDetail(
	ctx context.Context,
	fromID, toID int64,
) (*ReleaseComparison, error) {
	comparison, err := s.CompareReleases(ctx, fromID, toID)
	if err != nil {
		return nil, err
	}
	from, err := s.Release(ctx, fromID)
	if err != nil {
		return nil, err
	}
	to, err := s.Release(ctx, toID)
	if err != nil {
		return nil, err
	}
	comparison.Detail = compareReleaseSnapshots(from, to)
	comparison.Artifacts, err = s.releaseArtifactStatus(ctx, to)
	if err != nil {
		return nil, err
	}
	return comparison, nil
}

func compareReleaseSnapshots(from, to *ReleaseWithArtifacts) ReleaseDetailComparison {
	detail := ReleaseDetailComparison{
		Status: statusUnavailable, Fields: []ReleaseFieldChange{}, Variables: []ReleaseListChange{},
		Dependencies: []ReleaseListChange{}, Checks: []ReleaseListChange{}, Domains: []ReleaseListChange{},
	}
	fromSnapshot, fromErr := decodeReleaseRuntimeSnapshot(from)
	toSnapshot, toErr := decodeReleaseRuntimeSnapshot(to)
	if fromErr != nil || toErr != nil {
		detail.Reason = "One of these releases has no readable runtime snapshot, so only its recorded digests can be compared."
		return detail
	}
	detail.Status = statusAvailable
	field := func(name, before, after string) {
		detail.Fields = append(detail.Fields, ReleaseFieldChange{
			Field: name, From: before, To: after, Changed: before != after,
		})
	}
	field("source revision", immutableSourceRevision(fromSnapshot.SourceIdentity), immutableSourceRevision(toSnapshot.SourceIdentity))
	field("source reference", fromSnapshot.SourceIdentity.Ref, toSnapshot.SourceIdentity.Ref)
	field("image", snapshotImageLabel(fromSnapshot), snapshotImageLabel(toSnapshot))
	field("command", strings.Join(fromSnapshot.Plan.Command, " "), strings.Join(toSnapshot.Plan.Command, " "))
	field("internal port", portLabel(fromSnapshot.Plan.InternalPort), portLabel(toSnapshot.Plan.InternalPort))
	field("host port", portLabel(fromSnapshot.Plan.HostPort), portLabel(toSnapshot.Plan.HostPort))
	field("strategy", string(fromSnapshot.Plan.Strategy), string(toSnapshot.Plan.Strategy))
	field("stop signal", fromSnapshot.Plan.StopSignal, toSnapshot.Plan.StopSignal)
	field("grace period", secondsLabel(fromSnapshot.Plan.GracePeriodSeconds), secondsLabel(toSnapshot.Plan.GracePeriodSeconds))
	field("memory limit", memoryLimitLabel(fromSnapshot.Plan.MemoryMB), memoryLimitLabel(toSnapshot.Plan.MemoryMB))
	field("cpu limit", cpuLimitLabel(fromSnapshot.Plan.CPUs), cpuLimitLabel(toSnapshot.Plan.CPUs))
	field("pid limit", countLimitLabel(fromSnapshot.Plan.PidsLimit), countLimitLabel(toSnapshot.Plan.PidsLimit))
	field("restart policy", fromSnapshot.Plan.EffectiveRestartPolicy(), toSnapshot.Plan.EffectiveRestartPolicy())
	field("host network", boolLabel(fromSnapshot.Plan.HostNetwork), boolLabel(toSnapshot.Plan.HostNetwork))
	field("privileged", boolLabel(fromSnapshot.Plan.Privileged), boolLabel(toSnapshot.Plan.Privileged))
	field("capabilities", strings.Join(fromSnapshot.Plan.Capabilities, ", "), strings.Join(toSnapshot.Plan.Capabilities, ", "))
	field("devices", strings.Join(fromSnapshot.Plan.Devices, ", "), strings.Join(toSnapshot.Plan.Devices, ", "))
	field("expected downtime", boolLabel(from.Release.ExpectedDowntime), boolLabel(to.Release.ExpectedDowntime))

	detail.Variables = diffNamedValues(
		variableDigestPairs(fromSnapshot.Variables), variableDigestPairs(toSnapshot.Variables))
	for index := range detail.Variables {
		detail.Variables[index].Secret = true
		detail.Variables[index].Detail = "Compared by value digest; values are never read here."
	}
	detail.Dependencies = diffNamedValues(
		dependencyPairs(fromSnapshot.Dependencies), dependencyPairs(toSnapshot.Dependencies))
	detail.Checks = diffNamedValues(checkPairs(fromSnapshot.Checks), checkPairs(toSnapshot.Checks))
	detail.Domains = diffNamedValues(domainPairs(fromSnapshot.Domains), domainPairs(toSnapshot.Domains))

	detail.Fields = append(detail.Fields, ReleaseFieldChange{
		Field: "persistent storage", From: mountsLabel(fromSnapshot.Plan.Mounts), To: mountsLabel(toSnapshot.Plan.Mounts),
		Changed: mountsLabel(fromSnapshot.Plan.Mounts) != mountsLabel(toSnapshot.Plan.Mounts),
	})
	return detail
}

func diffNamedValues(before, after map[string]string) []ReleaseListChange {
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	changes := make([]ReleaseListChange, 0, len(ordered))
	for _, name := range ordered {
		fromValue, hadFrom := before[name]
		toValue, hadTo := after[name]
		change := ReleaseListChange{Name: name, From: fromValue, To: toValue}
		switch {
		case !hadFrom:
			change.Change = "added"
		case !hadTo:
			change.Change = "removed"
		case fromValue != toValue:
			change.Change = "changed"
		default:
			change.Change = "unchanged"
		}
		changes = append(changes, change)
	}
	return changes
}

func variableDigestPairs(variables []ReleaseVariableSnapshot) map[string]string {
	pairs := map[string]string{}
	for _, variable := range variables {
		pairs[variable.Name] = shortDigest(variable.ValueDigest) + " · " + variable.Sensitivity
	}
	return pairs
}

func dependencyPairs(dependencies []PlannedDependency) map[string]string {
	pairs := map[string]string{}
	for _, dependency := range dependencies {
		pairs[dependency.ResourceKind+" "+dependency.ResourceID] =
			string(dependency.Ownership) + " · " + dependency.Kind
	}
	return pairs
}

func checkPairs(checks []PlannedCheck) map[string]string {
	pairs := map[string]string{}
	for _, check := range checks {
		required := "optional"
		if check.Required {
			required = "required"
		}
		pairs[check.Name] = fmt.Sprintf("%s · %s · %s", check.Kind, check.Phase, required)
	}
	return pairs
}

func domainPairs(domains []PlannedDomain) map[string]string {
	pairs := map[string]string{}
	for _, domain := range domains {
		scheme := "http"
		if domain.HTTPS {
			scheme = "https"
		}
		pairs[domain.Hostname] = scheme + " · " + string(domain.Ownership)
	}
	return pairs
}

func snapshotImageLabel(snapshot runtimeReleaseSnapshot) string {
	if snapshot.Compose != nil {
		services := make([]string, 0, len(snapshot.Compose.Services))
		for _, service := range snapshot.Compose.Services {
			services = append(services, service.Plan.Name+"@"+shortDigest(service.Digest))
		}
		sort.Strings(services)
		return strings.Join(services, ", ")
	}
	if snapshot.Image.Reference == "" {
		return shortDigest(snapshot.Image.Digest)
	}
	return strings.Split(snapshot.Image.Reference, "@")[0] + "@" + shortDigest(snapshot.Image.Digest)
}

func mountsLabel(mounts []RuntimeMount) string {
	labels := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		suffix := ""
		if mount.ReadOnly {
			suffix = ":ro"
		}
		labels = append(labels, mount.Source+" -> "+mount.Target+suffix)
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		return "none"
	}
	return strings.Join(labels, ", ")
}

func portLabel(port int) string {
	if port <= 0 {
		return "none"
	}
	return fmt.Sprintf("%d", port)
}

func secondsLabel(seconds int) string {
	if seconds <= 0 {
		return "default"
	}
	return fmt.Sprintf("%ds", seconds)
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func shortDigest(digest string) string {
	trimmed := strings.TrimPrefix(digest, "sha256:")
	if trimmed == "" {
		return "none"
	}
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
}

// releaseArtifactStatus reuses the retention planner rather than re-deriving
// which artifacts survive a prune. Rollback promises are only as good as the
// artifacts the planner is keeping.
func (s *OrchestrationStore) releaseArtifactStatus(
	ctx context.Context,
	release *ReleaseWithArtifacts,
) ([]ReleaseArtifactStatus, error) {
	decisions, err := s.ArtifactRetentionPlan(ctx, s.now().UTC())
	if err != nil {
		return nil, err
	}
	byArtifact := map[int64]ArtifactRetentionDecision{}
	for _, decision := range decisions {
		byArtifact[decision.Artifact.ID] = decision
	}
	result := make([]ReleaseArtifactStatus, 0, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		status := ReleaseArtifactStatus{
			Kind: artifact.Kind, Reference: artifact.Reference, Digest: artifact.Digest,
			SizeBytes: artifact.SizeBytes, State: artifact.State, Retained: true,
			Reason: "This artifact was not offered to the retention planner.",
		}
		if decision, found := byArtifact[artifact.ID]; found {
			status.Retained, status.Reason = decision.Retain, decision.Reason
		}
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Reference < result[j].Reference
	})
	return result, nil
}

// ReleaseUpdate asks Docker whether the upstream reference a release recorded
// still resolves to the digest that release pinned.
func (s *OrchestrationStore) ReleaseUpdate(
	ctx context.Context,
	owner ImageUpdateObserver,
	releaseID int64,
) (*ReleaseUpdateStatus, error) {
	result := &ReleaseUpdateStatus{Status: statusUnavailable}
	if owner == nil {
		result.Reason = "Docker is unavailable, so no registry comparison can be made."
		return result, nil
	}
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		result.Reason = "This release has no readable runtime snapshot, so its upstream reference is unknown."
		return result, nil
	}
	reference := strings.Split(snapshot.Image.Reference, "@")[0]
	if snapshot.Compose != nil || reference == "" {
		result.Status, result.State = statusAvailable, "local"
		result.Reason = "This release was built here from source or Compose. There is no upstream tag to compare against."
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	status := owner.CheckUpdate(ctx, reference, false)
	result.Status = statusAvailable
	result.Reference, result.State = reference, status.State
	result.Local, result.Remote = status.LocalDigest, status.RemoteDigest
	result.Reason, result.CheckedAt = status.Reason, status.CheckedAt
	return result, nil
}

// ReleaseArtifactStatus reports retention for one release without requiring a
// second release to compare it against.
func (s *OrchestrationStore) ReleaseArtifactStatus(
	ctx context.Context,
	releaseID int64,
) ([]ReleaseArtifactStatus, error) {
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	return s.releaseArtifactStatus(ctx, release)
}

func memoryLimitLabel(mb int64) string {
	if mb <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d MiB", mb)
}

func cpuLimitLabel(cpus float64) string {
	if cpus <= 0 {
		return "unlimited"
	}
	return strconv.FormatFloat(cpus, 'f', -1, 64) + " CPU"
}

func countLimitLabel(count int64) string {
	if count <= 0 {
		return "unlimited"
	}
	return strconv.FormatInt(count, 10)
}
