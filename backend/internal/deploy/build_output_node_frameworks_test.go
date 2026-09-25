package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// The failures a JavaScript framework prints when its configuration and the
// plan disagree, named from their own output.
func TestNodeFrameworkFailuresAreNamed(t *testing.T) {
	t.Parallel()
	for _, test := range []buildCase{
		{
			name: "next/image in a static export", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{
				"   Collecting page data ...",
				"Error: Image Optimization using the default loader is not compatible with `{ output: 'export' }`.",
				"  Possible solutions:",
			},
			want: BuildCause{Code: "build_next_image_export", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "next"},
		},
		{
			name: "an .npmrc token the install was not given", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error Failed to replace env in config: ${NPM_TOKEN}"},
			want:  BuildCause{Code: "build_registry_auth", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "npmrc", Subjects: []string{"NPM_TOKEN"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collector, err := feedBuild(test)
			cause := buildFailureCause(err, collector, causeContext{build: test.build, hostMemory: 4 << 30}, nil)
			if cause == nil {
				t.Fatal("no cause")
			}
			got := *cause
			got.LineSeq = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", got, test.want)
			}
			if sentence := cause.sentence(); len(sentence) > causeSentenceLength || !strings.HasSuffix(sentence, ".") || causeTitle(cause.Code) == "" {
				t.Fatalf("sentence = %q, title %q", sentence, causeTitle(cause.Code))
			}
		})
	}
	for _, test := range []struct {
		line, detail, sentence, field string
	}{
		{`Error: "next start" does not work with "output: export" configuration. Use "npx serve@latest out" instead.`, "next-export", "set the output directory to out", "configuration.build.outputDirectory"},
		{"Error: Cannot find module '/app/dist/main'", "nestjs", "dist/src/main.js", "configuration.build.startCommand"},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{State: "exited", ExitCode: 1, Lines: runtimeLines(test.line)}}, runtimeCauseContext{})
		if cause == nil || cause.Code != "runtime_entry_missing" || cause.Detail != test.detail || cause.Fix == nil || cause.Fix.Field != test.field ||
			!strings.Contains(cause.sentence(), test.sentence) {
			t.Fatalf("%q = %+v (%s)", test.line, cause, cause.sentence())
		}
	}
}
