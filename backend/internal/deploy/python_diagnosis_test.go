package deploy

import (
	"testing"
)

// Django with DEBUG off and no LOGGING writes nothing about a request it
// refuses; an error status with no error in the output is named for what it
// is rather than left as a silent health gate.
func TestDjangoErrorsHiddenByItsLoggingAreNamed(t *testing.T) {
	t.Parallel()
	django := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", Framework: "django", StartCommand: "gunicorn site.wsgi"}
	checks := func(status int) []CheckEvidence {
		return []CheckEvidence{{Name: "readiness", Attempts: []CheckAttemptEvidence{{StatusCode: status}}}}
	}
	quiet := []ContainerDiagnostics{{State: "running", Lines: []RuntimeLogLine{
		{Text: "[INFO] Starting gunicorn 23.0.0"}, {Text: `127.0.0.1 - - "GET / HTTP/1.1" 500 145 "-" "Just-Dashboard"`},
	}}}
	cause := applicationOutputCause(quiet, runtimeCauseContext{build: django, checks: checks(500)})
	if cause == nil || cause.Code != "runtime_errors_hidden" || cause.Detail != "django" || cause.Subjects[0] != "500" || cause.sentence() == "" {
		t.Fatalf("500 = %+v", cause)
	}
	cause = applicationOutputCause(quiet, runtimeCauseContext{build: django, checks: checks(400)})
	if cause == nil || cause.Code != "runtime_host_disallowed" || cause.Detail != "django" {
		t.Fatalf("400 = %+v", cause)
	}
	loud := []ContainerDiagnostics{{State: "running", Lines: []RuntimeLogLine{{Text: "Traceback (most recent call last):"}, {Text: "ZeroDivisionError: division by zero"}}}}
	if cause := applicationOutputCause(loud, runtimeCauseContext{build: django, checks: checks(500)}); cause != nil && cause.Code == "runtime_errors_hidden" {
		t.Fatalf("a logged error is not hidden: %+v", cause)
	}
	flask := django
	flask.Framework = "flask"
	if cause := applicationOutputCause(quiet, runtimeCauseContext{build: flask, checks: checks(500)}); cause != nil && cause.Code == "runtime_errors_hidden" {
		t.Fatalf("only Django hides its errors this way: %+v", cause)
	}
	if outputCauseTitles["runtime_errors_hidden"] == "" {
		t.Fatal("the cause has no title")
	}
}

func TestPythonSystemLibraryFailuresPointAtTheSystemPackages(t *testing.T) {
	t.Parallel()
	python := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "python app.py"}
	missing := []ContainerDiagnostics{{State: "exited", ExitCode: 1, Lines: []RuntimeLogLine{{Text: "ImportError: libGL.so.1: cannot open shared object file: No such file or directory"}}}}
	cause := applicationOutputCause(missing, runtimeCauseContext{build: python})
	if cause == nil || cause.Code != "runtime_library_missing" || cause.Fix == nil || cause.Fix.Field != "configuration.build.systemPackages" || cause.Fix.Value != "libgl1" {
		t.Fatalf("libGL = %+v", cause)
	}
	node := python
	node.Recipe = "node"
	if cause := applicationOutputCause(missing, runtimeCauseContext{build: node}); cause == nil || cause.Fix != nil {
		t.Fatalf("only the Python recipe has system packages: %+v", cause)
	}
	for code, subject := range map[string]string{"build_system_library_missing": "pg_config", "build_native_toolchain_missing": "gcc"} {
		fix := buildCauseFix(&BuildCause{Code: code, Detail: "python", Subjects: []string{subject}}, causeContext{build: python})
		if fix == nil || fix.Field != "configuration.build.systemPackages" || fix.Value == "" {
			t.Fatalf("%s = %+v", code, fix)
		}
	}
}
