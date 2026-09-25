package deploy

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// OutputCause is the one thing a candidate's own output — or the kernel's
// list of the sockets it listens on (runtime_listeners.go), or its exit
// status — proves about why its checks failed. It carries identifiers and
// the objects they name, never a line of output, so step evidence stays
// free of whatever else the application printed alongside it. This file is
// the one table of runtime and release-task causes.
type OutputCause struct {
	Code  string `json:"code"`
	Table string `json:"table,omitempty"`
	// Subjects are the identifiers the output named: a variable, a port, a
	// module, a migration, a loopback listener. Detail names the tool the
	// code is about.
	Subjects []string  `json:"subjects,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Fix      *CauseFix `json:"fix,omitempty"`
}

// outputCauseTitles name the runtime and release-task causes, as causeTitles
// names the build's.
var outputCauseTitles = map[string]string{
	"schema_missing":                  "Database schema not applied",
	"runtime_oom":                     "Application ran out of memory",
	"runtime_prisma_engine_missing":   "Prisma engine cannot load",
	"runtime_env_missing":             "Variable missing at runtime",
	"runtime_database_localhost":      "Database address points at localhost",
	"runtime_host_disallowed":         "Host not allowed by the application",
	"runtime_entry_missing":           "Start command's entry missing",
	"runtime_module_missing":          "Module not installed",
	"runtime_library_missing":         "System library missing at runtime",
	"runtime_cgo_required":            "Binary built without cgo",
	"runtime_version":                 "Language version mismatch",
	"runtime_exec_format":             "Binary for another architecture",
	"runtime_command_not_found":       "Start command not found",
	"runtime_origin_rejected":         "Origin rejected by the application",
	"runtime_pidfile_stale":           "Stale PID file",
	"runtime_schema_push_refused":     "Schema push refused",
	"runtime_loopback_bind":           "Application listens on localhost only",
	"runtime_port_mismatch":           "Application listens on another port",
	"runtime_start_exited":            "Start command exited",
	"runtime_sqlite_not_writable":     "SQLite database not writable",
	"runtime_master_key_invalid":      "Credentials cannot be decrypted",
	"runtime_auth_untrusted_host":     "Host not trusted by Auth.js",
	"runtime_dotenv_missing":          ".env file missing",
	"runtime_errors_hidden":           "Application errors are not logged",
	"release_migration_failed":        "Migration failed",
	"release_migration_failed_before": "Earlier migration failed",
	"release_database_not_empty":      "Database has an unmanaged schema",
	"release_database_auth_failed":    "Database refused the credentials",
	"release_database_unreachable":    "Database unreachable from release task",
	"release_env_missing":             "Variable missing in release task",
	"release_task_command_not_found":  "Release task command not found",
	"release_task_timeout":            "Release task timed out",
	"release_task_failed":             "Release task failed",
	"candidate_start_failed":          "Candidate did not start",
	"health_gate_failed":              "Health check failed",
}

// A freshly linked database is empty, and an application that queries it
// before anything applied its schema fails every request with one of these.
// Each pattern captures the table so the message can name it.
var missingTablePatterns = []*regexp.Regexp{
	regexp.MustCompile("The table `([^`]+)` does not exist in the current database"), // Prisma P2021
	regexp.MustCompile(`relation "([^"]+)" does not exist`),                          // PostgreSQL 42P01
	regexp.MustCompile(`Table '([^']+)' doesn't exist`),                              // MySQL and MariaDB 1146
	regexp.MustCompile(`no such table: ([A-Za-z0-9_.]+)`),                            // SQLite
}

// A schema step that pushes the declared model refuses a change that would
// drop or rename data, or stops to ask about it, and the start command never
// reaches the server.
var schemaPushRefusedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`Use the --accept-data-loss flag`),                         // prisma db push
	regexp.MustCompile(`We found changes that cannot be executed`),                // prisma db push
	regexp.MustCompile(`Do you still want to push changes`),                       // prisma db push
	regexp.MustCompile(`created or renamed from another (?:column|table)`),        // drizzle-kit push
	regexp.MustCompile(`THIS ACTION WILL CAUSE DATA LOSS AND CANNOT BE REVERTED`), // drizzle-kit push
	regexp.MustCompile(`Interactive prompts require a TTY terminal`),              // drizzle-kit push
}

// A SQLite file the container cannot open or write: its directory is missing
// — a relocated path whose volume is gone — or not writable by the
// container's user. SQLite words both as "unable to open database file".
var sqliteReadOnlyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`attempt to write a readonly database`),
	regexp.MustCompile(`SQLITE_READONLY`),
	regexp.MustCompile(`SQLITE_CANTOPEN|unable to open database file`),
}

// envMissingSignatures are a variable the program reads and was not given,
// shared by the runtime and the release tasks.
var envMissingSignatures = []buildSignature{
	signature("", "prisma", "environment variable", `(?:Missing required environment variable|Cannot resolve environment variable): ([A-Za-z_]\w*)`),
	signature("", "prisma", "Environment variable not found", `Environment variable not found: ([A-Za-z_]\w*)`),
	signature("", "t3-env", "Invalid environment variables", `Invalid environment variables`).
		collecting(`(?:\b|')([A-Z][A-Z0-9_]{2,})'?"?\s*(?::\s*\[|\],)`),
	signature("", "django", "environment variable", `Set the ([A-Z_][A-Z0-9_]*) environment variable`),
	signature("", "django", "SECRET_KEY", `The SECRET_KEY setting must not be empty`).naming("SECRET_KEY"),
	signature("", "python", "KeyError", `KeyError: '([A-Z][A-Z0-9_]{2,})'`).requiring(`environ`),
	signature("", "rails", "secret_key_base", "Missing .?secret_key_base.? for").naming("SECRET_KEY_BASE"),
	signature("", "authjs", "MissingSecret", `\[auth\]\[error\] MissingSecret|MissingSecret: Please define a .secret.`).naming("AUTH_SECRET"),
	signature("", "phoenix", "environment variable", `environment variable ([A-Z_][A-Z0-9_]*) is missing`),
	signature("", "leptos", "LEPTOS_", `\b(LEPTOS_[A-Z_]+)\b[^\n]*(?:NotPresent|not (?:found|present|set)|is missing)`),
}

// genericEnvMissingSignatures are the sentences any program may print about a
// variable. They are read after every more specific cause, because a program
// prints them as a warning too and carries on — "SENTRY_DSN is not set" beside
// the port it really listens on — and a line that says it is a warning is not
// read at all. "X is not defined" is left out: that is JavaScript's
// ReferenceError for an identifier. A lone word "is required" is left out:
// "JWT is required" is a request refused, not a variable.
var genericEnvMissingSignatures = []buildSignature{
	signature("", "", "", `\b([A-Z][A-Z0-9]*_[A-Z0-9_]+) (?:is not set|must be set|is required|environment variable is (?:required|missing))\b|\b([A-Z]{3,}) (?:is not set|must be set|environment variable is (?:required|missing))\b|Missing (?:required )?(?:env(?:ironment)? )?variable:? "?([A-Z][A-Z0-9_]{2,})`).
		unlessLine(`(?i)\bwarn(?:ing)?\b`),
}

// runtimeSignatures are the candidate's own output after it failed its
// checks, most specific first.
var runtimeSignatures = []buildSignature{
	signature("runtime_prisma_engine_missing", "prisma", "", `Prisma failed to detect the libssl/openssl version|libssl\.so[\d.]*: cannot open shared object file|could not locate the Query Engine for runtime "([^"\s]+)"|Unable to require\(`),
	signature("runtime_master_key_invalid", "rails", "", `ActiveSupport::MessageEncryptor::InvalidMessage`).naming("RAILS_MASTER_KEY"),
	signature("runtime_auth_untrusted_host", "authjs", "", `\[auth\]\[error\] UntrustedHost|UntrustedHost: Host must be trusted`),
	signature("runtime_dotenv_missing", "", "", `open \.env: no such file or directory|Error loading \.env file|\.env file not found`),
	signature("runtime_database_localhost", "", "", "ECONNREFUSED (?:127\\.0\\.0\\.1|::1|localhost):(\\d+)|Can't reach database server at `(?:localhost|127\\.0\\.0\\.1):(\\d+)`|connection to server at \"(?:localhost|127\\.0\\.0\\.1)\"[^,]*, port (\\d+) failed|dial tcp (?:127\\.0\\.0\\.1|\\[::1\\]|localhost):(\\d+): connect: connection refused|Can't connect to (?:local )?(?:MySQL )?server on '(?:localhost|127\\.0\\.0\\.1)(?::(\\d+))?'|(?:localhost|127\\.0\\.0\\.1):(\\d+)\\D*Connection refused|Connection refused \\(os error 111\\).*(?:127\\.0\\.0\\.1|localhost):(\\d+)"),
	signature("runtime_host_disallowed", "django", "", `Invalid HTTP_HOST header: '([^':]+)|DisallowedHost`),
	signature("runtime_host_disallowed", "vite", "Blocked request", `Blocked request\. This host \("([^"]+)"\) is not allowed`),
	signature("runtime_host_disallowed", "play", "Host not allowed", `Host not allowed`),
	signature("runtime_entry_missing", "", "", `Could not find a production build in the '([^']+)' directory|Failed to find attribute '(\w+)' in '[\w.]+'|Error loading ASGI app\.|no main manifest attribute, in (\S+)|Unable to access jarfile (\S+)|can't open file '([^']+)': \[Errno 2\]|Cannot find module '(/[^']+)'|Module not found "(file:///[^"]+)"`),
	signature("runtime_library_missing", "", "cannot open shared object file", `(lib[\w+.-]+\.so[\d.]*): cannot open shared object file`),
	// psycopg 3 without its binary extra loads libpq itself; its import error
	// also lists the optional modules it tried first, which are not the cause.
	signature("runtime_library_missing", "psycopg", "libpq library not found", `libpq library not found`).naming("libpq"),
	signature("runtime_module_missing", "", "", `ModuleNotFoundError: No module named '([\w.]+)'|Cannot find (?:module|package) '([^'/.][^']*)'`),
	signature("runtime_cgo_required", "go", "", `go-sqlite3 requires cgo|Binary was compiled with 'CGO_ENABLED=0'`),
	signature("runtime_version", "java", "UnsupportedClassVersionError", `UnsupportedClassVersionError`),
	signature("runtime_exec_format", "", "exec format error", `exec format error`),
	signature("runtime_command_not_found", "", "", `(?:^|[\s/])(?:sh|bash|dash|ash)(?:: (?:line )?\d+)?: ([\w.+@-]+): (?:command )?not found|exec: "([\w./+-]+)": executable file not found in \$PATH`),
	signature("runtime_origin_rejected", "phoenix", "Could not check origin", `Could not check origin for Phoenix\.Socket transport`),
	signature("runtime_pidfile_stale", "play", "RUNNING_PID", `This application is already running \(Or delete (\S+) file\)`),
}

// loopbackListenRE are servers that print the address they actually bound,
// so a loopback address in their line proves nothing outside the container
// can reach them. Next.js prints "localhost" whatever it binds and is not
// among them.
var loopbackListenRE = regexp.MustCompile(`(?:Uvicorn|Hypercorn|Daphne) running on https?://(?:127\.0\.0\.1|localhost):(\d+)|Listening at: https?://(?:127\.0\.0\.1|localhost):(\d+)|\* Running on https?://(?:127\.0\.0\.1|localhost):(\d+)|Listening on (?:tcp|https?)://(?:127\.0\.0\.1|localhost|\[::1\]):(\d+)|Now listening on: https?://(?:127\.0\.0\.1|localhost):(\d+)`)

// listeningPortRE are the startup lines that name the port a server listens
// on. Each is specific to a framework or server so a line about a database or
// a cache the application connects to is never read as its own port.
var listeningPortRE = regexp.MustCompile(`(?i)(?:^|\s)- Local:\s+https?://[^\s:/]+:(\d{2,5})|➜\s+Local:\s+https?://[^\s:/]+:(\d{2,5})|\blistening on (?:port )?:?(\d{2,5})\b|\blistening on:? (?:https?|tcp)://[^\s]*?:(\d{2,5})\b|(?:Uvicorn|Hypercorn|Daphne) running on https?://[^\s]*?:(\d{2,5})|Listening at: https?://[^\s]*?:(\d{2,5})|\* Running on https?://[^\s]*?:(\d{2,5})|(?:Tomcat|Netty|Jetty|Undertow) started on port(?:\(s\))?:? (\d{2,5})|Listening and serving HTTP on [^\s]*:(\d{2,5})|http server started on [^\s]*:(\d{2,5})|server (?:is )?(?:running|listening|started) (?:on|at) (?:port )?(?:https?://[^\s]*?:)?(\d{2,5})\b|with (?:Bandit|Cowboy) [\d.]+ at [^\s]*:(\d{2,5}) \(http|Listening for HTTP on [^\s]*:(\d{2,5})\b|\{HTTP/1\.1[^}]*\}\{[^}]*:(\d{2,5})\}`)

var databasePorts = map[string]string{
	"5432": "PostgreSQL", "3306": "MySQL", "6379": "Redis", "27017": "MongoDB", "5672": "RabbitMQ", "9200": "Elasticsearch",
}

// runtimeCauseContext is what a runtime remedy is computed from.
type runtimeCauseContext struct {
	build     BuildPlanConfig
	runtime   RuntimePlanConfig
	variables []ReleaseVariableSnapshot
	compose   bool
	// checks are the readiness checks that failed, whose answers say what
	// an application that printed nothing refused.
	checks []CheckEvidence
}

func applicationOutputCause(containers []ContainerDiagnostics, context runtimeCauseContext) *OutputCause {
	for _, container := range containers {
		if container.OOMKilled {
			return &OutputCause{Code: "runtime_oom", Fix: &CauseFix{Kind: fixReview, Field: "runtime.memoryMb"}}
		}
	}
	lines := []collectedLine{}
	for _, container := range containers {
		for _, line := range container.Lines {
			lines = append(lines, collectedLine{text: truncateUTF8Prefix(line.Text, collectedLineBytes)})
		}
	}
	for _, line := range lines {
		for _, pattern := range missingTablePatterns {
			if match := pattern.FindStringSubmatch(line.text); match != nil {
				return &OutputCause{Code: "schema_missing", Table: match[1]}
			}
		}
	}
	// A refused push or an unopenable SQLite file stops the start before the
	// server exists, whatever else it printed.
	for _, line := range lines {
		for _, pattern := range schemaPushRefusedPatterns {
			if pattern.MatchString(line.text) {
				return &OutputCause{Code: "runtime_schema_push_refused", Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}}
			}
		}
		for _, pattern := range sqliteReadOnlyPatterns {
			if pattern.MatchString(line.text) {
				return &OutputCause{Code: "runtime_sqlite_not_writable", Fix: &CauseFix{Kind: fixReview, Field: "runtime.mounts"}}
			}
		}
	}
	envMissing := func(match *buildMatch) *OutputCause {
		cause := &OutputCause{Code: "runtime_env_missing", Detail: match.detail, Subjects: match.subjects}
		if len(match.subjects) > 0 {
			cause.Fix = variableFix(match.subjects[0], "runtime", "", context.variables)
		}
		return cause
	}
	if match := classifyLines(envMissingSignatures, lines, -1); match != nil {
		return envMissing(match)
	}
	if name := pydanticMissingField(lines); name != "" {
		cause := &OutputCause{Code: "runtime_env_missing", Detail: "pydantic", Subjects: []string{name}}
		cause.Fix = variableFix(strings.ToUpper(name), "runtime", "", context.variables)
		return cause
	}
	if match := classifyLines(runtimeSignatures, lines, -1); match != nil {
		cause := &OutputCause{Code: match.code, Detail: match.detail, Subjects: match.subjects}
		switch cause.Code {
		case "runtime_database_localhost":
			if len(cause.Subjects) > 0 && databasePorts[cause.Subjects[0]] != "" {
				cause.Detail = databasePorts[cause.Subjects[0]]
			}
			cause.Fix = &CauseFix{Kind: fixReview, Field: "dependencies"}
		case "runtime_entry_missing":
			cause.Fix = &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}
		case "runtime_master_key_invalid":
			cause.Fix = variableFix("RAILS_MASTER_KEY", "runtime", "", context.variables)
		case "runtime_auth_untrusted_host":
			cause.Subjects = []string{"AUTH_TRUST_HOST"}
			cause.Fix = variableFix("AUTH_TRUST_HOST", "runtime", "true", context.variables)
		case "runtime_origin_rejected":
			cause.Subjects = []string{"PHX_HOST"}
			cause.Fix = variableFix("PHX_HOST", "runtime", "", context.variables)
		case "runtime_library_missing":
			if context.build.Method == BuildRecipe && context.build.Recipe == "python" && len(cause.Subjects) > 0 {
				if packages := pythonLibraryPackages[cause.Subjects[0]]; packages != "" {
					cause.Fix = &CauseFix{Kind: fixSetBuild, Field: "configuration.build.systemPackages", Value: packages}
				}
			}
		}
		return cause
	}
	for _, line := range lines {
		if match := loopbackListenRE.FindStringSubmatch(line.text); match != nil && !anyLineMatches(lines, runningOnAllAddressesRE) {
			return loopbackBindCause(addFirstGroup(nil, match), context)
		}
	}
	// A server that says nothing about where it listens is read from the
	// kernel's list of its sockets: every one on loopback is a server that is
	// up and that nothing outside its container reaches.
	if listener := loopbackOnlyListener(containers); listener != "" {
		return loopbackBindCause([]string{listener}, context)
	}
	if context.runtime.InternalPort > 0 && !context.compose && !context.runtime.HostNetwork {
		// An application may print several listeners (a metrics port beside
		// its own); only one that never names the planned port is a mismatch.
		planned := strconv.Itoa(context.runtime.InternalPort)
		var printed []string
		for _, line := range lines {
			if match := listeningPortRE.FindStringSubmatch(line.text); match != nil {
				printed = addFirstGroup(printed, match)
			}
		}
		if len(printed) > 0 && !slices.Contains(printed, planned) {
			return &OutputCause{Code: "runtime_port_mismatch", Subjects: printed[:1],
				Fix: &CauseFix{Kind: fixSetRuntime, Field: "runtime.internalPort", Value: printed[0]}}
		}
	}
	if match := classifyLines(genericEnvMissingSignatures, lines, -1); match != nil {
		return envMissing(match)
	}
	if cause := djangoHiddenErrorCause(lines, context); cause != nil {
		return cause
	}
	if startCommandExited(containers) {
		return &OutputCause{Code: "runtime_start_exited", Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}}
	}
	return nil
}

// loopbackBindCause is a candidate listening on loopback only, with the start
// command bound to every interface when its flags say where it listens.
func loopbackBindCause(subjects []string, context runtimeCauseContext) *OutputCause {
	cause := &OutputCause{Code: "runtime_loopback_bind", Subjects: subjects}
	if command := loopbackStartCommand(context.build.StartCommand); command != "" && context.build.Method == BuildRecipe {
		cause.Fix = &CauseFix{Kind: fixSetBuild, Field: "configuration.build.startCommand", Value: command}
	}
	return cause
}

// startCommandExited says a start command returned instead of serving —
// `pm2 start`, `forever start`, a trailing `&`, a one-off script — which
// ends the container with exit code 0 while the restart policy starts it
// again until readiness gives up. The output is usually a line saying the
// application started, which reads like success; the exit status says
// otherwise. Several containers are a Compose stack, where a service that
// finishes (a migration, a seed) exits 0 by design.
func startCommandExited(containers []ContainerDiagnostics) bool {
	if len(containers) != 1 {
		return false
	}
	container := containers[0]
	if container.OOMKilled || container.ExitCode != 0 || container.State == "running" {
		return false
	}
	switch container.State {
	case "exited", "restarting", "dead":
		return true
	}
	return container.RestartCount > 0
}

// Werkzeug bound to 0.0.0.0 prints its loopback address too, after saying so.
var runningOnAllAddressesRE = regexp.MustCompile(`Running on all addresses`)

// pydanticMissingField reads pydantic-settings' validation error, which names
// the field on the line before "Field required".
func pydanticMissingField(lines []collectedLine) string {
	for index := 1; index < len(lines); index++ {
		if strings.Contains(lines[index].text, "Field required [type=missing") {
			name := strings.TrimSpace(lines[index-1].text)
			if envKeyRe.MatchString(name) {
				return name
			}
		}
	}
	return ""
}

var loopbackHostRE = regexp.MustCompile(`((?:--host|--bind|-b|-H)[ =])(?:127\.0\.0\.1|localhost)\b`)

// loopbackStartCommand is the start command bound to every interface, for
// the servers whose flags say where they listen.
func loopbackStartCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if loopbackHostRE.MatchString(command) {
		return loopbackHostRE.ReplaceAllString(command, "${1}0.0.0.0")
	}
	fields := strings.Fields(command)
	for len(fields) > 1 && (fields[0] == "exec" || fields[0] == "poetry" || fields[0] == "uv" || fields[0] == "pipenv" || fields[0] == "run" || fields[0] == "python" || fields[0] == "python3" || fields[0] == "-m") {
		fields = fields[1:]
	}
	if len(fields) == 0 || strings.Contains(command, "&&") {
		return ""
	}
	switch fields[0] {
	case "uvicorn", "hypercorn":
		return command + " --host 0.0.0.0"
	case "flask":
		return command + " --host 0.0.0.0"
	case "rails", "bin/rails":
		return command + " -b 0.0.0.0"
	}
	return ""
}

func (c *OutputCause) sentence() string {
	if c == nil {
		return ""
	}
	subject := ""
	if len(c.Subjects) > 0 {
		subject = c.Subjects[0]
	}
	switch c.Code {
	case "schema_missing":
		return fmt.Sprintf("the application reports that table %s does not exist in its database, so the linked database has not received the application's schema; apply it before the application starts — for Prisma, `prisma migrate deploy`, or `prisma db push` when the project has no migrations", c.Table)
	case "runtime_oom":
		return "the kernel killed the application for exceeding its memory limit; raise the runtime memory limit or reduce what the application holds in memory"
	case "runtime_prisma_engine_missing":
		return "Prisma's query engine could not load, because the image lacks the OpenSSL release it was generated for; install openssl in the image or set the Prisma generator's binaryTargets for it"
	case "runtime_env_missing":
		return "the application reads " + orDefault(strings.Join(c.Subjects, ", "), "a variable") + " and was not given it; " +
			variableAction(c.Fix, subject, "set it for the runtime")
	case "runtime_database_localhost":
		return "the application connects to " + orDefault(c.Detail, "a database") + " on localhost, which inside the container is the application itself; link a database (it becomes a db-N.jd.internal address) or point the variable at a reachable host"
	case "runtime_host_disallowed":
		return "the application refused the request's Host header" + parenthesized(subject) + "; allow the deployment's hostname and the readiness check's address (for Django, ALLOWED_HOSTS)"
	case "runtime_entry_missing":
		return "the start command runs a file or object the image does not contain" + parenthesized(subject) + "; set the start command to what the build produced"
	case "runtime_module_missing":
		return "the application imports " + orDefault(subject, "a module") + ", which the image does not have installed; add it to the project's dependencies"
	case "runtime_library_missing":
		return "the application needs the system library " + orDefault(subject, "it names") + ", which the image does not have; build with a Dockerfile that installs it, or use a dependency that bundles it"
	case "runtime_cgo_required":
		return "the binary was built without cgo, and a dependency needs it; switch to a pure-Go dependency such as modernc.org/sqlite, or build with a Dockerfile"
	case "runtime_version":
		return "the application was compiled for a newer runtime than the image runs; align the release the build targets with the runtime image"
	case "runtime_exec_format":
		return "the image's entry is built for another architecture or is a script with Windows line endings; build for this server, or convert the script to LF"
	case "runtime_command_not_found":
		return "`" + orDefault(subject, "the start command") + "` is not installed in the runtime image; start the application with a command the image has"
	case "runtime_origin_rejected":
		return "Phoenix rejected the socket's origin; set PHX_HOST to the domain the site is served on, or `check_origin` to the deployment's hostname"
	case "runtime_pidfile_stale":
		return "Play found a RUNNING_PID file from a previous start; start it with `-Dpidfile.path=/dev/null`"
	case "runtime_schema_push_refused":
		return "the start command's schema push refused a change that would drop or rename data (or stopped to ask about it), so the server never started; commit migrations so the start applies them — `prisma migrate dev` then `prisma migrate deploy`, or `drizzle-kit generate` then `drizzle-kit migrate` — or apply the change to the database by hand"
	case "runtime_sqlite_not_writable":
		return "the application cannot open or write its SQLite database file: the directory holding it is missing or not writable by the container's user; keep the file in the image's data directory or on a volume mounted there, which that user owns"
	case "runtime_master_key_invalid":
		return "Rails cannot decrypt its committed credentials with RAILS_MASTER_KEY; paste the contents of config/master.key under the project's Variables"
	case "runtime_auth_untrusted_host":
		return "Auth.js does not trust the host it is served on; set AUTH_TRUST_HOST to true, or AUTH_URL to the site's address"
	case "runtime_dotenv_missing":
		return "the application exits because it cannot open .env; the dashboard supplies variables in the environment, so create an empty .env in the image or ignore the load error"
	case "runtime_loopback_bind":
		where := "on localhost only"
		if strings.Contains(subject, ":") {
			where = "only on " + subject + ", a loopback address"
		}
		if c.Fix != nil {
			return "the application listens " + where + ", which nothing outside its container can reach; start it with `" + c.Fix.Value + "`"
		}
		return "the application listens " + where + ", which nothing outside its container can reach; bind it to 0.0.0.0 (or read the address from HOST) so the proxy and the readiness check can connect"
	case "runtime_port_mismatch":
		return "the application listens on port " + subject + ", not the configured internal port; set the internal port to " + subject + " or make the application listen on $PORT"
	case "runtime_errors_hidden":
		return "Django answered " + orDefault(subject, "an error") + " and logged nothing: with DEBUG off it sends errors to the admins' email, not to its output; add a LOGGING setting with a console handler to see the cause (the preflight finding django_errors_unlogged has one)"
	case "runtime_start_exited":
		return "the application's start command finished with exit code 0 instead of serving, so the container stopped; a start command must keep the server in the foreground — pm2 start, forever start, a trailing & and a one-off script all return at once (use pm2-runtime, or run the server directly)"
	}
	return ""
}

// diagnosticsSuffix is what the candidate's own output adds to a failure
// message: the one cause it proves, if any, and where the rest of it is.
func diagnosticsSuffix(diagnostics *runtimeDiagnosticsEvidence) string {
	if diagnostics == nil || diagnostics.Lines == 0 {
		if diagnostics != nil && diagnostics.Cause != nil {
			return "; " + diagnostics.Cause.sentence()
		}
		return ""
	}
	suffix := "; the application's last output is in the build log"
	if cause := diagnostics.Cause.sentence(); cause != "" {
		suffix = "; " + cause + suffix
	}
	return suffix
}

// releaseTaskSignatures are a release task's own output after it failed.
var releaseTaskSignatures = []buildSignature{
	signature("release_migration_failed_before", "prisma", "P3009", `P3009`).collecting("The `([\\w-]+)` migration started at"),
	signature("release_migration_failed", "prisma", "", `P3018|A migration failed to apply`).collecting(`Migration name: ([\w-]+)`),
	signature("release_database_not_empty", "prisma", "P3005", `P3005`),
	signature("release_database_auth_failed", "", "", `P1000|Authentication failed against database server|password authentication failed|Access denied for user`),
	signature("release_database_unreachable", "", "", `P1001|Can't reach database server|ECONNREFUSED|could not translate host name|getaddrinfo (?:ENOTFOUND|EAI_AGAIN)`).
		collecting("Can't reach database server at `([^`]+)`|getaddrinfo (?:ENOTFOUND|EAI_AGAIN) ([\\w.-]+)"),
	signature("release_task_command_not_found", "", "", `(?:^|[\s/])(?:sh|bash|dash|ash)(?:: (?:line )?\d+)?: ([\w.+@-]+): (?:command )?not found`),
}

// releaseTaskCause names why a release task failed from its own output, or
// returns nil when the output proves nothing in particular.
func releaseTaskCause(lines []collectedLine, exitCode int, variables []ReleaseVariableSnapshot) *OutputCause {
	envMissing := func(match *buildMatch) *OutputCause {
		cause := &OutputCause{Code: "release_env_missing", Detail: match.detail, Subjects: match.subjects}
		if len(match.subjects) > 0 {
			cause.Fix = variableFix(match.subjects[0], "release_task", "", variables)
		}
		return cause
	}
	if match := classifyLines(envMissingSignatures, lines, exitCode); match != nil {
		return envMissing(match)
	}
	match := classifyLines(releaseTaskSignatures, lines, exitCode)
	if match == nil {
		if match := classifyLines(genericEnvMissingSignatures, lines, exitCode); match != nil {
			return envMissing(match)
		}
		return nil
	}
	cause := &OutputCause{Code: match.code, Detail: match.detail, Subjects: match.subjects}
	if cause.Code == "release_task_command_not_found" {
		cause.Fix = &CauseFix{Kind: fixReview, Field: "configuration.build.releaseTasks"}
	}
	return cause
}

func (c *OutputCause) releaseSentence() string {
	subject := ""
	if len(c.Subjects) > 0 {
		subject = c.Subjects[0]
	}
	switch c.Code {
	case "release_migration_failed_before":
		return "an earlier migration" + parenthesized(subject) + " failed in this database, so Prisma refuses to apply more; resolve it with `prisma migrate resolve` and deploy again"
	case "release_migration_failed":
		return "the migration" + parenthesized(subject) + " failed to apply; its database error is in the build log"
	case "release_database_not_empty":
		return "the database already has a schema Prisma did not create; baseline it with `prisma migrate resolve --applied` for the initial migration"
	case "release_database_auth_failed":
		return "the database refused the task's credentials; check the connection variable given to the release task"
	case "release_database_unreachable":
		if c.Detail == "release image" {
			return "the task could not reach the database" + parenthesized(subject) + " from the release image, which joins the project's database networks; check that the database is linked and running, and that the connection variable names its address"
		}
		return "the task could not reach the database" + parenthesized(subject) + "; a task without a runner runs in the dashboard's own shell, where a project network address such as db-N.jd.internal does not resolve — run it in the release image instead, which joins the database's network"
	case "release_env_missing":
		return "the task reads " + orDefault(strings.Join(c.Subjects, ", "), "a variable") + " and was not given it; " +
			variableAction(c.Fix, subject, "give it the release_task scope")
	case "release_task_command_not_found":
		if c.Detail == "release image" {
			return "`" + orDefault(subject, "the command") + "` is not installed in the release image the task runs in; install it in the build, or run it through the package manager the image has (`npx`, `bunx`, `pnpm exec`)"
		}
		return "`" + orDefault(subject, "the command") + "` is not installed where this release task runs: a task without a runner runs in the dashboard's own shell over the unbuilt source, without the application's runtime or dependencies; run it in the release image instead, where the build installed them"
	}
	return ""
}

// outputCauseCode reports whether a step's error code is a named runtime
// cause, whose message scanRunStep rebuilds from the health evidence.
func outputCauseCode(code string) bool {
	return code == "schema_missing" || (strings.HasPrefix(code, "runtime_") && outputCauseTitles[code] != "")
}

// applicationErrorLineRE is a line that says what failed: a traceback, an
// exception, an error from Django's own loggers.
var applicationErrorLineRE = regexp.MustCompile(`Traceback|Error|Exception|DisallowedHost|Invalid HTTP_HOST`)

// djangoHiddenErrorCause names a Django candidate that answered the check
// with an error and wrote nothing about it: with DEBUG off and no LOGGING
// setting, Django routes request errors — a DisallowedHost 400, a 500 — to
// mail_admins only. A 400 is its host allowlist and a 5xx an error it hid;
// a 401, 403 or 404 is an answer, which the readiness classification names.
func djangoHiddenErrorCause(lines []collectedLine, context runtimeCauseContext) *OutputCause {
	if context.build.Framework != "django" {
		return nil
	}
	status := 0
	for _, check := range context.checks {
		for _, attempt := range check.Attempts {
			if attempt.StatusCode >= 400 {
				status = attempt.StatusCode
			}
		}
	}
	if status == 0 || anyLineMatches(lines, applicationErrorLineRE) {
		return nil
	}
	switch {
	case status == 400 || status == 421:
		return &OutputCause{Code: "runtime_host_disallowed", Detail: "django", Subjects: []string{strconv.Itoa(status)},
			Fix: &CauseFix{Kind: fixReview, Field: "variables"}}
	case status >= 500:
		return &OutputCause{Code: "runtime_errors_hidden", Detail: "django", Subjects: []string{strconv.Itoa(status)},
			Fix: &CauseFix{Kind: fixReview, Field: "configuration.build"}}
	}
	return nil
}
