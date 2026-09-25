package deploy

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A PHP application is described by more than composer.json: the lock says
// which extensions its dependencies need and whether it still matches the
// manifest, the code says which extensions it calls, the asset build says
// where it writes, and a WordPress tree says which of its shapes it is. All
// of it is read here as bounded data — the repository's code is never run —
// by one function that detection and the recipe both call, so what
// detection says a build will do is what the build does.

// DetectedPHP is what the PHP recipe read beyond composer.json, kept on the
// candidate so preflight can judge a plan without the tree.
type DetectedPHP struct {
	// Version is the catalogue release the recipe builds on, and
	// Constraints the requirements that chose it, as "source constraint".
	Version     string   `json:"version,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
	// Extensions are those the recipe installs beyond the image's built-ins,
	// each with why; Unsupported are those install-php-extensions cannot
	// build, which the recipe leaves out.
	Extensions  []DetectedPHPExtension `json:"extensions,omitempty"`
	Unsupported []DetectedPHPExtension `json:"unsupported,omitempty"`
	// Lock is "read", "absent" or "unread" (larger than its budget or not
	// JSON). LockMissing lists composer.json's requirements the lock lacks,
	// LockOutdated those whose locked version no longer satisfies it, and
	// DevLockOutdated the same for require-dev, which a production install
	// does not check.
	Lock            string   `json:"lock,omitempty"`
	LockMissing     []string `json:"lockMissing,omitempty"`
	LockOutdated    []string `json:"lockOutdated,omitempty"`
	DevLockOutdated []string `json:"devLockOutdated,omitempty"`
	// DevProviders are service providers of require-dev packages registered
	// for every environment, which `composer install --no-dev` cannot boot.
	DevProviders []string `json:"devProviders,omitempty"`
	// AssetOutput is where the asset stage writes, AssetsOutsideRoot that the
	// served directory does not contain it, and MixUnbuilt that Laravel Mix
	// assets are neither committed nor buildable.
	AssetOutput       string `json:"assetOutput,omitempty"`
	AssetsOutsideRoot bool   `json:"assetsOutsideRoot,omitempty"`
	MixUnbuilt        bool   `json:"mixUnbuilt,omitempty"`
	// WordPress is the shape of a WordPress tree — core, content, theme,
	// plugin or bedrock — and WordPressConfig says the recipe writes its
	// environment-driven wp-config.php because the repository has none.
	WordPress       string `json:"wordpress,omitempty"`
	WordPressConfig bool   `json:"wordpressConfig,omitempty"`
	// ServerConfig is a Procfile's heroku-php-* server configuration, which
	// FrankenPHP does not read.
	ServerConfig string `json:"serverConfig,omitempty"`
}

type DetectedPHPExtension struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

const (
	phpLockLimit       = 4 << 20
	phpCodeScanBytes   = 3 << 20
	phpCodeScanFiles   = 400
	phpCodeScanDepth   = 6
	phpReadBudgetBytes = 12 << 20
	phpSmallFile       = 64 << 10
	phpListedNames     = 32
)

func newPHPReadBudget() *nodeReadBudget { return &nodeReadBudget{remaining: phpReadBudgetBytes} }

// composerLock is the part of composer.lock the recipe reads: every
// package's name, version and requirements, and what it provides or
// replaces, since a requirement can be satisfied by a package of another
// name.
type composerLock struct {
	Packages    []composerLockedPackage `json:"packages"`
	PackagesDev []composerLockedPackage `json:"packages-dev"`
}

type composerLockedPackage struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Require map[string]string `json:"require"`
	Provide map[string]string `json:"provide"`
	Replace map[string]string `json:"replace"`
	Dist    json.RawMessage   `json:"dist"`
}

// phpProject is everything the recipe decides from an application's
// directory, read once.
type phpProject struct {
	manifest    composerManifest
	hasManifest bool
	parsed      bool
	lock        composerLock
	lockState   string
	platformPHP string
	envExample  []byte
	framework   string
	// extensions and unsupported are in the order their reasons were
	// found: the defaults, the framework, composer.json, the lock, the code.
	extensions   []DetectedPHPExtension
	unsupported  []DetectedPHPExtension
	requirements []phpVersionRequirement
	// lockUpdate lists the packages composer.json requires that the lock
	// lacks or no longer satisfies; the recipe updates those rather than
	// installing a lock Composer would refuse.
	lockUpdate                []string
	lockMissing, lockOutdated []string
	devLockOutdated           []string
	// git says Composer will clone a repository, which the image lacks.
	git bool
	// privateRepositories are the hosts of Composer repositories known to
	// answer only with credentials, otherRepositories those that may, and
	// authJSON a committed auth.json that supplies them.
	privateRepositories []string
	otherRepositories   []string
	vcsRepositories     []string
	authJSON            bool
	devProviders        []string
	assets              phpAssets
	wordpress           phpWordPress
	symfonyCommands     []string
	migrations          map[string]bool
	bedrockWebRoot      string
	codeEvidence        []DetectedPHPExtension
}

// phpAssets is the application's front-end build: which tool, the package
// script that runs it, where it writes and what it reads.
type phpAssets struct {
	kind     string // vite, encore, mix or wp-scripts
	script   string
	output   string
	evidence []string
	// devInstall says vite.config imports a require-dev package from
	// vendor/, so the asset stage installs development dependencies.
	devInstall bool
	mixUnbuilt bool
}

// phpWordPress is a WordPress tree's shape: core (WordPress itself
// committed), content (a wp-content directory), theme, plugin, or bedrock
// (Composer-managed, served from web/). slug is where a theme or plugin
// lives under wp-content, and header the file that says what it is.
type phpWordPress struct {
	shape, version, slug, header string
	config                       bool
}

// readPHPProjects reads every PHP root detection found, after the walk and
// under a budget of its own.
func readPHPProjects(checkout string, markers map[string]*detectedMarkers) {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return
	}
	defer root.Close()
	for _, marker := range markers {
		if marker.phpDocroot != "" && len(marker.composerJSON) == 0 && !marker.phpIndex && !marker.phpPublicIndex &&
			(phpRootOwnedElsewhere(marker) || phpInsideRoot(marker, markers)) {
			// A web/ or www/ index.php names an application only where nothing
			// else owns the directory above it.
			marker.phpDocroot = ""
		}
		if len(marker.composerJSON) == 0 && !marker.phpIndex && !marker.phpPublicIndex && marker.phpDocroot == "" && !marker.wordpressHeader {
			continue
		}
		project := readPHPProject(nodeFiles{root: root, dir: filepath.ToSlash(marker.root), budget: newPHPReadBudget()})
		marker.php = &project
	}
}

func phpRootOwnedElsewhere(marker *detectedMarkers) bool {
	return len(marker.packageJSON) > 0 || marker.goMod != "" || marker.hasPythonManifest() || len(marker.cargoToml) > 0 ||
		len(marker.pomXML) > 0 || len(marker.gradleBuild) > 0 || len(marker.csprojs) > 0 || len(marker.denoJSON) > 0 ||
		len(marker.dockerfiles) > 0 || len(marker.compose) > 0
}

// phpInsideRoot says a directory lies inside another PHP application, whose
// web/ or www/ is one of its own directories, not an application.
func phpInsideRoot(marker *detectedMarkers, markers map[string]*detectedMarkers) bool {
	for _, other := range markers {
		if other == marker || (len(other.composerJSON) == 0 && !other.phpIndex && !other.phpPublicIndex) {
			continue
		}
		if other.root == "" || strings.HasPrefix(filepath.ToSlash(marker.root)+"/", filepath.ToSlash(other.root)+"/") {
			return true
		}
	}
	return false
}

// databaseEvidence suggests MySQL for a WordPress tree whose wp-config.php
// the recipe writes, which reads the linked database from DATABASE_URL.
func (p *phpProject) databaseEvidence() []databaseEvidence {
	if p.wordpress.shape == "" || p.wordpress.shape == "bedrock" || p.wordpress.config {
		return nil
	}
	return []databaseEvidence{{"mysql", "WordPress keeps its content in MySQL"}}
}

// wordpressStatePaths: WordPress writes media to wp-content/uploads (Bedrock
// to web/app/uploads), which every release would otherwise start without.
func wordpressStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, layout stateLayout) []DetectedPersistentPath {
	if candidate.Recipe != "php" || candidate.Framework != "wordpress" || marker.php == nil {
		return nil
	}
	uploads := "wp-content/uploads"
	if marker.php.wordpress.shape == "bedrock" {
		uploads = path.Join(marker.php.bedrockWebRoot, "app/uploads")
	}
	target := layout.containerPath(uploads)
	source := "index.php"
	if marker.php.wordpress.header != "" {
		source = marker.php.wordpress.header
	}
	return []DetectedPersistentPath{{Kind: PersistentUploads, Path: target, Target: target,
		Source: joinRoot(marker.root, source), Reason: "WordPress stores uploaded media in " + uploads + "/"}}
}

// openPHPProject reads the application at a build root, the way detection
// read it.
func openPHPProject(dir string) (phpProject, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return phpProject{}, err
	}
	defer root.Close()
	return readPHPProject(nodeFiles{root: root, budget: newPHPReadBudget()}), nil
}

func readPHPProject(files nodeFiles) phpProject {
	project := phpProject{lockState: "absent", migrations: map[string]bool{}}
	if content, err := files.read("composer.json", 512<<10); err == nil {
		project.hasManifest = true
		project.manifest, project.parsed = parseComposerManifest(content)
	}
	if files.exists("composer.lock") {
		project.lockState = "unread"
		if file, err := files.open("composer.lock", phpLockLimit); err == nil {
			// Decoding keeps only the fields above; the rest of the lock is
			// tokenised and dropped.
			// A lock without "packages" is not one Composer wrote.
			if json.NewDecoder(file).Decode(&project.lock) == nil && project.lock.Packages != nil {
				project.lockState = "read"
			}
			file.Close()
		}
	}
	project.envExample, _ = files.read(".env.example", phpSmallFile)
	project.authJSON = files.exists("auth.json")
	for name := range map[string]bool{"app/Database/Migrations": true, "config/Migrations": true, "migrations": true, "database/migrations": true} {
		project.migrations[name] = phpDirectoryHasPHP(files, name)
	}
	if project.parsed {
		project.framework = phpFrameworkOf(project.manifest)
		project.platformPHP = project.manifest.platformPHP()
		project.bedrockWebRoot = project.manifest.wordpressWebRoot()
	}
	project.wordpress = readPHPWordPress(files, project)
	if project.wordpress.shape != "" {
		project.framework = "wordpress"
	}
	project.readLock()
	project.readRepositories()
	project.assets = readPHPAssets(files, project)
	project.readSymfony(files)
	project.devProviders = laravelDevProviders(files, project)
	project.codeEvidence = scanPHPExtensionUse(files, project.wordpress.shape == "core")
	project.extensions, project.unsupported = project.requiredExtensions()
	return project
}

func phpDirectoryHasPHP(files nodeFiles, dir string) bool {
	for _, entry := range phpListDirectory(files, dir) {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".php") {
			return true
		}
	}
	return false
}

// phpListDirectory lists a directory of the project, bounded, without
// following a symlink out of it.
func phpListDirectory(files nodeFiles, dir string) []fs.DirEntry {
	if dir != "" && dir != "." {
		if info, ok := files.stat(dir); !ok || !info.IsDir() {
			return nil
		}
	}
	name := files.dir
	if dir != "" && dir != "." {
		name = files.name(dir)
	}
	if name == "" {
		name = "."
	}
	handle, err := files.root.Open(name)
	if err != nil {
		return nil
	}
	defer handle.Close()
	entries, _ := handle.ReadDir(512)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries
}

// readLock compares the lock with composer.json and collects the PHP
// versions its packages accept.
func (p *phpProject) readLock() {
	root := strings.TrimSpace(p.manifest.Require["php"])
	if root != "" {
		p.requirements = append(p.requirements, phpVersionRequirement{source: "composer.json", constraint: root})
	}
	if p.lockState != "read" || !p.parsed {
		return
	}
	production := map[string]string{}
	everything := map[string]string{}
	satisfies := func(target map[string]string, pkg composerLockedPackage) {
		name := strings.ToLower(pkg.Name)
		target[name] = pkg.Version
		for _, provided := range []map[string]string{pkg.Provide, pkg.Replace} {
			for other := range provided {
				if _, locked := target[strings.ToLower(other)]; !locked {
					// A provided or replaced name counts as present; its
					// version is not a release of that name.
					target[strings.ToLower(other)] = ""
				}
			}
		}
	}
	for _, pkg := range p.lock.Packages {
		satisfies(production, pkg)
		satisfies(everything, pkg)
		if constraint := strings.TrimSpace(pkg.Require["php"]); constraint != "" && len(p.requirements) < 64 {
			p.requirements = append(p.requirements, phpVersionRequirement{source: pkg.Name, constraint: constraint})
		}
		if len(pkg.Dist) == 0 || string(pkg.Dist) == "null" {
			// A package with no archive is cloned.
			p.git = true
		}
	}
	for _, pkg := range p.lock.PackagesDev {
		satisfies(everything, pkg)
	}
	check := func(requirements map[string]string, locked map[string]string, update bool) (missing, outdated []string) {
		names := make([]string, 0, len(requirements))
		for name := range requirements {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			constraint := requirements[name]
			lower := strings.ToLower(name)
			if composerPlatformPackage(lower) {
				continue
			}
			version, present := locked[lower]
			switch {
			case !present:
				missing = append(missing, name)
			case version != "":
				if allowed, ok := composerConstraintAllows(constraint, version); ok && !allowed {
					outdated = append(outdated, name+" is locked at "+strings.TrimPrefix(version, "v")+", composer.json requires "+constraint)
				} else {
					continue
				}
			default:
				continue
			}
			if update && composerPackageNameRE.MatchString(lower) {
				p.lockUpdate = append(p.lockUpdate, lower)
			}
		}
		return missing, outdated
	}
	p.lockMissing, p.lockOutdated = check(p.manifest.Require, production, true)
	devMissing, devOutdated := check(p.manifest.RequireDev, everything, false)
	for _, name := range devMissing {
		p.devLockOutdated = append(p.devLockOutdated, name+" is not in composer.lock")
	}
	p.devLockOutdated = append(p.devLockOutdated, devOutdated...)
}

var composerPackageNameRE = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9](([_.]|-{1,2})?[a-z0-9]+)*$`)

// composerPublicRepositories are Composer repositories anyone installs
// from: Packagist and its public mirrors, and the stores the WordPress
// (Bedrock), Drupal, Yii and Magento templates list beside it.
var composerPublicRepositories = map[string]bool{
	"packagist.org": true, "repo.packagist.org": true, "wpackagist.org": true, "packages.drupal.org": true,
	"asset-packagist.org": true, "packages.firegento.com": true, "wp-languages.github.io": true, "composer.typo3.org": true,
	"mirrors.aliyun.com": true, "mirrors.tencent.com": true, "repo.huaweicloud.com": true, "packagist.phpcomposer.com": true,
}

// composerPrivateRepositories are stores that answer only with credentials:
// Laravel's paid packages, Private Packagist, Magento's marketplace, and
// the paid-package stores (Flux Pro, Spatie, ACF Pro, Delicious Brains,
// Anystack's *.composer.sh, Repman).
var composerPrivateRepositories = []string{
	"nova.laravel.com", "spark.laravel.com", "repo.packagist.com", "repo.magento.com", "composer.fluxui.dev",
	"satis.spatie.be", "connect.advancedcustomfields.com", "composer.deliciousbrains.com", "composer.sh", "repo.repman.io",
}

func composerPrivateRepository(host string) bool {
	return slices.ContainsFunc(composerPrivateRepositories, func(store string) bool {
		return host == store || strings.HasSuffix(host, "."+store)
	})
}

// composerURLCredentialsRE is a URL that carries its own user and password,
// which Composer sends without COMPOSER_AUTH.
var composerURLCredentialsRE = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^@/:]+:[^@/]+@`)

// readRepositories finds the repositories that need credentials. A
// Composer repository on a paid or private store needs them; one on a
// public store (Packagist, WPackagist, Drupal's, Asset Packagist) does not,
// and one on any other host may. A VCS repository is cloned, and may too.
func (p *phpProject) readRepositories() {
	if !p.parsed || len(p.manifest.Repositories) == 0 {
		return
	}
	var entries []map[string]any
	var list []json.RawMessage
	if json.Unmarshal(p.manifest.Repositories, &list) != nil {
		var object map[string]json.RawMessage
		if json.Unmarshal(p.manifest.Repositories, &object) != nil {
			return
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			list = append(list, object[key])
		}
	}
	for _, raw := range list {
		var entry map[string]any
		if json.Unmarshal(raw, &entry) == nil {
			entries = append(entries, entry)
		}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		kind, _ := entry["type"].(string)
		url, _ := entry["url"].(string)
		host := composerRepositoryHost(url)
		if host == "" || seen[host] {
			continue
		}
		switch strings.ToLower(kind) {
		case "composer":
			if composerPublicRepositories[host] || composerURLCredentialsRE.MatchString(strings.ToLower(strings.TrimSpace(url))) {
				continue
			}
			seen[host] = true
			if composerPrivateRepository(host) {
				p.privateRepositories = append(p.privateRepositories, host)
			} else {
				p.otherRepositories = append(p.otherRepositories, host)
			}
		case "vcs", "git", "github", "gitlab", "bitbucket":
			seen[host] = true
			p.vcsRepositories = append(p.vcsRepositories, host)
			if host != "github.com" {
				// Only GitHub's archive API spares Composer a clone.
				p.git = true
			}
		}
	}
}

var composerRepositoryHostRE = regexp.MustCompile(`^(?:[a-z][a-z0-9+.-]*://)?(?:[^@/]+@)?([a-z0-9.-]+\.[a-z]{2,})(?:[:/]|$)`)

func composerRepositoryHost(url string) string {
	match := composerRepositoryHostRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(url)))
	if match == nil {
		return ""
	}
	return match[1]
}

// Symfony's production assets: AssetMapper compiles what importmap.php
// names, after Tailwind or Sass have written their output.
func (p *phpProject) readSymfony(files nodeFiles) {
	if p.framework != "symfony" {
		return
	}
	if p.manifest.has("symfonycasts/tailwind-bundle") {
		p.symfonyCommands = append(p.symfonyCommands, "php bin/console tailwind:build --minify")
	}
	if p.manifest.has("symfonycasts/sass-bundle") {
		p.symfonyCommands = append(p.symfonyCommands, "php bin/console sass:build")
	}
	if p.manifest.has("symfony/asset-mapper") || files.exists("importmap.php") {
		if files.exists("importmap.php") {
			p.symfonyCommands = append(p.symfonyCommands, "php bin/console importmap:install")
		}
		p.symfonyCommands = append(p.symfonyCommands, "php bin/console asset-map:compile")
	}
}

// Assets --------------------------------------------------------------------

var (
	viteLaravelPublicRE = regexp.MustCompile(`publicDirectory\s*:\s*['"]([A-Za-z0-9_./-]+)['"]`)
	viteLaravelBuildRE  = regexp.MustCompile(`buildDirectory\s*:\s*['"]([A-Za-z0-9_./-]+)['"]`)
	viteOutDirRE        = regexp.MustCompile(`outDir\s*:\s*['"]([A-Za-z0-9_./-]+)['"]`)
	viteVendorImportRE  = regexp.MustCompile(`vendor/([a-z0-9_.-]+/[a-z0-9_.-]+)`)
	encoreOutputRE      = regexp.MustCompile(`\.setOutputPath\(\s*['"]([A-Za-z0-9_./-]+)['"]`)
	wpScriptsOutputRE   = regexp.MustCompile(`--output-path[=\s]+['"]?([A-Za-z0-9_./-]+)`)
	assetPathRE         = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
)

var viteConfigNames = []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.mts", "vite.config.cjs"}

// readPHPAssets reads package.json's build and the tool that runs it.
func readPHPAssets(files nodeFiles, project phpProject) phpAssets {
	content, err := files.read("package.json", 512<<10)
	if err != nil {
		return phpAssets{}
	}
	var manifest nodeManifest
	if !parseNodeManifest(content, &manifest) {
		return phpAssets{}
	}
	assets := phpAssets{}
	switch {
	case manifest.has("laravel-mix") && files.exists("webpack.mix.js"):
		if files.exists("public/mix-manifest.json") {
			// Compiled Mix assets are committed; nothing to build.
			return phpAssets{}
		}
		assets.kind, assets.output = "mix", "public"
		for _, script := range []string{"production", "prod", "build"} {
			if manifest.Scripts[script] != "" {
				assets.script = script
				break
			}
		}
		if assets.script == "" {
			assets.mixUnbuilt = true
			return assets
		}
		assets.evidence = append(assets.evidence, "Laravel Mix assets are built with the "+assets.script+" script")
		return assets
	case manifest.Scripts["build"] == "":
		return phpAssets{}
	case manifest.has("@symfony/webpack-encore"):
		assets.kind, assets.script, assets.output = "encore", "build", "public/build"
		if config, err := files.read("webpack.config.js", phpSmallFile); err == nil {
			if match := encoreOutputRE.FindSubmatch(config); match != nil {
				assets.output = string(match[1])
			}
		}
	case manifest.has("@wordpress/scripts"):
		// A block theme's or plugin's blocks are registered from what
		// wp-scripts writes, build/ unless --output-path moves it.
		assets.kind, assets.script, assets.output = "wp-scripts", "build", "build"
		if match := wpScriptsOutputRE.FindStringSubmatch(manifest.Scripts["build"]); match != nil {
			assets.output = match[1]
		}
	case manifest.has("laravel-vite-plugin") || manifest.has("vite"):
		assets.kind, assets.script = "vite", "build"
		config := []byte{}
		for _, name := range viteConfigNames {
			if content, err := files.read(name, phpSmallFile); err == nil {
				config = content
				break
			}
		}
		if manifest.has("laravel-vite-plugin") || strings.Contains(string(config), "laravel(") {
			public, build := "public", "build"
			if match := viteLaravelPublicRE.FindSubmatch(config); match != nil {
				public = string(match[1])
			}
			if match := viteLaravelBuildRE.FindSubmatch(config); match != nil {
				build = string(match[1])
			}
			assets.output = path.Join(public, build)
		} else {
			assets.output = "dist"
			if match := viteOutDirRE.FindSubmatch(config); match != nil {
				assets.output = string(match[1])
			}
		}
		if manifest.has("@laravel/vite-plugin-wayfinder") || strings.Contains(string(config), "wayfinder(") {
			assets.evidence = append(assets.evidence, "assets are built with PHP and vendor/ present (Wayfinder runs php artisan)")
		}
		for _, match := range viteVendorImportRE.FindAllSubmatch(config, -1) {
			name := string(match[1])
			if project.manifest.RequireDev[name] != "" && !project.manifest.has(name) {
				assets.devInstall = true
				assets.evidence = append(assets.evidence, "vite.config imports "+name+" from vendor/, a require-dev package, so the asset stage installs development dependencies")
				break
			}
		}
	default:
		return phpAssets{}
	}
	assets.output = strings.Trim(path.Clean(strings.TrimPrefix(assets.output, "./")), "/")
	if !assetPathRE.MatchString(assets.output) || !safeRelativePath(assets.output) {
		assets.output = ""
	}
	return assets
}

// phpAssetKind says package.json's build is one the PHP recipe's asset stage
// runs, from the manifest alone: Vite, Encore, Laravel Mix or wp-scripts.
func phpAssetKind(content []byte) string {
	var manifest nodeManifest
	if !parseNodeManifest(content, &manifest) {
		return ""
	}
	switch {
	case manifest.has("laravel-mix"):
		return "mix"
	case manifest.Scripts["build"] == "":
		return ""
	case manifest.has("@symfony/webpack-encore"):
		return "encore"
	case manifest.has("@wordpress/scripts"):
		return "wp-scripts"
	case manifest.has("laravel-vite-plugin") || manifest.has("vite"):
		return "vite"
	}
	return ""
}

// WordPress -----------------------------------------------------------------

var (
	wpVersionRE    = regexp.MustCompile(`\$wp_version\s*=\s*'([0-9][0-9.]*)'`)
	wpHeaderRE     = regexp.MustCompile(`(?m)^[ \t/*#@]*(Theme Name|Plugin Name|Text Domain):\s*(.+?)\s*$`)
	wpSlugRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	wpSlugUnsafeRE = regexp.MustCompile(`[^a-z0-9]+`)
)

func readPHPWordPress(files nodeFiles, project phpProject) phpWordPress {
	wordpress := phpWordPress{config: files.exists("wp-config.php")}
	switch {
	case project.parsed && (project.manifest.has("roots/wordpress") || project.manifest.has("johnpbloch/wordpress") ||
		strings.EqualFold(project.manifest.Name, "roots/bedrock")):
		wordpress.shape = "bedrock"
		return wordpress
	case files.exists("wp-settings.php") && files.exists("wp-includes/version.php"):
		wordpress.shape = "core"
		if content, err := files.read("wp-includes/version.php", phpSmallFile); err == nil {
			if match := wpVersionRE.FindSubmatch(content); match != nil {
				wordpress.version = string(match[1])
			}
		}
		return wordpress
	}
	if head := phpFileHead(files, "style.css"); head != nil {
		if headers := wordpressHeaders(head); headers["Theme Name"] != "" {
			wordpress.shape, wordpress.slug = "theme", wordpressSlug(headers["Text Domain"], headers["Theme Name"], "theme")
			wordpress.header = "style.css"
			return wordpress
		}
	}
	for index, entry := range phpListDirectory(files, "") {
		if index >= 256 {
			break
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".php") {
			continue
		}
		head := phpFileHead(files, entry.Name())
		if headers := wordpressHeaders(head); headers["Plugin Name"] != "" {
			wordpress.shape, wordpress.header = "plugin", entry.Name()
			wordpress.slug = wordpressSlug(headers["Text Domain"], strings.TrimSuffix(entry.Name(), ".php"), "plugin")
			return wordpress
		}
	}
	// A themes/ or plugins/ directory is common outside WordPress (Laravel
	// themers, plugin systems), so a wp-content tree is one no framework
	// owns that says it is WordPress's: the "Silence is golden" index.php,
	// or, with no index.php, a theme's or plugin's own header inside it.
	if project.framework == "" && (files.dirExists("themes") || files.dirExists("plugins") || files.dirExists("mu-plugins")) {
		index := phpFileHead(files, "index.php")
		if (index != nil && strings.Contains(string(index), "Silence is golden")) || (index == nil && wordpressContentHeaders(files)) {
			wordpress.shape = "content"
		}
	}
	return wordpress
}

// wordpressContentHeaders says a wp-content tree holds a theme (themes/*/
// style.css) or a plugin (plugins/*/*.php, plugins/*.php, mu-plugins/*.php)
// that carries WordPress's header, looking at a bounded number of each.
func wordpressContentHeaders(files nodeFiles) bool {
	for index, theme := range phpListDirectory(files, "themes") {
		if index >= 32 {
			break
		}
		if theme.IsDir() && wordpressHeaders(phpFileHead(files, "themes/"+theme.Name()+"/style.css"))["Theme Name"] != "" {
			return true
		}
	}
	plugin := func(name string) bool {
		return strings.HasSuffix(name, ".php") && wordpressHeaders(phpFileHead(files, name))["Plugin Name"] != ""
	}
	for _, dir := range []string{"plugins", "mu-plugins"} {
		for index, entry := range phpListDirectory(files, dir) {
			if index >= 32 {
				break
			}
			name := dir + "/" + entry.Name()
			if !entry.IsDir() {
				if plugin(name) {
					return true
				}
				continue
			}
			for count, file := range phpListDirectory(files, name) {
				if count >= 16 {
					break
				}
				if plugin(name + "/" + file.Name()) {
					return true
				}
			}
		}
	}
	return false
}

// wordpressHeaderPath names the files whose header makes the top of the
// checkout a WordPress root: its own style.css or PHP files (a theme, a
// plugin), and a theme's style.css under themes/ (a wp-content tree).
func wordpressHeaderPath(rel, name string) bool {
	switch parts := strings.Split(rel, "/"); len(parts) {
	case 1:
		return name == "style.css" || strings.HasSuffix(name, ".php")
	case 3:
		return parts[0] == "themes" && name == "style.css"
	}
	return false
}

// wordpressRootHeader says the head of such a file carries a theme's
// (style.css) or a plugin's (*.php) header: the repository is a WordPress
// root even without an index.php (a block theme, a @wordpress/create-block
// plugin, a wp-content tree). It is read during the walk, because nothing
// else names that directory.
func wordpressRootHeader(path, name string) (bool, int64) {
	header := "Plugin Name"
	if name == "style.css" {
		header = "Theme Name"
	} else if !strings.HasSuffix(name, ".php") {
		return false, 0
	}
	file, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer file.Close()
	head, _ := io.ReadAll(io.LimitReader(file, 8<<10))
	return wordpressHeaders(head)[header] != "", int64(len(head))
}

// phpFileHead is the first 8 KiB of a file, where WordPress reads a theme's
// or plugin's header.
func phpFileHead(files nodeFiles, name string) []byte {
	file, err := files.openHead(name, 2<<20)
	if err != nil {
		return nil
	}
	defer file.Close()
	head, _ := io.ReadAll(io.LimitReader(file, 8<<10))
	return head
}

func wordpressHeaders(head []byte) map[string]string {
	headers := map[string]string{}
	for _, match := range wpHeaderRE.FindAllSubmatch(head, 8) {
		if _, set := headers[string(match[1])]; !set {
			headers[string(match[1])] = strings.TrimSpace(strings.TrimSuffix(string(match[2]), "*/"))
		}
	}
	return headers
}

// wordpressSlug is the directory a theme or plugin is installed as: its text
// domain, else its name made safe, else a fixed word.
func wordpressSlug(domain, name, fallback string) string {
	for _, candidate := range []string{domain, name} {
		slug := strings.Trim(wpSlugUnsafeRE.ReplaceAllString(strings.ToLower(candidate), "-"), "-")
		if wpSlugRE.MatchString(slug) {
			return slug
		}
	}
	return fallback
}

// Laravel's development providers -----------------------------------------

// laravelDevNamespaces maps the namespace of a development package's
// provider to the package that installs it.
var laravelDevNamespaces = []struct{ namespace, pkg string }{
	{`Laravel\Telescope`, "laravel/telescope"}, {`Barryvdh\LaravelIdeHelper`, "barryvdh/laravel-ide-helper"},
	{`Barryvdh\Debugbar`, "barryvdh/laravel-debugbar"}, {`Laravel\Dusk`, "laravel/dusk"},
	{`Clockwork\Support\Laravel`, "itsgoingd/clockwork"}, {`Laravel\Sail`, "laravel/sail"},
	{`Laravel\Pail`, "laravel/pail"}, {`NunoMaduro\Collision`, "nunomaduro/collision"},
}

var (
	laravelProviderRE     = regexp.MustCompile(`([A-Za-z_\\][A-Za-z0-9_\\]*)::class`)
	laravelProvidersKeyRE = regexp.MustCompile(`['"]providers['"]\s*=>`)
	phpClassExtendsRE     = regexp.MustCompile(`\bclass\s+[A-Za-z_][A-Za-z0-9_]*\s+extends\s+(\\?[A-Za-z_][A-Za-z0-9_\\]*)`)
	phpUseRE              = regexp.MustCompile(`(?m)^\s*use\s+\\?([A-Za-z_][A-Za-z0-9_\\]*)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?\s*;`)
)

// laravelDevProviders finds providers registered for every environment
// whose classes come from require-dev packages: bootstrap/providers.php
// (Laravel 11 and later) and the providers of config/app.php — not its
// aliases, facades Laravel resolves only when called. The provider an
// application writes for Telescope lives in app/Providers and extends the
// package's own, so that class is followed through what it extends; what
// its body mentions is not a registration (Telescope's local-only
// installation registers the package inside an environment check).
func laravelDevProviders(files nodeFiles, project phpProject) []string {
	if project.framework != "laravel" || len(project.manifest.RequireDev) == 0 {
		return nil
	}
	devOnly := func(pkg string) bool { return project.manifest.RequireDev[pkg] != "" && !project.manifest.has(pkg) }
	found := []string{}
	for _, source := range []string{"bootstrap/providers.php", "config/app.php"} {
		content, err := files.read(source, phpSmallFile)
		if err != nil {
			continue
		}
		text := string(content)
		if source == "config/app.php" {
			text = phpArrayValue(text, laravelProvidersKeyRE)
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "environment(") || strings.Contains(line, "isLocal(") || strings.Contains(line, "class_exists(") ||
				strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, match := range laravelProviderRE.FindAllStringSubmatch(line, -1) {
				class := strings.TrimPrefix(match[1], `\`)
				pkg := laravelDevPackage(class)
				for parent, depth := class, 0; pkg == "" && strings.HasPrefix(parent, `App\Providers\`) && depth < 3; depth++ {
					parent = phpParentClass(files, parent)
					pkg = laravelDevPackage(parent)
				}
				if pkg != "" && devOnly(pkg) && len(found) < 8 {
					found = append(found, class+" in "+source+" ("+pkg+" is require-dev)")
				}
			}
		}
	}
	return found
}

// phpParentClass is the fully qualified class an App\Providers class
// extends, resolved the way PHP does: a leading backslash is absolute, a
// name whose first segment a `use` import aliases is that import, anything
// else is in the file's namespace.
func phpParentClass(files nodeFiles, class string) string {
	relative := strings.TrimPrefix(class, `App\Providers\`)
	body, err := files.read("app/Providers/"+strings.ReplaceAll(relative, `\`, "/")+".php", phpSmallFile)
	if err != nil {
		return ""
	}
	declaration := phpClassExtendsRE.FindSubmatchIndex(body)
	if declaration == nil {
		return ""
	}
	parent := string(body[declaration[2]:declaration[3]])
	if strings.HasPrefix(parent, `\`) {
		return strings.TrimPrefix(parent, `\`)
	}
	first, rest, nested := strings.Cut(parent, `\`)
	// Imports come before the class; a `use` inside its body is a trait.
	for _, use := range phpUseRE.FindAllSubmatch(body[:declaration[0]], 64) {
		imported := string(use[1])
		alias := string(use[2])
		if alias == "" {
			alias = imported[strings.LastIndex(imported, `\`)+1:]
		}
		if alias == first {
			if nested {
				return imported + `\` + rest
			}
			return imported
		}
	}
	if namespace := phpNamespaceRE.FindSubmatch(body[:declaration[0]]); namespace != nil {
		return string(namespace[1]) + `\` + parent
	}
	return parent
}

// phpArrayValue is the source text of one key's value in a PHP array
// literal: from `'key' =>` to the comma that ends it at the same depth, or
// the bracket that closes the array around it. Strings and comments are
// stepped over; the file is read as text, never run.
func phpArrayValue(text string, key *regexp.Regexp) string {
	location := key.FindStringIndex(text)
	if location == nil {
		return ""
	}
	start, depth := location[1], 0
	for i := start; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\'' || c == '"':
			for i++; i < len(text) && text[i] != c; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case (c == '#' && !strings.HasPrefix(text[i:], "#[")) || strings.HasPrefix(text[i:], "//"):
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return text[start:]
			}
			i += end + 3
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			if depth--; depth < 0 {
				return text[start:i]
			}
		case c == ',' && depth == 0:
			return text[start:i]
		}
	}
	return text[start:]
}

func laravelDevPackage(class string) string {
	for _, entry := range laravelDevNamespaces {
		if strings.HasPrefix(class, entry.namespace+`\`) {
			return entry.pkg
		}
	}
	return ""
}

// Extensions ------------------------------------------------------------------

// phpInstallableExtensions are the names install-php-extensions builds on
// the Alpine image, as Composer spells them after ext-.
var phpInstallableExtensions = map[string]bool{
	"amqp": true, "apcu": true, "ast": true, "bcmath": true, "brotli": true, "bz2": true, "calendar": true,
	"csv": true, "dba": true, "decimal": true, "ds": true, "enchant": true, "event": true, "excimer": true,
	"exif": true, "ffi": true, "gd": true, "gettext": true, "gmp": true, "gnupg": true, "grpc": true,
	"igbinary": true, "imagick": true, "imap": true, "inotify": true, "intl": true, "ldap": true,
	"lzf": true, "mailparse": true, "maxminddb": true, "memcache": true, "memcached": true, "mongodb": true,
	"msgpack": true, "mysqli": true, "oauth": true, "odbc": true, "opcache": true, "opentelemetry": true,
	"parallel": true, "pcntl": true, "pcov": true, "pdo_dblib": true, "pdo_firebird": true, "pdo_mysql": true,
	"pdo_odbc": true, "pdo_pgsql": true, "pdo_sqlsrv": true, "pgsql": true, "protobuf": true, "pspell": true,
	"raphf": true, "rdkafka": true, "redis": true, "shmop": true, "smbclient": true, "snmp": true, "soap": true,
	"sockets": true, "solr": true, "sqlsrv": true, "ssh2": true, "swoole": true, "sysvmsg": true, "sysvsem": true,
	"sysvshm": true, "tidy": true, "timezonedb": true, "uuid": true, "vips": true, "xdebug": true, "xhprof": true,
	"xlswriter": true, "xmlrpc": true, "xsl": true, "yaml": true, "zip": true, "zstd": true, "http": true,
	"pq": true, "psr": true, "geoip": true, "oci8": true, "pdo_oci": true, "openswoole": true,
}

// phpExtensionAliases are the other names Composer's platform uses.
var phpExtensionAliases = map[string]string{
	"zend-opcache": "opcache", "zend opcache": "opcache", "pdo-mysql": "pdo_mysql", "pdo-pgsql": "pdo_pgsql",
}

// phpPackageExtensions are what popular packages require when no lock says
// so: without composer.lock, their ext- requirements are not in the tree.
var phpPackageExtensions = []struct {
	pkg        string
	extensions []string
}{
	{"filament/filament", []string{"intl"}}, {"filament/support", []string{"intl"}},
	{"laravel/horizon", []string{"pcntl", "redis"}}, {"phpoffice/phpspreadsheet", []string{"gd", "zip"}},
	{"maatwebsite/excel", []string{"gd", "zip"}}, {"codeigniter4/framework", []string{"intl"}},
	{"cakephp/cakephp", []string{"intl"}}, {"drupal/core", []string{"gd"}}, {"drupal/core-recommended", []string{"gd"}},
	{"mongodb/mongodb", []string{"mongodb"}}, {"mongodb/laravel-mongodb", []string{"mongodb"}},
	{"spatie/laravel-medialibrary", []string{"exif"}}, {"intervention/image", []string{"gd"}},
}

// phpCodeExtensionRules are calls in the application's own code that need an
// extension the image lacks.
var phpCodeExtensionRules = []struct {
	extension, label string
	pattern          *regexp.Regexp
}{
	{"mysqli", "mysqli", regexp.MustCompile(`\bmysqli_[a-z_]+\s*\(|\bnew\s+\\?mysqli\b`)},
	{"gd", "an image* function", regexp.MustCompile(`\bimage(?:create\w*|copyresampled|jpeg|png|webp|gif|ttftext|scale)\s*\(`)},
	{"zip", "ZipArchive", regexp.MustCompile(`\bnew\s+\\?ZipArchive\b`)},
	{"intl", "an Intl class", regexp.MustCompile(`\bnew\s+\\?(?:NumberFormatter|IntlDateFormatter|Collator)\b|\bNumber::(?:format|currency|percentage|spell|ordinal)\s*\(`)},
	{"bcmath", "a bc* function", regexp.MustCompile(`\bbc(?:add|sub|mul|div|comp|mod|pow|sqrt|scale)\s*\(`)},
	{"exif", "exif_read_data", regexp.MustCompile(`\bexif_read_data\s*\(`)},
	{"redis", "the Redis class", regexp.MustCompile(`\bnew\s+\\?Redis\s*\(`)},
	{"pcntl", "a pcntl function", regexp.MustCompile(`\bpcntl_\w+\s*\(`)},
	{"gmp", "a gmp function", regexp.MustCompile(`\bgmp_\w+\s*\(`)},
	{"imagick", "Imagick", regexp.MustCompile(`\bnew\s+\\?Imagick\b`)},
	{"soap", "SoapClient", regexp.MustCompile(`\bnew\s+\\?Soap(?:Client|Server)\b`)},
	{"sockets", "a socket function", regexp.MustCompile(`\bsocket_(?:create|connect|bind)\s*\(`)},
	{"pgsql", "a pg_ function", regexp.MustCompile(`\bpg_(?:connect|pconnect|query)\s*\(`)},
	{"calendar", "a calendar function", regexp.MustCompile(`\b(?:cal_days_in_month|easter_date|jdtogregorian|gregoriantojd)\s*\(`)},
	{"gettext", "gettext", regexp.MustCompile(`\b(?:bindtextdomain|textdomain|dgettext)\s*\(`)},
	{"xsl", "XSLTProcessor", regexp.MustCompile(`\bnew\s+\\?XSLTProcessor\b`)},
	{"apcu", "an apcu function", regexp.MustCompile(`\bapcu_\w+\s*\(`)},
	{"memcached", "Memcached", regexp.MustCompile(`\bnew\s+\\?Memcached\b`)},
}

var phpScanSkippedDirs = map[string]bool{
	"vendor": true, "node_modules": true, ".git": true, "storage": true, "var": true, "cache": true, "tmp": true,
	"tests": true, "test": true, "Tests": true, "bootstrap": true, "uploads": true, "wp-admin": true, "wp-includes": true,
	"build": true, "dist": true, ".just-dashboard": true,
}

// scanPHPExtensionUse reads the application's own PHP files, breadth first
// and bounded, for the calls above. WordPress core's own files are skipped:
// its extensions are known.
func scanPHPExtensionUse(files nodeFiles, wordpressCore bool) []DetectedPHPExtension {
	found := []DetectedPHPExtension{}
	seen := map[string]bool{}
	read, count := int64(0), 0
	queue := []struct {
		dir   string
		depth int
	}{{"", 0}}
	for len(queue) > 0 && count < phpCodeScanFiles && read < phpCodeScanBytes && len(seen) < len(phpCodeExtensionRules) {
		current := queue[0]
		queue = queue[1:]
		for _, entry := range phpListDirectory(files, current.dir) {
			name := path.Join(current.dir, entry.Name())
			switch {
			case entry.IsDir():
				if !phpScanSkippedDirs[entry.Name()] && !strings.HasPrefix(entry.Name(), ".") && current.depth < phpCodeScanDepth &&
					!(wordpressCore && current.dir == "") {
					queue = append(queue, struct {
						dir   string
						depth int
					}{name, current.depth + 1})
				}
				continue
			case !entry.Type().IsRegular() || count >= phpCodeScanFiles || read >= phpCodeScanBytes:
				continue
			}
			extension := path.Ext(entry.Name())
			if extension != ".php" && extension != ".inc" && extension != ".phtml" {
				continue
			}
			content, err := files.read(name, 256<<10)
			if err != nil {
				continue
			}
			count++
			read += int64(len(content))
			for _, rule := range phpCodeExtensionRules {
				if seen[rule.extension] || !rule.pattern.Match(content) {
					continue
				}
				seen[rule.extension] = true
				found = append(found, DetectedPHPExtension{Name: rule.extension, Reason: rule.label + " in " + name})
			}
		}
	}
	return found
}

// requiredExtensions merges every reason an extension is needed — the
// defaults, the framework, composer.json, the lock or the package table,
// the code — into the list the recipe installs, and the names it cannot.
func (p *phpProject) requiredExtensions() (install, unsupported []DetectedPHPExtension) {
	reasons := map[string]string{}
	order := []string{}
	add := func(name, reason string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if alias, ok := phpExtensionAliases[name]; ok {
			name = alias
		}
		if name == "" || phpBuiltinExtension[name] || reasons[name] != "" {
			return
		}
		reasons[name] = reason
		order = append(order, name)
	}
	for _, name := range phpDefaultExtensions {
		add(name, "always installed, so a linked database works without asking")
	}
	if p.framework == "wordpress" {
		for _, name := range []string{"mysqli", "gd", "exif", "intl", "zip"} {
			add(name, "WordPress uses it")
		}
	}
	if p.parsed {
		names := make([]string, 0, len(p.manifest.Require))
		for name := range p.manifest.Require {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.HasPrefix(strings.ToLower(name), "ext-") {
				add(strings.TrimPrefix(strings.ToLower(name), "ext-"), name+" in composer.json")
			}
		}
		if p.lockState == "read" {
			for _, pkg := range p.lock.Packages {
				requires := make([]string, 0, len(pkg.Require))
				for name := range pkg.Require {
					requires = append(requires, name)
				}
				sort.Strings(requires)
				for _, name := range requires {
					if strings.HasPrefix(strings.ToLower(name), "ext-") {
						add(strings.TrimPrefix(strings.ToLower(name), "ext-"), name+" required by "+pkg.Name+" (composer.lock)")
					}
				}
			}
		}
		// Without a lock the packages' own requirements are not in the tree;
		// the table stands in for the ones that are commonly needed. Horizon
		// is there either way: its Redis connection is the application's.
		for _, entry := range phpPackageExtensions {
			if !p.manifest.has(entry.pkg) || (p.lockState == "read" && entry.pkg != "laravel/horizon") {
				continue
			}
			for _, name := range entry.extensions {
				add(name, entry.pkg+" needs it")
			}
		}
		if p.framework == "laravel" && !p.manifest.has("predis/predis") && laravelUsesPHPRedis(p.envExample) {
			add("redis", "Laravel's Redis client defaults to phpredis (REDIS_ in .env.example, no predis/predis)")
		}
	}
	for _, use := range p.codeEvidence {
		add(use.Name, use.Reason+" → "+use.Name)
	}
	for _, name := range order {
		entry := DetectedPHPExtension{Name: name, Reason: reasons[name]}
		if phpInstallableExtensions[name] && phpExtensionNameRE.MatchString(name) {
			install = append(install, entry)
		} else {
			unsupported = append(unsupported, entry)
		}
	}
	return install, unsupported
}

var (
	laravelRedisClientRE = regexp.MustCompile(`(?m)^\s*REDIS_CLIENT\s*=\s*['"]?(\w+)`)
	laravelRedisHostRE   = regexp.MustCompile(`(?m)^\s*REDIS_(?:HOST|URL)\s*=`)
)

// laravelUsesPHPRedis says .env.example configures Redis through Laravel's
// default client, the phpredis extension.
func laravelUsesPHPRedis(env []byte) bool {
	if match := laravelRedisClientRE.FindSubmatch(env); match != nil {
		return strings.EqualFold(string(match[1]), "phpredis")
	}
	return laravelRedisHostRE.Match(env)
}

// phpFrameworkOf names the framework a manifest requires.
func phpFrameworkOf(manifest composerManifest) string {
	for _, framework := range phpFrameworks {
		if manifest.has(framework.pkg) {
			return framework.name
		}
	}
	return ""
}

// Facts ---------------------------------------------------------------------

// detected is what detection records on the candidate.
func (p phpProject) detected(version string) *DetectedPHP {
	facts := &DetectedPHP{
		Version: version, Lock: p.lockState,
		Extensions: boundPHPExtensions(p.extensions), Unsupported: boundPHPExtensions(p.unsupported),
		LockMissing: boundPHPNames(p.lockMissing), LockOutdated: boundPHPNames(p.lockOutdated),
		DevLockOutdated: boundPHPNames(p.devLockOutdated), DevProviders: boundPHPNames(p.devProviders),
		MixUnbuilt: p.assets.mixUnbuilt, WordPress: p.wordpress.shape,
		WordPressConfig: p.wordpress.shape != "" && p.wordpress.shape != "bedrock" && !p.wordpress.config,
	}
	if !p.hasManifest {
		facts.Lock = ""
	}
	for _, requirement := range p.requirements {
		if len(facts.Constraints) >= phpListedNames {
			break
		}
		facts.Constraints = append(facts.Constraints, boundedEvidenceSentence(requirement.source+" "+requirement.constraint))
	}
	return facts
}

func boundPHPExtensions(list []DetectedPHPExtension) []DetectedPHPExtension {
	if len(list) > phpListedNames {
		list = list[:phpListedNames]
	}
	bounded := make([]DetectedPHPExtension, 0, len(list))
	for _, entry := range list {
		bounded = append(bounded, DetectedPHPExtension{Name: entry.Name, Reason: boundedEvidenceSentence(entry.Reason)})
	}
	return bounded
}

func boundPHPNames(list []string) []string {
	if len(list) > phpListedNames {
		list = list[:phpListedNames]
	}
	bounded := make([]string, 0, len(list))
	for _, entry := range list {
		bounded = append(bounded, boundedEvidenceSentence(entry))
	}
	return bounded
}

func (p phpProject) extensionNames() []string {
	names := make([]string, 0, len(p.extensions))
	for _, entry := range p.extensions {
		names = append(names, entry.Name)
	}
	return names
}

// phpExtensionForSymbol names the extension behind a function or class a
// PHP error says is undefined.
func phpExtensionForSymbol(symbol string) string {
	for _, entry := range []struct{ prefix, extension string }{
		{"mysqli", "mysqli"}, {"MySQL", "mysqli"}, {"image", "gd"}, {"bc", "bcmath"}, {"gmp", "gmp"}, {"pcntl", "pcntl"},
		{"exif", "exif"}, {"sodium", "sodium"}, {"apcu", "apcu"}, {"pg", "pgsql"}, {"ldap", "ldap"}, {"socket", "sockets"},
		{"Redis", "redis"}, {"ZipArchive", "zip"}, {"NumberFormatter", "intl"}, {"IntlDateFormatter", "intl"},
		{"Collator", "intl"}, {"Imagick", "imagick"}, {"SoapClient", "soap"}, {"Memcached", "memcached"},
		{"XSLTProcessor", "xsl"}, {"MongoDB", "mongodb"},
	} {
		if strings.HasPrefix(symbol, entry.prefix) {
			return entry.extension
		}
	}
	return ""
}
