package deploy

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The live fixtures are built from the internet on every run, so a fixture
// that asks for "streamlit" gets whatever was released that morning. In
// November 2026 a new Streamlit changed what its page names, the framework
// job failed on main and on both patch branches without a line of this
// repository having changed, and the release workflow would not tag 0.8.5.
//
// So a fixture says exactly what it installs: in a lock file where its tool
// writes one, and otherwise as an exact version in the manifest itself. This
// reads every manifest that has no lock beside it and fails on a range. A
// version is bumped on purpose, in a change that runs the live job.
//
// It holds the direct dependencies. What those pull in still resolves at
// build time for the fixtures without a lock, which is the limit of a
// manifest: the lock-less fixtures are there to prove the lock-less build.
func TestAppFixturesNameExactVersions(t *testing.T) {
	root := filepath.Join("testdata", "app-fixtures")
	var loose []string
	report := func(path, what string) {
		rel, _ := filepath.Rel(root, path)
		loose = append(loose, rel+": "+what)
	}
	has := func(dir string, names ...string) bool {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		return false
	}
	exactPython := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._-]+\])?==[A-Za-z0-9.!+]+$`)
	exactVersion := regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
	pyprojectDependencies := regexp.MustCompile(`(?ms)^dependencies\s*=\s*\[(.*?)\]`)
	quoted := regexp.MustCompile(`"([^"]+)"|'([^']+)'`)

	seen := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		dir, name := filepath.Dir(path), entry.Name()
		read := func() []byte {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			seen++
			return b
		}
		switch {
		case strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt"):
			for _, line := range strings.Split(string(read()), "\n") {
				line, _, _ = strings.Cut(line, "#")
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "-") {
					continue
				}
				if !exactPython.MatchString(strings.ReplaceAll(line, " ", "")) {
					report(path, line)
				}
			}
		case name == "pyproject.toml":
			if has(dir, "uv.lock", "pdm.lock", "poetry.lock") {
				return nil
			}
			if list := pyprojectDependencies.FindSubmatch(read()); list != nil {
				for _, match := range quoted.FindAllStringSubmatch(string(list[1]), -1) {
					if requirement := match[1] + match[2]; !exactPython.MatchString(strings.ReplaceAll(requirement, " ", "")) {
						report(path, requirement)
					}
				}
			}
		case name == "package.json":
			if has(dir, "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb") {
				return nil
			}
			var manifest struct {
				Dependencies         map[string]string `json:"dependencies"`
				DevDependencies      map[string]string `json:"devDependencies"`
				OptionalDependencies map[string]string `json:"optionalDependencies"`
			}
			if err := json.Unmarshal(read(), &manifest); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			for _, group := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies} {
				for dependency, version := range group {
					local := strings.HasPrefix(version, "file:") || strings.HasPrefix(version, "link:") || strings.HasPrefix(version, "workspace:")
					if !local && !exactVersion.MatchString(version) {
						report(path, dependency+" "+version)
					}
				}
			}
		case name == "composer.json":
			if has(dir, "composer.lock") {
				return nil
			}
			var manifest struct {
				Require    map[string]string `json:"require"`
				RequireDev map[string]string `json:"require-dev"`
			}
			if err := json.Unmarshal(read(), &manifest); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			for _, group := range []map[string]string{manifest.Require, manifest.RequireDev} {
				for dependency, version := range group {
					// The platform is what the recipe reads to choose an image,
					// not something Composer installs.
					platform := dependency == "php" || strings.HasPrefix(dependency, "ext-") || strings.HasPrefix(dependency, "lib-")
					if !platform && !exactVersion.MatchString(version) {
						report(path, dependency+" "+version)
					}
				}
			}
		case name == "Gemfile":
			seen++
			if !has(dir, "Gemfile.lock") {
				report(path, "no Gemfile.lock beside it")
			}
		case name == "mix.exs":
			seen++
			if !has(dir, "mix.lock") {
				report(path, "no mix.lock beside it")
			}
		case name == "Pipfile":
			seen++
			if !has(dir, "Pipfile.lock") {
				report(path, "no Pipfile.lock beside it")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 10 {
		t.Fatalf("read %d manifests under %s; the fixtures have moved and this checks nothing", seen, root)
	}
	if len(loose) > 0 {
		sort.Strings(loose)
		t.Fatalf("these fixtures install whatever is newest when they are built; name an exact version (or commit the tool's lock file):\n  %s",
			strings.Join(loose, "\n  "))
	}
}
