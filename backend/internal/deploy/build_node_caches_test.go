package deploy

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// nodeCacheMountRE matches the cache mounts build_node_caches.go renders,
// which tests of an install's command leave out of what they compare.
var nodeCacheMountRE = regexp.MustCompile(`--mount=type=cache,id=jd-[a-z0-9._-]*?-(?:npm|pnpm|yarn|yarn-berry|bun|next-webpack|next-turbopack|next-swc),[^ ]+ `)

func withoutNodeCacheMounts(dockerfile string) string {
	return nodeCacheMountRE.ReplaceAllString(dockerfile, "")
}

func TestNodeInstallMountsItsManagersCache(t *testing.T) {
	t.Parallel()
	cache := newRecipeCache("just-dashboard/deployment-4:run-9", "reuse")
	for _, test := range []struct {
		plan   nodeInstallPlan
		target string
	}{
		{nodeInstallPlan{manager: "npm"}, "id=jd-deployment-4-npm,target=/root/.npm,"},
		{nodeInstallPlan{manager: "pnpm"}, "id=jd-deployment-4-pnpm,target=/root/.local/share/pnpm/store,"},
		{nodeInstallPlan{manager: "yarn"}, "id=jd-deployment-4-yarn,target=/usr/local/share/.cache/yarn,"},
		{nodeInstallPlan{manager: "yarn", berry: true}, "id=jd-deployment-4-yarn-berry,target=/root/.yarn/berry,"},
		{nodeInstallPlan{manager: "bun"}, "id=jd-deployment-4-bun,target=/root/.bun/install/cache,"},
	} {
		test.plan.command = test.plan.manager + " install"
		run := test.plan.installRun(cache, "--mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN,required=true ")
		if !strings.HasPrefix(run, "RUN --mount=type=cache,"+test.target+"sharing=locked --mount=type=secret,id=NPM_TOKEN") {
			t.Fatalf("%s install = %q", test.plan.manager, run)
		}
		if forced := test.plan.installRun(newRecipeCache("just-dashboard/deployment-4:run-10", "no_cache"), ""); forced != "RUN "+test.plan.command {
			t.Fatalf("a forced build's %s install = %q", test.plan.manager, forced)
		}
	}
}

// Yarn Berry's cache leaves the image unless the application needs it.
func TestYarnBerryKeepsItsCacheInTheProjectOnlyWhereTheAppReadsIt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		plan nodeInstallPlan
		want string
	}{
		{nodeInstallPlan{berry: true}, "ENV YARN_ENABLE_GLOBAL_CACHE=true"},
		{nodeInstallPlan{berry: true, yarnPnP: true}, "ENV YARN_ENABLE_GLOBAL_CACHE=false"},
		{nodeInstallPlan{berry: true, yarnCache: true}, "ENV YARN_ENABLE_GLOBAL_CACHE=false"},
	} {
		if got := strings.Join(test.plan.yarnSettings(), "\n"); got != test.want {
			t.Fatalf("%+v: %q, want %q", test.plan, got, test.want)
		}
	}
	if settings := (nodeInstallPlan{}).yarnSettings(); settings != nil {
		t.Fatalf("Yarn 1 settings = %v", settings)
	}
	pnp := planNodeInstall(readNodeTree(t, map[string]string{
		"package.json": `{"name":"app","packageManager":"yarn@4.9.2","scripts":{"build":"yarn tsc","start":"yarn node dist/index.js"}}`,
		".yarnrc.yml":  "nodeLinker: pnp\n",
	}, "").facts, nodeInstallChoice{build: "yarn build", start: "yarn start"})
	linked := planNodeInstall(readNodeTree(t, map[string]string{
		"package.json": `{"name":"app","packageManager":"yarn@4.9.2","scripts":{"build":"tsc","start":"node dist/index.js"}}`,
		".yarnrc.yml":  "nodeLinker: pnp\n",
	}, "").facts, nodeInstallChoice{build: "yarn build", start: "node dist/index.js"})
	if !pnp.yarnPnP || linked.yarnPnP {
		t.Fatalf("Plug'n'Play: through yarn %v, adjusted to node_modules %v (env %q)", pnp.yarnPnP, linked.yarnPnP, linked.env)
	}
}

// A Next.js build mounts its compiler caches under its own directory, and
// never the fetch cache, which would answer from an earlier build's data.
func TestNextBuildMountsItsCompilerCaches(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":      `{"name":"web","version":"1.0.0","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.3.5"}}`,
		"package-lock.json": `{"name":"web","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"web","version":"1.0.0","dependencies":{"next":"16.3.5"}},"node_modules/next":{"version":"16.3.5"}}}`,
	}
	prepare := func(force bool) string {
		prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, files), BuildPlanConfig{
			Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start",
		}, force, "just-dashboard/deployment-5:run-1")
		if err != nil {
			t.Fatal(err)
		}
		return prepared.DockerfilePreview
	}
	dockerfile := prepare(false)
	want := "RUN --mount=type=cache,id=jd-deployment-5-next-webpack,target=/app/.next/cache/webpack,sharing=locked " +
		"--mount=type=cache,id=jd-deployment-5-next-turbopack,target=/app/.next/cache/turbopack,sharing=locked " +
		"--mount=type=cache,id=jd-deployment-5-next-swc,target=/app/.next/cache/swc,sharing=locked npm run build\n"
	assertDockerfile(t, dockerfile, []string{want, "RUN --mount=type=cache,id=jd-deployment-5-npm,target=/root/.npm,sharing=locked npm ci\n"},
		[]string{"fetch-cache", "target=/app/.next/cache,"})
	if forced := prepare(true); strings.Contains(forced, "type=cache") {
		t.Fatalf("a forced build mounts a cache:\n%s", forced)
	}
}
