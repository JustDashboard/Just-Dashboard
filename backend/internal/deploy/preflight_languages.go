package deploy

import "strings"

// languageRecipeFindings are what the Ruby, Elixir, Scala, Clojure, Dart
// and Gleam recipes know before Deploy that no other finding says: a
// Gemfile.lock resolved for another platform than this server's, a gem the
// build cannot clone, and a Bundler too old for the Ruby it would run on.
func languageRecipeFindings(candidate *DetectedCandidate, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	if candidate == nil || candidate.Toolchain == nil || candidate.Toolchain.Language != "ruby" {
		return nil
	}
	build := configuration.Build
	arch := observation.Architecture
	if _, platform, found := strings.Cut(strings.ToLower(build.TargetPlatform), "/"); found {
		arch, _, _ = strings.Cut(platform, "/")
	}
	recipe := build.Method == BuildRecipe && firstNonEmpty(build.Recipe, candidate.Recipe) == "ruby"
	toolchain := candidate.Toolchain
	findings := []PreflightFinding{}
	platforms := toolchain.LockPlatforms
	if build.Method == BuildDockerfile && candidate.BuildMethod == BuildDockerfile && len(platforms) > 0 {
		// A Dockerfile that adds the server's platform to the lock before
		// it installs has fixed what this finding is about.
		platforms = append(append([]string(nil), platforms...), toolchain.AddedPlatforms...)
	}
	if platform, ok := rubyLockPlatform(platforms, arch); !ok {
		listed := "Gemfile.lock PLATFORMS: " + strings.Join(toolchain.LockPlatforms, ", ")
		switch {
		case recipe:
			findings = append(findings, finding("ruby_lock_platform_added", PreflightWarning,
				"Gemfile.lock has no platform for this server", listed,
				"A frozen install refuses a lock without "+platform+", so the build adds it with bundle lock --add-platform "+platform+" first; the gems it resolves for it are not the ones anyone tested.",
				"Run `bundle lock --add-platform x86_64-linux aarch64-linux` and commit Gemfile.lock.", "deploy", "configuration.build"))
		case build.Method == BuildDockerfile && candidate.BuildMethod == BuildDockerfile:
			severity, means := PreflightWarning, "The Dockerfile installs the bundle without freezing it, so Bundler resolves "+platform+" itself; the gems it picks are not the ones anyone tested."
			if toolchain.BundleFrozen {
				severity, means = PreflightBlocked, "The Dockerfile installs the bundle frozen (BUNDLE_DEPLOYMENT), and Bundler refuses a lock without "+platform+": \"Your bundle only supports platforms …\"."
			}
			findings = append(findings, finding("ruby_lock_platform_missing", severity,
				"Gemfile.lock has no platform for this server", listed, means,
				"Run `bundle lock --add-platform x86_64-linux aarch64-linux` and commit Gemfile.lock.", "deploy", "configuration.build"))
		}
	}
	if toolchain.GitSSH && recipe {
		findings = append(findings, finding("ruby_git_gem_ssh", PreflightWarning,
			"A gem is fetched from Git over SSH", "a GIT source in Gemfile.lock is a git@ or ssh:// URL",
			"The build has no SSH key or known host, so Bundler cannot clone the repository and the install fails.",
			"Use an https:// source — for a private repository with its BUNDLE_<HOST> credential mapped to the install step — or publish the gem.",
			"deploy", "configuration.build"))
	}
	if version, _, ok := parseLanguageVersion(toolchain.Bundler); recipe && ok && version[0] < 2 {
		findings = append(findings, finding("ruby_bundler_outdated", PreflightWarning,
			"Gemfile.lock was written by Bundler "+toolchain.Bundler, "BUNDLED WITH "+toolchain.Bundler,
			"RubyGems runs the Bundler a lock was written with, and Bundler 1 does not run on Ruby 3.2 or later.",
			"Run `bundle update --bundler` and commit Gemfile.lock.", "deploy", "configuration.build"))
	}
	return findings
}

// unpinnedDependencyAdvice says, in the words of the recipe's own tooling,
// what is unpinned, what that means for a rebuild and how to pin it: telling
// a PHP, Ruby or Deno operator to run `uv lock` helped nobody.
func unpinnedDependencyAdvice(candidate *DetectedCandidate) (measured, means, action string) {
	const works = " when rebuilds must be identical; deploying as is works today."
	means = "Each build installs the newest versions the manifest allows, so a rebuild of this same commit can run different code."
	lockfiles := map[string][2]string{
		"deno":   {"deno.json imports without deno.lock", "Run `deno install` and commit deno.lock"},
		"rust":   {"Cargo.toml without Cargo.lock", "Run `cargo generate-lockfile` and commit Cargo.lock"},
		"ruby":   {"Gemfile without Gemfile.lock", "Run `bundle lock` and commit Gemfile.lock"},
		"elixir": {"mix.exs without mix.lock", "Run `mix deps.get` and commit mix.lock"},
		"dart":   {"pubspec.yaml without pubspec.lock", "Run `dart pub get` and commit pubspec.lock"},
		"gleam":  {"gleam.toml without manifest.toml", "Run `gleam deps download` and commit manifest.toml"},
	}
	switch lockfile, ok := lockfiles[candidate.Recipe]; {
	case candidate.Recipe == "php":
		return "composer.json without composer.lock",
			means + " Composer also refuses to resolve a version that has a security advisory, so a tightly pinned requirement can stop the build.",
			"Run `composer install` locally and commit composer.lock" + works
	case ok:
		return lockfile[0], means, lockfile[1] + works
	case candidate.Framework == "jekyll":
		return "no Gemfile.lock; the build resolves the gems", means,
			"Run `bundle lock` and commit Gemfile.lock (a site with no Gemfile gets the newest github-pages gem)" + works
	case candidate.Recipe == "python" && candidate.StaticSite != nil && candidate.StaticSite.Unpinned:
		return "no requirements file; the build installs pinned " + siteGeneratorNames[candidate.StaticSite.Generator] + " releases", means,
			"Commit a requirements.txt that lists the site's generator, theme and plugins with their versions" + works
	}
	return "unpinned entries in the dependency manifest", means, "Commit a lockfile (uv lock, poetry lock, or pip freeze > requirements.txt)" + works
}
