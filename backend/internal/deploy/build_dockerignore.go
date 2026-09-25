package deploy

import (
	"os"
	"path"
	"slices"
	"strings"
)

// A generated Dockerfile builds from the repository's whole checkout, and
// BuildKit reads `.just-dashboard/Dockerfile.dockerignore` in place of the
// repository's own .dockerignore once it exists. Writing one therefore
// decides both halves of what reaches the image: the dashboard's own
// exclusions always apply, and the repository's rules apply too — except a
// rule written for the repository's own prebuilt Dockerfile that would
// leave out a file the recipe reads (an allowlist `*` drops package.json).

type dockerignoreDrop struct {
	Rule  string
	Input string
}

// recipeInputNames are the root files the automatic recipes read by name.
var recipeInputNames = map[string]bool{
	"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "bun.lock": true,
	"bun.lockb": true, "pnpm-lock.yaml": true, "pnpm-workspace.yaml": true, "yarn.lock": true,
	".yarnrc.yml": true, ".npmrc": true, ".nvmrc": true, ".node-version": true, "tsconfig.json": true,
	"angular.json": true, "Procfile": true,
	"go.mod": true, "go.sum": true, "go.work": true, ".go-version": true,
	"requirements.txt": true, "pyproject.toml": true, "uv.lock": true, "poetry.lock": true,
	".python-version": true, "runtime.txt": true, "manage.py": true,
	"Cargo.toml": true, "Cargo.lock": true, "rust-toolchain": true, "rust-toolchain.toml": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true,
	"settings.gradle.kts": true, "gradlew": true, "gradle": true, "mvnw": true, ".mvn": true, ".java-version": true,
	"deno.json": true, "deno.jsonc": true, "deno.lock": true,
	"composer.json": true, "composer.lock": true, "artisan": true, "index.php": true, "index.html": true,
	"prisma": true, "drizzle.config.ts": true, "drizzle.config.js": true,
}

// jvmVersionFiles are the files a JDK pin is read from (javaVersionPin)
// besides .java-version, which every recipe keeps.
var jvmVersionFiles = []string{".sdkmanrc", ".tool-versions", "system.properties", "mise.toml", ".mise.toml"}

// languageRecipeInputs are the root files a recipe reads by name, kept for
// that recipe alone: a rule leaving out a directory called project/, a
// manifest.toml or a .tool-versions means nothing to a Node or static
// build, which would then copy or serve the file.
var languageRecipeInputs = map[string][]string{
	"java": append([]string{"gradle.properties", "buildSrc", "build-logic"}, jvmVersionFiles...),
	"dotnet": {"Directory.Build.props", "Directory.Build.targets", "Directory.Packages.props", "global.json",
		"NuGet.Config", "nuget.config", "NuGet.config", ".tool-versions", "mise.toml", ".mise.toml"},
	"ruby":    {"Gemfile", "Gemfile.lock", ".ruby-version", ".tool-versions", "config.ru", "Rakefile"},
	"elixir":  {"mix.exs", "mix.lock", ".tool-versions", ".elixir-version", "elixir_buildpack.config"},
	"scala":   append([]string{"build.sbt", "project"}, jvmVersionFiles...),
	"clojure": append([]string{"project.clj", "deps.edn", "build.clj"}, jvmVersionFiles...),
	"dart":    {"pubspec.yaml", "pubspec.lock"},
	"gleam":   {"gleam.toml", "manifest.toml"},
	// Site generators' configuration and the files their builds read: Hugo,
	// Zola, mdBook and Jekyll on their own recipe, MkDocs, Zensical and
	// Pelican on Python's, Lume on Deno's and Hexo on the JavaScript one.
	"site": {"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", ".hvm", "book.toml",
		"_config.yml", "_config.yaml", "Gemfile", "Gemfile.lock", ".ruby-version"},
	"python": {"mkdocs.yml", "mkdocs.yaml", "zensical.toml", "pelicanconf.py", "publishconf.py"},
	"deno":   {"_config.ts", "_config.js"},
	"node":   {"_config.yml", "_config.yaml"},
}

// recipeInputPrefixes are framework configuration files, named per tool.
var recipeInputPrefixes = []string{
	"next.config.", "vite.config.", "nuxt.config.", "svelte.config.", "astro.config.", "remix.config.",
	"react-router.config.", "gatsby-config.", "docusaurus.config.", "eleventy.config.", ".eleventy.",
	"tailwind.config.", "postcss.config.", "webpack.config.", "prisma.config.",
}

func recipeInput(name string) bool {
	if recipeInputNames[name] {
		return true
	}
	for _, prefix := range recipeInputPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".fsproj") || strings.HasSuffix(name, ".vbproj") ||
		strings.HasSuffix(name, ".sln") || strings.HasSuffix(name, ".slnx")
}

// recipeDockerignore renders the ignore file for a generated Dockerfile of
// the given recipe kind ("static" for a plain static site) from the
// repository's own rules and the names at the build root. installInputs are
// the context paths a Node install reads (a trailing slash marks a
// directory), which may sit below the root: a workspace member's
// package.json, .yarn/releases.
func recipeDockerignore(repository []byte, rootNames []string, kind string, installInputs []string) (string, []dockerignoreDrop) {
	rules := parseDockerignore(repository)
	inputs := []string{}
	for _, name := range rootNames {
		if recipeInput(name) || slices.Contains(languageRecipeInputs[kind], name) {
			inputs = append(inputs, name)
		}
	}
	dropped := []dockerignoreDrop{}
	// Setting one rule aside can expose an input a broader earlier rule
	// also covered, so this repeats until no input is left out.
	for round := 0; round < len(rules)+1; round++ {
		removed := false
		for _, input := range inputs {
			excluded, decidedBy := dockerignoreExcludes(rules, input)
			if !excluded {
				continue
			}
			kept := rules[:0:0]
			for _, rule := range rules {
				if rule.Line == decidedBy && !rule.Negate && !removed {
					removed = true
					continue
				}
				kept = append(kept, rule)
			}
			rules = kept
			dropped = append(dropped, dockerignoreDrop{Rule: decidedBy, Input: input})
			break
		}
		if !removed {
			break
		}
	}
	lines := []string{"# Generated by Just Dashboard for the automatic build; the repository's own rules follow."}
	for _, rule := range rules {
		lines = append(lines, rule.Line)
	}
	// An install input below the root is brought back after the rules that
	// leave it out rather than by setting them aside, so the repository's
	// exclusions of everything else under that directory still stand.
	for _, input := range installInputs {
		if len(rules) == 0 {
			break
		}
		directory := strings.HasSuffix(input, "/")
		input = strings.TrimSuffix(input, "/")
		excluded, decidedBy := dockerignoreExcludes(rules, input)
		// A directory is re-included even when the rules keep it, since one
		// of them may still reach a file under it (`*.cjs` and
		// .yarn/releases).
		if !excluded && !directory {
			continue
		}
		if excluded {
			dropped = append(dropped, dockerignoreDrop{Rule: decidedBy, Input: input})
		}
		lines = append(lines, "!"+input)
		if directory {
			lines = append(lines, "!"+input+"/**")
		}
	}
	// These come last so that no repository rule can bring them back.
	lines = append(lines, "**/node_modules", ".dockerignore", ".just-dashboard", ".just-dashboard-build-metadata-*")
	switch kind {
	case "static", "php", "node", "deno", "ruby":
		// Served or copied whole into the runtime image; the history and
		// remote a checkout carries are nobody's business there. Toolchains
		// that stamp or version builds from Git (Go, Python's setuptools-scm,
		// Maven's git-commit-id, SourceLink) keep it.
		lines = append(lines, ".git")
	}
	if kind == "static" {
		// A static site serves every file in its context, and a committed
		// .env is configuration, not content.
		lines = append(lines, ".env", ".env.*")
	}
	return strings.Join(lines, "\n") + "\n", dropped
}

// buildRootNames lists the names at a build root for recipeDockerignore.
func buildRootNames(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for index, entry := range entries {
		if index >= 4096 {
			break
		}
		names = append(names, path.Base(entry.Name()))
	}
	return names
}

// recipeDockerignoreAt renders the ignore file for a generated Dockerfile
// whose build context is root, with what the run log says about each
// repository rule the build set aside or overrode.
func recipeDockerignoreAt(root, kind string, installInputs []string) (string, []string) {
	repository, _ := readContainedRegular(root, ".dockerignore", 256<<10)
	content, dropped := recipeDockerignore(repository, buildRootNames(root), kind, installInputs)
	notes := make([]string, 0, len(dropped))
	for _, drop := range dropped {
		notes = append(notes, ".dockerignore excludes "+drop.Input+" (rule "+drop.Rule+"); the build context keeps it because the build reads it")
	}
	return content, notes
}
