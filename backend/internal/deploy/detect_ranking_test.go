package deploy

import (
	"strings"
	"testing"
)

func TestRankCandidatesComparesBuildabilityOnlyWithinARoot(t *testing.T) {
	recipe := func(root string, confidence DetectionConfidence, issue string) DetectedCandidate {
		return newDetectedCandidate(root, BuildRecipe, DetectedCandidate{
			Name: "recipe " + root, Recipe: "node", Framework: "nextjs", Confidence: confidence, RecipeIssue: issue,
		})
	}
	dockerfile := func(root string, confidence DetectionConfidence, blocked bool) DetectedCandidate {
		candidate := DetectedCandidate{Name: "Dockerfile " + root, Dockerfile: "Dockerfile", Confidence: confidence}
		if blocked {
			candidate.ImageBuildIssues = []ImageBuildIssue{{Code: "dockerfile_copy_source_missing", Severity: PreflightBlocked, Detail: "line 2 COPY .env: not in the build context ."}}
		}
		return newDetectedCandidate(root, BuildDockerfile, candidate)
	}
	for _, fixture := range []struct {
		name       string
		candidates []DetectedCandidate
		// selected is the index into candidates, or -1 for no selection.
		selected int
		reason   string
	}{
		{
			"a buildable Dockerfile beats an unbuildable recipe for the same directory",
			[]DetectedCandidate{recipe("", ConfidenceHigh, "needs a lockfile"), dockerfile("", ConfidenceHigh, false)},
			1, "cannot build as detected",
		},
		{
			// Buildability does not count across directories; the shallower
			// root, which a build of the repository starts from, does.
			"a buildable helper Dockerfile elsewhere does not beat the application's recipe",
			[]DetectedCandidate{recipe("", ConfidenceHigh, "needs a lockfile"), dockerfile("tools/worker", ConfidenceHigh, false)},
			0, "shallower root",
		},
		{
			"two directories as deep and as strong are the operator's call",
			[]DetectedCandidate{recipe("frontend", ConfidenceHigh, "needs a lockfile"), dockerfile("worker", ConfidenceHigh, false)},
			-1, "equally strong",
		},
		{
			"across directories the stronger evidence wins",
			[]DetectedCandidate{recipe("", ConfidenceHigh, "needs a lockfile"), dockerfile("docker/db", ConfidenceLow, false)},
			0, "stronger evidence",
		},
		{
			"a directory's best candidate represents it: the recipe beside a blocked Dockerfile ties another root as deep",
			[]DetectedCandidate{recipe("apps/web", ConfidenceHigh, ""), dockerfile("apps/web", ConfidenceHigh, true), recipe("apps/api", ConfidenceHigh, "")},
			-1, "equally strong",
		},
		{
			"a stronger directory wins even when a weaker one's candidate builds",
			[]DetectedCandidate{recipe("apps/web", ConfidenceHigh, "needs a lockfile"), dockerfile("apps/docs", ConfidenceMedium, false)},
			0, "stronger evidence",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			candidates := append([]DetectedCandidate(nil), fixture.candidates...)
			selected, reason := rankCandidates(candidates)
			want := ""
			if fixture.selected >= 0 {
				want = fixture.candidates[fixture.selected].ID
			}
			if selected != want || !strings.Contains(reason, fixture.reason) {
				t.Fatalf("selected %q (%s), want %q with a reason saying %q", selected, reason, want, fixture.reason)
			}
			if want != "" && candidates[0].ID != want {
				t.Fatalf("the selected candidate is not listed first: %+v", candidates)
			}
			if len(candidates) != len(fixture.candidates) {
				t.Fatalf("ranking changed the candidate count: %+v", candidates)
			}
		})
	}
}

func TestDetectionDoesNotChooseAHelperDockerfileOverTheApplication(t *testing.T) {
	incident := func(extra map[string]string) map[string]string {
		files := map[string]string{"package.json": nextManifest, "bun.lock": "{}", "package-lock.json": "{}", "app/page.tsx": ""}
		for name, content := range extra {
			files[name] = content
		}
		return files
	}
	t.Run("a database image customised in docker/db is not the application", func(t *testing.T) {
		result := detectFixture(t, incident(map[string]string{
			"docker/db/Dockerfile": "FROM postgres:17\nCOPY init.sql /docker-entrypoint-initdb.d/\n",
			"docker/db/init.sql":   "create table t (id int);\n",
		}))
		selected := selectedFixtureCandidate(t, result)
		if selected.BuildMethod != BuildRecipe || selected.Root != "" {
			t.Fatalf("selected %s at %q (%s), want the root recipe", selected.BuildMethod, selected.Root, result.SelectionReason)
		}
		for _, candidate := range result.Candidates {
			if candidate.BuildMethod == BuildDockerfile && candidate.Confidence != ConfidenceLow {
				t.Fatalf("the postgres Dockerfile kept confidence %s: %+v", candidate.Confidence, candidate.Evidence)
			}
		}
	})
	t.Run("an application Dockerfile in another directory does not outrank the root application", func(t *testing.T) {
		result := detectFixture(t, incident(map[string]string{
			"worker/Dockerfile": "FROM node:22\nWORKDIR /app\nCOPY worker.js .\nCMD [\"node\", \"worker.js\"]\n",
			"worker/worker.js":  "",
		}))
		// The root recipe still owes its package-manager choice; the
		// Dockerfile elsewhere must not win on buildability.
		selected := selectedFixtureCandidate(t, result)
		if selected.BuildMethod != BuildRecipe || selected.Root != "" || !strings.Contains(result.SelectionReason, "shallower root") {
			t.Fatalf("selected %s at %q (%s), want the root recipe", selected.BuildMethod, selected.Root, result.SelectionReason)
		}
	})
}

// One list of browser prefixes: what environment discovery classifies as
// browser-inlined for a framework is what a Dockerfile build may receive as
// a plain build argument, and what preflight names.
func TestPublicBuildVariablesFollowTheBrowserPrefixRules(t *testing.T) {
	t.Parallel()
	for _, rule := range browserPrefixRules {
		if !publicBuildVariable(rule.prefix+"API_URL") || !strings.Contains(publicBuildPrefixList(), rule.prefix) {
			t.Fatalf("%s is not a public build prefix", rule.prefix)
		}
	}
	if publicBuildVariable("DATABASE_URL") || publicBuildPrefixList() != "NEXT_PUBLIC_, VITE_, PUBLIC_, REACT_APP_, NUXT_PUBLIC_, EXPO_PUBLIC_, GATSBY_ or VUE_APP_" {
		t.Fatalf("list = %q", publicBuildPrefixList())
	}
}
