package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// The Go and Rust failures the recipes' new steps can still meet, in the
// tools' own words, each named with its fix.
func compiledBuildCases() []buildCase {
	download := "go mod download"
	cargoBuild := "cargo build --release --locked --bin svc"
	return []buildCase{
		{
			name: "private module without credentials", command: download, exit: 1, build: goBuild,
			lines: []string{"go: github.com/acme/shared-lib@v0.3.0: reading github.com/acme/shared-lib/go.mod at revision v0.3.0: git ls-remote -q origin in /go/pkg/mod/cache/vcs/1f: exit status 128:",
				"\tfatal: could not read Username for 'https://github.com': terminal prompts disabled"},
			want: BuildCause{Code: "build_registry_auth", Phase: phaseInstall, Command: download, ExitCode: 1, Detail: "go"},
		},
		{
			name: "private module the proxy has not seen", command: download, exit: 1, build: goBuild,
			lines: []string{"go: github.com/acme/shared-lib@v0.3.0: verifying module: github.com/acme/shared-lib@v0.3.0: reading https://sum.golang.org/lookup/github.com/acme/shared-lib@v0.3.0: 410 Gone"},
			want: BuildCause{Code: "build_dependency_unavailable", Phase: phaseInstall, Command: download, ExitCode: 1, Detail: "go",
				Subjects: []string{"https://sum.golang.org/lookup/github.com/acme/shared-lib@v0.3.0"}},
		},
		{
			name: "local replacement outside the context", command: download, exit: 1, build: goBuild,
			lines: []string{"go: example.com/shared@v0.0.0 (replaced by ../shared): reading ../shared/go.mod: open /shared/go.mod: no such file or directory",
				"go: example.com/api imports example.com/shared: replacement directory ../shared does not exist"},
			want: BuildCause{Code: "build_dependency_local_path", Phase: phaseInstall, Command: download, ExitCode: 1, Detail: "go", Subjects: []string{"../shared"}},
		},
		{
			name: "inconsistent vendoring", command: "go build -trimpath -tags timetzdata -ldflags='-s -w' -o /out/app ./", exit: 1, build: goBuild,
			lines: []string{"go: inconsistent vendoring in /src:", "\tgithub.com/go-chi/chi/v5@v5.2.1: is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt"},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseBuild, Command: "go build -trimpath -tags timetzdata -ldflags='-s -w' -o /out/app ./", ExitCode: 1, Detail: "vendor/modules.txt"},
		},
		{
			name: "module outside the go.work", command: download, exit: 1, build: goBuild,
			lines: []string{"go: current directory is contained in a module that is not one of the workspace modules listed in go.work. You can add the module to the workspace using:"},
			want: BuildCause{Code: "build_wrong_root", Phase: phaseInstall, Command: download, ExitCode: 1, Detail: "go.work",
				Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.rootDirectory"}},
		},
		{
			name: "bindgen without libclang", command: cargoBuild, exit: 101, build: rustBuild,
			lines: []string{"thread 'main' panicked at clang-sys-1.8.1/src/lib.rs:1:1:", "Unable to find libclang: \"couldn't find any valid shared libraries matching: ['libclang.so']\""},
			want:  BuildCause{Code: "build_system_library_missing", Phase: phaseBuild, Command: cargoBuild, ExitCode: 101, Detail: "rust", Subjects: []string{"libclang"}},
		},
		{
			name: "bindgen in a static musl build script", command: cargoBuild, exit: 101, build: rustBuild,
			lines: []string{"thread 'main' panicked at bindgen-0.72.0/lib.rs:616:27:",
				"Unable to find libclang: \"the `libclang` shared library at /usr/lib/llvm22/lib/libclang.so.22.1.3 could not be opened: Dynamic loading not supported\""},
			want: BuildCause{Code: "build_system_library_missing", Phase: phaseBuild, Command: cargoBuild, ExitCode: 101, Detail: "rust-static", Subjects: []string{"libclang"}},
		},
		{
			name: "a workspace prefetch that cannot reach a module", command: "go list -e -deps ./... >/dev/null", exit: 1, build: goBuild,
			lines: []string{"go: github.com/acme/billing@v1.2.0: reading github.com/acme/billing/go.mod at revision v1.2.0: git ls-remote -q origin in /go/pkg/mod/cache/vcs/2a: exit status 128:",
				"\tfatal: could not read Username for 'https://github.com': terminal prompts disabled"},
			want: BuildCause{Code: "build_registry_auth", Phase: phaseInstall, Command: "go list -e -deps ./... >/dev/null", ExitCode: 1, Detail: "go"},
		},
		{
			name: "a crate that builds with CMake", command: cargoBuild, exit: 101, build: rustBuild,
			lines: []string{"failed to execute command: No such file or directory (os error 2)", "is `cmake` not installed?"},
			want:  BuildCause{Code: "build_native_toolchain_missing", Phase: phaseBuild, Command: cargoBuild, ExitCode: 101, Subjects: []string{"cmake"}},
		},
	}
}

func TestCompiledBuildFailuresAreNamed(t *testing.T) {
	t.Parallel()
	for _, test := range compiledBuildCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collector, err := feedBuild(test)
			cause := buildFailureCause(err, collector, causeContext{build: test.build, prepared: test.prepared, hostMemory: 4 << 30}, nil)
			if cause == nil {
				t.Fatal("no cause")
			}
			got := *cause
			got.LineSeq = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", got, test.want)
			}
			if sentence := cause.sentence(); len(sentence) > causeSentenceLength || !strings.HasSuffix(sentence, ".") {
				t.Fatalf("sentence = %q", sentence)
			}
		})
	}
	private := BuildCause{Code: "build_registry_auth", Detail: "go", Phase: phaseInstall}
	if sentence := private.sentence(); !strings.Contains(sentence, "GIT_TOKEN") {
		t.Fatalf("private module sentence = %q", sentence)
	}
	static := BuildCause{Code: "build_system_library_missing", Detail: "rust-static", Subjects: []string{"libclang"}, Phase: phaseBuild}
	if what, action := static.explain(); !strings.Contains(what, "cannot load a shared library") || !strings.Contains(action, "crt-static") {
		t.Fatalf("static libclang = %q / %q", what, action)
	}
}

func TestLeptosConfigurationVariablesAreNamedAtRuntime(t *testing.T) {
	cause := applicationOutputCause([]ContainerDiagnostics{{State: "exited", ExitCode: 101, Lines: []RuntimeLogLine{
		{Text: `thread 'main' panicked at src/main.rs:9:66: called Result::unwrap() on an Err value: EnvVarError("LEPTOS_OUTPUT_NAME: NotPresent")`},
	}}}, runtimeCauseContext{})
	if cause == nil || cause.Code != "runtime_env_missing" || !reflect.DeepEqual(cause.Subjects, []string{"LEPTOS_OUTPUT_NAME"}) {
		t.Fatalf("cause = %+v", cause)
	}
}
