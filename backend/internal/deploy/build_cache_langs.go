package deploy

import (
	"path"
	"strings"
)

// The language recipes' package and compiler caches, each a
// config.cache.mount (build_cache_mounts.go): environment-scoped, and
// nothing at all on a forced build, which then builds exactly as it did
// before the caches existed. A mount's content never reaches a layer, so
// none of these can ship in an image or carry one build's secret into the
// next build's layer; and each tool treats its cache as disposable, so a
// mount the BuildKit garbage collector emptied is a cold build, not a
// failed one.

// cargoHome is CARGO_HOME in the official rust images.
const cargoHome = "/usr/local/cargo"

// rustDependencyMounts are the crates Cargo downloads (the registry's index
// and archives, and git dependencies' checkouts), which cargo fetch and the
// build both read. A fetch that needs an install secret (a private
// registry's token) keeps today's layer instead: the build step has no
// token, so it must find every crate in the image rather than in a cache
// the garbage collector may have emptied between the two steps.
func rustDependencyMounts(cache recipeCache, installSecrets string) string {
	if installSecrets != "" {
		return ""
	}
	return cache.mount("cargo-registry", cargoHome+"/registry") + cache.mount("cargo-git", cargoHome+"/git")
}

// rustTargetMount caches Cargo's output directory, so a commit recompiles
// only the crates it changed rather than every dependency. name keeps the
// musl build's directory apart from the Debian one the Leptos and Trunk
// builds use: their host toolchains differ, and Cargo would rebuild each
// from the other's anyway. It renders nothing when the directory is not
// one the build can mount without hiding the source.
func rustTargetMount(cache recipeCache, recipe rustRecipe, name string) string {
	directory := rustTargetPath(recipe)
	if directory == "" {
		return ""
	}
	return cache.mount(name, directory)
}

// rustTargetPath is Cargo's output directory in the build stage, or "" when
// the build keeps it out of the cache: an absolute target-dir, which could
// name a directory of the image itself, and one holding the checkout or
// the crate, which the mount would hide.
func rustTargetPath(recipe rustRecipe) string {
	directory := recipe.targetDir
	if directory == "" {
		directory = "target"
	}
	if strings.HasPrefix(directory, "/") {
		return ""
	}
	directory = path.Clean("/src/" + directory)
	if !strings.HasPrefix(directory, "/src/") || strings.HasPrefix(rustSourceDir(recipe)+"/", directory+"/") {
		return ""
	}
	return directory
}
