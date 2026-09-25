package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The PHP recipe serves an application with FrankenPHP's built-in server —
// one process, no nginx-plus-FPM pairing to configure — after Composer has
// installed its dependencies and, when package.json builds front-end
// assets, after an asset stage that has PHP and vendor/ beside Node has
// built them.

var (
	phpRecipeVersions = []string{"8.2", "8.3", "8.4", "8.5"}
	phpDefaultVersion = "8.3"
	// phpPreferredVersion is the newest release a constraint that allows
	// several is built on. 8.5 is chosen when the application requires it or
	// the build settings name it, so adding a release to the catalogue does
	// not move every PHP deployment onto it at their next build.
	phpPreferredVersion = "8.4"
	phpVersionRE        = regexp.MustCompile(`^([0-9]+)\.([0-9]+)`)
	phpBuiltinExtension = map[string]bool{
		"ctype": true, "curl": true, "date": true, "dom": true, "fileinfo": true, "filter": true, "hash": true,
		"iconv": true, "json": true, "libxml": true, "mbstring": true, "openssl": true, "pcre": true, "pdo": true,
		"pdo_sqlite": true, "phar": true, "posix": true, "readline": true, "session": true, "simplexml": true,
		"sodium": true, "sqlite3": true, "tokenizer": true, "xml": true, "xmlreader": true, "xmlwriter": true,
		"zlib": true, "spl": true, "standard": true, "core": true, "random": true, "reflection": true, "mysqlnd": true,
	}
	// mysqli is a default beside the PDO drivers because WordPress and most
	// code written without a framework connect through it.
	phpDefaultExtensions = []string{"pdo_mysql", "pdo_pgsql", "mysqli", "opcache"}
	phpExtensionNameRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// composerManifest is the inert view of composer.json: the packages it
// requires (framework and PHP extensions among them), its scripts, and the
// settings that decide where it installs and what it resolves against.
// Fields whose shape varies between projects stay raw, so one unusual value
// never makes the whole manifest unreadable.
type composerManifest struct {
	Name         string            `json:"name"`
	Require      map[string]string `json:"require"`
	RequireDev   map[string]string `json:"require-dev"`
	Scripts      map[string]any    `json:"scripts"`
	Config       json.RawMessage   `json:"config"`
	Extra        json.RawMessage   `json:"extra"`
	Repositories json.RawMessage   `json:"repositories"`
}

func parseComposerManifest(content []byte) (composerManifest, bool) {
	var manifest composerManifest
	if json.Unmarshal(manifestText(content), &manifest) != nil {
		return composerManifest{}, false
	}
	return manifest, true
}

func (m composerManifest) has(name string) bool { return m.Require[name] != "" }

// platformPHP is config.platform.php: the PHP the lock was resolved for.
func (m composerManifest) platformPHP() string {
	var config struct {
		Platform map[string]any `json:"platform"`
	}
	if json.Unmarshal(m.Config, &config) != nil {
		return ""
	}
	version, _ := config.Platform["php"].(string)
	return strings.TrimSpace(version)
}

// drupalWebRoot is where drupal/core-composer-scaffold writes index.php.
func (m composerManifest) drupalWebRoot() string {
	var extra struct {
		Scaffold struct {
			Locations map[string]any `json:"locations"`
		} `json:"drupal-scaffold"`
	}
	if json.Unmarshal(m.Extra, &extra) == nil {
		if root, ok := extra.Scaffold.Locations["web-root"].(string); ok {
			return phpDocumentDirectory(root)
		}
	}
	return "web"
}

// wordpressWebRoot is the directory Bedrock serves: the parent of the one
// WordPress itself is installed into (web/wp).
func (m composerManifest) wordpressWebRoot() string {
	var extra struct {
		InstallDir any `json:"wordpress-install-dir"`
	}
	if json.Unmarshal(m.Extra, &extra) == nil {
		if dir, ok := extra.InstallDir.(string); ok && path.Dir(strings.Trim(dir, "/")) != "." {
			return phpDocumentDirectory(path.Dir(strings.Trim(dir, "/")))
		}
	}
	return "web"
}

// phpDocumentDirectory is a relative directory safe to name in a start
// command, or "" for one that is not.
func phpDocumentDirectory(dir string) string {
	dir = strings.Trim(path.Clean(strings.TrimPrefix(strings.TrimSpace(dir), "./")), "/")
	if dir == "." || !assetPathRE.MatchString(dir) || !safeRelativePath(dir) {
		return ""
	}
	return dir
}

// phpVersionRequirement is one requirement on the PHP release: composer.json's
// own, or a locked package's.
type phpVersionRequirement struct{ source, constraint string }

// choosePHPVersion picks the catalogue release the build runs on. An
// explicit build setting wins. Otherwise the release must satisfy
// composer.json and every package the lock installs, read with Composer's
// semantics (php_constraints.go); config.platform.php, the release the lock
// was resolved for, is preferred when it qualifies; and among the rest the
// newest up to phpPreferredVersion is taken, a newer one only when nothing
// older qualifies. A requirement no catalogue release satisfies is a
// refusal that points at a Dockerfile, never a silent upgrade.
func choosePHPVersion(override, platform string, requirements []phpVersionRequirement) (string, error) {
	if override != "" {
		if !slices.Contains(phpRecipeVersions, override) {
			return "", fmt.Errorf("%w: the PHP recipe builds PHP %s; the build settings name %s", ErrUnsupportedBuilder, strings.Join(phpRecipeVersions, ", "), override)
		}
		return override, nil
	}
	readable := []phpVersionRequirement{}
	for _, requirement := range requirements {
		if _, ok := composerConstraintAllowsVersion(requirement.constraint, phpReleaseVersion("8.4")); ok {
			readable = append(readable, requirement)
		}
	}
	allowed := func(release string, among []phpVersionRequirement) bool {
		for _, requirement := range among {
			if ok, _ := composerConstraintAllowsVersion(requirement.constraint, phpReleaseVersion(release)); !ok {
				return false
			}
		}
		return true
	}
	pick := func(among []phpVersionRequirement) string {
		if len(among) == 0 {
			return phpDefaultVersion
		}
		if match := phpVersionRE.FindStringSubmatch(platform); match != nil {
			if family := match[1] + "." + match[2]; slices.Contains(phpRecipeVersions, family) && allowed(family, among) {
				return family
			}
		}
		chosen := ""
		for _, release := range phpRecipeVersions {
			if allowed(release, among) && (chosen == "" || release <= phpPreferredVersion) {
				chosen = release
			}
		}
		return chosen
	}
	if chosen := pick(readable); chosen != "" {
		return chosen, nil
	}
	root := []phpVersionRequirement{}
	for _, requirement := range readable {
		if requirement.source == "composer.json" {
			root = append(root, requirement)
		}
	}
	span := "PHP " + phpRecipeVersions[0] + " to " + phpRecipeVersions[len(phpRecipeVersions)-1]
	if len(root) > 0 && pick(root) == "" {
		return "", fmt.Errorf("%w: the PHP recipe builds %s; composer.json requires %s — use a Dockerfile for other releases",
			ErrUnsupportedBuilder, span, root[0].constraint)
	}
	conflicts := []string{}
	for _, requirement := range readable {
		if requirement.source != "composer.json" && pick(append(append([]phpVersionRequirement{}, root...), requirement)) == "" {
			conflicts = append(conflicts, requirement.source+" requires "+requirement.constraint)
		}
	}
	if len(conflicts) == 0 {
		conflicts = append(conflicts, "the locked packages' PHP requirements admit no common release")
	}
	if len(conflicts) > 3 {
		conflicts = append(conflicts[:3], fmt.Sprintf("and %d more", len(conflicts)-3))
	}
	return "", fmt.Errorf("%w: the PHP recipe builds %s, and none satisfies composer.lock: %s — update the lock or use a Dockerfile",
		ErrUnsupportedBuilder, span, strings.Join(conflicts, "; "))
}

// version is the release this project builds on with an optional override.
func (p phpProject) version(override string) (string, error) {
	return choosePHPVersion(override, p.platformPHP, p.requirements)
}

// versionEvidence says what chose the release: composer.json's constraint,
// and the locked package that narrowed it when one did.
func (p phpProject) versionEvidence(version string) string {
	root := ""
	for _, requirement := range p.requirements {
		if requirement.source == "composer.json" {
			root = requirement.constraint
		}
	}
	if match := phpVersionRE.FindStringSubmatch(p.platformPHP); match != nil && match[1]+"."+match[2] == version {
		return "config.platform.php " + p.platformPHP + " builds on PHP " + version
	}
	if unlocked, err := choosePHPVersion("", "", []phpVersionRequirement{{"composer.json", root}}); root != "" && err == nil && unlocked != version {
		for _, requirement := range p.requirements {
			if requirement.source == "composer.json" {
				continue
			}
			if ok, _ := composerConstraintAllowsVersion(requirement.constraint, phpReleaseVersion(unlocked)); !ok {
				return "php " + root + "; " + requirement.source + " requires " + requirement.constraint + " (composer.lock) → PHP " + version
			}
		}
	}
	if root == "" {
		return ""
	}
	return "php " + root + " builds on PHP " + version
}

// phpFramework names the framework a manifest requires, the directory its
// front controller is served from, and the migrations its start applies
// when the application has any (a migrate with nothing to migrate still
// needs a database, which not every application of these frameworks has).
type phpFramework struct {
	pkg, name, label, root string
	migrate                string
	migrations             string
}

var phpFrameworks = []phpFramework{
	{"laravel/framework", "laravel", "Laravel", "public", "php artisan migrate --force", ""},
	{"symfony/framework-bundle", "symfony", "Symfony", "public", "", ""},
	{"slim/slim", "slim", "Slim", "public", "", ""},
	{"cakephp/cakephp", "cakephp", "CakePHP", "webroot", "php bin/cake.php migrations migrate", "config/Migrations"},
	{"codeigniter4/framework", "codeigniter", "CodeIgniter", "public", "php spark migrate --all", "app/Database/Migrations"},
	{"yiisoft/yii2", "yii", "Yii", "web", "php yii migrate --interactive=0", "migrations"},
	{"mezzio/mezzio", "mezzio", "Mezzio", "public", "", ""},
	{"laminas/laminas-mvc", "laminas", "Laminas", "public", "", ""},
	{"drupal/core-recommended", "drupal", "Drupal", "web", "", ""},
	{"drupal/core", "drupal", "Drupal", "web", "", ""},
}

func phpFrameworkByName(name string) *phpFramework {
	for index := range phpFrameworks {
		if phpFrameworks[index].name == name {
			return &phpFrameworks[index]
		}
	}
	return nil
}

// phpWordPressLabels name each WordPress shape.
var phpWordPressLabels = map[string]string{
	"core": "WordPress site", "content": "WordPress content", "theme": "WordPress theme",
	"plugin": "WordPress plugin", "bedrock": "WordPress (Bedrock) site",
}

// herokuPHPRE reads a Heroku PHP buildpack web process: the server script,
// its flags, and the document root it serves.
var herokuPHPRE = regexp.MustCompile(`^(?:vendor/bin/)?heroku-php-(apache2|nginx)\b(.*)$`)

// phpStart is the FrankenPHP command serving root, after the migrations the
// framework applies.
func phpStart(migrate, root string) string {
	start := "frankenphp php-server --listen :80 --root /app" + strings.TrimSuffix("/"+root, "/")
	if migrate != "" {
		start = migrate + " && " + start
	}
	return start
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
	project := phpProject{lockState: "absent"}
	if marker.php != nil {
		project = *marker.php
	}
	manifest, parsed := parseComposerManifest(marker.composerJSON)
	source := joinRoot(marker.root, "composer.json")
	if len(marker.composerJSON) > 0 {
		reason := "Composer manifest"
		if !parsed {
			reason = "composer.json could not be parsed"
			candidate.Confidence = ConfidenceLow
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: reason})
		candidate.UnpinnedDependencies = !marker.composerLock
	} else {
		source = joinRoot(marker.root, "index.php")
	}
	version, err := project.version("")
	if err != nil {
		candidate.RecipeIssue = err.Error()
	} else if reason := project.versionEvidence(version); reason != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: boundedEvidenceSentence(reason)})
	}
	root, rooted, migrate := "", false, ""
	switch shape := project.wordpress.shape; {
	case shape != "":
		candidate.Framework, candidate.Confidence = "wordpress", ConfidenceHigh
		candidate.Name = phpWordPressLabels[shape] + " in " + rootLabel
		rooted = true
		reason := ""
		switch shape {
		case "core":
			reason = "WordPress core (wp-settings.php)"
			if project.wordpress.version != "" {
				reason = "WordPress " + project.wordpress.version + " core (wp-includes/version.php)"
			}
		case "bedrock":
			root = project.bedrockWebRoot
			reason = "Bedrock: WordPress installed by Composer, served from " + root + "/"
		case "theme", "plugin":
			reason = "a WordPress " + shape + " (its header), installed as wp-content/" + shape + "s/" + project.wordpress.slug +
				" in the WordPress release the recipe copies in; activate it in wp-admin"
		case "content":
			reason = "a wp-content directory (themes/, plugins/), served inside the WordPress release the recipe copies in"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: reason})
		if shape != "bedrock" && !project.wordpress.config {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "wp-config.php"),
				Reason: "no wp-config.php is committed; the recipe writes one that reads the database from DATABASE_URL"})
		}
	case parsed && phpFrameworkByName(project.framework) != nil:
		framework := phpFrameworkByName(project.framework)
		candidate.Framework = framework.name
		candidate.Name = framework.label + " application in " + rootLabel
		candidate.Confidence = ConfidenceHigh
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: framework.label + " dependency " + boundedEvidence(manifest.Require[framework.pkg])})
		root, rooted = framework.root, true
		if framework.name == "drupal" {
			root = manifest.drupalWebRoot()
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source,
				Reason: "document root " + root + "/ from composer.json extra.drupal-scaffold; run vendor/bin/drush deploy -y as a release task once the site is installed"})
		}
		migrate = framework.migrate
		migrations := framework.migrations
		switch {
		case framework.name == "laravel":
			migrations = "artisan"
		case framework.name == "symfony" && manifest.has("doctrine/doctrine-migrations-bundle"):
			migrate, migrations = "php bin/console doctrine:migrations:migrate --no-interaction", "bin/console"
		case migrations != "" && !project.migrations[migrations]:
			migrate = ""
		case framework.name == "cakephp" && !manifest.has("cakephp/migrations"):
			migrate = ""
		}
		if migrate != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, migrations), Reason: "migrations are applied before the server starts"})
		}
		if framework.name == "symfony" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source,
				Reason: "APP_ENV=prod and APP_DEBUG=0 are set by the recipe; the committed .env's environment no longer decides the build"})
			for _, command := range project.symfonyCommands {
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: "assets built at build: " + command})
			}
		}
	}
	if !rooted {
		switch {
		case marker.phpPublicIndex:
			root = "public"
		case marker.phpIndex:
			root = ""
		case marker.phpDocroot != "":
			root = marker.phpDocroot
		default:
			candidate.Confidence = ConfidenceLow
			candidate.NeedsDecision = append(candidate.NeedsDecision, "no index.php at the root or under public/; confirm the directory FrankenPHP should serve")
		}
		if root != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, joinRoot(root, "index.php")), Reason: "PHP entry point"})
		}
	}
	candidate.StartCommand = phpStart(migrate, root)
	serverConfig := ""
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		if match := herokuPHPRE.FindStringSubmatch(procfileWeb); match != nil {
			// The buildpack's Apache or nginx is not in the image and --no-dev
			// does not install its scripts: what the process names is the
			// document root, served by FrankenPHP after the framework's
			// migrations.
			docroot, configs := herokuDocumentRoot(match[2])
			if docroot != "-" {
				root = docroot
				candidate.StartCommand = phpStart(migrate, root)
				served := root + "/"
				if root == "" {
					served = "the application root"
				}
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"),
					Reason: boundedEvidenceSentence("Procfile " + boundedEvidence(procfileWeb) + " → FrankenPHP serving " + served)})
				serverConfig = strings.Join(configs, ", ")
			}
		} else {
			candidate.StartCommand = procfileWeb
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
		}
	}
	assets := project.assets
	building := (parsed || len(marker.composerJSON) == 0) && len(marker.packageJSON) > 0 && assets.kind != "" && project.wordpress.shape == ""
	if building && assets.mixUnbuilt {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "webpack.mix.js"),
			Reason: "Laravel Mix assets are not committed and package.json has no production script to build them"})
	} else if building && assets.output != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "package.json"),
			Reason: "front-end assets are built by an asset stage with PHP and vendor/, into " + assets.output + "/"})
		for _, reason := range assets.evidence {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "package.json"), Reason: boundedEvidenceSentence(reason)})
		}
		phpAssetInstallFacts(&candidate, marker, assets.script)
	}
	if updates := append(append([]string{}, project.lockMissing...), project.lockOutdated...); len(updates) > 0 {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "composer.lock"),
			Reason: boundedEvidenceSentence("composer.lock does not match composer.json (" + strings.Join(updates, "; ") + "); the install updates just those packages")})
	}
	for _, extension := range project.extensions {
		if !slices.Contains(phpDefaultExtensions, extension.Name) {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source, Reason: boundedEvidenceSentence("PHP extension " + extension.Name + ": " + extension.Reason)})
		}
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: source,
		Reason: "production php.ini; forwarded HTTPS honoured behind the platform proxy"})
	if hosts := slices.Concat(project.privateRepositories, project.otherRepositories, project.vcsRepositories); len(hosts) > 0 && !project.authJSON {
		candidate.Variables = withInstallVariables(candidate.Variables, []DetectedVariable{{
			Name: "COMPOSER_AUTH", Sources: []string{boundedEvidenceSentence("composer.json repositories (" + strings.Join(hosts, ", ") + ")")},
			Step: "install", InstallRequired: len(project.privateRepositories) > 0,
		}})
	}
	if err == nil {
		facts := project.detected(version)
		facts.ServerConfig = boundedEvidenceSentence(serverConfig)
		if building && !assets.mixUnbuilt && assets.output != "" {
			facts.AssetOutput = assets.output
			facts.AssetsOutsideRoot = root != "" && assets.output != root && !strings.HasPrefix(assets.output, root+"/")
		}
		candidate.PHP = facts
	}
	if candidate.RecipeIssue != "" && candidate.Confidence == ConfidenceHigh {
		candidate.Confidence = ConfidenceMedium
	}
	return candidate
}

// herokuDocumentRoot reads a heroku-php-* command's arguments: its last
// positional argument is the document root ("" for the application root,
// "-" when it is not a path to serve), and -C, -F and -i name server and PHP
// configurations FrankenPHP does not read.
func herokuDocumentRoot(arguments string) (string, []string) {
	fields := strings.Fields(arguments)
	configs := []string{}
	positional := []string{}
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		switch field {
		case "-C", "-F", "-i", "-l":
			if index+1 < len(fields) {
				if field != "-l" {
					configs = append(configs, field+" "+fields[index+1])
				}
				index++
			}
			continue
		}
		if !strings.HasPrefix(field, "-") {
			positional = append(positional, field)
		}
	}
	if len(positional) == 0 {
		return "", configs
	}
	docroot := strings.TrimPrefix(positional[len(positional)-1], "./")
	if docroot == "" || docroot == "." || docroot == "/" {
		return "", configs
	}
	if clean := phpDocumentDirectory(docroot); clean != "" {
		return clean, configs
	}
	return "-", configs
}

// phpAssetInstallFacts records the asset stage's install the way a Node
// candidate records its own: the same planner answers the same lockfile
// question here, before deploying, under the same package manager choice.
func phpAssetInstallFacts(candidate *DetectedCandidate, marker *detectedMarkers, script string) {
	if marker.node == nil {
		return
	}
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
		return runner + " run " + script, ""
	})
	candidate.Variables = facts.registry
}

// phpRecipe is what the builder needs beyond the plan.
type phpRecipe struct {
	version    string
	framework  string
	extensions []string
	composer   bool
	// lockUpdate lists the packages composer.json requires that composer.lock
	// lacks or no longer satisfies: the install updates just those instead of
	// stopping on a lock Composer would refuse.
	lockUpdate []string
	// git installs git before Composer runs, which clones some repositories.
	git bool
	// assets is the Node package manager that builds front-end assets, when
	// package.json declares a build; empty means no asset stage. node is
	// that stage's install plan, from the same planner as the Node recipe,
	// output the directory it writes, and devInstall whether it installs
	// Composer's development packages first.
	assets     string
	lockfile   string
	node       nodeInstallPlan
	inputs     []string
	output     string
	devInstall bool
	// symfony are the Symfony console commands that build assets.
	symfony []string
	// wordpress is the tree's shape; slug where a theme or plugin goes, and
	// config that the recipe writes wp-config.php.
	wordpress       string
	wordpressSlug   string
	wordpressConfig bool
}

// phpAssetInstall plans the asset stage's install for the application at
// root. The stage builds from the application's own directory, so its
// lockfile is looked for there alone.
func phpAssetInstall(root string, selected string, arch string, script string) (nodeInstallSource, nodeInstallPlan, error) {
	source, err := readNodeInstallSource(root, "", arch, newNodeReadBudget())
	if err != nil {
		return nodeInstallSource{}, nodeInstallPlan{}, err
	}
	plan := planNodeInstall(source.facts, nodeInstallChoice{selected: selected, build: "npm run " + script, assets: true})
	return source, plan, nil
}

func selectPHPRecipe(boundary, root string, config BuildPlanConfig) (phpRecipe, error) {
	project, err := openPHPProject(root)
	if err != nil {
		return phpRecipe{}, err
	}
	if project.hasManifest && !project.parsed {
		return phpRecipe{}, fmt.Errorf("%w: composer.json is malformed", ErrUnsupportedBuilder)
	}
	if !project.hasManifest && project.wordpress.shape == "" && !regularExists(root, "index.php") &&
		!slices.ContainsFunc(append([]string{"public"}, phpDocumentRoots...), func(dir string) bool { return regularExists(root, dir+"/index.php") }) {
		return phpRecipe{}, fmt.Errorf("%w: PHP recipe requires composer.json or an index.php", ErrUnsupportedBuilder)
	}
	version, err := project.version(config.PHPVersion)
	if err != nil {
		return phpRecipe{}, err
	}
	recipe := phpRecipe{
		version: version, framework: project.framework, extensions: project.extensionNames(),
		composer: project.hasManifest, lockUpdate: project.lockUpdate, git: project.git,
		symfony: project.symfonyCommands, wordpress: project.wordpress.shape, wordpressSlug: project.wordpress.slug,
		wordpressConfig: project.wordpress.shape != "" && project.wordpress.shape != "bedrock" && !project.wordpress.config,
	}
	if len(recipe.lockUpdate) > 32 {
		recipe.lockUpdate = []string{"*"}
	}
	assets := project.assets
	if assets.kind != "" && !assets.mixUnbuilt && assets.output != "" && project.wordpress.shape == "" {
		source, plan, err := phpAssetInstall(root, config.PackageManager, nodeTargetArch(config.TargetPlatform), assets.script)
		if err != nil {
			return phpRecipe{}, err
		}
		if plan.blocked != nil {
			return phpRecipe{}, fmt.Errorf("%w: the PHP recipe builds package.json's assets with a Node stage: %s", ErrUnsupportedBuilder, plan.blocked.Measured)
		}
		recipe.assets, recipe.lockfile, recipe.node = plan.manager, plan.lockfile, plan
		recipe.inputs = source.installInputs(plan)
		recipe.output, recipe.devInstall = assets.output, assets.devInstall
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		return phpRecipe{}, fmt.Errorf("%w: PHP recipe requires a start command; detection proposes frankenphp php-server for the application's public root", ErrUnsupportedBuilder)
	}
	return recipe, nil
}

// phpDocumentRoots are the directories besides public/ whose index.php is
// an application's front controller.
var phpDocumentRoots = []string{"web", "webroot", "htdocs", "public_html", "www"}

// phpRecipeBases lists the images the recipe resolves: FrankenPHP, Composer,
// WordPress when a theme, plugin or wp-content tree is served inside it, then
// the Node images of the asset stage when there is one.
func phpRecipeBases(recipe phpRecipe) []string {
	bases := []string{recipeBaseCatalogue["php:"+recipe.version][0], recipeBaseCatalogue["php:composer"][0]}
	if phpWordPressCopiesCore(recipe.wordpress) {
		bases = append(bases, recipeBaseCatalogue["php:wordpress"][0])
	}
	if recipe.assets != "" {
		bases = append(bases, recipe.node.baseImages(false)...)
	}
	return bases
}

func phpWordPressCopiesCore(shape string) bool {
	return shape == "content" || shape == "theme" || shape == "plugin"
}

// phpIniLines are the production settings every PHP image runs with:
// PHP's own php.ini-production (errors logged, never shown), uploads as
// large as the managed proxy lets through, and the forwarded-header prepend.
func phpIniLines() string {
	return fmt.Sprintf(`RUN cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" && `+
		`printf '%%s\n' 'upload_max_filesize=%dM' 'post_max_size=%dM' 'memory_limit=256M' 'expose_php=Off' `+
		`'auto_prepend_file=/usr/local/lib/just-dashboard/prepend.php' > "$PHP_INI_DIR/conf.d/zz-just-dashboard.ini"`,
		DefaultMaxRequestBodyMB, DefaultMaxRequestBodyMB)
}

// phpPrependSource runs before every script. Behind the managed proxy the
// connection is plain HTTP from a private address, and the proxy says in
// X-Forwarded-Proto and X-Forwarded-For what the visitor used. Only a peer
// on a loopback, private or reserved address is believed — a visitor who
// reaches a public port directly arrives with their own public address —
// and only while PHP_FORWARDED_TRUST=private, which the runtime withdraws
// when the port is published publicly. The client is the last X-Forwarded-For
// hop, the one the proxy itself appended. Yii's YII_ENV and YII_DEBUG are
// constants its front controllers define only when unset, so the
// environment's values are defined first, for the console too.
var phpPrependSource = []string{
	`<?php`,
	`if (getenv("YII_ENV") !== false && !defined("YII_ENV")) {`,
	`    define("YII_ENV", getenv("YII_ENV"));`,
	`    define("YII_DEBUG", getenv("YII_DEBUG") === "1" || getenv("YII_DEBUG") === "true");`,
	`}`,
	`(static function (): void {`,
	`    if (PHP_SAPI === "cli" || getenv("PHP_FORWARDED_TRUST") !== "private") {`,
	`        return;`,
	`    }`,
	`    $peer = $_SERVER["REMOTE_ADDR"] ?? "";`,
	`    if (filter_var($peer, FILTER_VALIDATE_IP) === false || filter_var($peer, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) !== false) {`,
	`        return;`,
	`    }`,
	`    $protocols = explode(",", (string) ($_SERVER["HTTP_X_FORWARDED_PROTO"] ?? ""));`,
	`    if (strtolower(trim((string) end($protocols))) === "https") {`,
	`        $_SERVER["HTTPS"] = "on";`,
	`        $_SERVER["REQUEST_SCHEME"] = "https";`,
	`        $_SERVER["SERVER_PORT"] = "443";`,
	`    }`,
	`    $hops = explode(",", (string) ($_SERVER["HTTP_X_FORWARDED_FOR"] ?? ""));`,
	`    $client = trim((string) end($hops));`,
	`    if (filter_var($client, FILTER_VALIDATE_IP) !== false) {`,
	`        $_SERVER["REMOTE_ADDR"] = $client;`,
	`    }`,
	`})();`,
}

// phpWordPressConfigSource is the wp-config.php the recipe writes when the
// repository commits none: the database from the DATABASE_URL a linked
// MySQL or MariaDB supplies (or the WORDPRESS_DB_* names the official image
// reads), and keys and salts from the environment when they are set —
// WordPress generates and stores its own in the database when they are
// not.
var phpWordPressConfigSource = []string{
	`<?php`,
	`$jd_database = parse_url((string) getenv("DATABASE_URL")) ?: [];`,
	`define("DB_NAME", getenv("WORDPRESS_DB_NAME") ?: ltrim($jd_database["path"] ?? "/wordpress", "/"));`,
	`define("DB_USER", getenv("WORDPRESS_DB_USER") ?: rawurldecode($jd_database["user"] ?? ""));`,
	`define("DB_PASSWORD", getenv("WORDPRESS_DB_PASSWORD") ?: rawurldecode($jd_database["pass"] ?? ""));`,
	`define("DB_HOST", getenv("WORDPRESS_DB_HOST") ?: ($jd_database["host"] ?? "localhost") . (isset($jd_database["port"]) ? ":" . $jd_database["port"] : ""));`,
	`define("DB_CHARSET", "utf8mb4");`,
	`define("DB_COLLATE", "");`,
	`foreach (["AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"] as $jd_key) {`,
	`    if (getenv("WORDPRESS_" . $jd_key)) {`,
	`        define($jd_key, getenv("WORDPRESS_" . $jd_key));`,
	`    }`,
	`}`,
	`$table_prefix = getenv("WORDPRESS_TABLE_PREFIX") ?: "wp_";`,
	`define("WP_DEBUG", getenv("WORDPRESS_DEBUG") === "1");`,
	`unset($jd_database, $jd_key);`,
	`if (!defined("ABSPATH")) {`,
	`    define("ABSPATH", __DIR__ . "/");`,
	`}`,
	`require_once ABSPATH . "wp-settings.php";`,
}

// printfFile writes constant platform text with printf; every line is single
// quoted, so the shell expands nothing, and none contains a quote of its own.
func printfFile(lines []string, target string) string {
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, "'"+line+"'")
	}
	return "printf '%s\\n' " + strings.Join(quoted, " ") + " > " + target
}

func renderPHPDockerfile(recipe phpRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	refs := phpRecipeBases(recipe)
	resolved := map[string]ResolvedImage{}
	for _, reference := range refs[:2] {
		image, err := resolveCatalogueImage(bases, reference)
		if err != nil {
			return nil, err
		}
		resolved[reference] = image
	}
	php, composer := resolved[refs[0]], resolved[refs[1]]
	env := "ENV COMPOSER_ALLOW_SUPERUSER=1 LOG_CHANNEL=stderr"
	switch recipe.framework {
	case "symfony":
		// The committed .env says dev, whose bundles --no-dev leaves out; a
		// runtime APP_ENV from the plan still overrides this.
		env += " APP_ENV=prod APP_DEBUG=0"
	case "yii":
		env += " YII_ENV=prod YII_DEBUG=0"
	}
	lines := []string{"FROM " + immutableImageReference(composer) + " AS composer"}
	if phpWordPressCopiesCore(recipe.wordpress) {
		wordpress, err := resolveCatalogueImage(bases, recipeBaseCatalogue["php:wordpress"][0])
		if err != nil {
			return nil, err
		}
		lines = append(lines, "FROM "+immutableImageReference(wordpress)+" AS wordpress")
	}
	lines = append(lines,
		"FROM "+immutableImageReference(php)+" AS php-base",
		"WORKDIR /app",
		env,
		// The extension installer ships with the image; opcache is compiled in
		// and only enabled here, the others are built from source.
		"RUN install-php-extensions "+strings.Join(recipe.extensions, " "),
		phpIniLines()+" && mkdir -p /usr/local/lib/just-dashboard && "+printfFile(phpPrependSource, "/usr/local/lib/just-dashboard/prepend.php"),
	)
	if recipe.git {
		lines = append(lines, "RUN apk add --no-cache git")
	}
	lines = append(lines, "COPY --from=composer /usr/bin/composer /usr/bin/composer", "FROM php-base AS vendor")
	working := ""
	switch recipe.wordpress {
	case "content":
		lines = append(lines, "COPY --from=wordpress /usr/src/wordpress /app", "COPY . wp-content/")
		working = "wp-content"
	case "theme", "plugin":
		working = "wp-content/" + recipe.wordpress + "s/" + recipe.wordpressSlug
		lines = append(lines, "COPY --from=wordpress /usr/src/wordpress /app", "COPY . "+working+"/")
	default:
		lines = append(lines, "COPY . .")
	}
	if recipe.framework == "laravel" {
		// Composer's package:discover and the asset stage's artisan both boot
		// the application, which needs its cache directories; a checkout
		// holds none of them unless a placeholder was committed.
		lines = append(lines, "RUN mkdir -p storage/framework/cache storage/framework/sessions storage/framework/views storage/logs storage/app/public bootstrap/cache database && chmod -R a+rwX storage bootstrap/cache database")
		// What `php artisan storage:link` writes, without booting the
		// application in the build: files on the public disk are served from
		// public/storage. A committed public/storage is left alone.
		lines = append(lines, "RUN if [ -d public ] && [ ! -e public/storage ]; then ln -s /app/storage/app/public public/storage; fi")
	}
	if recipe.composer {
		flags := " --no-dev --optimize-autoloader --no-interaction --prefer-dist"
		if working != "" {
			flags += " --working-dir=" + working
		}
		switch {
		case len(recipe.lockUpdate) == 1 && recipe.lockUpdate[0] == "*":
			lines = append(lines, "RUN "+installSecrets+"composer update"+flags)
		case len(recipe.lockUpdate) > 0:
			// composer.lock no longer matches composer.json for these
			// packages; updating just them keeps every other locked version.
			lines = append(lines, "RUN "+installSecrets+"composer update"+flags+" --with-all-dependencies "+strings.Join(recipe.lockUpdate, " "))
		default:
			lines = append(lines, "RUN "+installSecrets+"composer install"+flags)
		}
	}
	for _, command := range recipe.symfony {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	if recipe.assets != "" {
		stage, err := phpAssetStage(recipe, bases, installSecrets, buildSecrets, config)
		if err != nil {
			return nil, err
		}
		lines = append(lines, stage...)
	}
	lines = append(lines, "FROM php-base", "COPY --from=vendor /app /app")
	if recipe.assets != "" {
		lines = append(lines, "COPY --from=assets /app/"+recipe.output+" /app/"+recipe.output)
	}
	if recipe.wordpress != "" && recipe.wordpress != "bedrock" {
		write := "RUN mkdir -p wp-content/uploads"
		if recipe.wordpressConfig {
			write += " && " + printfFile(phpWordPressConfigSource, "wp-config.php")
		}
		lines = append(lines, write)
	}
	lines = append(lines, "ENV "+strings.Join(trustEnvironment(phpProxyTrust), " "))
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	lines = append(lines, shellCMD(config.StartCommand))
	return lines, nil
}

// phpAssetStage builds the front-end assets on top of the vendor stage, so
// the build finds PHP (Wayfinder runs `php artisan`) and vendor/ (Flux's and
// Ziggy's imports). Node, with its npm and Corepack, and the managers the
// toolchain stage pinned, are copied in from the Node image; the asset
// stage's Node is always the Alpine family (nodeImageFamily), which runs on
// the Alpine PHP image.
func phpAssetStage(recipe phpRecipe, bases []ResolvedImage, installSecrets, buildSecrets string, config BuildPlanConfig) ([]string, error) {
	plan := recipe.node
	node, err := resolveCatalogueImage(bases, plan.nodeImage())
	if err != nil {
		return nil, err
	}
	build := plan.buildRun(buildSecrets, plan.build, boundToBuild(config.Secrets))
	bun := ""
	if plan.bun != "" {
		image, err := resolveCatalogueImage(bases, plan.bun)
		if err != nil {
			return nil, err
		}
		bun = immutableImageReference(image)
	}
	toolchain := plan.toolchainLines(bun)
	lines := append([]string{"FROM " + immutableImageReference(node) + " AS assets-toolchain"}, toolchain...)
	lines = append(lines, "FROM vendor AS assets",
		"COPY --from=assets-toolchain /usr/local /opt/node",
		"COPY --from=assets-toolchain /opt /opt",
		"ENV PATH=/opt/node/bin:$PATH")
	for _, line := range toolchain {
		if strings.HasPrefix(line, "ENV ") {
			lines = append(lines, line)
		}
	}
	if packages := plan.image.buildPackages; len(packages) > 0 {
		lines = append(lines, nodePackagesLine(plan.family, packages))
	}
	for _, env := range plan.image.buildEnv {
		lines = append(lines, "ENV "+env)
	}
	if plan.berry {
		lines = append(lines, "ENV YARN_ENABLE_GLOBAL_CACHE=false")
	}
	if recipe.devInstall {
		lines = append(lines, "RUN "+installSecrets+"composer install --no-interaction --prefer-dist --no-scripts")
	}
	lines = append(lines, nodeRunWith(installSecrets, plan.installDefaults(), plan.installLine()), build)
	return lines, nil
}
