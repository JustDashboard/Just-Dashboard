package deploy

import (
	"path"
	"path/filepath"
	"regexp"
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

// The golang images' module cache (GOPATH=/go) and build cache.
const (
	goModCache   = "/go/pkg/mod"
	goBuildCache = "/root/.cache/go-build"
)

// goModuleMount caches the modules go mod download fetches, which every
// later go command reads. Like Rust's crates, a download that needs an
// install secret (a private module's token) keeps today's layer, so the
// build, which has no token, never has to fetch a private module again.
func goModuleMount(cache recipeCache, installSecrets string) string {
	if installSecrets != "" {
		return ""
	}
	return cache.mount("go-mod", goModCache)
}

// goCompileMounts are what a step that compiles reads: the modules and the
// build cache, which holds no secret and lets a commit recompile only the
// packages it changed.
func goCompileMounts(cache recipeCache, installSecrets string) string {
	return goModuleMount(cache, installSecrets) + cache.mount("go-build", goBuildCache)
}

// recipeCopyPathRE is a context path safe to write unquoted into COPY: no
// space, quote, variable or wildcard.
var recipeCopyPathRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@+/-]{0,255}$`)

// goModuleInputs are the context paths go mod download reads, which the
// recipe copies before the source so a commit that changes only code keeps
// the download: the module's go.mod and go.sum, and the go.mod of each local
// replacement. It is nil, and the download stays after the source, where
// the inputs are not all knowable: a vendored module downloads nothing, a
// go.work workspace lists its packages' imports rather than downloading
// (goWorkspaceDownload), and a replacement without a go.mod or with a path
// COPY cannot name is left to the build as today.
func goModuleInputs(boundary, root string, plan goBuildPlan) []string {
	if plan.vendored || plan.context.workspace {
		return nil
	}
	contextRel := checkoutPath(boundary, root)
	if plan.context.dir != "" {
		contextRel = plan.context.dir
	}
	inputs := []string{path.Join(plan.context.module, "go.mod")}
	if regularExists(root, "go.sum") {
		inputs = append(inputs, path.Join(plan.context.module, "go.sum"))
	}
	for _, target := range plan.context.replaces {
		if !regularExists(filepath.Join(boundary, filepath.FromSlash(target)), "go.mod") {
			return nil
		}
		inputs = append(inputs, path.Join(relativeBuildPath(rootLabelOf(contextRel), rootLabelOf(target)), "go.mod"))
	}
	inputs = uniqueOrdered(inputs)
	for _, input := range inputs {
		if !recipeCopyPathRE.MatchString(input) || strings.Contains(input, "..") {
			return nil
		}
	}
	return inputs
}

// copyInputsTo renders the COPY instructions that place context files at
// the same paths under dir, one per directory.
func copyInputsTo(dir string, inputs []string) []string {
	byDir := map[string][]string{}
	order := []string{}
	for _, input := range inputs {
		parent := path.Dir(input)
		if _, seen := byDir[parent]; !seen {
			order = append(order, parent)
		}
		byDir[parent] = append(byDir[parent], input)
	}
	lines := make([]string, 0, len(order))
	for _, parent := range order {
		destination := dir + "/"
		if parent != "." {
			destination = dir + "/" + parent + "/"
		}
		lines = append(lines, "COPY "+strings.Join(byDir[parent], " ")+" "+destination)
	}
	return lines
}
