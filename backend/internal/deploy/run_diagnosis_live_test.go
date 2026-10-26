package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveFailedBuildIsDiagnosedByARealModel builds a Node project whose build
// script needs a package it never declared through the real builder and
// BuildKit, writes the digest the route would send
// from its real transcript and cause, and asks a real model behind an
// OpenAI-compatible server — a model on this machine, never a hosted key.
// The answer must name evidence that is a line the build printed.
//
// JD_TEST_ASSISTANT_URL is the server (http://127.0.0.1:11434/v1 for Ollama),
// JD_TEST_ASSISTANT_MODEL the model. JD_TEST_DIAGNOSIS_OUT, when set, is a
// file the diagnosis is written to as the run page reads it.
func TestLiveFailedBuildIsDiagnosedByARealModel(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the live build diagnosis")
	}
	base, model := os.Getenv("JD_TEST_ASSISTANT_URL"), os.Getenv("JD_TEST_ASSISTANT_MODEL")
	if base == "" || model == "" {
		t.Skip("set JD_TEST_ASSISTANT_URL and JD_TEST_ASSISTANT_MODEL to an OpenAI-compatible server and its model")
	}
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	root := t.TempDir()
	// The build script needs a package the project never declared: the
	// install passes and `npm run build` fails, as a real one does.
	writeBuildFixture(t, root, "package.json", `{"name":"diagnosis-fixture","version":"1.0.0","scripts":{"build":"node build.js","start":"node -e 1"},"dependencies":{"is-odd":"3.0.1"}}`)
	writeBuildFixture(t, root, "build.js", "const pad = require(\"left-pad\")\nconsole.log(pad(\"built\", 10))\n")
	tag := fmt.Sprintf("just-dashboard-diagnosis:%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "npm", BuildCommand: "npm run build", StartCommand: "npm start"}
	prepared, err := builder.Prepare(context.Background(), root, config, false, tag)
	if err != nil {
		t.Fatal(err)
	}
	const buildStep = 5
	seq := int64(0)
	transcript := []RunTranscriptLine{}
	collector := newBuildOutputCollector(func() int64 { return seq })
	_, err = builder.Build(context.Background(), root, tag, config, prepared, nil, nil, "", SourceIdentity{}, nil, func(line BuildLog) error {
		seq++
		collector.observe(line)
		transcript = append(transcript, RunTranscriptLine{Seq: seq, StepID: buildStep, Stream: line.Stream, At: time.Now(), Text: line.Text})
		return nil
	})
	var failure *dockerx.BuildError
	if !errors.As(err, &failure) {
		for _, line := range transcript {
			t.Log(line.Text)
		}
		t.Fatalf("build error = %#v", err)
	}
	cause := buildFailureCause(err, collector, causeContext{build: config, prepared: prepared}, nil)
	if cause == nil {
		t.Fatal("the build's own rules named no cause")
	}
	t.Logf("the dashboard's rules named %s: %s", cause.Code, cause.sentence())
	seq++
	transcript = append(transcript, RunTranscriptLine{Seq: seq, StepID: buildStep, Stream: "status", Text: "Diagnosis: " + cause.sentence()})

	started := time.Now().Add(-90 * time.Second)
	ended := time.Now()
	material := DiagnosisMaterial{
		Snapshot: RunSnapshot{
			Run: EngineRun{ID: 1, RunNumber: 7, State: RunFailed, Operation: OperationDeploy, Trigger: TriggerGitPush,
				ClaimedAt: &started, EndedAt: &ended, TerminalCode: cause.Code, TerminalReason: cause.sentence()},
			Steps: []RunStep{
				{ID: 1, Key: StepAcquireSource, Ordinal: 2, State: StepPassed, Attempt: 1},
				{ID: 2, Key: StepAnalyzePlan, Ordinal: 3, State: StepPassed, Attempt: 1},
				{ID: 3, Key: StepPrepareContext, Ordinal: 4, State: StepPassed, Attempt: 1},
				{ID: buildStep, Key: StepBuildArtifact, Ordinal: 5, State: StepFailed, Attempt: 1, LastSeq: seq,
					ErrorCode: cause.Code, ErrorMessage: cause.sentence(), Evidence: mustJSON(map[string]any{"cause": cause})},
			},
		},
		Plan: &StoredExecutionPlan{
			SourceConfig:   DraftSourceConfig{Kind: SourceGit, Ref: "main"},
			SourceIdentity: SourceIdentity{Revision: strings.Repeat("4", 40)},
			Build:          config,
			Runtime:        RuntimePlanConfig{InternalPort: 3000, Strategy: StrategyBlueGreen},
			Variables:      []ReleaseVariableSnapshot{{Name: "NPM_TOKEN", Sensitivity: "secret", Scopes: "build"}},
		},
		Transcript: transcript,
	}
	digest := BuildDiagnosisDigest(material, NewDiagnosisMasker(nil))
	t.Logf("sent %d lines, %d bytes:\n%s", len(digest.Lines), len(digest.Text), digest.Text)

	settings, err := assistant.Prepare(assistant.Setup{Provider: assistant.ProviderOpenAI, BaseURL: base, Model: model}, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := assistant.New(settings, assistant.Options{HTTP: assistant.NewHTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	asked := time.Now()
	reply, err := provider.Stream(ctx, assistant.Request{
		System:   DiagnosisSystemPrompt,
		Messages: []assistant.Message{{Role: "user", Blocks: []assistant.Block{{Type: assistant.BlockText, Text: digest.Text}}}},
	}, func(assistant.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, block := range reply.Blocks {
		if block.Type == assistant.BlockText {
			text += block.Text
		}
	}
	answer := ReadDiagnosisAnswer(text, digest.Lines)
	t.Logf("%s answered in %s (%d tokens in, %d out):\n%s", model, time.Since(asked).Round(time.Second), reply.Usage.Input, reply.Usage.Output, text)
	read, _ := json.MarshalIndent(answer, "", "  ")
	t.Logf("read as:\n%s", read)
	if answer.Cause == "" || len(answer.Steps) == 0 {
		t.Errorf("the answer kept to no shape: %+v", answer)
	}
	if len(answer.Evidence) == 0 {
		t.Errorf("the answer quoted no line the build printed (%d quoted lines were not in it)", answer.Unmatched)
	}
	if out := os.Getenv("JD_TEST_DIAGNOSIS_OUT"); out != "" {
		usage, _ := json.Marshal(reply.Usage)
		kept := RunDiagnosis{RunID: 1, Provider: string(settings.Provider), Model: model, Answer: answer, Sent: digest.Text,
			SentLines: len(digest.Lines), Masked: digest.Masked, Usage: usage, CreatedBy: "ion", CreatedAt: time.Now().UTC()}
		encoded, _ := json.MarshalIndent(map[string]any{"diagnosis": kept, "transcript": digest.Lines, "cause": cause}, "", "  ")
		if err := os.WriteFile(out, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
