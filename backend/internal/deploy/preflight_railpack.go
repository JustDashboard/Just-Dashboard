package deploy

import (
	"context"
	"strings"
	"time"
)

// RailpackObservation is whether this server can run the pinned Railpack
// release: its image here already, or its registry answering for it. The
// image is the planner and the BuildKit frontend both, so one answer covers
// the two (build_railpack.go).
type RailpackObservation struct {
	Image    string `json:"image"`
	Present  bool   `json:"present"`
	Pullable bool   `json:"pullable"`
	Detail   string `json:"detail,omitempty"`
}

// railpackObservedImage is the image a plan's preflight asks about, or "" for
// a plan that does not build with Railpack.
func railpackObservedImage(build BuildPlanConfig) string {
	if build.Method != BuildRailpack {
		return ""
	}
	_, image, err := railpackRelease(build.RailpackVersion)
	if err != nil {
		return ""
	}
	return image
}

// observeRailpack asks the daemon for the pinned image and, when it does not
// have it, the registry for the same digest, the way a recipe's base is
// resolved before it is built on.
func (o *HostPreflightObserver) observeRailpack(ctx context.Context, image string) *RailpackObservation {
	observation := &RailpackObservation{Image: image}
	if o.docker == nil {
		observation.Detail = "Docker is unavailable"
		return observation
	}
	lookup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := o.docker.InspectImage(lookup, image); err == nil {
		observation.Present, observation.Pullable = true, true
		return observation
	}
	resolved, err := o.docker.ResolveDistributionImage(lookup, image, "")
	switch {
	case err != nil:
		observation.Detail = boundedEvidence(err.Error())
	case resolved == nil:
		observation.Detail = "the registry has no such image"
	default:
		observation.Pullable = true
	}
	return observation
}

// railpackFindings are what a Railpack plan is told before Deploy: whether
// the pinned release can run here, what Railpack decides rather than this
// dashboard, and that a plan detection could not read is Railpack's to judge.
func railpackFindings(draft *Draft, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	build := configuration.Build
	if build.Method != BuildRailpack {
		return nil
	}
	findings := []PreflightFinding{}
	version, image, err := railpackRelease(build.RailpackVersion)
	if err != nil {
		return findings
	}
	if railpack := observation.Railpack; railpack != nil {
		switch {
		case railpack.Present:
			findings = append(findings, finding("railpack_ready", PreflightPass,
				"Railpack is on this server", "Railpack "+version,
				"The planner and the BuildKit frontend are one image, pinned by digest, and this server already has it.",
				"", "docker", "configuration.build.railpackVersion"))
		case railpack.Pullable:
			findings = append(findings, finding("railpack_ready", PreflightPass,
				"Railpack can be pulled", "Railpack "+version,
				"The registry serves the pinned image; the first build pulls it.",
				"", "docker", "configuration.build.railpackVersion"))
		default:
			findings = append(findings, finding("railpack_unavailable", PreflightBlocked,
				"Railpack's image cannot be reached", image+": "+orDefault(railpack.Detail, "not on this server or its registry"),
				"The build plans and builds with this image; without it the deployment stops at Prepare build context.",
				"Check that this server reaches ghcr.io, or pull the image onto it, and run preflight again.",
				"docker", "configuration.build.railpackVersion"))
		}
	}
	findings = append(findings, finding("railpack_bases_unpinned", PreflightWarning,
		"Railpack chooses its own base images", "ghcr.io/railwayapp/railpack-builder and railpack-runtime, by mise release",
		"This dashboard pins Railpack "+version+" by digest, but not what its plan builds from. Railpack tags its builder and runtime images by the mise release they carry and never moves such a tag, resolves each language to an exact release in the plan (one at least 14 days old), and the run records both; the recipes' bases are digest-pinned from a catalogue this dashboard reviewed, and these are not.",
		"Build with a recipe or a Dockerfile when every base must be pinned by digest here.",
		"deploy", "configuration.build.method"))
	if draft != nil && draft.Data.Detection != nil {
		planned := plannedDetectionCandidate(draft.Data.Detection, build)
		if planned != nil && planned.BuildMethod == BuildRailpack && planned.Framework == "" {
			findings = append(findings, finding("railpack_detection_deferred", PreflightWarning,
				"Railpack decides at build time whether it can build this", rootLabelOf(build.RootDirectory),
				"No recipe or Dockerfile matched this directory, so nothing here has read it; Railpack's own detection does, when the deployment prepares its build, and stops it there if it finds no language it builds.",
				"Choose Railpack's provider in Build settings when you know it, or commit a Dockerfile.",
				"deploy", "configuration.build.railpackProvider"))
		}
	}
	if values := plainRailpackVariables(configuration); len(values) > 0 {
		findings = append(findings, finding("railpack_variables_configure_plan", PreflightPass,
			"Railpack reads its own variables", strings.Join(values, ", "),
			"Plain RAILPACK_* build variables configure Railpack's plan, as they do on Railway; a secret one reaches the build by name only and configures nothing.",
			"", "deploy", "variables"))
	}
	return findings
}

func plainRailpackVariables(configuration PlanConfiguration) []string {
	names := []string{}
	for _, variable := range configuration.Variables {
		if strings.HasPrefix(variable.Name, "RAILPACK_") && variable.Sensitivity == "plain" &&
			len(names) < 8 && containsScope(variable.Scopes, "build") {
			names = append(names, variable.Name)
		}
	}
	return names
}

func containsScope(scopes []string, scope string) bool {
	for _, value := range scopes {
		if value == scope {
			return true
		}
	}
	return false
}
