package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// What leaves the server is masked twice over: by the values the run's own
// variables and credentials hold, and by the shapes credentials take whoever
// they belong to.
func TestDiagnosisMaskerHidesSecretsByValueAndByShape(t *testing.T) {
	t.Parallel()
	masker := NewDiagnosisMasker([]string{
		"hunter2-and-more", "hunter2", "abc", "", "  ", "s3cr3t-value",
	})
	for _, tc := range []struct{ name, in, want string }{
		{"a variable's value", "connecting with s3cr3t-value now", "connecting with [REDACTED] now"},
		{"longest first", "pass hunter2-and-more", "pass [REDACTED]"},
		{"the shorter one alone", "pass hunter2 again", "pass [REDACTED] again"},
		{"too short to mask", "abc def", "abc def"},
		{"a URL's password", "dial postgres://shop:pa55w0rd@db:5432/shop failed", "dial postgres://shop:[REDACTED]@db:5432/shop failed"},
		{"an authorization header", "Authorization: Bearer abc.def.ghi", "Authorization: Bearer [REDACTED]"},
		{"an assignment", "export API_TOKEN=tok_12345 && run", "export API_TOKEN=[REDACTED] && run"},
		{"a JSON field", `{"client_secret": "zzz999", "port": 3000}`, `{"client_secret": "[REDACTED]", "port": 3000}`},
		{"a GitHub token", "remote: ghp_" + strings.Repeat("A", 36) + " denied", "remote: [REDACTED] denied"},
		{"an Anthropic key", "key sk-ant-api03-" + strings.Repeat("x", 30), "key [REDACTED]"},
		{"an AWS key", "AKIAABCDEFGHIJKLMNOP used", "[REDACTED] used"},
		{"a Slack token", "xoxb-1234567890-abcdef", "[REDACTED]"},
		{"a JWT", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abcdefghijk", "token [REDACTED]"},
		{"a private key", "-----BEGIN OPENSSH PRIVATE KEY----- b3BlbnNzaC1rZXk", "[REDACTED PRIVATE KEY]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, count := masker.Mask(tc.in)
			if got != tc.want {
				t.Errorf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if (count == 0) != (got == tc.in) {
				t.Errorf("Mask(%q) counted %d", tc.in, count)
			}
		})
	}
}

// A build's ordinary output survives: what a diagnosis needs is in it.
func TestDiagnosisMaskerLeavesOrdinaryOutputAlone(t *testing.T) {
	t.Parallel()
	masker := NewDiagnosisMasker(nil)
	for _, line := range []string{
		"npm ERR! code EUSAGE",
		"npm ERR! `npm ci` can only install packages when your package.json and package-lock.json are in sync.",
		"#8 [build 3/6] RUN npm ci",
		"#8 sha256:" + strings.Repeat("a", 64) + " 1.2MB / 1.2MB",
		"HEAD is now at " + strings.Repeat("b", 40),
		"Error: Cannot find module '/app/dist/main.js'",
		"listen tcp 0.0.0.0:3000: bind: address already in use",
		"API_TOKEN=${{credential.github}} and DB=$DATABASE_URL",
		"KEY=[REDACTED] was masked when it was written",
		"a task-runner-abcdefghijklmnopqrstuvwxyz package",
	} {
		if got, count := masker.Mask(line); got != line || count != 0 {
			t.Errorf("Mask(%q) = %q (%d)", line, got, count)
		}
	}
}

func TestTokenShapedValues(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]bool{
		"pk_live_51HqLyjWDarjtT1zdp7dc":    true,
		"9f8e7d6c5b4a39281706f5e4d3c2b1a0": true,
		"production":                       false,
		"https://api.example.com/v1/items": false,
		"/var/lib/app/uploads/2024":        false,
		"sha256:0123456789abcdef0123":      false,
		"a value with spaces 123456789":    false,
		"short1":                           false,
		"${{credential.registry}}":         false,
	} {
		if got := tokenShaped(value); got != want {
			t.Errorf("tokenShaped(%q) = %v, want %v", value, got, want)
		}
	}
	if !diagnosisMaskable("postgres://app:pw@db/app") || diagnosisMaskable("3000") {
		t.Error("a plain value with credentials in it is masked, a port is not")
	}
}

func diagnosisFixture() DiagnosisMaterial {
	started := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	ended := started.Add(134 * time.Second)
	cause := mustJSON(map[string]any{"cause": map[string]any{
		"code": "npm_lockfile_out_of_sync", "phase": "install", "command": "npm ci", "exitCode": 1,
		"subjects": []string{"package-lock.json"}, "lineSeq": 41,
		"fix": map[string]any{"kind": "set_build", "field": "build.packageManager", "value": "pnpm"},
	}})
	return DiagnosisMaterial{
		Snapshot: RunSnapshot{
			Run: EngineRun{
				ID: 9, RunNumber: 4, ProjectID: 2, EnvironmentID: 3, State: RunFailed, Operation: OperationDeploy,
				Trigger: TriggerManual, ClaimedAt: &started, EndedAt: &ended,
				TerminalCode: "npm_lockfile_out_of_sync", TerminalReason: "npm ci refused to install",
			},
			Steps: []RunStep{
				{ID: 11, Key: StepAcquireSource, Ordinal: 1, State: StepPassed, Attempt: 1, LastSeq: 10},
				{ID: 12, Key: StepBuildArtifact, Ordinal: 3, State: StepFailed, Attempt: 1, LastSeq: 42,
					ErrorCode: "npm_lockfile_out_of_sync", ErrorMessage: "npm ci refused: package-lock.json is out of sync", Evidence: cause},
				{ID: 13, Key: StepRetirePrevious, Ordinal: 9, State: StepPassed, Attempt: 1, LastSeq: 44},
			},
		},
		Plan: &StoredExecutionPlan{
			SourceConfig:   DraftSourceConfig{Kind: SourceGit, Ref: "main", URL: "https://deploy:ghp_" + strings.Repeat("Z", 36) + "@github.com/acme/shop.git"},
			SourceIdentity: SourceIdentity{Revision: strings.Repeat("c", 40)},
			Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", Framework: "nextjs", NodeVersion: "22",
				PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm start",
				ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: "npm run migrate", Env: []string{"SEED_TOKEN=plain-text-token"}}}},
			Runtime: RuntimePlanConfig{InternalPort: 3000, Strategy: StrategyBlueGreen, MemoryMB: 512},
			Checks:  []json.RawMessage{mustJSON(PlannedCheck{Name: "home", Kind: string(CheckHTTP), Phase: "readiness", Config: json.RawMessage(`{"path":"/"}`)})},
			Variables: []ReleaseVariableSnapshot{
				{Name: "DATABASE_URL", Sensitivity: "secret", Scopes: "runtime"},
				{Name: "NEXT_PUBLIC_SITE", Sensitivity: "plain", Scopes: "build,runtime"},
			},
		},
		Transcript: []RunTranscriptLine{
			{Seq: 9, StepID: 11, Stream: "stdout", Text: "Cloning into '/work'..."},
			{Seq: 40, StepID: 12, Stream: "stdout", Text: "#8 [build 3/6] RUN npm ci"},
			{Seq: 41, StepID: 12, Stream: "stderr", Text: "\x1b[31mnpm ERR!\x1b[0m `npm ci` can only install packages when your package.json and package-lock.json are in sync."},
			{Seq: 42, StepID: 12, Stream: "stderr", Text: "npm ERR! using registry token super-secret-registry-token"},
			{Seq: 44, StepID: 13, Stream: "status", Text: "Removed the candidate container"},
		},
		Earlier: true,
	}
}

func TestTheDigestSaysWhatTheRunPageShowsAndNoValue(t *testing.T) {
	t.Parallel()
	digest := BuildDiagnosisDigest(diagnosisFixture(), NewDiagnosisMasker([]string{"super-secret-registry-token"}))
	for _, want := range []string{
		"Operation: deploy, started by a person in the dashboard",
		"Outcome: failed after 2m14s",
		"Failed step: Build (build_artifact), attempt 1",
		"Cause: code npm_lockfile_out_of_sync, phase install, command `npm ci`, exit code 1",
		"It names: package-lock.json",
		"The transcript line that shows it: 41",
		"The fix it offers the operator as a button: set_build build.packageManager = pnpm",
		"Build: method recipe, recipe node, framework nextjs, Node 22, package manager npm, build command `npm run build`, start command `npm start`",
		"Release task: migrate, command `npm run migrate`",
		"Runtime: listens on 3000, strategy blue_green, memory 512 MB",
		`Check: home, readiness http {"path":"/"}`,
		"DATABASE_URL (runtime, secret); NEXT_PUBLIC_SITE (build, runtime)",
		"commit ccccccc",
		"== Build ==",
		"41 | npm ERR! `npm ci` can only install packages",
		"42 | npm ERR! using registry token [REDACTED]",
		"44 | Removed the candidate container",
		"(earlier lines are left out)",
	} {
		if !strings.Contains(digest.Text, want) {
			t.Errorf("the digest lacks %q:\n%s", want, digest.Text)
		}
	}
	for _, leak := range []string{"super-secret-registry-token", "plain-text-token", "ghp_", "Cloning into", "\x1b["} {
		if strings.Contains(digest.Text, leak) {
			t.Errorf("the digest holds %q:\n%s", leak, digest.Text)
		}
	}
	if digest.Masked == 0 || digest.FailedStep == nil || digest.FailedStep.ID != 12 {
		t.Errorf("masked %d, failed step %+v", digest.Masked, digest.FailedStep)
	}
	if len(digest.Lines) != 4 || digest.Lines[2].Text != "npm ERR! using registry token [REDACTED]" {
		t.Errorf("lines kept for the evidence = %+v", digest.Lines)
	}
}

// A run whose settings cannot be read, and a health gate's readings of the
// container, are said as such.
func TestTheDigestOfAFailedHealthGate(t *testing.T) {
	t.Parallel()
	material := diagnosisFixture()
	material.Plan = nil
	material.Snapshot.Run.State = RunRolledBack
	material.Snapshot.Steps[1] = RunStep{ID: 12, Key: StepVerifyReadiness, Ordinal: 6, State: StepFailed, Attempt: 2,
		Evidence: mustJSON(map[string]any{"diagnostics": map[string]any{
			"state": "exited", "exitCode": 1, "oomKilled": false, "logTail": []string{"x"},
			"cause": map[string]any{"code": "runtime_env_missing", "subjects": []string{"DATABASE_URL"}},
		}})}
	digest := BuildDiagnosisDigest(material, NewDiagnosisMasker(nil))
	for _, want := range []string{
		"rolled back: the release that was live before it stayed live",
		"attempt 2",
		"Cause: code runtime_env_missing",
		"It names: DATABASE_URL",
		"The container when the check gave up: exitCode 1, oomKilled false, state exited",
		"The settings this run used could not be read.",
	} {
		if !strings.Contains(digest.Text, want) {
			t.Errorf("the digest lacks %q:\n%s", want, digest.Text)
		}
	}
}

// The failed step's own lines are sent before any other step's, at most a
// few of what ran after it, each clipped, and the newest are kept when the
// budget runs out.
func TestTheTranscriptSentIsTheFailedStepsEnd(t *testing.T) {
	t.Parallel()
	failed := &RunStep{ID: 2, LastSeq: 600}
	lines := []RunTranscriptLine{}
	seq := int64(0)
	for i := 0; i < 100; i++ {
		seq++
		lines = append(lines, RunTranscriptLine{Seq: seq, StepID: 1, Text: fmt.Sprintf("earlier step %d", i)})
	}
	for i := 0; i < 500; i++ {
		seq++
		lines = append(lines, RunTranscriptLine{Seq: seq, StepID: 2, Text: fmt.Sprintf("build %d %s", i, strings.Repeat("x", 30))})
	}
	for i := 0; i < 50; i++ {
		seq++
		lines = append(lines, RunTranscriptLine{Seq: seq, StepID: 3, Text: fmt.Sprintf("cleanup %d", i)})
	}
	sent := diagnosisTranscript(lines, failed)
	owned, others := 0, 0
	for _, line := range sent {
		switch line.StepID {
		case 1:
			t.Fatalf("a line from before the failed step was sent: %+v", line)
		case 2:
			owned++
		case 3:
			others++
		}
	}
	if owned != DiagnosisTranscriptLines-diagnosisOtherLines || others != diagnosisOtherLines {
		t.Errorf("sent %d of the step's lines and %d after it", owned, others)
	}
	if sent[len(sent)-1].Text != "cleanup 49" || !strings.HasPrefix(sent[0].Text, "build 330 ") {
		t.Errorf("sent from %q to %q", sent[0].Text, sent[len(sent)-1].Text)
	}

	long := []RunTranscriptLine{}
	for i := 0; i < 150; i++ {
		long = append(long, RunTranscriptLine{Seq: int64(i + 1), StepID: 2, Text: fmt.Sprintf("%03d ", i) + strings.Repeat("y", 1000)})
	}
	sent = diagnosisTranscript(long, &RunStep{ID: 2, LastSeq: 150})
	total := 0
	for _, line := range sent {
		total += len(line.Text)
		if len(line.Text) > diagnosisLineBytes+len("…") {
			t.Fatalf("a line of %d bytes was sent", len(line.Text))
		}
	}
	if total > diagnosisTranscriptBytes || !strings.HasPrefix(sent[len(sent)-1].Text, "149 ") {
		t.Errorf("sent %d bytes ending %q", total, sent[len(sent)-1].Text[:4])
	}

	// A step that wrote nothing leaves the run's last lines.
	sent = diagnosisTranscript(lines[:120], &RunStep{ID: 9, LastSeq: 0})
	if len(sent) != 120 {
		t.Errorf("a silent failed step sent %d lines", len(sent))
	}
}

func TestTheAnswerIsReadIntoItsPartsAndItsEvidenceHeldToTheTranscript(t *testing.T) {
	t.Parallel()
	sent := []RunTranscriptLine{
		{Seq: 40, Text: "#8 [build 3/6] RUN npm ci"},
		{Seq: 41, Text: "npm ERR! `npm ci` can only install packages when your package.json and package-lock.json are in sync."},
		{Seq: 42, Text: "npm ERR! Missing: left-pad@1.3.0 from lock file"},
	}
	answer := ReadDiagnosisAnswer(`CAUSE: The lockfile does not match package.json, so npm ci refuses to install.
EVIDENCE:
> 41 | npm ERR! `+"`npm ci`"+` can only install packages when your package.json and package-lock.json are in sync.
> npm ERR! Missing: left-pad@1.3.0 from lock file
> npm ERR! something the run never printed
STEPS:
1. Run npm install locally and commit package-lock.json.
2. Or switch the package manager in Settings → Build
   to the one whose lockfile you keep.
`, sent)
	if answer.Cause != "The lockfile does not match package.json, so npm ci refuses to install." {
		t.Errorf("cause = %q", answer.Cause)
	}
	if len(answer.Evidence) != 2 || answer.Evidence[0].Seq != 41 || answer.Evidence[1].Seq != 42 ||
		answer.Evidence[1].Text != sent[2].Text || answer.Unmatched != 1 {
		t.Errorf("evidence = %+v, unmatched %d", answer.Evidence, answer.Unmatched)
	}
	if len(answer.Steps) != 2 || answer.Steps[1] != "Or switch the package manager in Settings → Build to the one whose lockfile you keep." {
		t.Errorf("steps = %q", answer.Steps)
	}

	// A small model pads its evidence with BuildKit's own lines and quotes out
	// of order, and ends on a remark that is not a step.
	answer = ReadDiagnosisAnswer("CAUSE: left-pad is missing.\nEVIDENCE:\n> npm ERR! Missing: left-pad@1.3.0 from lock file\n> #8 [build 3/6] RUN npm ci\n> 41 | npm ERR! `npm ci` can only install packages when your package.json and package-lock.json are in sync.\n"+
		"STEPS:\n1. Add left-pad.\n\nThese lines show the failure.\nLook at:\n- the lockfile\n", sent)
	if len(answer.Evidence) != 2 || answer.Evidence[0].Seq != 41 || answer.Evidence[1].Seq != 42 ||
		len(answer.Steps) != 1 || answer.Steps[0] != "Add left-pad." || !strings.Contains(answer.Text, "These lines show the failure.") {
		t.Errorf("padded answer = %+v", answer)
	}

	// A quote that dropped BuildKit's step number or its seconds is the line
	// it came from.
	buildKit := []RunTranscriptLine{{Seq: 46, Text: "#10 0.441 Error: Cannot find module 'left-pad'"}}
	answer = ReadDiagnosisAnswer("CAUSE: x\nEVIDENCE:\n> 43 | #10 Error: Cannot find module 'left-pad'\n> Error: Cannot find module 'left-pad'\n", buildKit)
	if len(answer.Evidence) != 1 || answer.Evidence[0].Seq != 46 || answer.Unmatched != 0 {
		t.Errorf("BuildKit quote = %+v", answer)
	}

	// The shape a model drifts into is still read.
	answer = ReadDiagnosisAnswer("**Cause:** npm ci refused.\n\n## Evidence\n- `npm ERR! Missing: left-pad@1.3.0 from lock file`\n\n**Steps:**\n- Update the lockfile.\n", sent)
	if answer.Cause != "npm ci refused." || len(answer.Evidence) != 1 || len(answer.Steps) != 1 {
		t.Errorf("drifted answer = %+v", answer)
	}

	// An answer in no shape at all is kept whole for the page to show.
	answer = ReadDiagnosisAnswer("I could not tell from these lines.", sent)
	if answer.Cause != "" || len(answer.Evidence) != 0 || answer.Text != "I could not tell from these lines." {
		t.Errorf("shapeless answer = %+v", answer)
	}
}
