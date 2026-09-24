package deploy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The PHP recipe serves an application with FrankenPHP's built-in server —
// one process, no nginx-plus-FPM pairing to configure — after Composer has
// installed its dependencies and, for a Laravel or Symfony application whose
// assets Vite builds, after a Node stage has built them.

var (
	phpRecipeVersions   = []string{"8.2", "8.3", "8.4"}
	phpDefaultVersion   = "8.3"
	phpSpecifierRE      = regexp.MustCompile(`^(>=|~|\^|==|=|>|<=|<)?\s*v?([0-9]+\.[0-9]+)(\.[0-9*]+)?$`)
	phpVersionRE        = regexp.MustCompile(`^([0-9]+)\.([0-9]+)`)
	phpBuiltinExtension = map[string]bool{
		"ctype": true, "curl": true, "date": true, "dom": true, "fileinfo": true, "filter": true, "hash": true,
		"iconv": true, "json": true, "libxml": true, "mbstring": true, "openssl": true, "pcre": true, "pdo": true,
		"pdo_sqlite": true, "phar": true, "posix": true, "readline": true, "session": true, "simplexml": true,
		"sodium": true, "sqlite3": true, "tokenizer": true, "xml": true, "xmlreader": true, "xmlwriter": true,
		"zlib": true, "spl": true, "standard": true, "core": true, "random": true,
	}
	phpDefaultExtensions = []string{"pdo_mysql", "pdo_pgsql", "opcache"}
	phpExtensionNameRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// composerManifest is the inert view of composer.json: the packages it
// requires (framework and PHP extensions among them) and its scripts.
type composerManifest struct {
	Name    string            `json:"name"`
	Require map[string]string `json:"require"`
	Scripts map[string]any    `json:"scripts"`
}

func parseComposerManifest(content []byte) (composerManifest, bool) {
	var manifest composerManifest
	if json.Unmarshal(manifestText(content), &manifest) != nil {
		return composerManifest{}, false
	}
	return manifest, true
}

func (m composerManifest) has(name string) bool { return m.Require[name] != "" }

// extensions lists the PHP extensions composer.json requires that the
// FrankenPHP image does not already carry, in a stable order.
func (m composerManifest) extensions() []string {
	names := []string{}
	for name := range m.Require {
		if !strings.HasPrefix(name, "ext-") {
			continue
		}
		extension := strings.ToLower(strings.TrimPrefix(name, "ext-"))
		if phpBuiltinExtension[extension] || !phpExtensionNameRE.MatchString(extension) {
			continue
		}
		names = append(names, extension)
	}
	if m.has("laravel/horizon") {
		// Horizon supervises its workers with process control and reaches
		// its queues through Redis. Its own composer.json requires both,
		// so the application's never names them.
		for _, extension := range []string{"pcntl", "redis"} {
			if !slices.Contains(names, extension) {
				names = append(names, extension)
			}
		}
	}
	sort.Strings(names)
	return names
}

// choosePHPRecipeVersion picks the newest catalogue release the manifest's
// `php` constraint allows. Composer's operators are read the way Composer
// reads them: `^8.2` is 8.2 up to 9.0, `~8.3.0` is the 8.3 family, `8.2.*`
// that family alone, `>=8.1 <8.4` a plain range, and `||` a union. A
// constraint no catalogue release satisfies — `^7.4` — is a refusal that
// points at a Dockerfile, never a silent upgrade.
func choosePHPRecipeVersion(constraint string) (string, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return phpDefaultVersion, nil
	}
	type bound struct{ major, minor int }
	less := func(a, b bound) bool { return a.major < b.major || (a.major == b.major && a.minor < b.minor) }
	parse := func(text string) (bound, bool) {
		match := phpVersionRE.FindStringSubmatch(text)
		if match == nil {
			return bound{}, false
		}
		return bound{major: phpMinor("0." + match[1]), minor: phpMinor("0." + match[2])}, true
	}
	allowed := func(version string) bool {
		candidate, _ := parse(version)
		for _, alternative := range strings.Split(constraint, "||") {
			floor, ceiling := bound{}, bound{999, 0}
			hasFloor, matched := false, false
			for _, clause := range strings.FieldsFunc(alternative, func(r rune) bool { return r == ',' || r == ' ' }) {
				match := phpSpecifierRE.FindStringSubmatch(clause)
				if match == nil {
					continue
				}
				operator, version, ok := match[1], match[2], true
				value, ok := parse(version)
				if !ok {
					continue
				}
				matched = true
				patch := match[3]
				switch operator {
				case "^":
					floor, hasFloor = value, true
					if less(bound{value.major + 1, 0}, ceiling) {
						ceiling = bound{value.major + 1, 0}
					}
				case "~":
					floor, hasFloor = value, true
					next := bound{value.major + 1, 0}
					if patch != "" && patch != ".*" {
						next = bound{value.major, value.minor + 1}
					}
					if less(next, ceiling) {
						ceiling = next
					}
				case ">=", ">":
					floor, hasFloor = value, true
				case "<":
					if less(value, ceiling) {
						ceiling = value
					}
				case "<=":
					if less(bound{value.major, value.minor + 1}, ceiling) {
						ceiling = bound{value.major, value.minor + 1}
					}
				default: // "", "=", "=="
					floor, hasFloor = value, true
					if less(bound{value.major, value.minor + 1}, ceiling) {
						ceiling = bound{value.major, value.minor + 1}
					}
				}
			}
			if !matched {
				continue
			}
			if (!hasFloor || !less(candidate, floor)) && less(candidate, ceiling) {
				return true
			}
		}
		return false
	}
	candidates := append([]string(nil), phpRecipeVersions...)
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	for _, version := range candidates {
		if allowed(version) {
			return version, nil
		}
	}
	return "", fmt.Errorf("%w: the PHP recipe builds PHP 8.2 to 8.4; composer.json requires %s — use a Dockerfile for other releases", ErrUnsupportedBuilder, constraint)
}

func phpMinor(version string) int { return pythonMinor(version) }

// phpFrameworks name the framework a manifest requires and how its
// production process starts. All serve through FrankenPHP on port 80.
var phpFrameworks = []struct {
	pkg, name, label, root string
	migrate                string
}{
	{"laravel/framework", "laravel", "Laravel", "public", "php artisan migrate --force"},
	{"symfony/framework-bundle", "symfony", "Symfony", "public", ""},
	{"slim/slim", "slim", "Slim", "public", ""},
}

// phpCandidate builds the candidate for a root with composer.json or an
// index.php.
func phpCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	candidate := DetectedCandidate{
		Name: "PHP application in " + rootLabel, Profile: ProfileWeb, Confidence: ConfidenceMedium,
		Framework: "php", Recipe: "php", Port: 80,
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
	}
	manifest, parsed := parseComposerManifest(marker.composerJSON)
	if len(marker.composerJSON) > 0 {
		reason := "Composer manifest"
		if !parsed {
			reason = "composer.json could not be parsed"
			candidate.Confidence = ConfidenceLow
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "composer.json"), Reason: reason})
		candidate.UnpinnedDependencies = !marker.composerLock
		if version, err := choosePHPRecipeVersion(manifest.Require["php"]); err != nil {
			candidate.RecipeIssue = err.Error()
		} else if manifest.Require["php"] != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "composer.json"), Reason: "php " + manifest.Require["php"] + " builds on PHP " + version})
		}
	}
	root := ""
	for _, framework := range phpFrameworks {
		if parsed && manifest.has(framework.pkg) {
			candidate.Framework = framework.name
			candidate.Name = framework.label + " application in " + rootLabel
			candidate.Confidence = ConfidenceHigh
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "composer.json"), Reason: framework.label + " dependency " + boundedEvidence(manifest.Require[framework.pkg])})
			root = framework.root
			start := "frankenphp php-server --listen :80 --root /app/" + root
			if framework.migrate != "" {
				start = framework.migrate + " && " + start
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "artisan"), Reason: "migrations are applied before the server starts"})
			}
			if framework.name == "symfony" && manifest.has("doctrine/doctrine-migrations-bundle") {
				start = "php bin/console doctrine:migrations:migrate --no-interaction && " + start
			}
			candidate.StartCommand = start
			break
		}
	}
	if root == "" {
		switch {
		case marker.phpPublicIndex:
			root = "public"
		case marker.phpIndex:
			root = ""
		default:
			candidate.Confidence = ConfidenceLow
			candidate.NeedsDecision = append(candidate.NeedsDecision, "no index.php at the root or under public/; confirm the directory FrankenPHP should serve")
		}
		if root != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, joinRoot(root, "index.php")), Reason: "PHP entry point"})
		}
		candidate.StartCommand = "frankenphp php-server --listen :80 --root /app" + strings.TrimSuffix("/"+root, "/")
	}
	if parsed && len(marker.packageJSON) > 0 && phpBuildsAssets(marker.packageJSON) {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "package.json"), Reason: "front-end assets are built by a Node stage inside the PHP recipe"})
		if marker.node != nil {
			// The asset stage installs through the same planner as the Node
			// recipe, so its lockfile question is asked here, before
			// deploying, and answered by the same package manager choice.
			facts := marker.node.facts
			candidate.PackageManagers = facts.lockfileManagers()
			candidate.Lockfiles = facts.detectedLockfiles()
			candidate.NodeVersion = nodeReleaseFor(facts).label()
			chosen, err := resolveNodeManager(facts, "")
			if err == nil {
				candidate.PackageManager = chosen.manager
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "package.json"), Reason: "assets install: " + boundedEvidenceSentence(chosen.reason)})
			} else {
				paths := []string{}
				for _, reading := range facts.readings {
					paths = append(paths, reading.Path)
				}
				candidate.NeedsDecision = append(candidate.NeedsDecision, "choose the package manager: competing lockfiles "+strings.Join(paths, ", "))
				if candidate.Confidence == ConfidenceHigh {
					candidate.Confidence = ConfidenceMedium
				}
			}
			candidate.NodeInstalls = facts.detectedInstalls(candidate.PackageManager, true, func(runner string) (string, string) {
				return runner + " run build", ""
			})
			candidate.Variables = facts.registry
		}
	}
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		candidate.StartCommand = procfileWeb
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	}
	if candidate.RecipeIssue != "" && candidate.Confidence == ConfidenceHigh {
		candidate.Confidence = ConfidenceMedium
	}
	return candidate
}

// phpRecipe is what the builder needs beyond the plan.
type phpRecipe struct {
	version    string
	framework  string
	extensions []string
	composer   bool
	// assets is the Node package manager that builds front-end assets, when
	// package.json declares a build script; empty means no asset stage. node
	// is that stage's install plan, from the same planner as the Node recipe.
	assets   string
	lockfile string
	node     nodeInstallPlan
	inputs   []string
}

// phpBuildsAssets says a PHP application's package.json is built by the
// recipe's asset stage: a build script, and Vite, laravel-vite-plugin or
// Encore to run it.
func phpBuildsAssets(content []byte) bool {
	var manifest nodeManifest
	return parseNodeManifest(content, &manifest) && manifest.Scripts["build"] != "" &&
		(manifest.has("laravel-vite-plugin") || manifest.has("vite") || manifest.has("@symfony/webpack-encore"))
}

// phpAssetInstall plans the asset stage's install for the application at
// root. The stage builds from the application's own directory, so its
// lockfile is looked for there alone.
func phpAssetInstall(root string, selected string, arch string) (nodeInstallSource, nodeInstallPlan, error) {
	source, err := readNodeInstallSource(root, "", arch, newNodeReadBudget())
	if err != nil {
		return nodeInstallSource{}, nodeInstallPlan{}, err
	}
	plan := planNodeInstall(source.facts, nodeInstallChoice{selected: selected, build: "npm run build", assets: true})
	return source, plan, nil
}

func selectPHPRecipe(boundary, root string, config BuildPlanConfig) (phpRecipe, error) {
	recipe := phpRecipe{version: phpDefaultVersion}
	if regularExists(root, "composer.json") {
		content, err := readContainedRegular(root, "composer.json", 512<<10)
		if err != nil {
			return phpRecipe{}, err
		}
		manifest, ok := parseComposerManifest(content)
		if !ok {
			return phpRecipe{}, fmt.Errorf("%w: composer.json is malformed", ErrUnsupportedBuilder)
		}
		recipe.composer = true
		recipe.extensions = manifest.extensions()
		version, err := choosePHPRecipeVersion(manifest.Require["php"])
		if err != nil {
			return phpRecipe{}, err
		}
		recipe.version = version
		for _, framework := range phpFrameworks {
			if manifest.has(framework.pkg) {
				recipe.framework = framework.name
				break
			}
		}
		if node, err := readContainedRegular(root, "package.json", 512<<10); err == nil && phpBuildsAssets(node) {
			source, plan, err := phpAssetInstall(root, config.PackageManager, nodeTargetArch(config.TargetPlatform))
			if err != nil {
				return phpRecipe{}, err
			}
			if plan.blocked != nil {
				return phpRecipe{}, fmt.Errorf("%w: the PHP recipe builds package.json's assets with a Node stage: %s", ErrUnsupportedBuilder, plan.blocked.Measured)
			}
			recipe.assets, recipe.lockfile, recipe.node = plan.manager, plan.lockfile, plan
			recipe.inputs = source.installInputs(plan)
		}
	} else if !regularExists(root, "index.php") && !regularExists(root, "public/index.php") {
		return phpRecipe{}, fmt.Errorf("%w: PHP recipe requires composer.json or an index.php", ErrUnsupportedBuilder)
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		return phpRecipe{}, fmt.Errorf("%w: PHP recipe requires a start command; detection proposes frankenphp php-server for the application's public root", ErrUnsupportedBuilder)
	}
	return recipe, nil
}

// phpRecipeBases lists the images the recipe resolves, in the order the
// Dockerfile references them: FrankenPHP, Composer, then the Node image of
// the asset stage when there is one.
func phpRecipeBases(recipe phpRecipe) []string {
	bases := []string{"dunglas/frankenphp:1-php" + recipe.version + "-alpine", recipeBaseCatalogue["php:composer"][0]}
	if recipe.assets != "" {
		bases = append(bases, recipe.node.baseImages(false)...)
	}
	return bases
}

func renderPHPDockerfile(recipe phpRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) < 2 || (recipe.assets != "" && len(bases) < 3) {
		return nil, ErrBuilderUnavailable
	}
	var lines []string
	if recipe.assets != "" {
		stage, _, err := nodeInstallStage(recipe.node, bases[2], bases, "assets-toolchain", "assets", installSecrets)
		if err != nil {
			return nil, err
		}
		lines = append(stage, recipe.node.buildRun(buildSecrets, recipe.node.build, boundToBuild(config.Secrets)))
	}
	extensions := append([]string(nil), phpDefaultExtensions...)
	for _, extension := range recipe.extensions {
		duplicate := false
		for _, existing := range extensions {
			duplicate = duplicate || existing == extension
		}
		if !duplicate {
			extensions = append(extensions, extension)
		}
	}
	lines = append(lines,
		"FROM "+immutableImageReference(bases[1])+" AS composer",
		"FROM "+immutableImageReference(bases[0]),
		"WORKDIR /app",
		"ENV COMPOSER_ALLOW_SUPERUSER=1 LOG_CHANNEL=stderr",
		// The extension installer ships with the image; opcache is compiled in
		// and only enabled here, the database drivers are built from source.
		"RUN install-php-extensions "+strings.Join(extensions, " "),
		"COPY --from=composer /usr/bin/composer /usr/bin/composer",
		"COPY . .",
	)
	if recipe.composer {
		lines = append(lines, "RUN "+installSecrets+"composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist")
	}
	if recipe.assets != "" {
		lines = append(lines, "COPY --from=assets /app/public/build /app/public/build")
	}
	if recipe.framework == "laravel" {
		lines = append(lines, "RUN mkdir -p storage/framework/cache storage/framework/sessions storage/framework/views storage/logs storage/app/public bootstrap/cache database && chmod -R a+rwX storage bootstrap/cache database")
		// What `php artisan storage:link` writes, without booting the
		// application in the build: files on the public disk are served from
		// public/storage. A committed public/storage is left alone.
		lines = append(lines, "RUN if [ -d public ] && [ ! -e public/storage ]; then ln -s /app/storage/app/public public/storage; fi")
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	lines = append(lines, shellCMD(config.StartCommand))
	return lines, nil
}
