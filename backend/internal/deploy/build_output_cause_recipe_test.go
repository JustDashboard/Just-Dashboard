package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The build classifier reads the steps the JavaScript recipe renders
// (build_node_*.go): a toolchain stage, an install behind its lockfile's
// flags, Prisma's placeholders exported before a step, build secrets
// mounted into it. A failed step is named by its phase and its own command,
// never by the defaults the recipe put in front of it.
func TestBuildCauseNamesTheJavaScriptRecipesOwnSteps(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, map[string]string{
		"package.json": `{"name":"shop","scripts":{"build":"prisma generate && next build","start":"next start"},` +
			`"dependencies":{"next":"16.1.3","@prisma/client":"7.0.0"},"devDependencies":{"prisma":"7.0.0"}}`,
		"pnpm-lock.yaml": "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    dependencies:\n      '@prisma/client':\n        specifier: 7.0.0\n        version: 7.0.0\n" +
			"      next:\n        specifier: 16.1.3\n        version: 16.1.3\n    devDependencies:\n      prisma:\n        specifier: 7.0.0\n        version: 7.0.0\n",
		"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
		"prisma.config.ts":     "import { defineConfig, env } from 'prisma/config'\nexport default defineConfig({ datasource: { url: env('DATABASE_URL') } })\n",
	})
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "pnpm run build", StartCommand: "pnpm run start",
		Secrets: []BuildSecretConfig{{Variable: "NEXT_PUBLIC_SITE", Step: "build"}}}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, build, false, "t:1", "NEXT_PUBLIC_SITE")
	if err != nil {
		t.Fatal(err)
	}
	if manager := preparedNodeManager(prepared); manager != "pnpm" {
		t.Fatalf("the prepared manager = %q from %q", manager, prepared.Install)
	}
	step := func(contains string) string {
		for _, line := range strings.Split(prepared.DockerfilePreview, "\n") {
			if strings.HasPrefix(line, "RUN ") && strings.Contains(line, contains) {
				return line
			}
		}
		t.Fatalf("no RUN with %q in:\n%s", contains, prepared.DockerfilePreview)
		return ""
	}
	for _, test := range []struct {
		instruction string
		lines       []string
		code, phase string
		command     string
	}{
		{
			instruction: step("pnpm install"),
			lines:       []string{"ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with \"frozen-lockfile\" because pnpm-lock.yaml is not up to date with package.json"},
			code:        "build_lockfile_out_of_sync", phase: phaseInstall, command: "pnpm install --frozen-lockfile",
		},
		{
			instruction: step("pnpm run build"),
			lines:       []string{"./app/page.tsx:3:7", "Type error: Type 'number' is not assignable to type 'string'."},
			code:        "build_type_error", phase: phaseBuild, command: "pnpm run build",
		},
	} {
		// BuildKit names the step by its instruction, mounts included, and
		// the failed process by what /bin/sh ran.
		process := stripRunFlags(strings.TrimPrefix(test.instruction, "RUN "))
		if !strings.HasPrefix(process, "export ") && test.phase == phaseInstall {
			t.Fatalf("the install step lost its placeholders: %q", process)
		}
		seq := int64(0)
		collector := newBuildOutputCollector(func() int64 { return seq })
		emit := func(text string) {
			seq++
			collector.observe(BuildLog{Stream: "stderr", Text: text})
		}
		emit("#12 [build 5/8] " + test.instruction)
		for _, line := range test.lines {
			emit("#12 2.114 " + line)
		}
		emit(fmt.Sprintf("#12 ERROR: process %q did not complete successfully: exit code: 1", "/bin/sh -c "+process))
		failure := &dockerx.BuildError{Step: 12, Instruction: test.instruction, Command: process, ExitCode: 1, BuildxExit: 1}
		cause := buildFailureCause(failure, collector, causeContext{build: build, prepared: prepared}, nil)
		if cause == nil || cause.Code != test.code || cause.Phase != test.phase || !strings.HasPrefix(cause.Command, test.command) {
			t.Fatalf("%s = %+v", test.command, cause)
		}
		if strings.Contains(cause.sentence(), "prisma-generate") {
			t.Fatalf("the sentence names the recipe's placeholder: %q", cause.sentence())
		}
	}
}
