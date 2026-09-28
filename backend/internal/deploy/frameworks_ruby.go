package deploy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The Ruby recipe builds a Rack application — Rails, Hanami, Sinatra, Roda,
// Grape or plain Rack — on the official ruby:<release>-slim image. Bundler
// installs the lock frozen, an asset pipeline precompiles with Node borrowed
// when the application's own package.json or ExecJS needs it, and the
// runtime stage carries the bundle and the application with only the shared
// libraries its gems load. RubyGems switches to the lock's BUNDLED WITH
// release by itself, so no Bundler is installed.

var rubyRecipeFamilies = []string{"3.3", "3.4", "4.0"}

const rubyDefaultFamily = "3.4"

var (
	gemfileRubyRE     = regexp.MustCompile(`(?m)^\s*ruby\s*\(?\s*(.+?)\s*\)?\s*(?:#.*)?$`)
	gemfileQuotedRE   = regexp.MustCompile(`["']([^"']+)["']`)
	gemfileSourceRE   = regexp.MustCompile(`(?m)^\s*source\s*\(?\s*["'](https?://[^"'\s]+)["']`)
	gemLockSpecLineRE = regexp.MustCompile(`^ {4}([A-Za-z0-9_.-]+) \(([^)]+)\)\s*$`)
	railsActiveRecord = regexp.MustCompile(`(?m)^\s*require\s+["'](?:rails/all|active_record/railtie)["']`)
	sinatraClassicRE  = regexp.MustCompile(`(?m)^\s*require\s+["']sinatra["']`)
	bundleFrozenRE    = regexp.MustCompile(`(?i)\bBUNDLE_(?:DEPLOYMENT|FROZEN)\s*[=\s]\s*["']?(?:1|true)\b|\bbundle\s+install\b[^\n]*--(?:deployment|frozen)\b|\bbundle\s+config\b[^\n]*\b(?:deployment|frozen)\s+["']?true\b`)
	startsWithDigitRE = regexp.MustCompile(`^\d`)
)

// rubyAssetGems are the gems whose presence means `assets:precompile` has
// work to do.
var rubyAssetGems = []string{
	"sprockets-rails", "propshaft", "sprockets", "jsbundling-rails", "cssbundling-rails", "tailwindcss-rails",
	"dartsass-rails", "shakapacker", "webpacker", "vite_rails", "importmap-rails",
}

// rubyGemPackages are the Debian packages a locked gem needs to compile
// (build) and to load (runtime). The names are the same on bookworm and
// trixie; `libvips` is the virtual package both resolve.
var rubyGemPackages = []struct {
	gem            string
	build, runtime []string
}{
	{"pg", []string{"libpq-dev"}, []string{"libpq5"}},
	{"mysql2", []string{"default-libmysqlclient-dev"}, []string{"libmariadb3"}},
	{"trilogy", []string{"libssl-dev"}, nil},
	{"sqlite3", []string{"libsqlite3-dev"}, []string{"libsqlite3-0"}},
	{"ruby-vips", nil, []string{"libvips"}},
	{"rmagick", []string{"libmagickwand-dev"}, []string{"imagemagick"}},
	{"mini_magick", nil, []string{"imagemagick"}},
	{"curb", []string{"libcurl4-openssl-dev"}, []string{"libcurl4"}},
	{"ffi", []string{"libffi-dev"}, nil},
}

// gemLock is Gemfile.lock read as data.
type gemLock struct {
	present bool
	// specs are the locked gems and their versions, a platform suffix
	// removed ("nokogiri (1.16.7-x86_64-linux)" is 1.16.7).
	specs       map[string]string
	platforms   []string
	bundledWith string
	ruby        string
	// gitGems are the gems a GIT source provides; gitSSH says one of those
	// sources is an SSH URL the build has no key for.
	gitGems []string
	gitSSH  bool
	// gitHosts are the hosts of the GIT sources fetched over HTTPS, whose
	// private repositories Bundler reads a BUNDLE_<HOST> credential for.
	gitHosts []string
}

func parseGemfileLock(content []byte) gemLock {
	lock := gemLock{present: content != nil, specs: map[string]string{}}
	section := ""
	for _, raw := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, " ") {
			section = strings.TrimSpace(raw)
			continue
		}
		trimmed := strings.TrimSpace(raw)
		switch section {
		case "GEM", "GIT", "PATH":
			if section == "GIT" && strings.HasPrefix(trimmed, "remote:") {
				remote := strings.TrimSpace(strings.TrimPrefix(trimmed, "remote:"))
				lock.gitSSH = lock.gitSSH || strings.HasPrefix(remote, "git@") || strings.HasPrefix(remote, "ssh://")
				if strings.HasPrefix(remote, "https://") && !strings.Contains(remote, "@") {
					if host := npmrcHost(remote); host != "" && !slices.Contains(lock.gitHosts, host) && len(lock.gitHosts) < 4 {
						lock.gitHosts = append(lock.gitHosts, host)
					}
				}
			}
			if match := gemLockSpecLineRE.FindStringSubmatch(raw); match != nil {
				name := strings.ToLower(match[1])
				version, _, _ := strings.Cut(match[2], "-")
				if _, known := lock.specs[name]; !known || lock.specs[name] == "" {
					lock.specs[name] = version
				}
				if section == "GIT" && len(lock.gitGems) < 16 {
					lock.gitGems = append(lock.gitGems, name)
				}
			}
		case "PLATFORMS":
			if len(lock.platforms) < 32 && toolchainTextRE.MatchString(trimmed) {
				lock.platforms = append(lock.platforms, trimmed)
			}
		case "RUBY VERSION":
			if version, found := strings.CutPrefix(trimmed, "ruby "); found {
				lock.ruby, _, _ = strings.Cut(version, "p")
			}
		case "BUNDLED WITH":
			lock.bundledWith = trimmed
		}
	}
	return lock
}

// rubyLockPlatform says whether a lock can install on arch as it is: it
// resolves for every platform (`ruby`) or for that architecture's Linux.
// arch is Go's name for it; the platform to add is returned when it cannot.
func rubyLockPlatform(platforms []string, arch string) (string, bool) {
	cpu := map[string]string{"amd64": "x86_64", "x64": "x86_64", "arm64": "aarch64"}[arch]
	if cpu == "" || len(platforms) == 0 {
		return "", true
	}
	for _, platform := range platforms {
		if platform == "ruby" || platform == cpu+"-linux" || platform == cpu+"-linux-gnu" {
			return "", true
		}
	}
	return cpu + "-linux", false
}

// rubyVersionChoice is the image a Ruby project builds on: an exact release
// when the project pins one, else a catalogue family.
type rubyVersionChoice struct {
	family, exact, source string
}

func (c rubyVersionChoice) image() string {
	if c.exact != "" {
		return "ruby:" + c.exact + "-slim"
	}
	return "ruby:" + c.family + "-slim"
}

func (c rubyVersionChoice) release() string { return firstNonEmpty(c.exact, c.family) }

// gemfileRubyDirective reads the Gemfile's `ruby` line: the requirement it
// states, whether it defers to .ruby-version (`file:` or File.read), and an
// engine other than CRuby it names.
func gemfileRubyDirective(gemfile []byte) (constraint string, fromFile bool, engine string) {
	match := gemfileRubyRE.FindSubmatch(gemfile)
	if match == nil {
		return "", false, ""
	}
	line := string(match[1])
	if strings.Contains(line, "file:") || strings.Contains(line, "File.read") {
		return "", true, ""
	}
	requirement, options, _ := strings.Cut(line, "engine:")
	if quoted := gemfileQuotedRE.FindStringSubmatch(options); quoted != nil && quoted[1] != "ruby" {
		return "", false, quoted[1]
	}
	parts := []string{}
	for _, quoted := range gemfileQuotedRE.FindAllStringSubmatch(requirement, 4) {
		parts = append(parts, quoted[1])
	}
	return strings.Join(parts, ", "), false, ""
}

// chooseRubyRecipeVersion reads the release a project declares: .ruby-version
// (a leading `ruby-` dropped), then .tool-versions, then the Gemfile's `ruby`
// requirement, then the lock's RUBY VERSION as a family. An exact release is
// built on its own image; anything else on the newest catalogue family the
// Gemfile allows, the reviewed default first.
func chooseRubyRecipeVersion(versionFile, toolVersions, gemfile []byte, lock gemLock) (rubyVersionChoice, error) {
	requirement, _, engine := gemfileRubyDirective(gemfile)
	if engine != "" {
		return rubyVersionChoice{}, fmt.Errorf("%w: the Gemfile asks for %s; the Ruby recipe builds CRuby 3.3, 3.4 or 4.0 — use a Dockerfile", ErrUnsupportedBuilder, engine)
	}
	constraint, ok := parseVersionConstraint(requirement)
	if !ok {
		constraint = nil
	}
	declared, source := "", ""
	if line := firstMeaningfulLine(string(versionFile)); line != "" {
		declared, source = strings.TrimPrefix(line, "ruby-"), ".ruby-version"
	} else if entry := toolVersionsEntry(toolVersions, "ruby"); entry != "" {
		declared, source = entry, ".tool-versions"
	}
	if declared == "" && requirement != "" && len(constraint) == 1 && len(constraint[0]) == 1 &&
		(constraint[0][0].operator == "" || constraint[0][0].operator == "=") {
		declared, source = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(requirement, "=")), "ruby-"), "Gemfile"
	}
	outside := func(release string) error {
		return fmt.Errorf("%w: the Ruby recipe builds on Ruby %s; %s names %s — use a Dockerfile for other releases",
			ErrUnsupportedBuilder, strings.Join(rubyRecipeFamilies, ", "), source, release)
	}
	if declared != "" {
		version, parts, ok := parseLanguageVersion(declared)
		if !ok || !startsWithDigitRE.MatchString(declared) {
			return rubyVersionChoice{}, outside(boundedText(declared, 32))
		}
		family := fmt.Sprintf("%d.%d", version[0], version[1])
		if !slices.Contains(rubyRecipeFamilies, family) {
			return rubyVersionChoice{}, outside(boundedText(declared, 32))
		}
		choice := rubyVersionChoice{family: family, source: source}
		if parts == 3 {
			choice.exact = version.String()
		}
		if len(constraint) > 0 && ((choice.exact != "" && !constraint.allows(version)) || (choice.exact == "" && !constraint.allowsFamily(family))) {
			return rubyVersionChoice{}, fmt.Errorf("%w: %s names Ruby %s, but the Gemfile requires ruby %s; make them agree",
				ErrUnsupportedBuilder, source, choice.release(), boundedText(requirement, 64))
		}
		return choice, nil
	}
	if len(constraint) > 0 {
		if constraint.allowsFamily(rubyDefaultFamily) {
			return rubyVersionChoice{family: rubyDefaultFamily, source: "Gemfile"}, nil
		}
		for index := len(rubyRecipeFamilies) - 1; index >= 0; index-- {
			if constraint.allowsFamily(rubyRecipeFamilies[index]) {
				return rubyVersionChoice{family: rubyRecipeFamilies[index], source: "Gemfile"}, nil
			}
		}
		source = "the Gemfile"
		return rubyVersionChoice{}, outside("ruby " + boundedText(requirement, 64))
	}
	if version, _, ok := parseLanguageVersion(lock.ruby); ok {
		if family := fmt.Sprintf("%d.%d", version[0], version[1]); slices.Contains(rubyRecipeFamilies, family) {
			return rubyVersionChoice{family: family, source: "Gemfile.lock"}, nil
		}
	}
	return rubyVersionChoice{family: rubyDefaultFamily}, nil
}

// rubyProject is what the recipe and detection read from a Ruby root.
type rubyProject struct {
	gemfile    []byte
	lock       gemLock
	gems       map[string]string
	version    rubyVersionChoice
	versionErr error
	framework  string
	// railsVersion is the locked railties release.
	railsVersion                                         string
	configRU, binRails, rakefile, database, activeRecord bool
	packageJSON                                          bool
	procfileWeb                                          string
	classicEntry                                         string
	hanamiMigrations                                     bool
	// sources are the Gemfile's gem servers other than rubygems.org, each
	// read by Bundler with the credential named after its host.
	sources []string
}

func readRubyProject(root string) rubyProject {
	project := rubyProject{
		gemfile: readRecipeFile(root, "Gemfile", 256<<10),
		lock:    parseGemfileLock(readRecipeFile(root, "Gemfile.lock", 2<<20)),
	}
	project.gems = map[string]string{}
	for name, version := range project.lock.specs {
		project.gems[name] = version
	}
	for _, match := range gemfileGemRE.FindAllSubmatch(project.gemfile, -1) {
		if _, known := project.gems[strings.ToLower(string(match[1]))]; !known {
			project.gems[strings.ToLower(string(match[1]))] = ""
		}
	}
	project.version, project.versionErr = chooseRubyRecipeVersion(readRecipeFile(root, ".ruby-version", 4096),
		readRecipeFile(root, ".tool-versions", 16<<10), project.gemfile, project.lock)
	project.configRU = regularExists(root, "config.ru")
	project.binRails = regularExists(root, "bin/rails")
	project.rakefile = regularExists(root, "Rakefile")
	project.database = regularExists(root, "config/database.yml")
	project.packageJSON = regularExists(root, "package.json")
	project.activeRecord = railsActiveRecord.Match(readRecipeFile(root, "config/application.rb", 64<<10))
	project.railsVersion = firstNonEmpty(project.gems["railties"], project.gems["rails"])
	switch {
	case has(project.gems, "rails", "railties"):
		project.framework = "rails"
	case has(project.gems, "hanami"):
		project.framework = "hanami"
		project.hanamiMigrations = has(project.gems, "hanami-db") && len(listContainedFiles(filepath.Join(root, "config", "db", "migrate"), ".rb")) > 0
	case has(project.gems, "sinatra"):
		project.framework = "sinatra"
	case project.configRU || has(project.gems, "roda", "grape", "rack"):
		project.framework = "ruby"
	}
	if project.framework == "sinatra" && !project.configRU {
		for _, name := range []string{"app.rb", "server.rb", "main.rb", "web.rb", "application.rb"} {
			if sinatraClassicRE.Match(readRecipeFile(root, name, 256<<10)) {
				project.classicEntry = name
				break
			}
		}
	}
	if procfile := readRecipeFile(root, "Procfile", 64<<10); procfile != nil {
		if web := procfileProcess(procfile, "web"); web != "" && rejectPlanSecretLiteral("Procfile web process", web) == nil {
			project.procfileWeb = web
		}
	}
	for _, match := range gemfileSourceRE.FindAllSubmatch(project.gemfile, 8) {
		host := npmrcHost(string(match[1]))
		if host != "" && host != "rubygems.org" && !slices.Contains(project.sources, host) && !strings.Contains(string(match[1]), "@") {
			project.sources = append(project.sources, host)
		}
	}
	return project
}

// rubyServerPort is the port each framework conventionally listens on.
var rubyServerPort = map[string]int{"rails": 3000, "hanami": 2300, "sinatra": 4567, "ruby": 9292}

// start is the command that serves the application and the decision left
// when there is none: a Procfile's web process, Rails' own server behind
// db:prepare, or the Rack server the lock installs.
func (p rubyProject) start() (string, string) {
	if p.procfileWeb != "" {
		return p.procfileWeb, ""
	}
	port := strconv.Itoa(rubyServerPort[p.framework])
	switch p.framework {
	case "rails":
		if !p.binRails {
			return "", "commit bin/rails (bin/rails app:update:bin writes it), or set the start command"
		}
		server := "bundle exec rails server --binding 0.0.0.0 --port ${PORT:-" + port + "}"
		if p.activeRecord && p.database && has(p.gems, "activerecord", "rails") {
			// db:prepare creates the database and applies its migrations or
			// schema, which a new SQLite volume and a new server alike need.
			return "bundle exec rails db:prepare && exec " + server, ""
		}
		return "exec " + server, ""
	case "hanami":
		server := p.rackServer(port)
		if server == "" {
			return "", "no Rack server (puma) is locked; add puma to the Gemfile or set the start command"
		}
		if p.hanamiMigrations {
			return "bundle exec hanami db migrate && exec " + server, ""
		}
		return "exec " + server, ""
	}
	if !p.configRU && p.classicEntry != "" {
		// Sinatra's own server runs through rackup's handlers, which moved
		// out of Rack 3, on whichever server the bundle has.
		if (has(p.gems, "rackup") || rackBundlesRackup(p.gems["rack"])) && has(p.gems, "puma", "thin", "falcon", "webrick") {
			return "exec bundle exec ruby " + p.classicEntry + " -o 0.0.0.0 -p ${PORT:-" + port + "}", ""
		}
		return "", "Sinatra's built-in server needs rackup and puma; add them to the Gemfile or set the start command"
	}
	if !p.configRU {
		return "", "no config.ru; set the command that starts the server"
	}
	if server := p.rackServer(port); server != "" {
		return "exec " + server, ""
	}
	return "", "no Rack server (puma, rackup, unicorn or thin) is locked; add puma to the Gemfile or set the start command"
}

// rackServer is the locked server's command for config.ru on every interface.
func (p rubyProject) rackServer(port string) string {
	switch {
	case has(p.gems, "puma"):
		return "bundle exec puma --port ${PORT:-" + port + "}"
	case has(p.gems, "unicorn"):
		return "bundle exec unicorn --port ${PORT:-" + port + "}"
	case has(p.gems, "thin"):
		return "bundle exec thin start --address 0.0.0.0 --port ${PORT:-" + port + "}"
	case has(p.gems, "rackup") || rackBundlesRackup(p.gems["rack"]):
		return "bundle exec rackup --host 0.0.0.0 --port ${PORT:-" + port + "}"
	}
	return ""
}

// rackBundlesRackup says the locked rack still ships the rackup command,
// which moved to its own gem in Rack 3.
func rackBundlesRackup(version string) bool {
	parsed, _, ok := parseLanguageVersion(version)
	return ok && parsed[0] < 3
}

// precompile is the asset step the build runs, if any. Rails 7.1 and later
// boot for it with SECRET_KEY_BASE_DUMMY, which is what the Dockerfile
// rails new writes does; an older Rails is given a throwaway key made
// inside the step, never a value from the plan.
func (p rubyProject) precompile() string {
	switch p.framework {
	case "rails":
		if !p.rakefile || !has(p.gems, rubyAssetGems...) {
			return ""
		}
		if versionAtLeast(p.railsVersion, 7, 1) {
			return "SECRET_KEY_BASE_DUMMY=1 bundle exec rails assets:precompile"
		}
		return `SECRET_KEY_BASE="$(ruby -rsecurerandom -e 'print SecureRandom.hex(64)')" bundle exec rails assets:precompile`
	case "hanami":
		if p.packageJSON && has(p.gems, "hanami-assets") {
			return "bundle exec hanami assets compile"
		}
	}
	return ""
}

// rubyAssetStageTools are what the Ruby build stage has beside the Node it
// copies in: a package script that runs rails or bundle finds them there.
var rubyAssetStageTools = []string{"ruby", "bundle", "rails"}

// needsNode says the build stage needs Node: the application's package.json
// is its asset pipeline, or Sprockets compresses through ExecJS.
func (p rubyProject) needsNode() (install, runtimeOnly bool) {
	if (p.framework == "rails" || p.framework == "hanami") && p.packageJSON {
		return true, false
	}
	return false, p.framework == "rails" && has(p.gems, "execjs") && !has(p.gems, "mini_racer", "therubyracer")
}

// packages are the Debian packages the build and runtime stages install.
func (p rubyProject) packages(nodeNative bool) ([]string, []string) {
	build := map[string]bool{"build-essential": true, "pkg-config": true, "libyaml-dev": true}
	runtime := map[string]bool{}
	if len(p.lock.gitGems) > 0 || !p.lock.present {
		build["git"] = true
	}
	if nodeNative {
		build["python3"] = true
	}
	if p.framework == "rails" {
		// Rails' own Dockerfile runs its servers on jemalloc, which keeps a
		// long-running Ruby process's memory from fragmenting.
		runtime["libjemalloc2"] = true
	}
	for _, entry := range rubyGemPackages {
		if _, ok := p.gems[entry.gem]; !ok {
			continue
		}
		if entry.gem == "mini_magick" && has(p.gems, "ruby-vips") {
			// image_processing locks both; Rails 7 and later process
			// through vips.
			continue
		}
		for _, name := range entry.build {
			build[name] = true
		}
		for _, name := range entry.runtime {
			runtime[name] = true
		}
	}
	return sortedNames(build), sortedNames(runtime)
}

// sourceVariables are the credentials Bundler reads for the Gemfile's gem
// servers — BUNDLE_GEMS__CONTRIBSYS__COM for gems.contribsys.com, which the
// install needs — and for the hosts of its Git gems, which only a private
// repository needs.
func (p rubyProject) sourceVariables(root string) []DetectedVariable {
	variables := []DetectedVariable{}
	add := func(host, source string, required bool) {
		name := "BUNDLE_" + strings.ToUpper(strings.NewReplacer("-", "___", ".", "__").Replace(host))
		if ValidateEnvKey(name) == nil && !slices.ContainsFunc(variables, func(variable DetectedVariable) bool { return variable.Name == name }) {
			variables = append(variables, DetectedVariable{Name: name, Sources: []string{source}, Step: "install", InstallRequired: required})
		}
	}
	for _, host := range p.sources {
		add(host, joinRoot(root, "Gemfile"), true)
	}
	for _, host := range p.lock.gitHosts {
		add(host, joinRoot(root, "Gemfile.lock"), false)
	}
	return variables
}

// rubyEnvironment is what the image sets for each framework: its
// environment, and for Rails the logging and static files Rails 7.0 and
// earlier only enable when asked.
func rubyEnvironment(framework string) []string {
	switch framework {
	case "rails":
		return []string{"RAILS_ENV=production", "RACK_ENV=production", "RAILS_LOG_TO_STDOUT=1", "RAILS_SERVE_STATIC_FILES=1"}
	case "hanami":
		return []string{"HANAMI_ENV=production", "RACK_ENV=production"}
	}
	return []string{"RACK_ENV=production", "APP_ENV=production"}
}

// rubyCandidate is the recipe candidate for a Ruby root.
func rubyCandidate(buildRoot, root string, match *ecosystemMatch) DetectedCandidate {
	project := readRubyProject(buildRoot)
	label := match.label
	candidate := DetectedCandidate{
		Name: label + " in " + rootLabelOf(root), Profile: ProfileWeb, Confidence: ConfidenceHigh,
		Framework: project.framework, Recipe: "ruby", Port: rubyServerPort[project.framework],
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
		Toolchain: &DetectedToolchain{Language: "ruby", Release: project.version.release(), From: project.version.source,
			LockPlatforms: project.lock.platforms, Bundler: project.lock.bundledWith, GitSSH: project.lock.gitSSH},
	}
	if project.framework == "" {
		candidate.Framework = match.framework
	}
	if project.versionErr != nil {
		candidate.RecipeIssue = recipeRefusalText(project.versionErr, "")
	} else {
		reason := "builds on Ruby " + project.version.release()
		if project.version.source != "" {
			reason += " (" + project.version.source + ")"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, firstNonEmpty(project.version.source, "Gemfile")), Reason: reason})
	}
	if !project.lock.present {
		candidate.UnpinnedDependencies = true
	} else if project.lock.bundledWith != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "Gemfile.lock"),
			Reason: "installed frozen from the lock; RubyGems runs the Bundler it was written with (" + boundedEvidence(project.lock.bundledWith) + ")"})
	}
	start, decision := project.start()
	candidate.StartCommand = start
	if project.procfileWeb != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "Procfile"), Reason: "web process: " + boundedEvidence(project.procfileWeb)})
	}
	if decision != "" {
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, decision)
	}
	if step := project.precompile(); step != "" {
		// The command itself reads like an assignment of a secret, which
		// evidence never quotes.
		reason := "the build precompiles the asset pipeline (rails assets:precompile)"
		if project.framework == "hanami" {
			reason = "the build compiles the assets (hanami assets compile)"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "Gemfile.lock"), Reason: reason})
	}
	if install, _ := project.needsNode(); install {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "package.json"),
			Reason: "the asset pipeline's JavaScript dependencies are installed in the Ruby build"})
		if assets, err := planLanguageNodeAssets(buildRoot, "", nodeTargetArch(""), nodeInstallChoice{provided: rubyAssetStageTools}); err == nil {
			facts := assets.source.facts
			candidate.PackageManagers, candidate.Lockfiles = facts.lockfileManagers(), facts.detectedLockfiles()
			candidate.NodeVersion = assets.plan.node.label()
			candidate.PackageManager = assets.plan.manager
			candidate.NodeInstalls = facts.detectedInstalls(assets.plan.manager, true, rubyAssetStageTools, func(runner string) (string, string) { return runner + " run build", "" })
			candidate.Variables = facts.registry
		} else if !strings.Contains(err.Error(), "requires package.json") {
			candidate.NeedsDecision = append(candidate.NeedsDecision, boundedText("choose the package manager: "+recipeRefusalText(err, buildRoot), 512))
			candidate.Confidence = ConfidenceLow
		}
	}
	candidate.Variables = withInstallVariables(candidate.Variables, project.sourceVariables(root))
	if candidate.RecipeIssue != "" {
		candidate.Confidence = ConfidenceLow
	}
	return candidate
}

// rubyRecipe is what the builder needs beyond the plan.
type rubyRecipe struct {
	version   rubyVersionChoice
	framework string
	locked    bool
	// addPlatform is the Linux platform the lock lacks for this build,
	// added before the frozen install.
	addPlatform             string
	precompile              string
	buildPackages, packages []string
	node                    *languageNodeAssets
}

func selectRubyRecipe(root string, config BuildPlanConfig) (rubyRecipe, error) {
	if !regularExists(root, "Gemfile") {
		return rubyRecipe{}, fmt.Errorf("%w: Ruby recipe requires a Gemfile", ErrUnsupportedBuilder)
	}
	project := readRubyProject(root)
	if project.versionErr != nil {
		return rubyRecipe{}, project.versionErr
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		_, decision := project.start()
		return rubyRecipe{}, fmt.Errorf("%w: Ruby recipe requires a start command; %s", ErrUnsupportedBuilder, firstNonEmpty(decision, "detection proposes the Rails, Hanami or Rack server the lock installs"))
	}
	recipe := rubyRecipe{version: project.version, framework: project.framework, locked: project.lock.present, precompile: project.precompile()}
	if recipe.locked {
		arch := nodeTargetArch(config.TargetPlatform)
		if platform, ok := rubyLockPlatform(project.lock.platforms, arch); !ok {
			recipe.addPlatform = platform
		}
	}
	install, runtimeOnly := project.needsNode()
	switch {
	case install:
		assets, err := planLanguageNodeAssets(root, "", nodeTargetArch(config.TargetPlatform),
			nodeInstallChoice{selected: config.PackageManager, nodeVersion: config.NodeVersion, provided: rubyAssetStageTools})
		if err != nil {
			return rubyRecipe{}, fmt.Errorf("%w: the Ruby recipe installs package.json's assets with Node: %s", ErrUnsupportedBuilder, strings.TrimPrefix(err.Error(), ErrUnsupportedBuilder.Error()+": "))
		}
		recipe.node = assets
	case runtimeOnly:
		recipe.node = languageNodeRuntime()
	}
	recipe.buildPackages, recipe.packages = project.packages(recipe.node != nil && len(recipe.node.plan.image.buildPackages) > 0)
	return recipe, nil
}

// rubyRecipeBases lists the images in the order the Dockerfile names them:
// Ruby, then Node's and Bun's when the build stage borrows them.
func rubyRecipeBases(recipe rubyRecipe) []string {
	bases := []string{recipe.version.image()}
	if recipe.node != nil {
		bases = append(bases, recipe.node.bases()...)
	}
	return bases
}

// lendRubyDockerfileFacts gives a repository Dockerfile beside a Ruby
// application the lock's platforms and whether the Dockerfile installs the
// bundle frozen, which preflight reads against the server's architecture.
func lendRubyDockerfileFacts(candidate *DetectedCandidate, buildRoot string) {
	lock := parseGemfileLock(readRecipeFile(buildRoot, "Gemfile.lock", 2<<20))
	if !lock.present || candidate.Toolchain != nil {
		return
	}
	dockerfile := readRecipeFile(buildRoot, firstNonEmpty(candidate.Dockerfile, "Dockerfile"), 2<<20)
	candidate.Toolchain = (&DetectedToolchain{Language: "ruby", LockPlatforms: lock.platforms, Bundler: lock.bundledWith,
		BundleFrozen: bundleFrozenRE.Match(dockerfile), AddedPlatforms: dockerfileAddedPlatforms(dockerfile)}).bounded()
}

var bundleLockRE = regexp.MustCompile(`\bbundle\s+lock\b([^\n&;|]*)`)

// dockerfileAddedPlatforms are the platforms a Dockerfile's `bundle lock
// --add-platform` adds, which a frozen install after it then accepts.
func dockerfileAddedPlatforms(dockerfile []byte) []string {
	text := strings.ReplaceAll(string(dockerfile), "\\\n", " ")
	var platforms []string
	for _, match := range bundleLockRE.FindAllStringSubmatch(text, 16) {
		adding := false
		for _, field := range strings.Fields(strings.ReplaceAll(match[1], ",", " ")) {
			switch {
			case field == "--add-platform":
				adding = true
			case strings.HasPrefix(field, "--add-platform="):
				adding = true
				platforms = append(platforms, strings.TrimPrefix(field, "--add-platform="))
			case strings.HasPrefix(field, "-"):
				adding = false
			case adding:
				platforms = append(platforms, field)
			}
		}
	}
	return platforms
}
