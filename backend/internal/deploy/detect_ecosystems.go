package deploy

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Ecosystems the builder has no automatic recipe for. Recognising one turns
// "No deployable plan was detected" — or worse, an asset bundle's
// package.json offered as the application — into a candidate that names the
// language and says exactly what to commit. The recipes for several of these
// are planned; an entry here is removed when its recipe lands, and until then
// the candidate's RecipeIssue is what preflight's recipe_unsupported shows.

type ecosystemMatch struct {
	language  string
	framework string
	label     string
	marker    string
	profile   WorkloadProfile
	port      int
	remedy    string
	// owner says the ecosystem is the application at its root even when a
	// supported manifest sits beside it: a Rails app's package.json only
	// builds its assets.
	owner      bool
	ownsAssets bool
	// primary says the root is this ecosystem's application whatever else
	// is there; a secondary one (CMake beside a Node addon) is named only
	// when nothing else at the root is.
	primary   bool
	processes []DetectedProcess
	databases []DetectedDatabase
	evidence  []DetectionEvidence
	// notDeployable sets the root aside instead of offering a candidate.
	notDeployable string
}

var (
	gemfileGemRE    = regexp.MustCompile(`(?m)^\s*gem\s+["']([A-Za-z0-9_.-]+)["']`)
	gemfileLockRE   = regexp.MustCompile(`(?m)^ {4}([A-Za-z0-9_.-]+) \(([^)]+)\)\s*$`)
	mixDependencyRE = regexp.MustCompile(`\{\s*:([a-z0-9_]+)\s*,`)
	versionPrefixRE = regexp.MustCompile(`^([0-9]+)\.([0-9]+)`)
)

// rubyGems reads the gem names a Gemfile and its lock declare, with the
// locked version when there is one.
func rubyGems(gemfile, lock []byte) map[string]string {
	gems := map[string]string{}
	for _, match := range gemfileGemRE.FindAllSubmatch(gemfile, -1) {
		gems[strings.ToLower(string(match[1]))] = ""
	}
	for _, match := range gemfileLockRE.FindAllSubmatch(lock, -1) {
		gems[strings.ToLower(string(match[1]))] = string(match[2])
	}
	return gems
}

func versionAtLeast(version string, major, minor int) bool {
	match := versionPrefixRE.FindStringSubmatch(version)
	if match == nil {
		return false
	}
	gotMajor, _ := strconv.Atoi(match[1])
	gotMinor, _ := strconv.Atoi(match[2])
	return gotMajor > major || (gotMajor == major && gotMinor >= minor)
}

// rubyEcosystem recognises a Ruby application. A Gemfile that only brings
// CocoaPods or fastlane to a React Native repository is tooling, not one.
func rubyEcosystem(root string, s *repoShapeScan) *ecosystemMatch {
	gemfile, hasGemfile := s.file(root, "gemfile")
	lock, hasLock := s.file(root, "gemfile.lock")
	if !hasGemfile && !hasLock {
		return nil
	}
	gems := rubyGems(gemfile, lock)
	marker := joinRoot(root, "Gemfile")
	if !hasGemfile {
		marker = joinRoot(root, "Gemfile.lock")
	}
	match := &ecosystemMatch{language: "Ruby", marker: marker, primary: true, owner: true}
	_, rack := s.file(root, "config.ru")
	switch {
	case has(gems, "rails", "railties"):
		version := gems["rails"]
		if version == "" {
			version = gems["railties"]
		}
		match.framework, match.label, match.profile, match.port = "rails", "Rails application", ProfileWeb, 3000
		match.remedy = "bundle add dockerfile-rails --group development && bin/rails generate dockerfile, then commit the Dockerfile"
		if versionAtLeast(version, 7, 1) {
			match.remedy = "rails new generated a production Dockerfile for this version; commit it (bin/rails generate dockerfile from the dockerfile-rails gem writes one again)"
		}
		match.ownsAssets = true
		if version != "" {
			match.evidence = append(match.evidence, DetectionEvidence{Path: joinRoot(root, "Gemfile.lock"), Reason: "Rails " + version})
		}
	case has(gems, "hanami"):
		match.framework, match.label, match.profile, match.port = "hanami", "Hanami application", ProfileWeb, 2300
		match.remedy = "commit a Dockerfile that runs bundle install and bundle exec hanami server"
		match.ownsAssets = true
	case has(gems, "middleman"):
		match.framework, match.label, match.profile, match.port = "middleman", "Middleman site", ProfileStatic, 80
		match.remedy = "bundle exec middleman build writes build/; commit a Dockerfile that builds it and serves build/ with nginx"
	case has(gems, "sinatra"):
		match.framework, match.label, match.profile, match.port = "sinatra", "Sinatra application", ProfileWeb, 4567
		match.remedy = "commit a Dockerfile that runs bundle install and bundle exec rackup --host 0.0.0.0"
	case rack || has(gems, "roda", "grape", "rack", "puma"):
		match.framework, match.label, match.profile, match.port = "ruby", "Rack application", ProfileWeb, 9292
		match.remedy = "commit a Dockerfile that runs bundle install and bundle exec rackup --host 0.0.0.0"
	default:
		return nil
	}
	if has(gems, "jsbundling-rails", "cssbundling-rails", "vite_rails", "shakapacker", "webpacker", "hanami-assets") {
		match.ownsAssets = true
	}
	source := joinRoot(root, "Gemfile")
	switch {
	case has(gems, "sidekiq"):
		match.processes = append(match.processes, DetectedProcess{Name: "sidekiq", Kind: "worker", Command: "bundle exec sidekiq", Source: source, Reason: "sidekiq is in the Gemfile"})
	case has(gems, "solid_queue"):
		match.processes = append(match.processes, DetectedProcess{Name: "jobs", Kind: "worker", Command: "bin/jobs", Source: source,
			Reason: "solid_queue is in the Gemfile; run bin/jobs, or set SOLID_QUEUE_IN_PUMA=true to run jobs inside the web server"})
	case has(gems, "good_job"):
		match.processes = append(match.processes, DetectedProcess{Name: "good_job", Kind: "worker", Command: "bundle exec good_job start", Source: source, Reason: "good_job is in the Gemfile"})
	case has(gems, "resque"):
		match.processes = append(match.processes, DetectedProcess{Name: "resque", Kind: "worker", Command: "QUEUE=* bundle exec rake resque:work", Source: source, Reason: "resque is in the Gemfile"})
	case has(gems, "delayed_job", "delayed_job_active_record"):
		match.processes = append(match.processes, DetectedProcess{Name: "delayed_job", Kind: "worker", Command: "bundle exec rake jobs:work", Source: source, Reason: "delayed_job is in the Gemfile"})
	}
	for _, driver := range []struct{ gem, engine string }{{"pg", "postgres"}, {"mysql2", "mysql"}, {"trilogy", "mysql"}, {"redis", "redis"}, {"mongoid", "mongodb"}} {
		if has(gems, driver.gem) {
			match.databases = append(match.databases, DetectedDatabase{Engine: driver.engine, Variable: databaseVariableNames[driver.engine], Evidence: driver.gem + " in " + source})
		}
	}
	return match
}

func has(set map[string]string, names ...string) bool {
	for _, name := range names {
		if _, ok := set[name]; ok {
			return true
		}
	}
	return false
}

func elixirEcosystem(root string, s *repoShapeScan) *ecosystemMatch {
	content, ok := s.file(root, "mix.exs")
	if !ok {
		return nil
	}
	deps := map[string]string{}
	for _, match := range mixDependencyRE.FindAllSubmatch(content, -1) {
		deps[string(match[1])] = ""
	}
	match := &ecosystemMatch{language: "Elixir", marker: joinRoot(root, "mix.exs"), primary: true, owner: true,
		framework: "elixir", label: "Elixir project", profile: ProfileService,
		remedy: "commit a Dockerfile that builds a mix release"}
	switch {
	case has(deps, "phoenix"):
		match.framework, match.label, match.profile, match.port = "phoenix", "Phoenix application", ProfileWeb, 4000
		match.remedy = "mix phx.gen.release --docker writes a Dockerfile and release scripts; commit them"
		match.ownsAssets = true
	case has(deps, "plug_cowboy", "bandit"):
		match.label, match.profile, match.port = "Elixir web application", ProfileWeb, 4000
	}
	for _, driver := range []struct{ dep, engine string }{{"postgrex", "postgres"}, {"myxql", "mysql"}, {"redix", "redis"}, {"mongodb_driver", "mongodb"}} {
		if has(deps, driver.dep) {
			match.databases = append(match.databases, DetectedDatabase{Engine: driver.engine, Variable: databaseVariableNames[driver.engine], Evidence: driver.dep + " in " + joinRoot(root, "mix.exs")})
		}
	}
	return match
}

// longTailEcosystems are named by one manifest each; the frameworks listed
// make the candidate a web service on the framework's conventional port.
var longTailEcosystems = []struct {
	key, file, language, slug, remedy string
	frameworks                        []struct {
		marker, name string
		port         int
	}
	profile WorkloadProfile
}{
	{key: "shard.yml", file: "shard.yml", language: "Crystal", slug: "crystal", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with crystallang/crystal (shards build --release) and runs the binary",
		frameworks: frameworksOf("kemal", "kemal", 3000, "lucky", "lucky", 3000, "amber", "amber", 3000)},
	{key: "stack.yaml", file: "stack.yaml", language: "Haskell", slug: "haskell", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with the haskell image (stack build or cabal build) and copies the executable into a slim runtime",
		frameworks: frameworksOf("servant", "servant", 8080, "scotty", "scotty", 3000, "yesod", "yesod", 3000, "warp", "warp", 8080)},
	{key: ".cabal", file: "*.cabal", language: "Haskell", slug: "haskell", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with the haskell image (cabal build) and copies the executable into a slim runtime",
		frameworks: frameworksOf("servant", "servant", 8080, "scotty", "scotty", 3000, "yesod", "yesod", 3000, "warp", "warp", 8080)},
	{key: "build.zig", file: "build.zig", language: "Zig", slug: "zig", profile: ProfileService,
		remedy: "commit a Dockerfile that runs zig build -Doptimize=ReleaseSafe and copies zig-out/bin into a slim runtime"},
	{key: "package.swift", file: "Package.swift", language: "Swift", slug: "swift", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with the swift image (swift build -c release) and runs the product",
		frameworks: frameworksOf("vapor", "vapor", 8080, "hummingbird", "hummingbird", 8080)},
	{key: "build.sbt", file: "build.sbt", language: "Scala", slug: "scala", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with sbt (sbt stage or sbt-native-packager) and runs on a JRE",
		frameworks: frameworksOf("playframework", "play", 9000, "http4s", "http4s", 8080, "akka-http", "akka-http", 8080, "zio-http", "zio-http", 8080)},
	{key: "project.clj", file: "project.clj", language: "Clojure", slug: "clojure", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds an uberjar (lein uberjar) and runs it on a JRE",
		frameworks: frameworksOf("ring", "ring", 3000, "compojure", "compojure", 3000, "pedestal", "pedestal", 8080)},
	{key: "deps.edn", file: "deps.edn", language: "Clojure", slug: "clojure", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds an uberjar (clojure -T:build uber) and runs it on a JRE",
		frameworks: frameworksOf("ring", "ring", 3000, "pedestal", "pedestal", 8080, "http-kit", "http-kit", 8080)},
	{key: "gleam.toml", file: "gleam.toml", language: "Gleam", slug: "gleam", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with gleam export erlang-shipment and runs it on an Erlang image",
		frameworks: frameworksOf("wisp", "wisp", 8000, "mist", "mist", 8000)},
	{key: ".fsproj", file: "*.fsproj", language: "F#", slug: "fsharp", profile: ProfileService,
		remedy:     "commit a Dockerfile that publishes with mcr.microsoft.com/dotnet/sdk and runs on the aspnet image",
		frameworks: frameworksOf("giraffe", "giraffe", 8080, "saturn", "saturn", 8080, "falco", "falco", 8080, "Microsoft.NET.Sdk.Web", "aspnet", 8080)},
	{key: "dune-project", file: "dune-project", language: "OCaml", slug: "ocaml", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with the ocaml/opam image (dune build) and copies the executable into a slim runtime",
		frameworks: frameworksOf("dream", "dream", 8080)},
	{key: ".nimble", file: "*.nimble", language: "Nim", slug: "nim", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds with the nimlang/nim image (nimble build -d:release) and runs the binary",
		frameworks: frameworksOf("jester", "jester", 5000, "prologue", "prologue", 8080)},
	{key: "cpanfile", file: "cpanfile", language: "Perl", slug: "perl", profile: ProfileService,
		remedy:     "commit a Dockerfile on the perl image that installs the cpanfile (cpanm --installdeps .) and starts the server",
		frameworks: frameworksOf("Mojolicious", "mojolicious", 3000, "Dancer2", "dancer2", 5000, "Plack", "plack", 5000)},
	{key: "rebar.config", file: "rebar.config", language: "Erlang", slug: "erlang", profile: ProfileService,
		remedy:     "commit a Dockerfile that builds a release with rebar3 and runs it on an Erlang image",
		frameworks: frameworksOf("cowboy", "cowboy", 8080)},
	{key: "cmakelists.txt", file: "CMakeLists.txt", language: "C/C++", slug: "cpp", profile: ProfileService,
		remedy: "commit a Dockerfile that builds with CMake and copies the program into a slim runtime"},
	{key: "meson.build", file: "meson.build", language: "C/C++", slug: "cpp", profile: ProfileService,
		remedy: "commit a Dockerfile that builds with Meson and copies the program into a slim runtime"},
	{key: "elm.json", file: "elm.json", language: "Elm", slug: "elm", profile: ProfileStatic,
		remedy: "elm make compiles the site to JavaScript; commit a Dockerfile that builds it and serves the output with nginx"},
}

func frameworksOf(values ...any) []struct {
	marker, name string
	port         int
} {
	var result []struct {
		marker, name string
		port         int
	}
	for index := 0; index+2 < len(values); index += 3 {
		result = append(result, struct {
			marker, name string
			port         int
		}{values[index].(string), values[index+1].(string), values[index+2].(int)})
	}
	return result
}

// otherEcosystem recognises the remaining languages, R and Dart, which need
// more than one file to say what they are.
func otherEcosystem(root string, s *repoShapeScan) *ecosystemMatch {
	files := s.roots[root]
	if files == nil {
		return nil
	}
	if content, ok := files.files["pubspec.yaml"]; ok {
		text := string(content)
		if strings.Contains(text, "flutter:") || strings.Contains(text, "sdk: flutter") {
			// Only a walk that saw the whole root can say web/ is missing;
			// otherwise the web target is given the benefit of the doubt.
			if s.files[joinRoot(root, "web/index.html")] || !s.absenceKnown(root) {
				return &ecosystemMatch{language: "Flutter", framework: "flutter", label: "Flutter web app", marker: joinRoot(root, "pubspec.yaml"),
					profile: ProfileStatic, port: 80, primary: true,
					remedy: "flutter build web writes build/web; commit a Dockerfile that builds it and serves build/web with nginx"}
			}
			return &ecosystemMatch{language: "Flutter", marker: joinRoot(root, "pubspec.yaml"), notDeployable: "mobile-app"}
		}
		match := &ecosystemMatch{language: "Dart", framework: "dart", label: "Dart project", marker: joinRoot(root, "pubspec.yaml"),
			profile: ProfileService, primary: true, remedy: "commit a Dockerfile that runs dart compile exe and copies the executable into a slim runtime"}
		for _, server := range []string{"dart_frog", "shelf", "serverpod"} {
			if strings.Contains(text, server+":") {
				match.framework, match.label, match.profile, match.port = server, "Dart web server", ProfileWeb, 8080
				break
			}
		}
		return match
	}
	_, description := files.files["description"]
	_, renv := files.files["renv.lock"]
	_, appR := files.files["app.r"]
	_, uiR := files.files["ui.r"]
	_, serverR := files.files["server.r"]
	_, plumberR := files.files["plumber.r"]
	if (description || renv || appR || uiR) && (appR || (uiR && serverR)) {
		return &ecosystemMatch{language: "R", framework: "shiny", label: "R Shiny application", marker: joinRoot(root, "app.R"),
			profile: ProfileWeb, port: 3838, primary: true,
			remedy: "commit a Dockerfile on rocker/shiny that installs the packages (renv::restore()) and copies the app into /srv/shiny-server"}
	}
	if plumberR {
		return &ecosystemMatch{language: "R", framework: "plumber", label: "R Plumber API", marker: joinRoot(root, "plumber.R"),
			profile: ProfileWeb, port: 8000, primary: true,
			remedy: "commit a Dockerfile on rstudio/plumber that installs the packages and runs plumber.R"}
	}
	for _, entry := range longTailEcosystems {
		content, ok := files.files[entry.key]
		if !ok {
			continue
		}
		match := &ecosystemMatch{language: entry.language, framework: entry.slug, label: entry.language + " project",
			marker: joinRoot(root, entry.file), profile: entry.profile, remedy: entry.remedy,
			primary: entry.slug != "cpp" && entry.slug != "mkdocs"}
		if entry.profile == ProfileStatic {
			match.label, match.port = entry.language+" site", 80
		}
		// A framework is named in whichever manifest lists dependencies —
		// package.yaml beside stack.yaml, the .cabal file — so every file
		// recorded at the root is searched.
		all := []string{string(content)}
		for key, other := range files.files {
			if key != "readme.md" {
				all = append(all, string(other))
			}
		}
		lower := strings.ToLower(strings.Join(all, "\n"))
		for _, framework := range entry.frameworks {
			if strings.Contains(lower, strings.ToLower(framework.marker)) {
				match.framework, match.label, match.profile, match.port = framework.name, entry.language+" web application", ProfileWeb, framework.port
				break
			}
		}
		return match
	}
	return nil
}

// hasDirectory says whether the walk saw any file under root/name.
func (s *repoShapeScan) hasDirectory(root, name string) bool {
	prefix := joinRoot(root, name) + "/"
	for file := range s.files {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

func detectEcosystem(root string, s *repoShapeScan) *ecosystemMatch {
	if match := rubyEcosystem(root, s); match != nil {
		return match
	}
	if match := elixirEcosystem(root, s); match != nil {
		return match
	}
	return otherEcosystem(root, s)
}

// applyEcosystems names the ecosystems without a recipe, takes their asset
// pipelines out of the Node candidates, and lends a Dockerfile at the same
// root the framework it builds.
func (s *repoShapeScan) applyEcosystems(result *DetectionResult, context shapeContext) {
	roots := make([]string, 0, len(s.roots))
	for root := range s.roots {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		match := detectEcosystem(root, s)
		if match == nil {
			continue
		}
		if match.notDeployable != "" {
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(root), Kind: match.notDeployable,
				Reason: match.language + " app (" + match.marker + "): " + notDeployableKinds[match.notDeployable] + ", not a service this server can run"})
			removeCandidates(result, func(candidate DetectedCandidate) bool {
				return candidate.Root == root || underRoot(candidate.Root, joinRoot(root, "android")) || underRoot(candidate.Root, joinRoot(root, "ios"))
			})
			continue
		}
		if marker := context.markers[root]; marker != nil && len(marker.procfile) > 0 {
			declared := procfileKinds(marker.procfile)
			kept := match.processes[:0]
			for _, process := range match.processes {
				if !declared[process.Kind] {
					kept = append(kept, process)
				}
			}
			match.processes = kept
		}
		// A site generator's package.json at its own root runs its CSS or
		// formatting tooling: the site is the generator's, as a Rails app's
		// asset bundle is Rails'.
		siteTooling := match.profile == ProfileStatic && s.nodeToolingOnly(root, context.markers[root])
		pipeline := func(candidate DetectedCandidate) bool {
			return candidate.Recipe == "node" && (match.ownsAssets || (siteTooling && candidate.Root == root))
		}
		dockerfile := -1
		others := 0
		for index, candidate := range result.Candidates {
			if candidate.Root != root {
				continue
			}
			if candidate.BuildMethod == BuildDockerfile {
				dockerfile = index
			} else if candidate.BuildMethod != BuildStatic && !pipeline(candidate) {
				others++
			}
		}
		if match.ownsAssets || siteTooling {
			removeCandidates(result, func(candidate DetectedCandidate) bool {
				owned := candidate.Recipe == "node" && (candidate.Root == root || (match.ownsAssets && (candidate.Root == joinRoot(root, "assets") ||
					candidate.Root == joinRoot(root, "frontend") || candidate.Root == joinRoot(root, "app/javascript"))))
				if owned {
					s.addSetAside(DetectionSetAside{Path: joinRoot(candidate.Root, "package.json"), Kind: "asset-pipeline",
						Reason: "package.json builds the " + match.label + "'s assets; the application needs a " + match.language + " build"})
				}
				return owned
			})
			dockerfile = -1
			for index, candidate := range result.Candidates {
				if candidate.Root == root && candidate.BuildMethod == BuildDockerfile {
					dockerfile = index
				}
			}
		}
		evidence := append([]DetectionEvidence{{Path: match.marker, Reason: match.language + " project manifest"}}, match.evidence...)
		if dockerfile >= 0 {
			candidate := &result.Candidates[dockerfile]
			if candidate.Framework == "" {
				candidate.Framework = match.framework
			}
			candidate.Evidence = append(candidate.Evidence, evidence...)
			candidate.Processes = appendProcesses(candidate.Processes, match.processes...)
			candidate.Databases = appendDatabases(candidate.Databases, match.databases...)
			continue
		}
		if !match.primary && others > 0 {
			continue
		}
		if !match.primary && s.nestedInCandidate(root, result.Candidates) {
			continue
		}
		if !match.owner && others > 0 {
			continue
		}
		rootLabel := rootLabelOf(root)
		variables := context.variables(root)
		candidate := newDetectedCandidate(root, BuildRecipe, DetectedCandidate{
			Name: match.label + " in " + rootLabel, Profile: match.profile, Confidence: ConfidenceLow,
			Framework: match.framework, Port: match.port,
			RecipeIssue:   boundedText(match.language+" ("+path.Base(match.marker)+") has no automatic recipe; "+match.remedy, 512),
			Evidence:      evidence,
			NeedsDecision: []string{},
			Variables:     variables,
			Processes:     appendProcesses(nil, match.processes...),
			Databases:     appendDatabases(context.databases(root, variables), match.databases...),
		})
		result.Candidates = append(result.Candidates, candidate)
	}
}

// nestedInCandidate says whether root lies inside another candidate's root,
// where a secondary manifest is that application's vendored code.
func (s *repoShapeScan) nestedInCandidate(root string, candidates []DetectedCandidate) bool {
	for _, candidate := range candidates {
		if candidate.Root != root && underRoot(root, candidate.Root) {
			return true
		}
	}
	return false
}

func appendProcesses(existing []DetectedProcess, processes ...DetectedProcess) []DetectedProcess {
	for _, process := range processes {
		if len(existing) >= 16 {
			break
		}
		duplicate := false
		for _, known := range existing {
			if (known.Name == process.Name && known.Kind == process.Kind) || (process.Command != "" && known.Command == process.Command) {
				duplicate = true
				break
			}
		}
		if duplicate || rejectPlanSecretLiteral("detected process", process.Command) != nil {
			continue
		}
		existing = append(existing, process)
	}
	return existing
}

func appendDatabases(existing []DetectedDatabase, databases ...DetectedDatabase) []DetectedDatabase {
	for _, database := range databases {
		if len(existing) >= 8 || !validDetectedDatabaseEngine(database.Engine) {
			continue
		}
		duplicate := false
		for _, known := range existing {
			if known.Engine == database.Engine {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, database)
		}
	}
	return existing
}
