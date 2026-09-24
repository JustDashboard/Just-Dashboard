package deploy

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// BuildCause is the one thing a failed build's own output proves about why it
// failed: a code from a fixed table, where in the build it happened, and the
// identifiers the failure named. Like OutputCause it carries no line of
// output, so step evidence stays free of whatever else the build printed; the
// line is in the transcript, and LineSeq says which one.
type BuildCause struct {
	Code     string   `json:"code"`
	Phase    string   `json:"phase"`
	Command  string   `json:"command,omitempty"`
	ExitCode int      `json:"exitCode"`
	Subjects []string `json:"subjects,omitempty"`
	// Detail names what the code is about when one code covers several
	// ecosystems: "package-lock.json", "go.sum", "python". It always comes from
	// the signature table, never from the output.
	Detail  string    `json:"detail,omitempty"`
	LineSeq int64     `json:"lineSeq,omitempty"`
	Service string    `json:"service,omitempty"`
	Fix     *CauseFix `json:"fix,omitempty"`
}

// CauseFix is the one plan change a cause's evidence supports, for a button
// that opens the field it targets. Value is a computed constant — a package
// manager, a rewritten command, a flag — never anything a variable holds.
type CauseFix struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
	Value string `json:"value,omitempty"`
	Scope string `json:"scope,omitempty"`
}

// Where in the build a failure happened, read from which instruction failed.
const (
	phaseInstall     = "install"
	phaseBuild       = "build"
	phaseOutputCheck = "output_check"
	phaseSetup       = "setup"
	phaseDockerfile  = "dockerfile"
	phaseBaseImage   = "base_image"
	phasePull        = "pull"
)

// Fix kinds. set_build and set_runtime replace a plan field with Value;
// add_variable creates the named variable in Scope; variable_scope adds Scope
// to an existing variable; review opens a field whose right value the output
// cannot prove.
const (
	fixSetBuild      = "set_build"
	fixSetRuntime    = "set_runtime"
	fixAddVariable   = "add_variable"
	fixVariableScope = "variable_scope"
	fixReview        = "review"
)

// composeServiceBuildStatus opens each Compose service's build on the status
// stream, so the transcript and the collector both know which buildx run a
// line belongs to: every run numbers its steps from #1 again.
const composeServiceBuildStatus = "Building Compose service "

// ComposeServiceError is a Compose service whose image could not be built.
type ComposeServiceError struct {
	Service string
	Err     error
}

func (e *ComposeServiceError) Error() string {
	return fmt.Sprintf("build Compose service %s: %v", e.Service, e.Err)
}

func (e *ComposeServiceError) Unwrap() error { return e.Err }

// Bounds on what the collector keeps. A vertex ring holds the step that will
// be diagnosed; the stream ring holds what surrounds it, including BuildKit's
// closing replay, which carries no step number.
const (
	collectedLineBytes  = 2048
	vertexRingLines     = 400
	vertexRingBytes     = 64 << 10
	streamRingLines     = 1200
	streamRingBytes     = 64 << 10
	openVertexLimit     = 48
	causeCommandLength  = 160
	causeSubjectLimit   = 5
	causeSubjectLength  = 128
	outputRingLines     = 400
	outputRingBytes     = 64 << 10
	causeSentenceLength = 1200
)

type collectedLine struct {
	text string
	seq  int64
}

// lineRing keeps the newest lines within a line and byte budget and refuses a
// line it already holds: BuildKit replays a clipped step's tail at the end of
// the step, and repeats a failed step's last lines in its closing error block.
type lineRing struct {
	lines    []collectedLine
	bytes    int
	held     map[string]int
	maxLines int
	maxBytes int
}

func newLineRing(maxLines, maxBytes int) *lineRing {
	return &lineRing{held: map[string]int{}, maxLines: maxLines, maxBytes: maxBytes}
}

func (r *lineRing) add(line collectedLine) {
	if strings.TrimSpace(line.text) == "" || r.held[line.text] > 0 {
		return
	}
	r.lines = append(r.lines, line)
	r.held[line.text]++
	r.bytes += len(line.text)
	for len(r.lines) > r.maxLines || r.bytes > r.maxBytes {
		oldest := r.lines[0]
		r.lines = r.lines[1:]
		r.bytes -= len(oldest.text)
		if r.held[oldest.text]--; r.held[oldest.text] <= 0 {
			delete(r.held, oldest.text)
		}
	}
	if cap(r.lines) > 4*r.maxLines {
		r.lines = append([]collectedLine(nil), r.lines...)
	}
}

var (
	// "#10 1.303 npm error code EUSAGE": a step's own output.
	buildKitOutputRE = regexp.MustCompile(`^#(\d+) \d+\.\d+ (.*)$`)
	// "#10 [4/5] RUN npm ci": a step starting.
	buildKitHeaderRE = regexp.MustCompile(`^#(\d+) \[[^\]]*\] (.+)$`)
	// "#10 DONE 0.4s", "#10 CACHED": a step that finished without failing.
	buildKitClosedRE = regexp.MustCompile(`^#(\d+) (?:DONE|CACHED)\b`)
	// "#7 ERROR: failed to calculate checksum …": a step's own failure.
	buildKitErrorRE = regexp.MustCompile(`^#(\d+) ERROR: (.*)$`)
	// Any other numbered line: transfers, resolves, extraction progress.
	buildKitVertexRE = regexp.MustCompile(`^#\d+ `)
	// "1.303 npm error code EUSAGE": the closing replay, without its step.
	buildKitReplayRE = regexp.MustCompile(`^\d+\.\d+ (.*)$`)
	// The closing block's frame and Dockerfile excerpt repeat the failed
	// instruction's text, which is the plan's, not what the build printed.
	buildKitFrameRE = regexp.MustCompile(`^(?:-{3,}|\s*\d+ \|.*| > \[.*|Dockerfile(?::\d+)?|ERROR: .*process ".*" did not complete successfully.*)$`)
)

// buildOutputCollector reads a build's transcript as it is persisted, after
// redaction, and keeps just enough of it to name a failure: a ring per open
// BuildKit step and one over the whole stream. It is fed by one goroutine.
type buildOutputCollector struct {
	seq      func() int64
	service  string
	vertices map[int]*lineRing
	names    map[int]string
	order    []int
	stream   *lineRing
}

func newBuildOutputCollector(seq func() int64) *buildOutputCollector {
	if seq == nil {
		seq = func() int64 { return 0 }
	}
	return &buildOutputCollector{
		seq: seq, vertices: map[int]*lineRing{}, names: map[int]string{},
		stream: newLineRing(streamRingLines, streamRingBytes),
	}
}

func (c *buildOutputCollector) observe(line BuildLog) {
	if line.Stream == "status" {
		// The engine's own sentences separate one buildx run from the next.
		c.vertices, c.names, c.order = map[int]*lineRing{}, map[int]string{}, nil
		c.stream = newLineRing(streamRingLines, streamRingBytes)
		c.service = ""
		if service, ok := strings.CutPrefix(line.Text, composeServiceBuildStatus); ok {
			c.service = service
		}
		return
	}
	text := strings.TrimRight(truncateUTF8Prefix(line.Text, collectedLineBytes), "\r")
	if buildKitFrameRE.MatchString(text) {
		return
	}
	if match := buildKitHeaderRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		if _, open := c.names[vertex]; !open {
			c.order = append(c.order, vertex)
		}
		c.names[vertex] = match[2]
		return
	}
	if match := buildKitClosedRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		c.close(vertex)
		return
	}
	seq := c.seq()
	if match := buildKitOutputRE.FindStringSubmatch(text); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		c.vertex(vertex).add(collectedLine{text: match[2], seq: seq})
		c.stream.add(collectedLine{text: match[2], seq: seq})
		return
	}
	if match := buildKitErrorRE.FindStringSubmatch(text); match != nil {
		if strings.HasPrefix(match[2], `process "`) {
			// The failed process's own command line, which dockerx has read.
			return
		}
		vertex, _ := strconv.Atoi(match[1])
		c.vertex(vertex).add(collectedLine{text: match[2], seq: seq})
		c.stream.add(collectedLine{text: match[2], seq: seq})
		return
	}
	if buildKitVertexRE.MatchString(text) {
		return
	}
	if match := buildKitReplayRE.FindStringSubmatch(text); match != nil {
		text = match[1]
	}
	c.stream.add(collectedLine{text: text, seq: seq})
}

func (c *buildOutputCollector) vertex(vertex int) *lineRing {
	if ring := c.vertices[vertex]; ring != nil {
		return ring
	}
	if len(c.vertices) >= openVertexLimit {
		// A step that is still writing after this many others started is
		// the least likely to be the failure; its lines stay in the stream.
		for _, oldest := range c.order {
			if c.vertices[oldest] != nil {
				delete(c.vertices, oldest)
				break
			}
		}
	}
	ring := newLineRing(vertexRingLines, vertexRingBytes)
	c.vertices[vertex] = ring
	return ring
}

func (c *buildOutputCollector) close(vertex int) {
	delete(c.vertices, vertex)
	delete(c.names, vertex)
	for index, open := range c.order {
		if open == vertex {
			c.order = append(c.order[:index], c.order[index+1:]...)
			break
		}
	}
}

// stepLines is what the step BuildKit named as failed printed, if it is
// still held.
func (c *buildOutputCollector) stepLines(step int) []collectedLine {
	if ring := c.vertices[step]; step > 0 && ring != nil {
		return ring.lines
	}
	return nil
}

// runningInstruction is the newest step that had started and not finished:
// what a build that ran out of time was still doing.
func (c *buildOutputCollector) runningInstruction() string {
	for index := len(c.order) - 1; index >= 0; index-- {
		if name := c.names[c.order[index]]; strings.HasPrefix(name, "RUN ") {
			return name
		}
	}
	if len(c.order) > 0 {
		return c.names[c.order[len(c.order)-1]]
	}
	return ""
}

// buildSignature is one known failure: a pattern over single lines whose
// first group, when it has one, is the subject the failure names.
type buildSignature struct {
	code   string
	detail string
	// needle is a literal every matching line contains, checked before the
	// pattern so a long transcript is not run through every expression.
	needle  string
	pattern *regexp.Regexp
	// exit, when set, is the exit code the failed step must have ended with.
	exit int
	// requires is a second pattern some line in the same scope must match.
	requires *regexp.Regexp
	// subjects, when set, collects the subjects from every line in scope in
	// place of the pattern's own group.
	subjects *regexp.Regexp
	// subject, when set, is the fixed subject a match names.
	subject string
}

type buildMatch struct {
	code, detail string
	subjects     []string
	seq          int64
}

func (s buildSignature) match(lines []collectedLine, exitCode int) *buildMatch {
	if s.exit != 0 && exitCode != s.exit {
		return nil
	}
	first := -1
	var subjects []string
	for index, line := range lines {
		if s.needle != "" && !strings.Contains(line.text, s.needle) {
			continue
		}
		groups := s.pattern.FindStringSubmatch(line.text)
		if groups == nil {
			continue
		}
		if first < 0 {
			first = index
		}
		if s.subjects == nil && s.subject == "" {
			subjects = addFirstGroup(subjects, groups)
		}
	}
	if first < 0 {
		return nil
	}
	if s.requires != nil && !anyLineMatches(lines, s.requires) {
		return nil
	}
	if s.subject != "" {
		subjects = addCauseSubject(subjects, s.subject)
	}
	if s.subjects != nil {
		for _, line := range lines {
			for _, groups := range s.subjects.FindAllStringSubmatch(line.text, -1) {
				subjects = addFirstGroup(subjects, groups)
			}
		}
	}
	return &buildMatch{code: s.code, detail: s.detail, subjects: subjects, seq: lines[first].seq}
}

// addFirstGroup adds the first group a pattern filled, as one subject or, when
// the output listed several ("esbuild, sharp"), as each of them.
func addFirstGroup(subjects []string, groups []string) []string {
	for _, group := range groups[1:] {
		if group == "" {
			continue
		}
		for _, part := range strings.Split(group, ", ") {
			subjects = addCauseSubject(subjects, part)
		}
		break
	}
	return subjects
}

func anyLineMatches(lines []collectedLine, pattern *regexp.Regexp) bool {
	for _, line := range lines {
		if pattern.MatchString(line.text) {
			return true
		}
	}
	return false
}

// causeSubjectRE is what a subject may look like: an identifier, a version, a
// path or a package name. Nothing with whitespace or quotes survives, so a
// subject can never carry a sentence of output into evidence.
var causeSubjectRE = regexp.MustCompile(`^[A-Za-z0-9@._/+:~^=<>!*|,\[\]$-]{1,128}$`)

func addCauseSubject(subjects []string, value string) []string {
	// Only quotes and trailing punctuation are the sentence's; a leading dot
	// is the subject's own (".next", ".sqlx").
	value = strings.Trim(strings.TrimRight(strings.TrimSpace(value), ".,;:"), `"'`+"`")
	if len(subjects) >= causeSubjectLimit || !causeSubjectRE.MatchString(value) {
		return subjects
	}
	for _, existing := range subjects {
		if existing == value {
			return subjects
		}
	}
	return append(subjects, value)
}

func classifyLines(signatures []buildSignature, lines []collectedLine, exitCode int) *buildMatch {
	if len(lines) == 0 {
		return nil
	}
	for _, signature := range signatures {
		if match := signature.match(lines, exitCode); match != nil {
			return match
		}
	}
	return nil
}

// causeContext is what a cause's remedy is computed from: the plan the run
// built, what detection read in the exact commit, and the variables the run
// was given (names and scopes only).
type causeContext struct {
	build      BuildPlanConfig
	runtime    RuntimePlanConfig
	prepared   PreparedBuild
	candidate  *causeCandidate
	variables  []ReleaseVariableSnapshot
	hostMemory int64
}

// causeCandidate is the part of a detected candidate a remedy reads. It is
// decoded from JSON — analyze_plan's recorded candidate or the plan's stored
// one — so it reads lockfile evidence whichever detector wrote it.
type causeCandidate struct {
	Recipe          string `json:"recipe"`
	PackageManager  string `json:"packageManager"`
	OutputDirectory string `json:"outputDirectory"`
	StartCommand    string `json:"startCommand"`
	Port            int    `json:"port"`
	Lockfiles       []struct {
		Path    string   `json:"path"`
		Manager string   `json:"manager"`
		State   string   `json:"state"`
		Missing []string `json:"missing"`
	} `json:"lockfiles"`
}

// buildFailureCause names a failed build step from what it printed. err is
// what the builder returned; collector saw the redacted transcript. It
// returns nil for an error that is not a build's own failure (a missing
// variable binding, an invalid plan), which keeps its existing code.
func buildFailureCause(err error, collector *buildOutputCollector, context causeContext, redact func(string) string) *BuildCause {
	var composeFailure *ComposeServiceError
	service := ""
	if errors.As(err, &composeFailure) {
		service = composeFailure.Service
	}
	if redact == nil {
		redact = func(value string) string { return value }
	}
	if errors.Is(err, dockerx.ErrBuildTimeout) {
		instruction := redact(collector.runningInstruction())
		command, _ := strings.CutPrefix(instruction, "RUN ")
		cause := &BuildCause{Code: "build_timeout", ExitCode: -1, Service: service}
		if strings.HasPrefix(instruction, "RUN ") {
			cause.Command = boundedCauseCommand(stripRunFlags(command))
			cause.Phase = buildPhase(context, cause.Command, instruction)
		}
		return cause
	}
	var failure *dockerx.BuildError
	if !errors.As(err, &failure) {
		// A pull that failed (an image source, a Compose service's image)
		// says why in its own error, which is the registry's text.
		if !isPullFailure(err) {
			return nil
		}
		lines := append([]collectedLine(nil), collector.stream.lines...)
		lines = append(lines, collectedLine{text: truncateUTF8Prefix(redact(err.Error()), collectedLineBytes)})
		cause := &BuildCause{Code: "build_failed", Phase: phasePull, ExitCode: -1, Service: service}
		if match := classifyLines(pullSignatures, lines, -1); match != nil {
			cause.Code, cause.Detail, cause.Subjects, cause.LineSeq = match.code, match.detail, match.subjects, match.seq
		}
		return cause
	}
	cause := &BuildCause{Code: "build_failed", ExitCode: failure.ExitCode, Service: service}
	if failure.Command != "" {
		cause.Command = boundedCauseCommand(redact(failure.Command))
	}
	cause.Phase = buildPhase(context, cause.Command, failure.Instruction)
	if cause.Command == "" {
		reason := redact(failure.Reason)
		switch {
		case strings.Contains(reason, "dockerfile parse error"):
			cause.Code, cause.Phase = "build_dockerfile_invalid", phaseDockerfile
			return cause
		case strings.Contains(reason, "failed to resolve source metadata") || strings.Contains(reason, "pull access denied"):
			cause.Phase = phaseBaseImage
		}
	}
	match := classifyLines(buildSignatures, collector.stepLines(failure.Step), failure.ExitCode)
	if match == nil {
		lines := collector.stream.lines
		if failure.Reason != "" {
			lines = append(append([]collectedLine(nil), lines...),
				collectedLine{text: truncateUTF8Prefix(redact(failure.Reason), collectedLineBytes)})
		}
		match = classifyLines(buildSignatures, lines, failure.ExitCode)
	}
	if match != nil {
		cause.Code, cause.Detail, cause.Subjects, cause.LineSeq = match.code, match.detail, match.subjects, match.seq
	}
	if cause.Code == "build_output_missing" && cause.Phase == phaseDockerfile {
		cause.Code = "build_copy_source_missing"
	}
	if cause.Code == "build_lockfile_out_of_sync" && cause.Detail == "" {
		cause.Detail = lockfileForCommand(cause.Command)
	}
	cause.Fix = buildCauseFix(cause, context)
	return cause
}

func isPullFailure(err error) bool {
	text := err.Error()
	return strings.Contains(text, "pull image:") || strings.Contains(text, "pull Compose service") ||
		strings.Contains(text, "resolve Compose service")
}

// installCommandRE names the recipes' dependency installers: whatever a
// generated Dockerfile runs before the plan's own build command.
var installCommandRE = regexp.MustCompile(`(?:^|&& |; )(?:npm (?:ci|install)|corepack enable|pnpm install|yarn install|yarn$|bun install|pip3? install|python3? -m pip|uv (?:sync|pip)|poetry install|pipenv install|go mod download|cargo fetch|mvn .*dependency:|gradle .*dependencies|\./gradlew .*dependencies|dotnet restore|deno (?:install|cache)|composer install|bundle install|mix deps\.get|apk add|apt-get|install-php-extensions)`)

// buildCommandRE names the recipes' default build commands, for a plan that
// left its own build command empty.
var buildCommandRE = regexp.MustCompile(`^(?:go build|cargo build|mvn |\./mvnw |gradle |\./gradlew |dotnet publish|deno task build|(?:npm|pnpm|yarn|bun) run build|(?:npx |bunx )?(?:next|vite|astro|nuxt|ng) build)`)

// guardCommandRE is the recipes' own output check: a test that prints what the
// build was meant to produce and fails.
var guardCommandRE = regexp.MustCompile(`' >&2; exit 1\)$`)

func buildPhase(context causeContext, command, instruction string) string {
	method := context.build.Method
	if method == BuildDockerfile || method == BuildCompose {
		return phaseDockerfile
	}
	if command == "" {
		if strings.HasPrefix(instruction, "COPY") {
			return phaseOutputCheck
		}
		if strings.HasPrefix(instruction, "FROM") {
			return phaseBaseImage
		}
		return phaseBuild
	}
	trimmed := strings.TrimSpace(command)
	switch {
	case guardCommandRE.MatchString(trimmed):
		return phaseOutputCheck
	case trimmed == strings.TrimSpace(context.build.BuildCommand) && trimmed != "":
		return phaseBuild
	case installCommandRE.MatchString(trimmed):
		return phaseInstall
	case buildCommandRE.MatchString(trimmed):
		return phaseBuild
	}
	return phaseSetup
}

// stripRunFlags removes a RUN instruction's own flags, which the step's name
// carries and its process does not: `--mount=type=secret,…`.
func stripRunFlags(command string) string {
	command = strings.TrimSpace(command)
	for strings.HasPrefix(command, "--") {
		_, rest, found := strings.Cut(command, " ")
		if !found {
			return ""
		}
		command = strings.TrimSpace(rest)
	}
	return command
}

func boundedCauseCommand(command string) string {
	command = strings.Join(strings.Fields(command), " ")
	if len(command) <= causeCommandLength {
		return command
	}
	return strings.TrimRight(truncateUTF8Prefix(command, causeCommandLength-3), " ") + "…"
}

func truncateUTF8Prefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// lockfileForCommand names the lockfile a frozen install reads, for the
// installs whose own failure text does not.
func lockfileForCommand(command string) string {
	for _, entry := range []struct{ prefix, lockfile string }{
		{"npm ", "package-lock.json"}, {"bun ", "bun.lock"}, {"corepack enable && pnpm", "pnpm-lock.yaml"},
		{"pnpm ", "pnpm-lock.yaml"}, {"corepack enable && yarn", "yarn.lock"}, {"yarn ", "yarn.lock"},
		{"poetry ", "poetry.lock"}, {"uv ", "uv.lock"}, {"cargo ", "Cargo.lock"}, {"go ", "go.sum"},
		{"composer ", "composer.lock"}, {"deno ", "deno.lock"}, {"bundle ", "Gemfile.lock"},
	} {
		if strings.HasPrefix(command, entry.prefix) {
			return entry.lockfile
		}
	}
	return ""
}

// nodeManagerForCommand names the package manager a Node command runs with.
func nodeManagerForCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) > 2 && fields[0] == "corepack" {
		fields = fields[3:]
	}
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "npm", "npx":
		return "npm"
	case "pnpm":
		return "pnpm"
	case "yarn":
		return "yarn"
	case "bun", "bunx":
		return "bun"
	}
	return ""
}

// preparedNodeManager is the package manager the rendered Node recipe
// installed with, read from its install line.
func preparedNodeManager(prepared PreparedBuild) string {
	if prepared.Recipe != "node" && prepared.Recipe != "php" {
		return ""
	}
	for _, line := range strings.Split(prepared.DockerfilePreview, "\n") {
		command, ok := strings.CutPrefix(strings.TrimSpace(line), "RUN ")
		if !ok {
			continue
		}
		command = stripRunFlags(command)
		if installCommandRE.MatchString(command) {
			if manager := nodeManagerForCommand(command); manager != "" {
				return manager
			}
		}
	}
	return ""
}
