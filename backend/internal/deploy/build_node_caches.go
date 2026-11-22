package deploy

// A JavaScript install starts from a cold download cache on every build
// that reinstalls, and a Next.js build from a cold compiler cache on every
// commit. Both are mounted from BuildKit's cache (recipeCache, scoped to the
// environment and absent from a forced build), so what the last build
// fetched or compiled is reused. A mount never reaches the image.

// installCache is the install RUN's mount of its manager's download cache,
// where that manager looks for it when it runs as root:
//   - npm's ~/.npm (its _cacache);
//   - pnpm's content-addressed store, ~/.local/share/pnpm/store, from which
//     it copies into node_modules when the store is on another mount;
//   - Yarn 1's /usr/local/share/.cache/yarn;
//   - Yarn Berry's global folder, ~/.yarn/berry, whose cache is the global
//     cache, or, when the cache stays in the project, the mirror that
//     project cache is filled from;
//   - Bun's ~/.bun/install/cache.
func (p nodeInstallPlan) installCache(cache recipeCache) string {
	switch {
	case p.manager == "npm":
		return cache.mount("npm", "/root/.npm")
	case p.manager == "pnpm":
		return cache.mount("pnpm", "/root/.local/share/pnpm/store")
	case p.manager == "yarn" && p.berry:
		return cache.mount("yarn-berry", "/root/.yarn/berry")
	case p.manager == "yarn":
		return cache.mount("yarn", "/usr/local/share/.cache/yarn")
	case p.manager == "bun":
		return cache.mount("bun", "/root/.bun/install/cache")
	}
	return ""
}

// installRun is the plan's install RUN with its cache and secret mounts.
func (p nodeInstallPlan) installRun(cache recipeCache, installSecrets string) string {
	return nodeRunWith(p.installCache(cache)+installSecrets, p.installDefaults(), p.installLine())
}

// yarnSettings are the ENV lines a Yarn Berry install runs under. The
// cache stays in the project — and ships with it — only where the
// application needs it: Plug'n'Play resolves packages from the cache's
// archives at run time, and a zero-installs repository commits its cache.
// Otherwise Yarn keeps its cache in the global folder, outside /app.
func (p nodeInstallPlan) yarnSettings() []string {
	switch {
	case !p.berry:
		return nil
	case p.yarnCache || p.yarnPnP:
		return []string{"ENV YARN_ENABLE_GLOBAL_CACHE=false"}
	}
	return []string{"ENV YARN_ENABLE_GLOBAL_CACHE=true"}
}

// nextBuildCaches mount the compiler caches next build keeps under
// .next/cache in workdir: webpack's, Turbopack's and SWC's, which Next.js's
// own CI guidance persists between builds. The fetch cache and the image
// cache beside them are not mounted: a build-time fetch would otherwise
// answer from an earlier build's data, and both stay in the image as before.
func nextBuildCaches(cache recipeCache, workdir string) string {
	mounts := ""
	for _, name := range []string{"webpack", "turbopack", "swc"} {
		mounts += cache.mount("next-"+name, workdir+"/.next/cache/"+name)
	}
	return mounts
}
