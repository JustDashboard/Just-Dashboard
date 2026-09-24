package deploy

import (
	"fmt"
	"sort"
	"strings"
)

// Selection used to compare confidence alone, so a repository with both a
// Dockerfile and a recognised framework — the most ordinary shape there is —
// stopped on "choose one" every time, and the form quietly filled itself
// from whichever candidate sorted first (Compose, which cannot build from a
// Git source). Candidates for the same directory are compared on whether
// each can be chosen on its own at all, whether it builds as detected, its
// confidence, and what the repository says it intends; different
// directories only on whether they can be chosen and their confidence.

type candidateScore struct {
	selectable bool
	buildable  bool
	confidence int
	preference int
	// blocker is why the candidate cannot build as detected, for the reason.
	blocker string
}

// publicBuildPrefixes name the variables a framework compiles into its
// browser bundle, which only exist in the image if the build receives them.
var publicBuildPrefixes = []string{"NEXT_PUBLIC_", "VITE_", "PUBLIC_", "NUXT_PUBLIC_", "REACT_APP_", "EXPO_PUBLIC_"}

func publicBuildVariable(name string) bool {
	for _, prefix := range publicBuildPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// reservedBuildArgName keeps a build argument from replacing the builder's
// own environment: the value travels as a variable of that name in buildx's
// process environment.
func reservedBuildArgName(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"DOCKER_", "BUILDKIT_", "BUILDX_", "JD_", "VPSD_", "XDG_", "COMPOSE_"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	switch upper {
	case "PATH", "HOME", "USER", "SHELL", "TMPDIR", "PWD", "LD_PRELOAD", "LD_LIBRARY_PATH", "SSH_AUTH_SOCK":
		return true
	}
	return false
}

func scoreCandidate(candidate DetectedCandidate, only bool) candidateScore {
	score := candidateScore{selectable: true, buildable: true, confidence: confidenceRank(candidate.Confidence)}
	if issue, blocked := candidate.blockingImageIssue(); blocked {
		score.buildable, score.blocker = false, issue.Detail
	}
	if candidate.RecipeIssue != "" {
		score.buildable, score.blocker = false, candidate.RecipeIssue
	}
	if candidate.Recipe == "node" && candidate.PackageManager == "" && len(candidate.PackageManagers) > 1 {
		score.buildable = false
		score.blocker = "it needs a package manager chosen among " + strings.Join(candidate.PackageManagers, ", ")
	}
	switch candidate.BuildMethod {
	case BuildDockerfile:
		switch candidate.DockerfileRole {
		case DockerfileRoleDevelopment:
			score.selectable = false
		case DockerfileRoleProduction:
			score.preference = 4
		default:
			score.preference = 3
		}
		if score.preference > 0 && dockerfileMissesPublicArg(candidate) != "" {
			score.preference = 1
		}
		for _, issue := range candidate.ImageBuildIssues {
			if issue.Code == "dockerfile_dev_server" {
				score.preference = 0
			}
		}
	case BuildCompose:
		// A Compose file found in a repository has not been analysed as a
		// Compose source, and the plan cannot build it until it is.
		score.buildable = false
		score.blocker = "it has to be deployed as a Compose source to be analysed"
		score.selectable = only
	default:
		score.preference = 2
	}
	return score
}

// dockerfileMissesPublicArg is a browser-public variable the source reads
// that the Dockerfile declares no ARG for: a Dockerfile build could never
// compile it into the bundle, where the recipe passes it to the build.
func dockerfileMissesPublicArg(candidate DetectedCandidate) string {
	declared := map[string]bool{}
	for _, arg := range candidate.DockerfileArgs {
		declared[arg.Name] = true
	}
	for _, variable := range candidate.Variables {
		if publicBuildVariable(variable.Name) && !declared[variable.Name] {
			return variable.Name
		}
	}
	return ""
}

// outranksAtRoot compares two candidates for the same directory: whether
// each can be chosen on its own, whether it builds as detected, its
// confidence, and what the repository says it intends. These are two ways to
// build one application, so the one that builds wins.
func (s candidateScore) outranksAtRoot(other candidateScore) int {
	if result := compareFlags(s.selectable, other.selectable); result != 0 {
		return result
	}
	if result := compareFlags(s.buildable, other.buildable); result != 0 {
		return result
	}
	if result := compareRanks(s.confidence, other.confidence); result != 0 {
		return result
	}
	return compareRanks(s.preference, other.preference)
}

// outranksAcrossRoots compares the best candidates of two directories, which
// are different things rather than two ways to build one: a helper image in
// docker/db/ that builds is no better an answer than the application whose
// recipe needs one decision, so only whether each can be chosen and how
// strong its evidence is count, and anything closer is the operator's call.
func (s candidateScore) outranksAcrossRoots(other candidateScore) int {
	if result := compareFlags(s.selectable, other.selectable); result != 0 {
		return result
	}
	return compareRanks(s.confidence, other.confidence)
}

func compareFlags(a, b bool) int {
	switch {
	case a && !b:
		return 1
	case b && !a:
		return -1
	}
	return 0
}

func compareRanks(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

// rankCandidates orders candidates best first — so a form that has to fall
// back to the first one falls back to a strong one — and returns the
// selected candidate with the reason it won, or no selection with the
// reason nothing did. Each directory's candidates are ordered among
// themselves, then directories by their best candidate, which keeps the
// order a true ordering although the two comparisons differ.
func rankCandidates(candidates []DetectedCandidate) (string, string) {
	if len(candidates) == 0 {
		return "", ""
	}
	only := len(candidates) == 1
	scores := make(map[string]candidateScore, len(candidates))
	byRoot := map[string][]DetectedCandidate{}
	roots := []string{}
	for _, candidate := range candidates {
		scores[candidate.ID] = scoreCandidate(candidate, only)
		if byRoot[candidate.Root] == nil {
			roots = append(roots, candidate.Root)
		}
		byRoot[candidate.Root] = append(byRoot[candidate.Root], candidate)
	}
	for _, root := range roots {
		group := byRoot[root]
		sort.SliceStable(group, func(i, j int) bool {
			if order := scores[group[i].ID].outranksAtRoot(scores[group[j].ID]); order != 0 {
				return order > 0
			}
			if group[i].BuildMethod != group[j].BuildMethod {
				return group[i].BuildMethod < group[j].BuildMethod
			}
			return group[i].ID < group[j].ID
		})
	}
	leader := func(root string) candidateScore { return scores[byRoot[root][0].ID] }
	sort.SliceStable(roots, func(i, j int) bool {
		if order := leader(roots[i]).outranksAcrossRoots(leader(roots[j])); order != 0 {
			return order > 0
		}
		return roots[i] < roots[j]
	})
	candidates = candidates[:0]
	for _, root := range roots {
		candidates = append(candidates, byRoot[root]...)
	}
	best := candidates[0]
	bestScore := scores[best.ID]
	if !bestScore.selectable {
		if len(candidates) == 1 {
			return "", "only " + candidateLabel(best) + " was found, which is written for development; choose it to deploy it anyway"
		}
		return "", fmt.Sprintf("none of the %d candidates is chosen on its own: development Dockerfiles, or a Compose file that has to be deployed as a Compose source", len(candidates))
	}
	tied := []string{}
	if group := byRoot[best.Root]; len(group) > 1 && bestScore.outranksAtRoot(scores[group[1].ID]) == 0 {
		tied = append(tied, candidateLabel(group[1]))
	}
	for _, root := range roots[1:] {
		if bestScore.outranksAcrossRoots(leader(root)) == 0 {
			tied = append(tied, candidateLabel(byRoot[root][0]))
		}
	}
	if len(tied) > 0 {
		return "", fmt.Sprintf("%s and %s are equally strong", candidateLabel(best), strings.Join(tied, ", "))
	}
	if len(candidates) == 1 {
		return best.ID, "the only candidate: " + candidateLabel(best)
	}
	runnerUp := candidates[1]
	runnerScore := scores[runnerUp.ID]
	sameRoot := runnerUp.Root == best.Root
	reason := candidateLabel(best) + " over " + candidateLabel(runnerUp) + ": "
	devServer, runnerStartsDevServer := dockerfileDevServerIssue(runnerUp)
	switch {
	case runnerStartsDevServer:
		reason += "it starts a development server (" + devServer + ")"
	case !runnerScore.selectable:
		reason += candidateLabel(runnerUp) + " is written for development"
		if runnerUp.BuildMethod == BuildCompose {
			reason = candidateLabel(best) + " over " + candidateLabel(runnerUp) + ": " + runnerScore.blocker
		}
	case sameRoot && bestScore.buildable && !runnerScore.buildable:
		reason += candidateLabel(runnerUp) + " cannot build as detected; " + runnerScore.blocker
	case bestScore.confidence != runnerScore.confidence || !sameRoot:
		reason += "stronger evidence"
	case best.BuildMethod == BuildDockerfile:
		reason += "the repository's own Dockerfile builds it as written"
	case runnerUp.BuildMethod == BuildDockerfile && dockerfileMissesPublicArg(runnerUp) != "":
		reason += "the Dockerfile declares no ARG for " + dockerfileMissesPublicArg(runnerUp) + ", so its build could not receive it"
	default:
		reason += "stronger evidence"
	}
	if len(reason) > 500 {
		reason = reason[:497] + "..."
	}
	if rejectPlanSecretLiteral("selection reason", reason) != nil {
		reason = "selected over " + fmt.Sprint(len(candidates)-1) + " other candidate(s)"
	}
	return best.ID, reason
}

func dockerfileDevServerIssue(candidate DetectedCandidate) (string, bool) {
	for _, issue := range candidate.ImageBuildIssues {
		if issue.Code == "dockerfile_dev_server" {
			return issue.Detail, true
		}
	}
	return "", false
}

// candidateLabel names a candidate the way a reader would: the Dockerfile's
// path, the framework's recipe, the Compose file.
func candidateLabel(candidate DetectedCandidate) string {
	switch candidate.BuildMethod {
	case BuildDockerfile:
		return joinRoot(candidate.Root, candidate.Dockerfile)
	case BuildCompose:
		return "the Compose file in " + rootLabel(candidate.Root)
	case BuildStatic:
		return "the static site in " + rootLabel(candidate.Root)
	}
	label := candidate.Framework
	if label == "" {
		label = candidate.Recipe
	}
	if label == "" {
		label = "automatic"
	}
	return "the " + label + " recipe in " + rootLabel(candidate.Root)
}
