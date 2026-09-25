package deploy

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// causeTitles name every cause code in a few words, for the surfaces that
// have room for no more: a commit status, a notification field, a diagnosis
// finding. The frontend keeps the same vocabulary.
var causeTitles = map[string]string{
	"build_lockfile_out_of_sync":         "Lockfile out of sync",
	"build_lockfile_incompatible":        "Lockfile from another package-manager release",
	"build_package_manager_mismatch":     "Package manager differs from the project's",
	"build_lifecycle_script_blocked":     "Dependency install scripts blocked",
	"build_runtime_version":              "Language version mismatch",
	"build_hugo_extended_required":       "Hugo extended edition required",
	"build_env_missing":                  "Variable missing at build",
	"build_sqlx_offline":                 "sqlx has no offline query data",
	"build_database_unreachable":         "Database unreachable during build",
	"build_prerender_failed":             "Page failed to prerender",
	"build_next_image_export":            "next/image in a static export",
	"build_prisma_client_missing":        "Prisma Client not generated",
	"build_platform_binary_missing":      "Platform binary missing from the lockfile",
	"build_legacy_openssl":               "Build tool needs legacy OpenSSL",
	"build_system_library_missing":       "System library missing",
	"build_native_toolchain_missing":     "Compiler toolchain missing",
	"build_install_script_failed":        "Dependency install script failed",
	"build_php_extension_missing":        "PHP extension missing",
	"build_dependency_conflict":          "Dependency versions conflict",
	"build_dependency_advisory_blocked":  "Dependency blocked by a security advisory",
	"build_dependency_local_path":        "Dependency points at a local path",
	"build_dependency_unavailable":       "Dependency not found",
	"build_registry_auth":                "Registry refused the credentials",
	"build_registry_rate_limited":        "Registry rate limit reached",
	"build_network":                      "Network unreachable during build",
	"build_base_image_missing":           "Base image not found",
	"build_image_not_found":              "Image not found",
	"build_platform_unsupported":         "Built for another architecture",
	"build_script_crlf":                  "Script has Windows line endings",
	"build_permission":                   "Script not executable",
	"build_wrapper_missing":              "Build tool wrapper missing",
	"build_embed_source_missing":         "go:embed files missing",
	"build_dev_dependency_in_production": "Development package loaded in production",
	"build_bundle_platform_missing":      "Gemfile.lock lacks Linux",
	"build_wrong_root":                   "Wrong root directory",
	"build_script_missing":               "Build script missing",
	"build_module_not_found":             "Module not found",
	"build_type_error":                   "Type error",
	"build_compile_error":                "Compile error",
	"build_command_not_found":            "Command not found",
	"build_output_missing":               "Build output missing",
	"build_copy_source_missing":          "COPY source missing",
	"build_out_of_memory":                "Build ran out of memory",
	"build_disk_full":                    "Disk full during build",
	"build_dockerfile_invalid":           "Dockerfile does not parse",
	"build_timeout":                      "Build timed out",
	"build_failed":                       "Build failed",
	"builder_missing":                    "Docker Buildx missing",
	"registry_rate_limited":              "Registry rate limit reached",
	"registry_unreachable":               "Registry unreachable",
	"registry_auth_failed":               "Registry refused the server",
	"base_image_missing":                 "Base image not found",
	"source_auth_failed":                 "Git credential refused",
	"source_repository_missing":          "Repository not found",
	"source_unreachable":                 "Git remote unreachable",
	"source_revision_unavailable":        "Commit no longer on the remote",
	"ref_not_found":                      "Branch or tag not found",
	"runtime_port_in_use":                "Port already in use",
	"image_missing":                      "Image missing",
	"mount_invalid":                      "Mount cannot be created",
}

// causeTitle is the short name of a cause code, or "" for a code that is not
// a named cause (an engine fault, a plan refusal).
func causeTitle(code string) string {
	if title := causeTitles[code]; title != "" {
		return title
	}
	return outputCauseTitles[code]
}

// namedCauseTitle is causeTitle for a code that names why, not only where: a
// "build failed" says less than the sentence it came with.
func namedCauseTitle(code string) string {
	switch code {
	case "build_failed", "release_task_failed", "health_gate_failed", "candidate_start_failed":
		return ""
	}
	return causeTitle(code)
}

var lockfileManifests = map[string]string{
	"package-lock.json": "package.json", "bun.lock": "package.json", "pnpm-lock.yaml": "package.json",
	"yarn.lock": "package.json", "poetry.lock": "pyproject.toml", "uv.lock": "pyproject.toml",
	"Cargo.lock": "Cargo.toml", "go.sum": "go.mod", "go.mod": "the code's imports",
	"composer.lock": "composer.json", "deno.lock": "deno.json and the code's imports", "Gemfile.lock": "Gemfile",
}

var lockfileRegenerate = map[string]string{
	"package-lock.json": "npm install", "bun.lock": "bun install", "pnpm-lock.yaml": "pnpm install",
	"yarn.lock": "yarn install", "poetry.lock": "poetry lock", "uv.lock": "uv lock",
	"Cargo.lock": "cargo update --workspace", "go.sum": "go mod tidy", "go.mod": "go mod tidy",
	"composer.lock": "composer update --lock", "deno.lock": "deno install", "Gemfile.lock": "bundle install",
}

var nodeManagerLockfiles = map[string]string{
	"npm": "package-lock.json", "bun": "bun.lock", "pnpm": "pnpm-lock.yaml", "yarn": "yarn.lock",
}

// languageTools are commands that belong to a language the automatic recipes
// never install, so "not found" means the repository needs its own Dockerfile.
var languageTools = map[string]string{
	"bundle": "Ruby", "ruby": "Ruby", "rails": "Ruby", "rake": "Ruby", "mix": "Elixir", "elixir": "Elixir",
	"dart": "Dart", "flutter": "Flutter", "swift": "Swift", "sbt": "Scala", "lein": "Clojure",
	"clojure": "Clojure", "stack": "Haskell", "cabal": "Haskell", "gleam": "Gleam", "crystal": "Crystal",
	"zig": "Zig", "php": "PHP", "composer": "PHP", "hugo": "Hugo", "jekyll": "Ruby", "java": "Java",
	"dotnet": "the .NET SDK",
}

// nodeDependencyTools are commands a Node dependency provides: not finding one
// means the dependency was not installed, not that the image lacks a tool.
var nodeDependencyTools = map[string]bool{
	"next": true, "vite": true, "tsc": true, "nest": true, "react-scripts": true, "astro": true,
	"nuxt": true, "nuxi": true, "svelte-kit": true, "remix": true, "prisma": true, "webpack": true,
	"rollup": true, "esbuild": true, "tsx": true, "ts-node": true, "ng": true, "vue-cli-service": true,
	"gatsby": true, "eleventy": true, "turbo": true, "drizzle-kit": true, "tailwindcss": true,
}

// buildCauseFix is the one plan change the evidence supports, or nil.
func buildCauseFix(cause *BuildCause, context causeContext) *CauseFix {
	build := context.build
	recipe := build.Method == BuildRecipe
	switch cause.Code {
	case "build_lockfile_out_of_sync":
		failing := nodeManagerForCommand(cause.Command)
		if failing == "" || context.candidate == nil || !recipe {
			return nil
		}
		for _, lockfile := range context.candidate.Lockfiles {
			if lockfile.State == LockfileInSync && lockfile.Manager != failing && nodeManagerLockfiles[lockfile.Manager] != "" {
				return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.packageManager", Value: lockfile.Manager}
			}
		}
	case "build_package_manager_mismatch":
		if len(cause.Subjects) > 0 && nodeManagerLockfiles[cause.Subjects[0]] != "" && recipe &&
			build.PackageManager != cause.Subjects[0] {
			return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.packageManager", Value: cause.Subjects[0]}
		}
	case "build_runtime_version":
		if len(cause.Subjects) == 0 || !recipe {
			return nil
		}
		switch cause.Detail {
		case "go":
			version := goFamily(cause.Subjects[0])
			if goRecipeVersionRE.MatchString(version) && version != build.GoVersion {
				return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.goVersion", Value: version}
			}
		case "python":
			version := pythonVersionForConstraint(cause.Subjects[0])
			if slices.Contains(pythonRecipeVersions, version) && version != build.PythonVersion {
				return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.pythonVersion", Value: version}
			}
		case "node":
			// The subject is the range the failing package asks for; the
			// newest catalogue major inside it is the one to choose.
			if majors, known := nodeRangeMajors(cause.Subjects[0]); known && len(majors) > 0 && build.Recipe == "node" {
				if version := strconv.Itoa(majors[len(majors)-1]); version != build.NodeVersion {
					return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.nodeVersion", Value: version}
				}
			}
		}
	case "build_env_missing":
		if len(cause.Subjects) == 0 || !recipe {
			return nil
		}
		return variableFix(cause.Subjects[0], "build", "", context.variables)
	case "build_sqlx_offline":
		if cause.Detail == "sqlx" && recipe {
			return variableFix("SQLX_OFFLINE", "build", "true", context.variables)
		}
	case "build_out_of_memory":
		if cause.Detail != "node" || !recipe {
			return nil
		}
		if heap := nodeBuildHeapMB(context.hostMemory); heap > 0 {
			return variableFix("NODE_OPTIONS", "build", "--max-old-space-size="+strconv.Itoa(heap), context.variables)
		}
	case "build_legacy_openssl":
		if recipe {
			return variableFix("NODE_OPTIONS", "build", "--openssl-legacy-provider", context.variables)
		}
	case "build_command_not_found":
		if len(cause.Subjects) == 0 || !recipe {
			return nil
		}
		manager := preparedNodeManager(context.prepared)
		if manager == "" || nodeManagerForCommand(cause.Subjects[0]) == "" {
			return nil
		}
		field, command := "configuration.build.buildCommand", build.BuildCommand
		if cause.Phase != phaseBuild {
			return nil
		}
		if rewritten := nodeRunnerFor(command, manager); rewritten != command {
			return &CauseFix{Kind: fixSetBuild, Field: field, Value: rewritten}
		}
	case "build_script_missing":
		return &CauseFix{Kind: fixReview, Field: "configuration.build.buildCommand"}
	case "build_prisma_client_missing":
		command := strings.TrimSpace(build.BuildCommand)
		manager := preparedNodeManager(context.prepared)
		// The recipe generates the client itself when the prisma CLI is a
		// dependency (build_node_prisma.go); without the CLI a generate step
		// would download an unpinned one, so the fix is to add it.
		if command == "" || manager == "" || strings.Contains(command, "prisma generate") || !recipe ||
			strings.Contains(context.prepared.DockerfilePreview, " prisma generate") {
			return nil
		}
		return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.buildCommand", Value: nodeExecRunner(manager) + " prisma generate && " + command}
	case "build_output_missing":
		if context.candidate != nil && context.candidate.OutputDirectory != "" &&
			context.candidate.OutputDirectory != build.OutputDirectory && recipe {
			return &CauseFix{Kind: fixSetBuild, Field: "configuration.build.outputDirectory", Value: context.candidate.OutputDirectory}
		}
		if build.OutputDirectory != "" {
			return &CauseFix{Kind: fixReview, Field: "configuration.build.outputDirectory"}
		}
		return &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}
	case "build_wrong_root":
		return &CauseFix{Kind: fixReview, Field: "configuration.build.rootDirectory"}
	}
	return nil
}

// variableFix adds a variable, or adds a scope to one the run already had.
func variableFix(name, scope, value string, variables []ReleaseVariableSnapshot) *CauseFix {
	if !envKeyRe.MatchString(name) {
		return nil
	}
	for _, variable := range variables {
		if variable.Name != name {
			continue
		}
		if slices.Contains(strings.Split(variable.Scopes, ","), scope) {
			// It was there and still missing, or a value is set that the fix
			// would overwrite: nothing the output proves can be applied.
			return nil
		}
		return &CauseFix{Kind: fixVariableScope, Field: "variables." + name, Value: scope, Scope: scope}
	}
	return &CauseFix{Kind: fixAddVariable, Field: "variables." + name, Value: value, Scope: scope}
}

// nodeBuildHeapMB is a V8 heap limit for the build: three quarters of the
// server's memory, in 512 MiB steps, capped at 8 GiB. A server too small to
// give a build 1 GiB gets no suggestion, because raising the limit would
// only move where it dies.
func nodeBuildHeapMB(hostMemory int64) int {
	if hostMemory <= 0 {
		return 0
	}
	heap := int(hostMemory>>20) * 3 / 4 / 512 * 512
	if heap > 8192 {
		heap = 8192
	}
	if heap < 1024 {
		return 0
	}
	return heap
}

func hostMemoryTotal() int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}

func goFamily(version string) string {
	parts := strings.Split(strings.TrimPrefix(version, "go"), ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// sentence is the failed step's message: where the build failed, what its
// output proves, and what to change.
func (c *BuildCause) sentence() string {
	if c == nil {
		return ""
	}
	var message strings.Builder
	if c.Service != "" {
		message.WriteString("Compose service " + c.Service + ": ")
	}
	message.WriteString(c.lead())
	what, action := c.explain()
	if what != "" {
		message.WriteString(": " + what)
	}
	message.WriteString(". ")
	if action != "" {
		message.WriteString(upperFirst(action) + ".")
	}
	return truncateUTF8Prefix(strings.TrimSpace(message.String()), causeSentenceLength)
}

func (c *BuildCause) lead() string {
	command := ""
	if c.Command != "" {
		command = " (`" + c.Command + "`)"
	}
	exited := ""
	if c.ExitCode >= 0 {
		exited = fmt.Sprintf(" exited with code %d", c.ExitCode)
	}
	if c.Code == "build_timeout" {
		if command == "" {
			return "The build was still running when it reached its 30-minute limit"
		}
		return "The build step" + command + " was still running when the build reached its 30-minute limit"
	}
	switch c.Phase {
	case phaseInstall:
		return "The install step" + command + exited
	case phaseBuild:
		if c.Command == "" {
			return "The build failed"
		}
		return "The build command" + command + exited
	case phaseOutputCheck:
		return "The build finished without its output"
	case phaseSetup:
		return "The image setup step" + command + exited
	case phaseDockerfile:
		if c.Command == "" {
			return "The Dockerfile build failed"
		}
		return "The Dockerfile step" + command + exited
	case phaseBaseImage:
		return "The build could not use its base image"
	case phasePull:
		return "The image could not be pulled"
	}
	return "The build failed"
}

func (c *BuildCause) subjectList() string {
	return strings.Join(c.Subjects, ", ")
}

func (c *BuildCause) subject() string {
	if len(c.Subjects) == 0 {
		return ""
	}
	return c.Subjects[0]
}

// explain says what the cause is and what to do about it, in that order.
func (c *BuildCause) explain() (string, string) {
	subject, subjects := c.subject(), c.subjectList()
	fix := c.Fix
	switch c.Code {
	case "build_lockfile_out_of_sync":
		manifest := lockfileManifests[c.Detail]
		lockfile := c.Detail
		if lockfile == "" {
			lockfile, manifest = "the lockfile", "its manifest"
		}
		what := lockfile + " is out of sync with " + manifest
		if subjects != "" {
			what += " (missing " + subjects + ")"
		}
		regenerate := "regenerate " + lockfile + " and commit it"
		if command := lockfileRegenerate[c.Detail]; command != "" {
			regenerate = "regenerate " + lockfile + " with `" + command + "` and commit it"
		}
		if fix != nil && fix.Kind == fixSetBuild {
			return what, "build with " + nodeManagerLabel(fix.Value) + ", whose " + nodeManagerLockfiles[fix.Value] +
				" matches package.json, or " + regenerate
		}
		return what, "the build installs exactly what the lockfile records, so " + regenerate
	case "build_lockfile_incompatible":
		return c.Detail + " was written by a " + lockfileTool(c.Detail) + " release this build does not run",
			"pin the release that wrote it (for Node, a `packageManager` field in package.json) or regenerate the lockfile with the release the build uses"
	case "build_package_manager_mismatch":
		if fix != nil {
			return "package.json's `packageManager` field names " + subject + ", and the build runs another package manager",
				"build with " + nodeManagerLabel(subject) + ", the package manager the project declares"
		}
		return "the project pins a different package-manager release than the build runs",
			"align the Build setting with package.json's `packageManager` field"
	case "build_lifecycle_script_blocked":
		if c.Detail == "pnpm" {
			return "pnpm refused to run dependency install scripts" + parenthesized(subjects),
				"allow them under `pnpm.onlyBuiltDependencies` in package.json and commit it"
		}
		return "Bun blocked dependency install scripts that the build needed",
			"list those packages under `trustedDependencies` in package.json and commit it; once the field exists it replaces Bun's default allowlist"
	case "build_runtime_version":
		return c.runtimeVersion(subject)
	case "build_hugo_extended_required":
		return "the site uses Sass through Hugo Pipes, which only Hugo's extended edition compiles",
			"build with the extended edition of Hugo"
	case "build_env_missing":
		what := "the build reads " + orDefault(subjects, "a variable") + " and no variable supplies it"
		switch c.Detail {
		case "sveltekit":
			what = "SvelteKit compiles " + subject + " from `$env/static` into the build, and the build has no such variable"
		case "t3-env":
			what = "the environment schema rejected " + orDefault(subjects, "the build's variables") + " while the build validated it"
		case "prisma":
			what = "Prisma reads " + subject + " while the build runs it, and the build has no such variable"
		}
		return what, variableAction(fix, subject, "set it for the build")
	case "build_sqlx_offline":
		if c.Detail == ".sqlx" {
			return "SQLX_OFFLINE is set, but the repository has no prepared query data",
				"run `cargo sqlx prepare` and commit the .sqlx directory"
		}
		return "sqlx checks its queries against a database at compile time, and the build has none",
			"run `cargo sqlx prepare`, commit the .sqlx directory, and " + variableAction(fix, "SQLX_OFFLINE", "set SQLX_OFFLINE=true for the build")
	case "build_database_unreachable":
		what := "the build could not reach its database, which only the running application's network can reach"
		if c.Detail == "next" && subject != "" {
			what = "page " + subject + " queries the database while the build prerenders it, and the database is reachable only from the running application"
		}
		return what, "render that page at request time (in Next.js, `export const dynamic = 'force-dynamic'`), or move database steps into the start command or a release task"
	case "build_next_image_export":
		return "next/image's default loader optimises images on a server, and next.config exports a static site with none",
			"set images: { unoptimized: true } in next.config, or give next/image a custom loader"
	case "build_prerender_failed":
		return "page " + orDefault(subject, "a page") + " threw while the build prerendered it",
			"its error is in the build log just above; if it needs runtime data, render it at request time"
	case "build_prisma_client_missing":
		if fix != nil {
			return "the build used Prisma Client before `prisma generate` created it", "generate it first: `" + fix.Value + "`"
		}
		return "the build used Prisma Client before `prisma generate` created it",
			"add the `prisma` CLI to the project's devDependencies, which the automatic build generates the client with, or run `prisma generate` before the build"
	case "build_platform_binary_missing":
		return "the lockfile has no Linux build of " + orDefault(subject, "a native dependency") +
				"; it was written on another platform, where the package manager leaves other platforms' optional packages out",
			"regenerate the lockfile on Linux, or delete it with node_modules, install again and commit the result"
	case "build_legacy_openssl":
		return "a build tool (webpack 4 or older react-scripts) uses a hash Node 17 and later refuse",
			variableAction(fix, "NODE_OPTIONS", "upgrade the tool") + ", or upgrade the tool"
	case "build_system_library_missing":
		return c.systemLibrary(subject)
	case "build_native_toolchain_missing":
		return c.nativeToolchain(subject)
	case "build_install_script_failed":
		return "the install script of " + orDefault(subject, "a dependency") + " failed",
			"its error is in the build log above; a native package usually needs a prebuilt binary for Linux musl or a toolchain the image lacks"
	case "build_php_extension_missing":
		return "a locked package requires the PHP extension " + orDefault(subject, "it names") + ", which the image does not install",
			"declare it in composer.json's `require` (for example `\"" + orDefault(subject, "ext-intl") + "\": \"*\"`) so the recipe installs it"
	case "build_dependency_conflict":
		if c.Detail == "npm" {
			return "npm could not satisfy the peer dependency ranges" + parenthesized(subjects),
				"fix the ranges, or commit a lockfile made with `npm install --legacy-peer-deps` and set `legacy-peer-deps=true` in .npmrc"
		}
		return "the dependencies pin versions that conflict" + parenthesized(subjects), "relax the conflicting constraints and regenerate the lockfile"
	case "build_dependency_advisory_blocked":
		return "Composer refused a package version that has a known security advisory",
			"update the affected package with `composer update <package>`, or set `config.audit.block-insecure` to false in composer.json"
	case "build_dependency_local_path":
		return "a requirement points at a file that exists only on the machine that wrote it" + parenthesized(subject),
			"regenerate requirements.txt with `pip list --format=freeze` from a virtual environment, not a conda environment"
	case "build_dependency_unavailable":
		return orDefault(subjects, "a dependency") + " could not be found in its registry",
			"check the name and version; a private package needs its registry token as a build variable scoped to install"
	case "build_registry_auth":
		if c.Phase == phasePull {
			// A registry answers a private image it will not serve and an image
			// that does not exist alike, so the sentence names both.
			return "the registry refused it: the image does not exist, or it is private and no valid credential was given",
				"check the image reference and, for a private image, the source's registry credential"
		}
		if c.Detail == "npmrc" && subject != "" {
			return ".npmrc authenticates the registry with " + subject + ", which the install was not given",
				"add " + subject + " as a build variable mapped to the install step"
		}
		return "the package registry refused the build's credentials",
			"add the registry token as a build variable scoped to install (for npm, `NPM_TOKEN` read by an .npmrc)"
	case "build_registry_rate_limited", "registry_rate_limited":
		return "the registry's rate limit for this server's address was reached",
			"sign the server in to Docker Hub (`docker login`), or wait for the limit to reset and deploy again"
	case "build_network":
		return "the build could not reach " + orDefault(subject, "the network"),
			"check the server's outbound network and DNS, then deploy again"
	case "build_base_image_missing":
		return "the base image " + orDefault(subject, "it names") + " does not exist or is private", "check the Dockerfile's FROM line"
	case "build_image_not_found":
		return "the image does not exist in its registry", "check the image reference and its tag or digest"
	case "build_platform_unsupported":
		return "an image or binary in the build was not built for this server's architecture",
			"use a multi-architecture image, or one built for this server"
	case "build_script_crlf":
		return "a script has Windows line endings, which the Linux shell cannot run",
			"convert it to LF line endings and add `*.sh text eol=lf` to .gitattributes"
	case "build_permission":
		return orDefault(subject, "a script") + " is not executable in the repository",
			"commit it with its executable bit: `git update-index --chmod=+x " + orDefault(subject, "<file>") + "`"
	case "build_wrapper_missing":
		if c.Detail == "maven" {
			return "the Maven wrapper's files are not committed, so ./mvnw cannot start", "commit the .mvn/wrapper directory"
		}
		return "gradle/wrapper/gradle-wrapper.jar is not committed, so ./gradlew cannot start",
			"commit the wrapper jar; a `*.jar` rule in .gitignore usually excludes it"
	case "build_embed_source_missing":
		return "the go:embed pattern " + orDefault(subject, "") + " matched no files, because they are generated or ignored",
			"generate them in the build command before `go build`, or commit them"
	case "build_dev_dependency_in_production":
		if c.Detail == "laravel" {
			return "a development package's service provider is registered while the production install leaves it out",
				"register it only for the local environment"
		}
		return subject + " is a development bundle, and the production install leaves it out",
			"enable it for the dev environment only in config/bundles.php"
	case "build_bundle_platform_missing":
		return "Gemfile.lock lists only " + orDefault(subjects, "other platforms") + ", not Linux",
			"run `bundle lock --add-platform x86_64-linux` and commit Gemfile.lock"
	case "build_wrong_root":
		return "the build ran in a directory that is not the project's root", "set the root directory to the folder that holds the project's manifest"
	case "build_script_missing":
		return "the project defines no `" + orDefault(subject, "build") + "` script", "set the build command to a script the project defines"
	case "build_module_not_found":
		return "the build imports " + orDefault(subject, "a module") + ", which is not installed or does not exist",
			"add it to the project's dependencies, or correct the import path; paths on Linux are case-sensitive"
	case "build_type_error":
		return "TypeScript found type errors" + parenthesized(subjects), "fix them; each one is in the build log"
	case "build_compile_error":
		return "the compiler rejected the code" + parenthesized(subjects), "fix the errors shown in the build log and push"
	case "build_command_not_found":
		return c.commandNotFound(subject)
	case "build_output_missing":
		what := "the build did not produce " + orDefault(subject, "its output")
		if fix != nil && fix.Kind == fixSetBuild {
			return what, "detection reads `" + fix.Value + "` as this project's output directory; set it as the output directory"
		}
		return what, "set the output directory and start command to where the build writes"
	case "build_copy_source_missing":
		return "the Dockerfile copies " + orDefault(subject, "a path") + ", which the build context does not contain",
			"check the path and that .dockerignore does not exclude it"
	case "build_out_of_memory":
		what := "the build was killed for using more memory than the server could give it"
		if c.Detail == "node" {
			what = "Node ran out of heap memory"
		}
		if fix != nil {
			return what, variableAction(fix, "NODE_OPTIONS", "")
		}
		return what, "give the server more memory or swap, or build the image elsewhere and deploy it as an image"
	case "build_disk_full":
		return "the server ran out of disk space", "free space (unused images and build cache usually hold it: `docker builder prune`), then deploy again"
	case "build_dockerfile_invalid":
		return "the Dockerfile does not parse", "BuildKit's reason is at the end of the build log"
	case "build_timeout":
		return "", "keep the build cache between builds (Rebuild without cache discards it), build less, or build the image elsewhere and deploy it as an image"
	case "builder_missing":
		return "Docker Buildx is not installed on this server", "install the docker-buildx plugin"
	}
	return "", "the build log holds its output"
}

func (c *BuildCause) runtimeVersion(subject string) (string, string) {
	fix := c.Fix
	switch c.Detail {
	case "go":
		if fix != nil {
			return "go.mod requires Go " + subject, "set the Go version to " + fix.Value
		}
		return "go.mod requires Go " + subject + ", newer than the recipe's releases", "build with a Dockerfile, or lower the `go` line in go.mod"
	case "python":
		if fix != nil {
			return "the project requires Python " + subject, "set the Python version to " + fix.Value
		}
		return "the project requires Python " + subject + ", which the recipe's releases do not satisfy", "build with a Dockerfile, or relax the requirement"
	case "node":
		if fix != nil {
			return "the project requires Node " + subject, "set the Node version to " + fix.Value
		}
		return "the project requires Node " + orDefault(subject, "a different release"),
			"choose the release it needs as the Node version in Build settings, or declare it (`engines.node` in package.json or .nvmrc)"
	case "rust":
		return "the code requires Rust " + orDefault(subject, "a newer release"), "pin a newer toolchain in rust-toolchain.toml, or lower the dependency"
	case "java", "gradle":
		return "the project targets a Java release the build's JDK does not support" + parenthesized(subject),
			"set the Java release in the build file to the JDK the recipe uses, upgrade the Gradle wrapper, or build with a Dockerfile"
	case "dotnet":
		return "the project targets .NET " + orDefault(subject, "a release") + ", newer than the build's SDK", "change the TargetFramework, or build with a Dockerfile"
	case "php":
		return "composer.json requires PHP " + subject, "set `require.php` to a release the recipe offers, or build with a Dockerfile"
	case "ruby":
		return "the Gemfile requires Ruby " + subject, "build with a Dockerfile that installs that release"
	}
	return "the project requires a " + c.Detail + " release the build does not run" + parenthesized(subject),
		"build with a Dockerfile that provides it"
}

func (c *BuildCause) systemLibrary(subject string) (string, string) {
	switch {
	case subject == "pg_config":
		return "psycopg2 compiles against libpq, and the image has no libpq development files",
			"depend on `psycopg[binary]` or `psycopg2-binary`, or build with a Dockerfile that installs libpq-dev"
	case subject == "mysql_config":
		return "mysqlclient compiles against the MySQL client library, and the image has none",
			"use PyMySQL, or build with a Dockerfile that installs default-libmysqlclient-dev and pkg-config"
	case c.Detail == "prisma" || strings.HasPrefix(subject, "libssl"):
		return "Prisma's engine needs OpenSSL, which the image does not have",
			"install openssl in the image, or set the Prisma generator's binaryTargets for the image's OpenSSL"
	case c.Detail == "musl":
		return "a dependency ships a glibc binary, and the image is Alpine (musl)",
			"use the dependency's musl build, or a Dockerfile on a glibc base image"
	case strings.HasSuffix(subject, "-sys"):
		return "the Rust crate " + subject + " builds a C library the image does not have",
			"enable the crate's vendored or rustls feature, or build with a Dockerfile that installs the library"
	}
	return "the build needs the system library " + orDefault(subject, "it names") + ", which the image does not have",
		"build with a Dockerfile that installs it, or use a dependency that ships it prebuilt"
}

func (c *BuildCause) nativeToolchain(subject string) (string, string) {
	switch c.Detail {
	case "node-gyp":
		return "a dependency compiles a native addon with node-gyp, which needs python3, make and g++",
			"use a dependency with prebuilt binaries, or build with a Dockerfile that installs the toolchain"
	case "python":
		return orDefault(subject, "a dependency") + " has no prebuilt wheel for this Python and the image has no compiler",
			"choose a Python version the package publishes wheels for, depend on a binary build, or build with a Dockerfile"
	case "go":
		return "a dependency uses cgo, and the Go recipe builds without a C compiler",
			"switch to a pure-Go dependency (modernc.org/sqlite for mattn/go-sqlite3), or build with a Dockerfile"
	case "rust":
		return "the Rust build could not link its native code", "build with a Dockerfile that installs the linker and libraries"
	case "dotnet":
		return "native AOT publishing needs " + subject + ", which the SDK image lacks", "publish without native AOT, or build with a Dockerfile"
	}
	return "the build needs " + orDefault(subject, "a compiler") + ", which the image does not have",
		"build with a Dockerfile that installs it"
}

func (c *BuildCause) commandNotFound(subject string) (string, string) {
	fix := c.Fix
	switch {
	case c.Detail == "laravel":
		return "Laravel's Wayfinder Vite plugin runs `php artisan wayfinder:generate` while the assets build, and the image that builds them has no PHP",
			"build with a Dockerfile whose asset stage has PHP and the Composer dependencies"
	case fix != nil:
		return "`" + subject + "` is not installed in the image this build runs on",
			"run the command with the build's package manager: `" + fix.Value + "`"
	case languageTools[subject] != "":
		return "`" + subject + "` belongs to " + languageTools[subject] + ", which this build's image does not include",
			"build this part of the repository with a Dockerfile that installs " + languageTools[subject]
	case nodeDependencyTools[subject]:
		return "`" + subject + "` comes from a dependency that was not installed",
			"list its package in package.json, and do not omit dev dependencies at install"
	case subject == "":
		return "the command it runs is not installed", "use a command the image has, or build with a Dockerfile that installs it"
	}
	return "`" + subject + "` is not installed in the image", "use a command the image has, or build with a Dockerfile that installs it"
}

func variableAction(fix *CauseFix, name, fallback string) string {
	if fix == nil {
		if fallback != "" {
			return fallback
		}
		return "add " + name + " for the build"
	}
	variable := strings.TrimPrefix(fix.Field, "variables.")
	switch fix.Kind {
	case fixVariableScope:
		return "give " + variable + " the " + fix.Scope + " scope in Variables"
	case fixAddVariable:
		if fix.Value != "" {
			return "add " + variable + "=" + fix.Value + " with the " + fix.Scope + " scope"
		}
		return "add " + variable + " as a variable with the " + fix.Scope + " scope"
	}
	return fallback
}

func lockfileTool(lockfile string) string {
	switch lockfile {
	case "pnpm-lock.yaml":
		return "pnpm"
	case "Cargo.lock":
		return "Cargo"
	case "poetry.lock":
		return "Poetry"
	case "uv.lock":
		return "uv"
	case "bun.lock":
		return "Bun"
	}
	return "package-manager"
}

func parenthesized(value string) string {
	if value == "" {
		return ""
	}
	return " (" + value + ")"
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// upperFirst starts a sentence, unless it starts with a name whose case is
// its own: a file ("bun.lock"), a command, a package.
func upperFirst(value string) string {
	first, _, _ := strings.Cut(value, " ")
	if value == "" || strings.ContainsAny(first, ".`/@") || value[0] < 'a' || value[0] > 'z' {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
