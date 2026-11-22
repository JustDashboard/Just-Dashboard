package deploy

import (
	"strings"
	"testing"
)

func TestRecipeCacheIsScopedToTheEnvironmentAndOffForAForcedBuild(t *testing.T) {
	cache := newRecipeCache("just-dashboard/deployment-13:run-18", "reuse")
	if got, want := cache.mount("npm", "/root/.npm"),
		"--mount=type=cache,id=jd-deployment-13-npm,target=/root/.npm,sharing=locked "; got != want {
		t.Fatalf("mount = %q, want %q", got, want)
	}
	other := newRecipeCache("just-dashboard/deployment-14:run-2", "reuse")
	if cache.mount("npm", "/root/.npm") == other.mount("npm", "/root/.npm") {
		t.Fatal("two environments share one cache id")
	}
	for _, off := range []recipeCache{newRecipeCache("just-dashboard/deployment-13:run-19", "no_cache"), newRecipeCache("", "reuse")} {
		if off.enabled() || off.mount("npm", "/root/.npm") != "" {
			t.Fatalf("cache %+v renders a mount", off)
		}
	}
}

func TestEveryGeneratedDockerfileNamesThePinnedFrontend(t *testing.T) {
	if !strings.Contains(recipeDockerfileSyntax, "@sha256:") {
		t.Fatalf("the recipe frontend %q is not pinned by digest", recipeDockerfileSyntax)
	}
	rendered, err := renderStaticDockerfile(BuildPlanConfig{OutputDirectory: "dist"}, staticServing{},
		ResolvedImage{Reference: "nginx:1.29-alpine", Digest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if first, _, _ := strings.Cut(rendered, "\n"); first != recipeDockerfileSyntax {
		t.Fatalf("first line = %q", first)
	}
}
