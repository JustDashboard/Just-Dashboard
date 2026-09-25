package deploy

import (
	"slices"
	"strings"
)

// Findings about a PHP or Deno plan, from what detection read of the tree
// (detect_php.go, detect_deno.go): each is a build that would fail, or a
// site that would serve wrongly, said before Deploy.

func phpDenoFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	if candidate == nil || build.Method != BuildRecipe || build.Recipe != candidate.Recipe {
		return nil
	}
	switch build.Recipe {
	case "php":
		return phpFindings(candidate, configuration)
	case "deno":
		return denoFindings(candidate, configuration)
	}
	return nil
}

func phpFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	findings := []PreflightFinding{}
	build := configuration.Build
	if len(candidate.NodeInstalls) == 0 {
		// A plan with an asset stage hears about its registry credentials
		// with the rest of that install (nodeInstallFindings).
		findings = append(findings, registryCredentialFindings(candidate, configuration)...)
	}
	facts := candidate.PHP
	if facts == nil {
		return findings
	}
	if len(facts.Extensions) > 0 {
		names, reasons := []string{}, []string{}
		for _, extension := range facts.Extensions {
			names = append(names, extension.Name)
			if !slices.Contains(phpDefaultExtensions, extension.Name) {
				reasons = append(reasons, extension.Name+": "+extension.Reason)
			}
		}
		means := "The image installs them before the application is copied; " + strings.Join(phpDefaultExtensions, ", ") + " are always there, so a linked database works."
		if len(reasons) > 0 {
			means += " " + strings.Join(reasons, "; ") + "."
		}
		findings = append(findings, finding("php_extensions", PreflightPass,
			"PHP extensions the application needs are installed", strings.Join(names, ", "), boundedFindingText(means), "", "deploy", "configuration.build"))
	}
	if len(facts.Unsupported) > 0 {
		unsupported := []string{}
		for _, extension := range facts.Unsupported {
			unsupported = append(unsupported, extension.Name+" ("+extension.Reason+")")
		}
		findings = append(findings, finding("php_extension_unsupported", PreflightWarning,
			"A PHP extension cannot be installed by the recipe", boundedFindingText(strings.Join(unsupported, "; ")),
			"install-php-extensions has no build of it for the Alpine image, so the recipe leaves it out; Composer refuses a package that requires it, and code that calls it fails when it runs.",
			"Remove the requirement if nothing uses it, or build with a Dockerfile that installs the extension.", "deploy", "configuration.build.method"))
	}
	if facts.Lock == "unread" {
		findings = append(findings, finding("php_extensions_unverified", PreflightWarning,
			"The extensions composer.lock's packages need were not read", "composer.lock is larger than 4 MiB or not a lock Composer wrote",
			"The recipe installs the extensions composer.json and the code name; one that only a locked package requires is missing, and composer install stops on it.",
			"Declare every extension the application needs in composer.json's require (\"ext-intl\": \"*\").", "deploy", "configuration.build"))
	}
	if len(facts.LockMissing) > 0 {
		findings = append(findings, finding("composer_lock_missing_packages", PreflightWarning,
			"composer.lock is missing packages composer.json requires", strings.Join(facts.LockMissing, ", "),
			"composer install would stop with 'Required package … is not present in the lock file'; the build updates just these packages instead, so the image runs versions the lock does not record.",
			"Run composer update "+strings.Join(facts.LockMissing, " ")+" and commit composer.lock.", "deploy", "configuration.build"))
	}
	if len(facts.LockOutdated) > 0 {
		findings = append(findings, finding("composer_lock_outdated", PreflightWarning,
			"composer.lock no longer satisfies composer.json", boundedFindingText(strings.Join(facts.LockOutdated, "; ")),
			"composer install would stop because a locked version does not satisfy its constraint; the build updates those packages instead, so the image runs versions the lock does not record.",
			"Run composer update for those packages and commit composer.lock.", "deploy", "configuration.build"))
	}
	if len(facts.DevLockOutdated) > 0 {
		findings = append(findings, finding("composer_lock_dev_outdated", PreflightWarning,
			"composer.lock does not match composer.json's require-dev", boundedFindingText(strings.Join(facts.DevLockOutdated, "; ")),
			"The production install leaves development packages out, so this build is not affected; the next composer install on a developer machine will stop on it.",
			"Run composer update and commit composer.lock.", "deploy", "configuration.build"))
	}
	if build.PHPVersion != "" {
		conflicts := []string{}
		for _, constraint := range facts.Constraints {
			source, requirement, found := strings.Cut(constraint, " ")
			if !found {
				continue
			}
			if allowed, ok := composerConstraintAllowsVersion(requirement, phpReleaseVersion(build.PHPVersion)); ok && !allowed {
				conflicts = append(conflicts, source+" requires "+requirement)
			}
		}
		if len(conflicts) > 0 {
			findings = append(findings, finding("php_version_unsupported", PreflightBlocked,
				"The chosen PHP release does not satisfy the application", boundedFindingText("PHP "+build.PHPVersion+": "+strings.Join(conflicts, "; ")),
				"Composer checks the running PHP against every requirement and stops the install when one is not met.",
				"Choose Automatic, or a release every requirement accepts.", "deploy", "configuration.build.phpVersion"))
		}
	}
	if len(facts.DevProviders) > 0 {
		findings = append(findings, finding("laravel_dev_provider_registered", PreflightBlocked,
			"A development-only package is registered for production", boundedFindingText(strings.Join(facts.DevProviders, "; ")),
			"The production install leaves require-dev packages out, and Composer's package:discover boots every registered provider, so the build stops with 'Class … not found'.",
			"Register the provider only in the local environment (Telescope's docs describe a local-only installation), or move the package to require.",
			"deploy", "configuration.build"))
	}
	if facts.AssetsOutsideRoot {
		findings = append(findings, finding("php_assets_outside_docroot", PreflightWarning,
			"Built assets are not under the served directory", facts.AssetOutput+"/",
			"The asset build writes outside the directory FrankenPHP serves, so the browser cannot load them.",
			"Set the build's output directory (Vite's build.outDir) under the document root, or serve the directory that holds them.", "deploy", "configuration.build.startCommand"))
	}
	if facts.MixUnbuilt {
		findings = append(findings, finding("laravel_mix_unbuilt", PreflightWarning,
			"Laravel Mix assets are not built", "webpack.mix.js, no public/mix-manifest.json, no production script",
			"Compiled assets are neither committed nor built, so mix() throws 'Mix manifest does not exist' and pages that use it answer 500.",
			"Add a production script to package.json (mix --production), or commit the compiled assets and mix-manifest.json.", "deploy", "configuration.build"))
	}
	if facts.ServerConfig != "" {
		findings = append(findings, finding("php_procfile_server_config_ignored", PreflightWarning,
			"The Procfile's web server configuration is not used", facts.ServerConfig,
			"FrankenPHP serves the document root the Procfile names; Apache, nginx and PHP-FPM configuration for Heroku's buildpack does not apply to it.",
			"Move rewrite or access rules into the application, or build with a Dockerfile that runs that server.", "deploy", "configuration.build.startCommand"))
	}
	if candidate.Framework == "wordpress" && !hasDatabaseDependency(configuration.Dependencies) &&
		!slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
			return variable.Name == "DATABASE_URL" || variable.Name == "WORDPRESS_DB_HOST" || variable.Name == "DB_HOST"
		}) {
		findings = append(findings, finding("wordpress_database_required", PreflightDecision,
			"WordPress needs a MySQL or MariaDB database", "no database is linked",
			"WordPress keeps its content, users and settings in MySQL; without one every page answers 'Error establishing a database connection'.",
			"Link a MySQL or MariaDB database, whose URL reaches WordPress as DATABASE_URL, or set the database variables yourself.", "deploy", "dependencies"))
	}
	return findings
}

func denoFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	facts := candidate.Deno
	if facts == nil {
		return nil
	}
	findings := []PreflightFinding{}
	if len(facts.LockStale) > 0 {
		findings = append(findings, finding("deno_lock_outdated", PreflightWarning,
			"deno.lock does not match the project's dependencies", boundedFindingText(strings.Join(facts.LockStale, ", ")),
			"`deno install --frozen` would stop with 'The lockfile is out of date'; the build installs without --frozen instead, so it resolves versions the lock does not record.",
			"Run deno install and commit deno.lock.", "deploy", "configuration.build"))
	}
	if facts.Declared != "" {
		findings = append(findings, finding("deno_version_mismatch", PreflightWarning,
			"The repository targets a Deno release the recipe does not build", "declared "+facts.Declared+"; the recipe builds with Deno "+facts.Version,
			"Code that relies on that release's APIs or defaults may behave differently or fail.",
			"Declare a Deno 1 or 2 release (.dvmrc, .tool-versions), or build with a Dockerfile on the image you need.", "deploy", "configuration.build"))
	}
	if facts.WatchStart && configuration.Build.StartCommand == candidate.StartCommand {
		findings = append(findings, finding("deno_start_watch_mode", PreflightWarning,
			"The start task runs a development watcher", candidate.StartCommand,
			"A watcher recompiles on request and watches files that never change in a container; the build's output is not what it serves.",
			"Add a task that serves the built application (Fresh's preview task) and set it as the start command.", "deploy", "configuration.build.startCommand"))
	}
	return findings
}

// unpinnedDependencyAdvice is the dependencies_unpinned warning's copy in the
// recipe's own terms.
func unpinnedDependencyAdvice(recipe string) (means, action string) {
	means = "Each build installs the newest versions the manifest allows, so a rebuild of this same commit can run different code."
	switch recipe {
	case "php":
		return means + " Composer also refuses to resolve a version that has a security advisory, so a tightly pinned requirement can stop the build.",
			"Run composer install locally and commit composer.lock; deploying as is works today."
	case "deno":
		return means, "Run deno install and commit deno.lock; deploying as is works today."
	}
	return means, "Commit a lockfile (uv lock, poetry lock, or pip freeze > requirements.txt) when rebuilds must be identical; deploying as is works today."
}
