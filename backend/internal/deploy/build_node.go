package deploy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// nodeLockfiles is every lockfile a frozen install can pin, with the manager
// that reads it. Order breaks the tie only between one manager's own files.
var nodeLockfiles = []struct{ path, manager string }{
	{"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"},
	{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
}

func validNodePackageManager(manager string) bool {
	switch manager {
	case "bun", "npm", "pnpm", "yarn":
		return true
	}
	return false
}

// declaredNodePackageManager reads the Corepack packageManager field
// ("bun@1.2.21"), the one place a repository states its manager itself.
func declaredNodePackageManager(manifest []byte) string {
	var declared struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(manifest, &declared) != nil {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimSpace(declared.PackageManager), "@")
	if !validNodePackageManager(name) {
		return ""
	}
	return name
}

// resolveNodePackageManager picks the manager and lockfile a frozen install
// uses from the lockfile names present at the package root. An operator's
// selection wins, then the manifest's packageManager field. Between competing
// lockfiles with neither, it refuses rather than guesses: repositories often
// keep the lockfile of a manager they stopped using, and installing from it
// builds dependency versions nobody has run.
func resolveNodePackageManager(present []string, declared, selected string) (string, string, error) {
	lockfileFor := func(manager string) string {
		for _, lock := range nodeLockfiles {
			if lock.manager == manager && slices.Contains(present, lock.path) {
				return lock.path
			}
		}
		return ""
	}
	if selected != "" {
		if lockfile := lockfileFor(selected); lockfile != "" {
			return selected, lockfile, nil
		}
		return "", "", fmt.Errorf("%w: the build uses %s, but the source has no %s lockfile", ErrUnsupportedBuilder, selected, selected)
	}
	found := []string{}
	for _, lock := range nodeLockfiles {
		if slices.Contains(present, lock.path) {
			found = append(found, lock.path)
		}
	}
	switch {
	case len(found) == 0:
		return "", "", fmt.Errorf("%w: Node recipes require a lockfile (bun.lock, package-lock.json, pnpm-lock.yaml or yarn.lock)", ErrUnsupportedBuilder)
	case len(found) == 1:
		return nodeManagerForLockfile(found[0]), found[0], nil
	case declared != "" && lockfileFor(declared) != "":
		return declared, lockfileFor(declared), nil
	}
	return "", "", fmt.Errorf("%w: competing lockfiles %s; choose the package manager in the build settings, declare packageManager in package.json, or delete the lockfile this project no longer uses", ErrUnsupportedBuilder, strings.Join(found, " and "))
}

func nodeManagerForLockfile(path string) string {
	for _, lock := range nodeLockfiles {
		if lock.path == path {
			return lock.manager
		}
	}
	return ""
}

// nodePackageManagers lists each manager with a lockfile present, once.
func nodePackageManagers(present []string) []string {
	managers := []string{}
	for _, lock := range nodeLockfiles {
		if slices.Contains(present, lock.path) && !slices.Contains(managers, lock.manager) {
			managers = append(managers, lock.manager)
		}
	}
	return managers
}

// Package manifests are inert input. Detection and preparation never evaluate
// a repository's JavaScript configuration on the dashboard host.

func validateNodeRecipeContent(content []byte, config BuildPlanConfig) (string, error) {
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(content, &manifest) != nil {
		return "", fmt.Errorf("%w: package.json is malformed", ErrUnsupportedBuilder)
	}
	has := func(name string) bool {
		return manifest.Dependencies[name] != "" || manifest.DevDependencies[name] != ""
	}
	if !has("@sveltejs/kit") {
		return "", nil
	}
	if has("@sveltejs/adapter-node") == has("@sveltejs/adapter-static") {
		return "", fmt.Errorf("%w: SvelteKit requires one of adapter-node or adapter-static; configure one supported adapter or use a Dockerfile", ErrUnsupportedBuilder)
	}
	if strings.TrimSpace(config.BuildCommand) == "" {
		return "", fmt.Errorf("%w: SvelteKit needs a build command", ErrUnsupportedBuilder)
	}
	if has("@sveltejs/adapter-static") {
		if strings.TrimSpace(config.OutputDirectory) == "" {
			return "", fmt.Errorf("%w: SvelteKit adapter-static needs its generated output directory (normally build)", ErrUnsupportedBuilder)
		}
		return "sveltekit-static", nil
	}
	if config.OutputDirectory != "" {
		return "", fmt.Errorf("%w: SvelteKit adapter-node produces a server; clear static output and set a server start command", ErrUnsupportedBuilder)
	}
	return "sveltekit-node", nil
}
