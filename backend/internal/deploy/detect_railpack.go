package deploy

// Detection offers Railpack (build_railpack.go) in two ways. Beside a recipe
// it is a choice, never the selection: a recipe candidate records the
// Railpack provider that would build the same directory (RailpackProvider),
// which the build step offers next to the recipe. Where nothing else builds a
// directory it is the fallback: a language detection names without a recipe
// that Railpack has a provider for (C/C++, Zig), or a repository in which
// nothing was recognised at all. The fallback is a candidate of its own at low
// confidence, whose evidence says Railpack's detection decides, and the
// ranking prefers it there because it is the only one that can build.

// offerRailpack adds the fallback candidates before the ranking. setAside is
// what the repository's shape deliberately left out (a mobile app, a library):
// a repository understood as not a service gets no fallback.
func offerRailpack(result *DetectionResult, hasFiles bool, setAside []DetectionSetAside, variables func(string) []DetectedVariable) {
	for _, group := range groupCandidatesByRoot(result.Candidates) {
		if rootBuilds(group.candidates) {
			continue
		}
		for _, candidate := range group.candidates {
			provider := railpackEcosystemProviders[candidate.Framework]
			if candidate.BuildMethod != BuildRecipe || candidate.Recipe != "" || provider == "" || candidate.NotDeployable != "" {
				continue
			}
			result.Candidates = append(result.Candidates, railpackEcosystemCandidate(candidate, provider))
			break
		}
	}
	if len(result.Candidates) > 0 || len(setAside) > 0 || !hasFiles {
		return
	}
	candidate := newDetectedCandidate("", BuildRailpack, DetectedCandidate{
		Name: "Railpack build", Profile: ProfileWeb, Confidence: ConfidenceLow, Port: railpackDefaultPort,
		Evidence: []DetectionEvidence{{Path: ".", Reason: "no recipe or Dockerfile matched this repository; Railpack's own detection reads it when the build is prepared, and stops the deployment there if it finds no language it builds"}},
	})
	if variables != nil {
		candidate.Variables = variables("")
	}
	result.Candidates = append(result.Candidates, candidate)
}

// railpackDefaultPort is the port a Railpack fallback listens on. A Railpack
// image takes its port from PORT, which the runtime sets to the plan's
// internal port, so any port works; this is the one Railpack's own
// providers default to when PORT is unset.
const railpackDefaultPort = 8080

// rootBuilds says whether one of a directory's candidates builds it as
// detected: a recipe, Dockerfile or static site with nothing that refuses it.
func rootBuilds(candidates []DetectedCandidate) bool {
	for _, candidate := range candidates {
		switch candidate.BuildMethod {
		case BuildRecipe, BuildDockerfile, BuildStatic, BuildRailpack:
		default:
			continue
		}
		if candidate.NotDeployable != "" || candidate.RecipeIssue != "" {
			continue
		}
		if _, blocked := candidate.blockingImageIssue(); blocked {
			continue
		}
		return true
	}
	return false
}

// railpackEcosystemCandidate is Railpack in place of a language without a
// recipe: the same directory, name, port and facts, built by the provider
// Railpack has for it.
func railpackEcosystemCandidate(ecosystem DetectedCandidate, provider string) DetectedCandidate {
	candidate := ecosystem
	candidate.Name = ecosystem.Name + " (Railpack)"
	candidate.RecipeIssue = ""
	candidate.Confidence = ConfidenceLow
	candidate.RailpackProvider = provider
	if candidate.Port == 0 && candidate.Profile != ProfileWorker && candidate.Profile != ProfileStatic {
		candidate.Port = railpackDefaultPort
	}
	candidate.Evidence = append(append([]DetectionEvidence(nil), ecosystem.Evidence...), DetectionEvidence{
		Path: rootLabelOf(ecosystem.Root), Reason: "no recipe builds " + ecosystem.Framework + "; Railpack's " + provider + " provider does",
	})
	candidate.NeedsDecision = append([]string(nil), ecosystem.NeedsDecision...)
	return newDetectedCandidate(ecosystem.Root, BuildRailpack, candidate)
}

// annotateRailpackProviders records, on each recipe candidate Railpack could
// build too, the provider it would build it with.
func annotateRailpackProviders(candidates []DetectedCandidate) {
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.BuildMethod == BuildRecipe && candidate.RailpackProvider == "" {
			candidate.RailpackProvider = railpackRecipeProviders[candidate.Recipe]
		}
	}
}
