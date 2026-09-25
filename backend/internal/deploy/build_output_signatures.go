package deploy

import "regexp"

// signature builds a table entry. needle is a literal the pattern's lines all
// contain, so most lines are rejected without running the expression.
func signature(code, detail, needle, pattern string) buildSignature {
	return buildSignature{code: code, detail: detail, needle: needle, pattern: regexp.MustCompile(pattern)}
}

// exitSignature is a failure the exit code alone proves. It names no line,
// since no line is the cause: the step's first is only where it began.
func exitSignature(code string, exit int) buildSignature {
	return buildSignature{code: code, exit: exit}
}

func (s buildSignature) exitCode(code int) buildSignature { s.exit = code; return s }

func (s buildSignature) requiring(pattern string) buildSignature {
	s.requires = regexp.MustCompile(pattern)
	return s
}

func (s buildSignature) collecting(pattern string) buildSignature {
	s.subjects = regexp.MustCompile(pattern)
	return s
}

func (s buildSignature) naming(subject string) buildSignature { s.subject = subject; return s }

func (s buildSignature) unlessLine(pattern string) buildSignature {
	s.unless = regexp.MustCompile(pattern)
	return s
}

// databaseErrorPattern is a database the build could not reach, in the words
// of the drivers and ORMs that print it.
const databaseErrorPattern = `ECONNREFUSED|P1001|Can't reach database server|getaddrinfo (?:ENOTFOUND|EAI_AGAIN)|Connection terminated unexpectedly|could not translate host name|connection to server at|Access denied for user|password authentication failed`

// A shell's "X: not found" is printed for a command a script only probed for,
// too — a config that runs `git rev-parse` inside a try — and the build carries
// on. What says the missing command is why the step failed is the status a
// shell gives it, 127, which npm and Yarn pass on, or a line reporting that
// status from a wrapper that exits with a status of its own.
const (
	shellNotFoundPattern     = `(?:^|[\s/])(?:sh|bash|dash|ash)(?:: (?:line )?\d+)?: ([\w.+@-]+): (?:command )?not found`
	toolchainNotFoundPattern = `(?:^|[\s/])(?:sh|bash|dash|ash)(?:: (?:line )?\d+)?: (make|g\+\+|gcc|cc|c\+\+|clang|cmake|python3?): (?:command )?not found`
	exit127ReportPattern     = `(?:exit code|exited with code|error code)[: ]+127\b|exited \(127\)`
)

// buildSignatures is every build failure the dashboard can name, most
// specific first: the first one to match a line wins. The failed step's own
// lines are read first, then what the steps still unfinished and BuildKit's
// closing replay printed, so a signature here must not match what a step
// prints on its way to succeeding.
var buildSignatures = []buildSignature{
	// The builder itself, and the host under it.
	signature("builder_missing", "", "docker", `'buildx' is not a docker command|unknown command:? "?(?:docker )?buildx|buildx component is missing`),
	signature("build_disk_full", "", "", `no space left on device|ENOSPC`),
	signature("build_out_of_memory", "node", "heap", `JavaScript heap out of memory|Reached heap limit|Ineffective mark-compacts near heap limit`),
	signature("build_out_of_memory", "java", "OutOfMemoryError", `java\.lang\.OutOfMemoryError`),
	signature("build_out_of_memory", "", "", `^Killed$|signal: killed|Cannot allocate memory|fatal error: runtime: out of memory|out of memory allocating`),
	exitSignature("build_out_of_memory", 137),
	signature("build_registry_rate_limited", "", "", `toomanyrequests|pull rate limit|429 Too Many Requests|rate limit exceeded|ERR_PNPM_FETCH_429|E429`),

	// Lockfiles that no longer describe their manifest.
	signature("build_lockfile_out_of_sync", "package-lock.json", "can only install packages when your package.json and",
		`can only install packages when your package\.json and package-lock\.json`).
		collecting(`(?:Missing: |Invalid: lock file's )(@?[^@\s]+)@`),
	signature("build_lockfile_out_of_sync", "bun.lock", "lockfile had changes", `lockfile had changes, but lockfile is frozen`),
	signature("build_lockfile_out_of_sync", "pnpm-lock.yaml", "ERR_PNPM_OUTDATED_LOCKFILE", `ERR_PNPM_OUTDATED_LOCKFILE`),
	signature("build_lockfile_out_of_sync", "yarn.lock", "frozen-lockfile", `Your lockfile needs to be updated, but yarn was run with`),
	signature("build_lockfile_out_of_sync", "yarn.lock", "YN0028", `YN0028`),
	signature("build_lockfile_out_of_sync", "poetry.lock", "poetry.lock", `changed significantly since poetry\.lock was last generated`),
	signature("build_lockfile_out_of_sync", "uv.lock", "--locked", "needs to be updated, but `--locked` was provided"),
	signature("build_lockfile_out_of_sync", "Cargo.lock", "--locked", `needs to be updated but --locked was passed`),
	signature("build_lockfile_out_of_sync", "go.sum", "go.sum", `missing go\.sum entry(?: for module providing package ([^\s;(]+))?`),
	signature("build_lockfile_out_of_sync", "go.mod", "updates to go.mod needed", `updates to go\.mod needed`),
	signature("build_lockfile_out_of_sync", "composer.lock", "lock file", `Required package "([^"]+)" is not present in the lock file|([\w.-]+/[\w.-]+) is in the lock file as .* but that does not satisfy your constraint|Your lock file does not contain a compatible set of packages`),
	signature("build_lockfile_out_of_sync", "deno.lock", "lockfile", `The lockfile is out of date|does not match the expected hash in the lock file`),
	signature("build_lockfile_out_of_sync", "Gemfile.lock", "Gemfile", `in deployment mode after changing your Gemfile|The gemspecs for path gems changed`),

	// Lockfiles written by a package-manager release this image does not run.
	signature("build_lockfile_incompatible", "pnpm-lock.yaml", "PNPM", `ERR_PNPM_LOCKFILE_BREAKING_CHANGE|ERR_PNPM_BROKEN_LOCKFILE|Ignoring not compatible lockfile`),
	signature("build_lockfile_incompatible", "Cargo.lock", "lock file", "lock file version `?\\d+`? (?:was found, but this version of Cargo does not understand|requires `-Znext-lockfile-bump`)"),
	signature("build_lockfile_incompatible", "poetry.lock", "lock file", `The lock file is not compatible with the current version of Poetry`),
	signature("build_lockfile_incompatible", "uv.lock", "uv.lock", "Failed to parse `uv\\.lock`|`uv\\.lock` uses an unsupported schema version"),
	signature("build_lockfile_incompatible", "bun.lock", "ockfile version", `(?i)(?:unknown|unsupported) lockfile version`),
	signature("build_package_manager_mismatch", "", "configured to use", `This project is configured to use (npm|pnpm|yarn|bun)\b`),
	signature("build_package_manager_mismatch", "pnpm", "ERR_PNPM_BAD_PM_VERSION", `ERR_PNPM_BAD_PM_VERSION`),

	// Dependency install scripts a package manager refused to run.
	signature("build_lifecycle_script_blocked", "pnpm", "ERR_PNPM_IGNORED_BUILDS", `ERR_PNPM_IGNORED_BUILDS`).
		collecting(`Ignored build scripts: ([^.]+)`),
	signature("build_lifecycle_script_blocked", "bun", "Blocked", `Blocked \d+ postinstalls?`).
		requiring(`@prisma/client did not initialize yet|Cannot find module|Could not load the "sharp" module|esbuild|install script`),

	// Language and tool versions the image does not provide.
	signature("build_runtime_version", "node", "EBADENGINE", `code EBADENGINE`).collecting(`Not compatible with your version of node/npm: (\S+)`),
	signature("build_runtime_version", "node", "ERR_PNPM_UNSUPPORTED_ENGINE", `ERR_PNPM_UNSUPPORTED_ENGINE`).collecting(`Expected version: (\S+)`),
	signature("build_runtime_version", "node", "engine", `The engine "node" is incompatible with this module\. Expected version "([^"\s]+)"`),
	signature("build_runtime_version", "node", "For Next.js", `For Next\.js, Node\.js version "([^"\s]+)" is required`),
	signature("build_runtime_version", "go", "GOTOOLCHAIN=local", `requires go >= (\S+) \(running go \S+; GOTOOLCHAIN=local\)`),
	signature("build_runtime_version", "rust", "rustc", `requires rustc (\S+) or newer`),
	signature("build_runtime_version", "rust", "feature", "feature `(edition\\d{4})` is required"),
	signature("build_runtime_version", "java", "release", `invalid (?:target|source) release:? (\S+)|release version (\S+) not supported`),
	signature("build_runtime_version", "gradle", "class file major version", `Unsupported class file major version (\d+)`),
	signature("build_runtime_version", "java", "JVM", `requires JVM (\d+) or later|UnsupportedClassVersionError`),
	signature("build_runtime_version", "gradle-toolchain", "languageVersion=", `(?:No matching toolchains found for requested specification|Cannot find a Java installation on your machine matching this tasks requirements): \{languageVersion=(\d+)`),
	signature("build_runtime_version", "dotnet", "NETSDK1045", `NETSDK1045: The current \.NET SDK does not support targeting \.NET ([\d.]+)`),
	signature("build_runtime_version", "dotnet", "compatible .NET SDK", `A compatible \.NET SDK was not found`).collecting(`Requested SDK version: (\S+)`),
	signature("build_runtime_version", "dotnet-restore", "NETSDK1005", `NETSDK1005: Assets file '(?:[^']*/)?([^/']+)/obj/project\.assets\.json' doesn't have a target for`),
	signature("build_runtime_version", "php", "your php version", `requires php (\S+) -> your php version \([\d.]+\) does not satisfy that requirement|requires php (\S+) but your php version \([\d.]+\) does not satisfy`),
	signature("build_runtime_version", "python", "requires a different Python", `requires a different Python: \S+ not in '([^']+)'`),
	signature("build_runtime_version", "python", "Python", "(?:locked|project's) Python requirement: `([^`]+)`|does not satisfy Python(>=?[\\d.]+)|is not supported by the project \\(([^)]+)\\)"),
	signature("build_runtime_version", "ruby", "Your Ruby version", `Your Ruby version is \S+, but your Gemfile specified (\S+)`),
	signature("build_runtime_version", "elixir", "Elixir", `supports only Elixir ~> ([\d.]+)`),
	signature("build_runtime_version", "hugo", "Hugo", `not available in your current Hugo version|requires Hugo (?:version )?(\S+)`),
	signature("build_hugo_extended_required", "hugo", "", `TOCSS|you need the extended version|this feature is not available in this edition of Hugo`),

	// Variables the build reads and was not given.
	signature("build_env_missing", "prisma", "environment variable", `(?:Missing required environment variable|Cannot resolve environment variable): ([A-Za-z_]\w*)`),
	signature("build_env_missing", "prisma", "Environment variable not found", `Environment variable not found: ([A-Za-z_]\w*)`),
	signature("build_env_missing", "t3-env", "Invalid environment variables", `Invalid environment variables`).
		collecting(`(?:\b|')([A-Z][A-Z0-9_]{2,})'?"?\s*(?::\s*\[|\],)`),
	signature("build_env_missing", "sveltekit", "$env/", `"([A-Za-z_]\w*)" is not exported by "\$env/(?:static|dynamic)/(?:private|public)"`),
	signature("build_env_missing", "astro", "is missing", `^\s*- ([A-Z][A-Z0-9_]+) is missing`),
	signature("build_env_missing", "rails", "secret_key_base", `Missing secret_key_base for`).naming("SECRET_KEY_BASE"),
	signature("build_env_missing", "phoenix", "environment variable", `environment variable ([A-Z_][A-Z0-9_]*) is missing`),
	signature("build_env_missing", "django", "environment variable", `Set the ([A-Z_][A-Z0-9_]*) environment variable`),
	signature("build_env_missing", "django", "SECRET_KEY", `The SECRET_KEY setting must not be empty`).naming("SECRET_KEY"),
	signature("build_env_missing", "python", "KeyError", `KeyError: '([A-Z][A-Z0-9_]{2,})'`).requiring(`environ`),
	signature("build_sqlx_offline", "sqlx", "DATABASE_URL", "set `DATABASE_URL` to use query macros").naming("DATABASE_URL"),
	signature("build_sqlx_offline", ".sqlx", "SQLX_OFFLINE", "`SQLX_OFFLINE=true` but there is no cached data|SQLX_OFFLINE=true but there is no cached data"),

	// Pages rendered at build time that needed something only runtime has.
	signature("build_database_unreachable", "next", "prerender", `Error occurred prerendering page "([^"]+)"|Failed to collect page data for (\S+)|Export encountered an error on ([^\s,:]+)`).
		requiring(databaseErrorPattern),
	signature("build_database_unreachable", "", "database server", "Can't reach database server at `([^`]+)`"),
	// A linked database's address resolves only on the project's network,
	// which a build is not attached to.
	signature("build_database_unreachable", "", ".jd.internal", `getaddrinfo (?:ENOTFOUND|EAI_AGAIN) ([\w.-]+\.jd\.internal)`),
	signature("build_database_unreachable", "django", "OperationalError", `OperationalError: (?:could not translate host name|connection to server at)`),
	signature("build_prerender_failed", "next", "", `Error occurred prerendering page "([^"]+)"|Failed to collect page data for (\S+)|Export encountered an error on ([^\s,:]+)`),

	// What a Node build needed from its dependencies and did not get.
	signature("build_prisma_client_missing", "prisma", "prisma", `@prisma/client did not initialize yet|Cannot find module '\.prisma/client|Can't resolve '\.prisma/client`),
	signature("build_platform_binary_missing", "", "",
		`(?:Cannot find (?:module|package) '?|Could not load the "sharp" module using the |Failed to load SWC binary for )([\w./-]*(?:@rollup/rollup-linux-[\w-]+|@esbuild/linux-[\w-]+|@next/swc-linux-[\w-]+|@tailwindcss/oxide-linux-[\w-]+|@img/sharp-linux[\w-]*|lightningcss\.linux-[\w-]+\.node|linux(?:musl)?-[\w-]+|linux/\w+))`),
	signature("build_legacy_openssl", "node", "", `0308010C|ERR_OSSL_EVP_UNSUPPORTED`),

	// System libraries and compilers the image does not carry.
	signature("build_system_library_missing", "musl", "shared library", `Error loading shared library (ld-linux[\w.-]*)`),
	signature("build_system_library_missing", "prisma", "libssl", `Prisma failed to detect the libssl/openssl version`).naming("openssl"),
	signature("build_system_library_missing", "", "cannot open shared object file", `(lib[\w+.-]+\.so[\d.]*): cannot open shared object file`),
	signature("build_system_library_missing", "python", "pg_config", `pg_config executable not found|(pg_config) is required`).naming("pg_config"),
	signature("build_system_library_missing", "python", "mysql_config", `mysql_config(?::)? not found|Can not find valid pkg-config name`).naming("mysql_config"),
	signature("build_system_library_missing", "", "pkg-config", `Package '?([\w.+-]+)'?,? (?:was not found in the pkg-config search path|required by '[^']+', not found)`),
	signature("build_system_library_missing", "", "cannot find -l", `cannot find -l([\w+.-]+)`),
	signature("build_system_library_missing", "rust", "custom build command", "failed to run custom build command for `([\\w-]+-sys)"),
	signature("build_system_library_missing", "", "No such file or directory", `fatal error: ([\w/.+-]+\.h): No such file or directory`),
	signature("build_system_library_missing", "", "OpenSSL", `Could not find (?:directory of )?OpenSSL installation`).naming("openssl"),
	signature("build_system_library_missing", "android", "SDK location not found", `SDK location not found`).naming("android-sdk"),
	signature("build_native_toolchain_missing", "node-gyp", "gyp ERR!", `gyp ERR! find (Python)`),
	signature("build_native_toolchain_missing", "node-gyp", "gyp ERR!", `gyp ERR! (?:stack Error|build error|configure error|not ok)`).naming("node-gyp"),
	signature("build_native_toolchain_missing", "", "not found", toolchainNotFoundPattern).exitCode(127),
	signature("build_native_toolchain_missing", "", "not found", toolchainNotFoundPattern).requiring(exit127ReportPattern),
	signature("build_native_toolchain_missing", "python", "command", `error: command '([\w./+-]*(?:gcc|g\+\+|cc|clang))' failed`),
	signature("build_native_toolchain_missing", "python", "Rust", `(?i)can't find Rust compiler`).naming("rust"),
	signature("build_native_toolchain_missing", "python", "wheel", `Failed building wheel for ([\w.-]+)|Failed to build installable wheels for some pyproject\.toml based projects \(([^)]+)\)|Failed to build `+"`"+`([\w.-]+)`),
	signature("build_native_toolchain_missing", "go", "C compiler", `cgo: C compiler "([\w.+-]+)" not found|exec: "(gcc|g\+\+|cc|clang)": executable file not found`),
	signature("build_native_toolchain_missing", "rust", "linking with", "linking with `([\\w.+-]+)` failed"),
	signature("build_native_toolchain_missing", "", "protoc", "Could not find `(protoc)`"),
	signature("build_native_toolchain_missing", "", "perl", `Can't locate [\w/]+\.pm in @INC|Command '(perl)' not found`).naming("perl"),
	signature("build_native_toolchain_missing", "dotnet", "Platform linker", `Platform linker \('(\w+)'\) not found`),

	// A dependency's own install script that failed for a reason of its own.
	signature("build_install_script_failed", "npm", "node_modules", `npm error path /app/node_modules/((?:@[^/\s]+/)?[^/\s]+)`).
		requiring(`npm error command failed`),
	signature("build_install_script_failed", "yarn", "YN0009", "YN0009: .*?((?:@[^@\\s]+/)?[^@\\s│]+)@\\S+ couldn't be built successfully"),

	// PHP extensions a locked package requires and the image lacks.
	signature("build_php_extension_missing", "php", "it is missing from your system", `requires? (ext-[a-z0-9_]+) \S+ -> it is missing from your system`),

	// A private Maven or Gradle repository that refused the build's
	// credentials; Maven's line also says it could not resolve the
	// dependencies, which is what it could not do, not why.
	signature("build_registry_auth", "java", "status code", `status code: 40[13], reason phrase|Received status code 40[13] from server`),

	// Dependencies the registry could not supply.
	signature("build_dependency_conflict", "npm", "ERESOLVE", `ERESOLVE`).collecting(`(?:peer|Found:) (@?[^@\s]+)@`),
	signature("build_dependency_conflict", "python", "", `ResolutionImpossible|these package versions have conflicting dependencies|No solution found when resolving dependencies|version solving failed`),
	signature("build_dependency_conflict", "composer", "installable set", `Your requirements could not be resolved to an installable set of packages`),
	signature("build_dependency_conflict", "rust", "failed to select a version", "failed to select a version for (?:the requirement )?`([\\w-]+)"),
	signature("build_dependency_conflict", "dotnet", "NU1107", `NU1107`),
	signature("build_dependency_advisory_blocked", "composer", "security advisories", `affected by security advisories`),
	signature("build_dependency_local_path", "python", "", `No such file or directory: '(/(?:croot|opt/conda|tmp/build|home/[\w.-]+|Users/[\w.-]+)/[^']*)'`),
	signature("build_dependency_unavailable", "npm", "is not in this registry", `'(@?[^@'\s]+)@[^']*' is not in this registry`),
	signature("build_dependency_unavailable", "npm", "No matching version found", `No matching version found for (@?[^@\s]+)@`),
	signature("build_dependency_unavailable", "python", "No matching distribution", `No matching distribution found for ([\w.\-\[\]=<>!~,]+)`),
	signature("build_dependency_unavailable", "python", "was not found in the package registry", `Because ([\w.-]+) was not found in the package registry`),
	signature("build_dependency_unavailable", "dotnet", "NU1101", `NU1101: Unable to find package ([\w.-]+)`),
	signature("build_dependency_unavailable", "dotnet", "NU1015", `NU1015`),
	signature("build_dependency_unavailable", "go", "", `unknown revision (\S+)|no matching versions for query|(?:reading|verifying) (\S+): 404 Not Found`),
	signature("build_dependency_unavailable", "java", "", `Could not find artifact (\S+)|Non-resolvable parent POM for (\S+)|Could not resolve dependencies for project (\S+)|Could not resolve all (?:files|dependencies) for configuration`),
	signature("build_dependency_unavailable", "rust", "no matching package", "no matching package named `([\\w-]+)` found"),
	signature("build_dependency_unavailable", "composer", "could not be found", `(?:Package|The requested package) ([\w.-]+/[\w.-]+) could not be found`),
	signature("build_dependency_unavailable", "ruby", "Could not find gem", `Could not find gem '([\w.-]+)`),
	signature("build_dependency_unavailable", "hugo", "module", `module "([^"\s]+)" not found`),

	// Registries that refused the build, and a network that failed it.
	signature("build_registry_auth", "", "", `code E401|code E403|ERR_PNPM_FETCH_40[13]|YN0041|401 Unauthorized|403 Forbidden|401 Client Error|Invalid credentials for|authentication required|terminal prompts disabled|unauthorized: `),
	signature("build_registry_auth", "dotnet", "NU1301", `NU1301`).requiring(`401|403`),
	signature("build_network", "", "", `getaddrinfo (?:ENOTFOUND|EAI_AGAIN) ([\w.-]+)|Could not resolve host:? ([\w.-]+)|dial tcp: lookup ([\w.-]+)|Temporary failure in name resolution|TLS handshake timeout|i/o timeout|network is unreachable|Failed to establish a new connection|Connection timed out|ETIMEDOUT|ECONNRESET|ECONNREFUSED|socket hang up|EAI_AGAIN|NU1301`),

	// Custom Dockerfiles and images.
	signature("build_base_image_missing", "", "failed to resolve source metadata", `failed to resolve source metadata for ([\w./:@-]+)`),
	signature("build_platform_unsupported", "", "", `no match for platform in manifest|exec format error`),

	// Scripts and files in the repository that the build could not run.
	signature("build_script_crlf", "", "", `\^M|\\r'?: No such file or directory|\$'\\r': command not found|\r: not found`),
	signature("build_permission", "", "Permission denied", `(\./[\w./-]+|/[\w./-]+): Permission denied`).exitCode(126),
	signature("build_wrapper_missing", "gradle", "GradleWrapperMain", `org\.gradle\.wrapper\.GradleWrapperMain`).naming("gradle-wrapper.jar"),
	signature("build_wrapper_missing", "maven", "maven-wrapper", `\.mvn/wrapper/maven-wrapper\.(?:jar|properties)`).naming("maven-wrapper"),
	signature("build_embed_source_missing", "go", "no matching files found", `pattern ([^:\s]+): no matching files found`),
	signature("build_dev_dependency_in_production", "symfony", "Attempted to load class", `Attempted to load class "(\w+Bundle)"`),
	signature("build_dev_dependency_in_production", "laravel", "Telescope", `Class "Laravel\\+Telescope`).naming("laravel/telescope"),
	// Laravel's Wayfinder Vite plugin runs artisan while the assets build, and
	// fails the build with it; Vite exits 1 whatever the shell said.
	signature("build_command_not_found", "laravel", "wayfinder:generate", `php artisan wayfinder:generate`).
		requiring(`\bphp: (?:command )?not found`).naming("php"),
	signature("build_bundle_platform_missing", "ruby", "Your bundle only supports platforms", `Your bundle only supports platforms`).collecting(`"([\w.-]+)"`),
	signature("build_wrong_root", "", "", "Couldn't find any `pages` or `app` directory|go: cannot find main module|no Go files in (\\S+)|could not find `Cargo\\.toml`|there is no POM in this directory|Could not read package\\.json|ENOENT: no such file or directory, open '(/[\\w./-]*package\\.json)'|MSB1003|failed to find a workspace root|error inheriting `[\\w-]+` from workspace root|Config file '([\\w./-]+)' does not exist|does not contain a Gradle build|Could not open input file: (artisan)"),

	// The code itself.
	signature("build_prisma_client_missing", "prisma", "Prisma Client", `Prisma Client could not locate the Query Engine`),
	signature("build_script_missing", "", "", `Missing script: "?([\w:.-]+)"?|error: Script not found "([\w:.-]+)"|ERR_PNPM_NO_SCRIPT\s+Missing script: ([\w:.-]+)|error Command "([\w:.-]+)" not found`),
	signature("build_module_not_found", "", "", `error TS2307: Cannot find module '([^']+)'|Module not found: (?:Error: )?Can't resolve '([^']+)'|Cannot find (?:module|package) '([^']+)'|Rollup failed to resolve import "([^"]+)"|Could not resolve "([^"]+)"|Failed to resolve import "([^"]+)"|ModuleNotFoundError: No module named '([\w.]+)'|no required module provides package ([^\s;]+)`),
	signature("build_type_error", "typescript", "", `Type error: |error (TS\d{4,5})\b`).
		collecting(`(?:^|\s)\.?/?((?:[\w@\[\]().-]+/)*[\w@\[\]().-]+\.(?:tsx?|mts|cts|vue|svelte|astro)):\d+:\d+|\b(TS\d{4,5})\b`),
	signature("build_compile_error", "rust", "error", "error\\[(E\\d{4})\\]|error: could not compile `([\\w-]+)`"),
	signature("build_compile_error", "dotnet", "error CS", `error (CS\d{4})`),
	signature("build_compile_error", "java", "", `([\w/.$-]+\.(?:java|kt)):\[?\d+|COMPILATION ERROR|^e: (?:file://)?(\S+\.kt)`),
	signature("build_compile_error", "go", ".go:", `^(?:\S+/)?([\w.-]+\.go):\d+:\d+: `),
	signature("build_compile_error", "java", "Failed to execute goal", `Failed to execute goal ([\w.-]+:[\w.-]+)`),
	signature("build_compile_error", "gradle", "Execution failed for task", `Execution failed for task '([^']+)'`),
	signature("build_compile_error", "", "", `Failed to compile\.|\[(vite:[\w-]+)\] |SyntaxError: |PHP (?:Parse|Fatal) error:`),

	// Commands the image does not have.
	// pip names the git a VCS requirement needs as it gives up on the install.
	signature("build_command_not_found", "", "Cannot find command", `Cannot find command '(git)'`),
	signature("build_command_not_found", "", "not found", shellNotFoundPattern).exitCode(127),
	signature("build_command_not_found", "", "not found", shellNotFoundPattern).requiring(exit127ReportPattern),
	signature("build_command_not_found", "", "", `exec: "([\w./+-]+)": executable file not found in \$PATH|could not determine executable to run`),

	// What the build was meant to produce and did not.
	signature("build_output_missing", "", "", `must produce ([\w./\[\]-]+);|must write (?:an executable to )?([\w./-]+?)(?:;|$)|build produced no executable jar in ([\w./-]+?)/?$|failed to compute cache key: .*"(/[^"]+)": not found|failed to calculate checksum of ref [^"]*"(/[^"]+)": not found`),
}

// pullSignatures name why pulling an image source or a Compose service's image
// failed, from the registry's own error.
var pullSignatures = []buildSignature{
	signature("build_disk_full", "", "", `no space left on device`),
	signature("build_registry_rate_limited", "", "", `toomanyrequests|pull rate limit|429 Too Many Requests`),
	signature("build_registry_auth", "", "", `unauthorized|pull access denied|denied: requested access|authentication required`),
	signature("build_image_not_found", "", "", `manifest unknown|not found|does not exist`),
	signature("build_platform_unsupported", "", "", `no match for platform in manifest`),
	signature("build_network", "", "", `no such host|i/o timeout|TLS handshake timeout|connection refused|network is unreachable|Temporary failure in name resolution`),
}
