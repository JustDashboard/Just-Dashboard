package deploy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A failed run explained by the model the operator set up for the SQL
// editor's assistant (internal/assistant). This file is what the deployment
// side decides: what of the run is sent, how it is masked first, and how the
// answer is read back. The route that asks is api/handlers_deploy_diagnosis.go.
//
// What is sent is what the run's page already shows any signed-in account —
// the failed step, the cause the dashboard's own rules named, the settings the
// run used, the names of its variables and the end of its transcript — masked
// a second time on the way out, because the transcript was masked when it was
// written with the values the run had then, and a model is a third party.

const (
	// DiagnosisTranscriptLines is how many of the run's last lines are read;
	// the failed step's are kept before any other step's.
	DiagnosisTranscriptLines = 200
	// diagnosisLineBytes and diagnosisTranscriptBytes bound one line and all
	// of them, the newest kept: a minified bundle printed on one line would
	// otherwise be the whole request.
	diagnosisLineBytes       = 400
	diagnosisTranscriptBytes = 24 << 10
	// diagnosisOtherLines is how many lines of the steps after the failed one
	// — the compensation that removed the candidate, a rollback — are kept
	// beside it.
	diagnosisOtherLines = 30
	// diagnosisMinMask is the shortest value masked by its text. Anything
	// shorter is a word or a number the transcript is full of, and masking
	// it would leave nothing to diagnose; nothing that short is a credential.
	diagnosisMinMask = 4
	// DiagnosisMasked is what a masked value reads as, the same words the
	// build's own redaction writes.
	DiagnosisMasked      = "[REDACTED]"
	maxDiagnosisEvidence = 4
	maxDiagnosisSteps    = 8
)

// DiagnosisMaterial is what a digest is written from, read from the stores
// by the route.
type DiagnosisMaterial struct {
	Snapshot RunSnapshot
	// Plan is nil when the run's settings could not be read, which an older
	// run's may not be; the digest says so instead.
	Plan       *StoredExecutionPlan
	Transcript []RunTranscriptLine
	// Earlier says the transcript has lines before the ones read.
	Earlier bool
}

// DiagnosisDigest is the masked text that is sent, with what it holds.
type DiagnosisDigest struct {
	Text string
	// Lines are the transcript lines as they were sent, masked, which quoted
	// evidence is held to.
	Lines []RunTranscriptLine
	// Masked counts the values masked on the way out, beyond what the
	// transcript had masked already.
	Masked int
	// FailedStep is the step the digest names as the one that failed.
	FailedStep *RunStep
}

// DiagnosisMasker hides secret values in text: every value it was given,
// longest first so one that contains another is not left half masked, then
// anything shaped like a credential whoever's it is.
type DiagnosisMasker struct {
	values []string
}

// NewDiagnosisMasker takes the values to hide. Duplicates, blanks and values
// shorter than diagnosisMinMask are dropped.
func NewDiagnosisMasker(values []string) *DiagnosisMasker {
	seen := map[string]bool{}
	kept := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < diagnosisMinMask || seen[value] || value == DiagnosisMasked {
			continue
		}
		seen[value] = true
		kept = append(kept, value)
	}
	sort.Slice(kept, func(i, j int) bool {
		if len(kept[i]) != len(kept[j]) {
			return len(kept[i]) > len(kept[j])
		}
		return kept[i] < kept[j]
	})
	return &DiagnosisMasker{values: kept}
}

// Mask returns the text with every secret hidden and how many it hid.
func (m *DiagnosisMasker) Mask(text string) (string, int) {
	count := 0
	for _, value := range m.values {
		if n := strings.Count(text, value); n > 0 {
			text = strings.ReplaceAll(text, value, DiagnosisMasked)
			count += n
		}
	}
	for _, pattern := range diagnosisPatterns {
		text = pattern.re.ReplaceAllStringFunc(text, func(match string) string {
			masked := pattern.mask(match)
			if masked != match {
				count++
			}
			return masked
		})
	}
	return text, count
}

type diagnosisPattern struct {
	re   *regexp.Regexp
	mask func(match string) string
}

// keepPrefix masks what follows the pattern's first group: the name of a
// header or a variable stays, so the model can still say which one it was.
func keepPrefix(re *regexp.Regexp) diagnosisPattern {
	return diagnosisPattern{re: re, mask: func(match string) string {
		groups := re.FindStringSubmatch(match)
		if len(groups) < 3 {
			return DiagnosisMasked
		}
		value := groups[2]
		if value == DiagnosisMasked || strings.HasPrefix(value, "${") || strings.HasPrefix(value, "$") ||
			strings.HasPrefix(value, "[REDACTED") || strings.TrimSpace(value) == "" {
			return match
		}
		return groups[1] + DiagnosisMasked
	}}
}

func whole(re *regexp.Regexp) diagnosisPattern {
	return diagnosisPattern{re: re, mask: func(string) string { return DiagnosisMasked }}
}

// diagnosisPatterns are credentials by their shape: what the transcript can
// hold that no variable of this run named — a token a build script printed, a
// key in a dependency's error, a password in a connection string.
var diagnosisPatterns = []diagnosisPattern{
	// A private key never belongs in a request, however much of it there is.
	{re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*`), mask: func(string) string {
		return "[REDACTED PRIVATE KEY]"
	}},
	// The password of a URL, its user and host kept.
	{re: regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@'"]*:[^\s/@'"]+@`), mask: func(match string) string {
		scheme, rest, _ := strings.Cut(match, "://")
		user, password, _ := strings.Cut(strings.TrimSuffix(rest, "@"), ":")
		if password == DiagnosisMasked || strings.HasPrefix(password, "${") {
			return match
		}
		return scheme + "://" + user + ":" + DiagnosisMasked + "@"
	}},
	keepPrefix(regexp.MustCompile(`(?i)((?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:bearer|basic|token|bot)?\s*)([^\s'",]+)`)),
	// NAME=value and "name": "value" where the name says it holds a secret.
	keepPrefix(regexp.MustCompile(`(?i)(\b[A-Za-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*["']?\s*[:=]\s*["']?)([^\s"',;&]+)`)),
	// Tokens whose issuer gave them a shape of their own.
	whole(regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,255}|github_pat_[A-Za-z0-9_]{22,255}|glpat-[A-Za-z0-9_-]{20,}|glrt-[A-Za-z0-9_-]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|(?:AKIA|ASIA)[A-Z0-9]{16}|sk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}|[rsp]k_(?:live|test)_[A-Za-z0-9]{16,}|AIza[0-9A-Za-z_-]{35}|npm_[A-Za-z0-9]{36}|hf_[A-Za-z0-9]{30,}|dckr_pat_[A-Za-z0-9_-]{20,})\b`)),
	whole(regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)),
}

// tokenShaped says a plain value looks like a key rather than a setting: long,
// one word, letters and digits mixed, and not an address or a digest. An API
// key saved as a plain variable is still masked.
func tokenShaped(value string) bool {
	if len(value) < 20 || strings.Contains(value, "://") || strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "sha256:") || strings.HasPrefix(value, "${{") {
		return false
	}
	letters, digits := false, false
	for _, r := range value {
		switch {
		case unicode.IsSpace(r):
			return false
		case unicode.IsLetter(r):
			letters = true
		case unicode.IsDigit(r):
			digits = true
		}
	}
	return letters && digits
}

// diagnosisMaskable says whether a plain value is masked: one that carries
// credential material, as the build's own redaction decides, or that looks
// like a key.
func diagnosisMaskable(value string) bool {
	return rejectPlanSecretLiteral("variable", value) != nil || tokenShaped(value)
}

// urlPassword is the password inside a connection string, which a driver's
// error can print on its own.
func urlPassword(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.User == nil {
		return ""
	}
	password, _ := u.User.Password()
	return password
}

// diagnosisFailedStep is the step a run stopped on, as the run page reads it:
// the first failed or blocked step among the latest attempts.
func diagnosisFailedStep(steps []RunStep) *RunStep {
	latest := map[StepKey]int{}
	for index, step := range steps {
		if current, ok := latest[step.Key]; !ok || step.Attempt > steps[current].Attempt {
			latest[step.Key] = index
		}
	}
	var failed *RunStep
	for index := range steps {
		step := &steps[index]
		if latest[step.Key] != index || (step.State != StepFailed && step.State != StepBlocked) {
			continue
		}
		if failed == nil || step.Ordinal < failed.Ordinal {
			failed = step
		}
	}
	return failed
}

// diagnosisCause is the cause the step's evidence records — a build's or a
// release task's `cause`, or a health gate's `diagnostics.cause` — and the
// rest of a health gate's readings of the container.
func diagnosisCause(step *RunStep) (cause map[string]any, readings map[string]any) {
	if step == nil || len(step.Evidence) == 0 {
		return nil, nil
	}
	var evidence map[string]any
	if json.Unmarshal(step.Evidence, &evidence) != nil {
		return nil, nil
	}
	if found, ok := evidence["cause"].(map[string]any); ok {
		cause = found
	}
	if diagnostics, ok := evidence["diagnostics"].(map[string]any); ok {
		if found, ok := diagnostics["cause"].(map[string]any); ok && cause == nil {
			cause = found
		}
		readings = map[string]any{}
		for key, value := range diagnostics {
			switch value.(type) {
			case string, float64, bool:
				readings[key] = value
			}
		}
	}
	return cause, readings
}

// BuildDiagnosisDigest writes what is sent about a failed run, masked.
func BuildDiagnosisDigest(m DiagnosisMaterial, masker *DiagnosisMasker) DiagnosisDigest {
	run := m.Snapshot.Run
	failed := diagnosisFailedStep(m.Snapshot.Steps)
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("A deployment run failed. What the dashboard recorded about it follows. Values it masked read %s.", DiagnosisMasked)
	line("")
	line("Run")
	line("- Operation: %s, started by %s", run.Operation, diagnosisTrigger(run.Trigger))
	outcome := "failed"
	if run.State == RunRolledBack {
		outcome = "rolled back: the release that was live before it stayed live"
	}
	if run.ClaimedAt != nil && run.EndedAt != nil {
		outcome += " after " + run.EndedAt.Sub(*run.ClaimedAt).Round(time.Second).String()
	}
	line("- Outcome: %s", outcome)
	if run.TerminalCode != "" {
		line("- Code: %s", run.TerminalCode)
	}
	if run.TerminalReason != "" {
		line("- Reason: %s", clipDiagnosis(run.TerminalReason, 600))
	}

	if failed != nil {
		line("")
		line("Failed step: %s (%s), attempt %d", stepLabelOrKey(failed.Key, run.Operation), failed.Key, failed.Attempt)
		if failed.ErrorCode != "" {
			line("- Code: %s", failed.ErrorCode)
		}
		if failed.ErrorMessage != "" {
			line("- Message: %s", clipDiagnosis(failed.ErrorMessage, 600))
		}
		cause, readings := diagnosisCause(failed)
		if cause != nil {
			line("")
			line("The dashboard's own reading of the failure, from its rules rather than a model:")
			writeDiagnosisCause(&b, cause)
		}
		if len(readings) > 0 {
			keys := make([]string, 0, len(readings))
			for key := range readings {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, key := range keys {
				parts = append(parts, fmt.Sprintf("%s %v", key, readings[key]))
			}
			line("- The container when the check gave up: %s", clipDiagnosis(strings.Join(parts, ", "), 600))
		}
	}

	line("")
	if m.Plan != nil {
		line("Settings this run used")
		writeDiagnosisPlan(&b, m.Plan)
	} else {
		line("The settings this run used could not be read.")
	}

	line("")
	line("Steps")
	for index, step := range latestDiagnosisSteps(m.Snapshot.Steps) {
		state := string(step.State)
		if step.ErrorCode != "" {
			state += ": " + step.ErrorCode
		}
		line("%d. %s — %s", index+1, stepLabelOrKey(step.Key, run.Operation), state)
	}

	sent := diagnosisTranscript(m.Transcript, failed)
	line("")
	if len(sent) == 0 {
		line("The run's transcript is empty.")
	} else {
		// Blank lines are dropped without saying so; a line with something on
		// it before the first one sent is what "left out" means.
		earlier := ""
		left := m.Earlier
		for _, entry := range m.Transcript {
			if entry.Seq < sent[0].Seq && strings.TrimSpace(stripTerminal(entry.Text)) != "" {
				left = true
				break
			}
		}
		if left {
			earlier = " (earlier lines are left out)"
		}
		line("Transcript: %d lines, each as sequence | text%s", len(sent), earlier)
		labels := map[int64]string{}
		for _, step := range m.Snapshot.Steps {
			labels[step.ID] = stepLabelOrKey(step.Key, run.Operation)
		}
		group := int64(-1)
		for _, entry := range sent {
			if entry.StepID != group {
				group = entry.StepID
				name := labels[entry.StepID]
				if name == "" {
					name = "Run"
				}
				line("== %s ==", name)
			}
			line("%d | %s", entry.Seq, entry.Text)
		}
	}

	text, masked := masker.Mask(b.String())
	// The lines kept for checking evidence are masked by the same rules, so a
	// quoted line matches what the model was shown.
	for index := range sent {
		sent[index].Text, _ = masker.Mask(sent[index].Text)
	}
	return DiagnosisDigest{Text: text, Lines: sent, Masked: masked, FailedStep: failed}
}

func diagnosisTrigger(trigger TriggerKind) string {
	switch trigger {
	case TriggerManual:
		return "a person in the dashboard"
	case TriggerGitPush, TriggerGitHub, TriggerGitLab, TriggerBitbucket, TriggerGitea:
		return "a push to the repository"
	case TriggerSchedule:
		return "a schedule"
	case TriggerAPI:
		return "the API"
	}
	return string(trigger)
}

func stepLabelOrKey(key StepKey, operation Operation) string {
	if label := StepLabel(key, operation); label != "" {
		return label
	}
	return string(key)
}

func writeDiagnosisCause(b *strings.Builder, cause map[string]any) {
	text := func(key string) string {
		value, _ := cause[key].(string)
		return value
	}
	parts := []string{}
	if code := text("code"); code != "" {
		parts = append(parts, "code "+code)
	}
	if phase := text("phase"); phase != "" {
		parts = append(parts, "phase "+phase)
	}
	if detail := text("detail"); detail != "" {
		parts = append(parts, "about "+detail)
	}
	if service := text("service"); service != "" {
		parts = append(parts, "service "+service)
	}
	if command := text("command"); command != "" {
		parts = append(parts, "command `"+clipDiagnosis(command, 200)+"`")
	}
	if exit, ok := cause["exitCode"].(float64); ok && exit != 0 {
		parts = append(parts, "exit code "+strconv.Itoa(int(exit)))
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "- Cause: %s\n", strings.Join(parts, ", "))
	}
	if subjects, ok := cause["subjects"].([]any); ok && len(subjects) > 0 {
		names := make([]string, 0, len(subjects))
		for _, subject := range subjects {
			if name, ok := subject.(string); ok {
				names = append(names, name)
			}
		}
		fmt.Fprintf(b, "- It names: %s\n", strings.Join(names, ", "))
	}
	if seq, ok := cause["lineSeq"].(float64); ok && seq > 0 {
		fmt.Fprintf(b, "- The transcript line that shows it: %d\n", int64(seq))
	}
	if fix, ok := cause["fix"].(map[string]any); ok {
		kind, _ := fix["kind"].(string)
		field, _ := fix["field"].(string)
		value, _ := fix["value"].(string)
		scope, _ := fix["scope"].(string)
		described := kind + " " + field
		if value != "" {
			described += " = " + value
		}
		if scope != "" {
			described += " (scope " + scope + ")"
		}
		fmt.Fprintf(b, "- The fix it offers the operator as a button: %s\n", strings.TrimSpace(described))
	}
}

func writeDiagnosisPlan(b *strings.Builder, plan *StoredExecutionPlan) {
	item := func(label string, parts []string) {
		kept := parts[:0]
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				kept = append(kept, part)
			}
		}
		if len(kept) > 0 {
			fmt.Fprintf(b, "- %s: %s\n", label, strings.Join(kept, ", "))
		}
	}
	labelled := func(label, value string) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return label + " " + value
	}
	command := func(label, value string) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return label + " `" + clipDiagnosis(value, 300) + "`"
	}
	source := plan.SourceConfig
	item("Source", []string{
		string(source.Kind), labelled("branch", source.Ref), labelled("subdirectory", source.Subdirectory),
		labelled("image", source.Image), labelled("platform", source.Platform),
		labelled("commit", shortRevision(plan.SourceIdentity.Revision)),
	})
	build := plan.Build
	item("Build", []string{
		labelled("method", string(build.Method)), labelled("recipe", build.Recipe), labelled("framework", build.Framework),
		labelled("Node", build.NodeVersion), labelled("Python", build.PythonVersion), labelled("Go", build.GoVersion),
		labelled("PHP", build.PHPVersion), labelled("package manager", build.PackageManager),
		labelled("root directory", build.RootDirectory), labelled("Dockerfile", build.Dockerfile),
		labelled("target", build.Target), command("build command", build.BuildCommand),
		command("start command", build.StartCommand), labelled("output directory", build.OutputDirectory),
		labelled("platform", build.TargetPlatform),
	})
	// A release task's environment is left out: it is NAME=value pairs.
	for _, task := range build.ReleaseTasks {
		item("Release task", []string{task.Name, command("command", task.Command), labelled("runs in", task.Runner)})
	}
	runtime := plan.Runtime
	ports := []string{}
	for _, port := range runtime.Ports {
		encoded, _ := json.Marshal(port)
		ports = append(ports, string(encoded))
	}
	limits := ""
	if runtime.MemoryMB > 0 {
		limits = fmt.Sprintf("memory %d MB", runtime.MemoryMB)
	}
	cpus := ""
	if runtime.CPUs > 0 {
		cpus = fmt.Sprintf("%g CPUs", runtime.CPUs)
	}
	port := ""
	if runtime.InternalPort > 0 {
		port = fmt.Sprintf("listens on %d", runtime.InternalPort)
	}
	item("Runtime", []string{
		labelled("image", runtime.Image), command("command", strings.Join(runtime.Command, " ")), port,
		labelled("published ports", strings.Join(ports, " ")), labelled("strategy", string(runtime.Strategy)),
		limits, cpus, labelled("restart", runtime.RestartPolicy),
	})
	for _, raw := range plan.Checks {
		var check PlannedCheck
		if json.Unmarshal(raw, &check) != nil {
			continue
		}
		config := ""
		if len(check.Config) > 0 {
			config = " " + clipDiagnosis(string(check.Config), 200)
		}
		fmt.Fprintf(b, "- Check: %s, %s %s%s\n", check.Name, check.Phase, check.Kind, config)
	}
	if len(plan.Variables) > 0 {
		names := make([]string, 0, len(plan.Variables))
		for _, variable := range plan.Variables {
			described := variable.Name + " (" + strings.ReplaceAll(variable.Scopes, ",", ", ")
			if variable.Sensitivity == "secret" {
				described += ", secret"
			}
			names = append(names, described+")")
		}
		fmt.Fprintf(b, "- Variables, by name and scope (their values are never sent): %s\n", strings.Join(names, "; "))
	} else {
		fmt.Fprintf(b, "- Variables: none\n")
	}
}

// latestDiagnosisSteps are the steps in order, each by its latest attempt.
func latestDiagnosisSteps(steps []RunStep) []RunStep {
	latest := map[StepKey]RunStep{}
	for _, step := range steps {
		if current, ok := latest[step.Key]; !ok || step.Attempt > current.Attempt {
			latest[step.Key] = step
		}
	}
	out := make([]RunStep, 0, len(latest))
	for _, step := range latest {
		out = append(out, step)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out
}

// diagnosisTranscript picks the lines sent: the failed step's own and, after
// them, a few of what ran later, each clipped, the newest kept when the
// budget runs out. The end of a step is where it failed.
func diagnosisTranscript(all []RunTranscriptLine, failed *RunStep) []RunTranscriptLine {
	// A blank line says nothing and costs a model as much as a short one.
	lines := make([]RunTranscriptLine, 0, len(all))
	for _, line := range all {
		if strings.TrimSpace(stripTerminal(line.Text)) != "" {
			lines = append(lines, line)
		}
	}
	keep := make([]bool, len(lines))
	if failed != nil {
		owned, others := 0, 0
		for index := len(lines) - 1; index >= 0; index-- {
			switch {
			case lines[index].StepID == failed.ID && owned < DiagnosisTranscriptLines-diagnosisOtherLines:
				keep[index], owned = true, owned+1
			case lines[index].StepID != failed.ID && lines[index].Seq > failed.LastSeq && others < diagnosisOtherLines:
				keep[index], others = true, others+1
			}
		}
		// A step that wrote nothing of its own leaves the run's last lines.
		if owned == 0 {
			failed = nil
		}
	}
	if failed == nil {
		for index := len(lines) - 1; index >= 0 && len(lines)-index <= DiagnosisTranscriptLines; index-- {
			keep[index] = true
		}
	}
	picked := []RunTranscriptLine{}
	total := 0
	for index := len(lines) - 1; index >= 0; index-- {
		if !keep[index] {
			continue
		}
		entry := lines[index]
		entry.Text = clipDiagnosis(strings.TrimRight(stripTerminal(entry.Text), "\r\n"), diagnosisLineBytes)
		if total+len(entry.Text) > diagnosisTranscriptBytes && len(picked) > 0 {
			break
		}
		total += len(entry.Text)
		picked = append(picked, entry)
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked
}

var terminalEscapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

// stripTerminal drops colour codes and the line breaks inside one stored
// line, which a model reads as noise and the evidence check cannot match.
func stripTerminal(text string) string {
	text = terminalEscapeRE.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

func clipDiagnosis(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// DiagnosisSystemPrompt tells the model what it is reading and the shape to
// answer in. The shape is plain labelled text rather than JSON because a small
// local model keeps to it more reliably, and an answer that strays from it is
// still shown whole.
const DiagnosisSystemPrompt = `You help the operator of a self-hosted deployment dashboard understand why one deployment run failed.
You are given what the dashboard recorded about the run: the step that failed, the cause its own rules named when they named one, the settings the run used, the names of its variables (never their values) and the last lines of its transcript. Values the dashboard masked read [REDACTED]; never guess them and never ask for them.
The operator changes settings in the deployment's Settings: General (the source), Build (method, recipe, versions, package manager, root directory, build and start commands, output directory), Runtime (port, memory, restart policy), Variables, Domains and Databases.

Answer in exactly this shape, in plain text, with no headings, bold or code fences:
CAUSE: one or two sentences naming the most likely cause.
EVIDENCE:
> a line copied exactly from the transcript, without its sequence number
(one to four such lines, the ones that show the cause)
STEPS:
1. one concrete thing the operator does, naming the setting, file or command
(one to five steps)

When the dashboard's own reading is given, say whether the transcript agrees with it and build on it rather than contradict it without evidence. When the transcript does not show why the run failed, say so in CAUSE and say what to look at next. Never suggest turning off a security check.`

// DiagnosisEvidence is one transcript line the answer quoted, as the
// transcript has it.
type DiagnosisEvidence struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// DiagnosisAnswer is the model's answer read into its parts.
type DiagnosisAnswer struct {
	Cause    string              `json:"cause,omitempty"`
	Evidence []DiagnosisEvidence `json:"evidence"`
	Steps    []string            `json:"steps"`
	// Unmatched counts the lines the answer quoted that are not in the
	// transcript it was sent. They are left out: evidence is only ever a
	// line the run wrote.
	Unmatched int `json:"unmatched,omitempty"`
	// Text is the whole answer, shown as it came when it kept to no shape.
	Text string `json:"text"`
}

var (
	diagnosisLabelRE = regexp.MustCompile(`(?i)^[#*\s_]*(likely cause|cause|evidence|suggested steps|next steps|steps|fix)[*_\s]*(?::[*_\s]*(.*)|$)`)
	diagnosisItemRE  = regexp.MustCompile(`^(?:\d+[.)]|[-*•])\s+(.*)$`)
	diagnosisSeqRE   = regexp.MustCompile(`^\[?(\d+)\]?\s*\|\s?`)
	// buildKitBookkeepingRE is a line BuildKit writes about a step rather than
	// anything the step printed, which shows nothing about a cause.
	buildKitBookkeepingRE = regexp.MustCompile(`^#\d+ (?:CACHED|DONE [\d.]+s|\[[^\]]+\] .*)$`)
)

// ReadDiagnosisAnswer splits an answer into its cause, the transcript lines
// it quoted — held to the lines that were sent — and its steps.
func ReadDiagnosisAnswer(text string, sent []RunTranscriptLine) DiagnosisAnswer {
	answer := DiagnosisAnswer{Evidence: []DiagnosisEvidence{}, Steps: []string{}, Text: strings.TrimSpace(text)}
	section := ""
	var cause []string
	quoted := []string{}
	// A paragraph after a blank line closes the step before it: it is the
	// model's remark, kept in Text, not part of what to do.
	closed := false
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		if match := diagnosisLabelRE.FindStringSubmatch(trimmed); match != nil {
			switch strings.ToLower(match[1]) {
			case "cause", "likely cause":
				section = "cause"
			case "evidence":
				section = "evidence"
			default:
				section = "steps"
			}
			trimmed = strings.TrimSpace(match[2])
			if trimmed == "" {
				continue
			}
		}
		if trimmed == "" {
			closed = true
			continue
		}
		switch section {
		case "cause":
			cause = append(cause, trimmed)
		case "evidence":
			if quote := strings.TrimSpace(strings.TrimLeft(trimmed, ">")); quote != trimmed || !diagnosisItemRE.MatchString(trimmed) {
				quoted = append(quoted, quote)
			} else if item := diagnosisItemRE.FindStringSubmatch(trimmed); item != nil {
				quoted = append(quoted, item[1])
			}
		case "steps":
			if item := diagnosisItemRE.FindStringSubmatch(trimmed); item != nil {
				answer.Steps = append(answer.Steps, strings.TrimSpace(item[1]))
			} else if len(answer.Steps) > 0 && !closed {
				answer.Steps[len(answer.Steps)-1] += " " + trimmed
			} else if len(answer.Steps) == 0 {
				answer.Steps = append(answer.Steps, trimmed)
			} else {
				// A paragraph of its own after the steps — "Look at:" and a
				// list of its own — is not more of them.
				section = ""
			}
		}
		closed = false
	}
	answer.Cause = strings.TrimSpace(strings.Join(cause, " "))
	seen := map[int64]bool{}
	for _, quote := range quoted {
		if len(answer.Evidence) >= maxDiagnosisEvidence {
			break
		}
		found, ok := matchDiagnosisEvidence(quote, sent)
		if !ok {
			if strings.TrimSpace(quote) != "" {
				answer.Unmatched++
			}
			continue
		}
		if !seen[found.Seq] && !buildKitBookkeepingRE.MatchString(found.Text) {
			seen[found.Seq] = true
			answer.Evidence = append(answer.Evidence, found)
		}
	}
	// Read in the order the run wrote them, whatever order they were quoted in.
	sort.Slice(answer.Evidence, func(i, j int) bool { return answer.Evidence[i].Seq < answer.Evidence[j].Seq })
	if len(answer.Steps) > maxDiagnosisSteps {
		answer.Steps = answer.Steps[:maxDiagnosisSteps]
	}
	return answer
}

// matchDiagnosisEvidence finds the transcript line a quote came from: the
// line its sequence names when the model copied that too, else the last line
// holding the quote, or held by it when the model quoted a line and more.
func matchDiagnosisEvidence(quote string, sent []RunTranscriptLine) (DiagnosisEvidence, bool) {
	quote = strings.Trim(strings.TrimSpace(quote), "`\"'")
	named := int64(0)
	if match := diagnosisSeqRE.FindStringSubmatch(quote); match != nil {
		named, _ = strconv.ParseInt(match[1], 10, 64)
		quote = strings.TrimSpace(quote[len(match[0]):])
	}
	needle := quotedLineText(strings.TrimSuffix(quote, "…"))
	if len(needle) < 6 {
		return DiagnosisEvidence{}, false
	}
	holds := func(entry RunTranscriptLine) bool {
		text := quotedLineText(entry.Text)
		return strings.Contains(text, needle) || (len(text) >= 12 && strings.Contains(needle, text))
	}
	for _, entry := range sent {
		if named != 0 && entry.Seq == named && holds(entry) {
			return DiagnosisEvidence{Seq: entry.Seq, Text: entry.Text}, true
		}
	}
	for index := len(sent) - 1; index >= 0; index-- {
		if holds(sent[index]) {
			return DiagnosisEvidence{Seq: sent[index].Seq, Text: sent[index].Text}, true
		}
	}
	return DiagnosisEvidence{}, false
}

// buildKitPrefixRE is what BuildKit writes before a step's line — its number
// and the seconds since the step began — which a model quoting the line
// drops or rewrites as often as it keeps.
var buildKitPrefixRE = regexp.MustCompile(`^(?:#\d+\s+)?(?:\d+\.\d+\s+)?`)

// quotedLineText is a line as quoted evidence is compared: its spacing
// collapsed and BuildKit's prefix gone.
func quotedLineText(text string) string {
	return buildKitPrefixRE.ReplaceAllString(strings.Join(strings.Fields(text), " "), "")
}
