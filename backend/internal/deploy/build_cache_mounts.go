package deploy

// recipeDockerfileSyntax is the BuildKit Dockerfile frontend every generated
// Dockerfile names, pinned by digest so the frontend that parses a recipe is
// as fixed as its bases. 1.22 is the first pin with COPY --exclude, which
// lets a runtime stage copy node_modules as a layer of its own and the rest
// of the tree without it.
const recipeDockerfileSyntax = "# syntax=docker/dockerfile:1.22@sha256:4a43a54dd1fedceb30ba47e76cfcf2b47304f4161c0caeac2db1c61804ea3c91"

// recipeCache renders the BuildKit cache mounts a generated Dockerfile gives
// a package manager's download cache or a compiler's incremental cache, so a
// build after a lockfile or source change reuses what the last build fetched
// or compiled instead of starting cold.
//
// The id is scoped to the release tag's repository, "deployment-<environment>",
// the scope Railpack's cache key already has (railpackCacheKey). One
// environment never reads another's cache: not another project's, and not a
// staging or preview environment's, whose branch could otherwise seed what
// production's build reuses.
//
// A forced build (CachePolicy "no_cache") renders no mounts: BuildKit's
// --no-cache leaves cache mounts as they are, and a forced build has to start
// from nothing.
//
// A mount's content never reaches the image, since BuildKit leaves its target
// empty in the layer. A cache inside the tree the runtime stage copies (a
// Next.js .next/cache, Cargo's target/) is therefore safe to mount, but
// anything the image needs from it has to be copied out in the same RUN.
type recipeCache struct {
	scope string
}

func newRecipeCache(tag, policy string) recipeCache {
	if policy == "no_cache" || tag == "" {
		return recipeCache{}
	}
	return recipeCache{scope: railpackCacheKey(tag)}
}

// enabled reports whether mount renders anything for this build.
func (c recipeCache) enabled() bool { return c.scope != "" }

// mount is the --mount flag for one named cache, followed by a space so it
// prefixes a RUN's command the way buildSecretMounts' flags do, or "" when
// this build has no caches. name says whose cache it is ("npm",
// "cargo-registry", "cargo-target"); target is where the tool looks for it.
// sharing=locked: one environment never builds twice at once, and if it
// ever did, a compiler's cache must not have two writers.
func (c recipeCache) mount(name, target string) string {
	if c.scope == "" {
		return ""
	}
	return "--mount=type=cache,id=jd-" + c.scope + "-" + name + ",target=" + target + ",sharing=locked "
}
