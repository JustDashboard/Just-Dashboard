package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func TestShapesThatAreNotServicesAreNamed(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		files map[string]string
		root  string
		kind  string
	}{
		{"npm library", map[string]string{
			"package.json":      `{"name":"lib","main":"dist/index.js","types":"dist/index.d.ts","exports":{".":"./dist/index.js"},"files":["dist"],"scripts":{"build":"tsup"}}`,
			"package-lock.json": "{}",
		}, "", "library"},
		{"component library built with vite", map[string]string{
			"package.json":      `{"name":"ui","exports":{".":"./dist/ui.js"},"files":["dist"],"peerDependencies":{"react":"19"},"scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`,
			"package-lock.json": "{}",
		}, "", "library"},
		{"command-line tool", map[string]string{
			"package.json": `{"name":"tool","bin":{"tool":"cli.js"}}`, "package-lock.json": "{}",
		}, "", "cli"},
		{"vs code extension", map[string]string{
			"package.json": `{"name":"ext","main":"out/extension.js","engines":{"vscode":"^1.90.0"},"scripts":{"build":"tsc"}}`, "package-lock.json": "{}",
		}, "", "editor-extension"},
		{"browser extension", map[string]string{
			"package.json": `{"name":"ext","scripts":{"build":"wxt build"},"devDependencies":{"wxt":"0.20","vite":"6"}}`, "package-lock.json": "{}",
		}, "", "browser-extension"},
		{"electron app", map[string]string{
			"package.json": `{"name":"desk","main":"main.js","scripts":{"start":"electron ."},"devDependencies":{"electron":"33"}}`, "package-lock.json": "{}",
		}, "", "desktop-app"},
		{"react native app", map[string]string{
			"package.json": `{"name":"mobile","scripts":{"start":"react-native start"},"dependencies":{"react-native":"0.76"}}`, "package-lock.json": "{}",
		}, "", "mobile-app"},
		{"python library", map[string]string{
			"pyproject.toml": "[project]\nname = \"lib\"\ndependencies = [\"requests>=2\"]\n\n[build-system]\nrequires = [\"hatchling\"]\n",
		}, "", "library"},
		{"notebooks", map[string]string{
			"requirements.txt": "pandas==2.2.0\n", "analysis.ipynb": "{}",
		}, "", "notebook"},
		{"rust library crate", map[string]string{
			"Cargo.toml": "[package]\nname = \"lib\"\nversion = \"0.1.0\"\n\n[lib]\nname = \"lib\"\n", "src/lib.rs": "",
		}, "", "library"},
		{"windows forms program", map[string]string{
			"App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>WinExe</OutputType><TargetFramework>net8.0-windows</TargetFramework><UseWindowsForms>true</UseWindowsForms></PropertyGroup></Project>`,
		}, "", "windows-only"},
		{"go library", map[string]string{
			"go.mod": "module example.test/lib\n\ngo 1.26\n", "lib.go": "package lib\n\nfunc Answer() int { return 42 }\n",
		}, "", "library"},
		{"github action", map[string]string{
			"action.yml":   "name: hello\nruns:\n  using: node20\n  main: dist/index.js\n",
			"package.json": `{"name":"action","main":"dist/index.js","scripts":{"build":"ncc build"}}`, "package-lock.json": "{}",
		}, "", "github-action"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectShapeFixture(t, fixture.files)
			var found *DetectedCandidate
			for index := range result.Candidates {
				if result.Candidates[index].Root == fixture.root {
					found = &result.Candidates[index]
				}
			}
			if found == nil || found.NotDeployable != fixture.kind {
				t.Fatalf("candidate = %#v, want %s", result.Candidates, fixture.kind)
			}
			if result.SelectedID != "" {
				t.Fatalf("a %s was selected on its own", fixture.kind)
			}
			configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}
			findings := detectionOutcomeFindings(&result, configuration)
			if len(findings) != 1 || findings[0].Code != "source_not_a_service" || findings[0].Severity != PreflightBlocked {
				t.Fatalf("findings = %#v", findings)
			}
		})
	}
}

func TestServicesAreNotMistakenForLibraries(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"express service": {"package.json": expressManifest, "package-lock.json": "{}"},
		"go service": {
			"go.mod": "module example.test/app\n\ngo 1.26\n", "cmd/app/main.go": "package main\n\nfunc main() {}\n",
			"internal/lib.go": "package internal\n",
		},
		"rust binary":   {"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\n", "src/main.rs": ""},
		"flask app":     {"requirements.txt": "flask==3.1.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n"},
		"python script": {"requirements.txt": "requests==2.32.0\n", "main.py": "print(1)\n"},
		"python bot packaged with a build backend": {
			"pyproject.toml": "[project]\nname = \"bot\"\ndependencies = [\"discord.py\"]\n\n[build-system]\nrequires = [\"hatchling\"]\n",
			"bot.py":         "import discord\n",
		},
		"python bot in a src layout run with python -m": {
			"pyproject.toml":         "[project]\nname = \"bot\"\ndependencies = [\"discord.py>=2\"]\n\n[build-system]\nrequires = [\"hatchling\"]\n",
			"src/bot/__init__.py":    "",
			"src/bot/__main__.py":    "from bot.client import run\nrun()\n",
			"src/bot/client.py":      "import discord\n",
			"tests/test_client.py":   "",
			"tests/fixtures/data.py": "",
		},
		"python package with a __main__ and no known library": {
			"pyproject.toml":       "[project]\nname = \"tool\"\ndependencies = [\"requests\"]\n\n[build-system]\nrequires = [\"hatchling\"]\n",
			"src/tool/__main__.py": "",
			"src/tool/__init__.py": "",
		},
		"python worker with a known library and nothing at the top": {
			"pyproject.toml":   "[project]\nname = \"jobs\"\ndependencies = [\"celery[redis]\"]\n\n[build-system]\nrequires = [\"setuptools\"]\n",
			"jobs/tasks.py":    "",
			"jobs/__init__.py": "",
		},
		"python package with a Procfile": {
			"pyproject.toml":  "[project]\nname = \"svc\"\n\n[build-system]\nrequires = [\"hatchling\"]\n",
			"Procfile":        "worker: python -m svc\n",
			"svc/__init__.py": "",
		},
		"python bot with a console script": {
			"pyproject.toml": "[project]\nname = \"bot\"\n\n[project.scripts]\nbot = \"bot.main:run\"\n\n[build-system]\nrequires = [\"hatchling\"]\n",
			"bot/main.py":    "",
		},
		"web app with an electron devDependency": {
			"package.json": `{"name":"web","scripts":{"build":"vite build"},"devDependencies":{"vite":"6","electron":"33"}}`, "package-lock.json": "{}",
		},
		"capacitor web app": {
			"package.json": `{"name":"hybrid","scripts":{"build":"vite build"},"dependencies":{"@capacitor/core":"6"},"devDependencies":{"vite":"6"}}`, "package-lock.json": "{}",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := detectShapeFixture(t, files)
			if len(result.Candidates) != 1 || result.Candidates[0].NotDeployable != "" || result.SelectedID == "" {
				t.Fatalf("service = %#v", result)
			}
		})
	}
}

func TestMobileAndDesktopSubprojectsAreSetAside(t *testing.T) {
	t.Run("android module in a react native repository", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": `{"name":"mobile","dependencies":{"react-native":"0.76"}}`, "package-lock.json": "{}",
			"android/build.gradle":     "buildscript { dependencies { classpath(\"com.android.tools.build:gradle:8.5.0\") } }",
			"android/app/build.gradle": "apply plugin: \"com.android.application\"\n",
		})
		for _, candidate := range result.Candidates {
			if candidate.Recipe == "java" {
				t.Fatalf("an Android module became a Java candidate: %#v", candidate)
			}
		}
		if setAsideKind(result, "mobile-app") == nil {
			t.Fatalf("android module not set aside: %#v", result.SetAside)
		}
	})
	t.Run("android-only repository is not a service", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"settings.gradle":  "include ':app'\n",
			"app/build.gradle": "plugins { id 'com.android.application' }\n",
		})
		findings := detectionOutcomeFindings(&result, PlanConfiguration{})
		if len(result.Candidates) != 0 || len(findings) != 1 || findings[0].Code != "source_not_a_service" {
			t.Fatalf("android repository = %#v, %#v", result, findings)
		}
	})
	t.Run("flutter mobile app", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"pubspec.yaml":             "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n",
			"android/app/build.gradle": "plugins { id \"com.android.application\" }\n",
		})
		if len(result.Candidates) != 0 || setAsideKind(result, "mobile-app") == nil {
			t.Fatalf("flutter = %#v", result)
		}
	})
	t.Run("tauri frontend and shell", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":              `{"name":"desk","scripts":{"build":"vite build"},"dependencies":{"@tauri-apps/api":"2"},"devDependencies":{"vite":"6"}}`,
			"package-lock.json":         "{}",
			"src-tauri/Cargo.toml":      "[package]\nname = \"desk\"\nversion = \"0.1.0\"\n\n[dependencies]\ntauri = \"2\"\n",
			"src-tauri/src/main.rs":     "",
			"src-tauri/tauri.conf.json": "{}",
		})
		frontend := candidateAtRoot(result, "", BuildRecipe)
		shell := candidateAtRoot(result, "src-tauri", BuildRecipe)
		if frontend == nil || frontend.DesktopShell != "tauri" || frontend.Demotion == "" || shell == nil || shell.NotDeployable != "desktop-app" || result.SelectedID != "" {
			t.Fatalf("tauri = %#v", result.Candidates)
		}
		result.SelectedID = frontend.ID
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "desktop_frontend_only", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("expo web export", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"app","scripts":{"start":"expo start"},"dependencies":{"expo":"52","react-native":"0.76","react-native-web":"0.19"}}`,
			"package-lock.json": "{}",
			"app.json":          `{"expo":{"web":{"output":"static"}}}`,
		})
		selected := selectedOf(result)
		if selected == nil || selected.BuildCommand != "npx expo export --platform web" || selected.OutputDirectory != "dist" ||
			selected.Profile != ProfileStatic || selected.StartCommand != "" {
			t.Fatalf("expo = %#v", result.Candidates)
		}
	})
	t.Run("hosted functions", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": viteManifest, "package-lock.json": "{}",
			"supabase/functions/hello/deno.json": `{"tasks":{"start":"deno run index.ts"}}`,
			"firebase.json":                      `{"functions":{"source":"functions"}}`,
			"functions/package.json":             `{"name":"functions","main":"index.js","dependencies":{"firebase-functions":"6"}}`,
		})
		if len(result.Candidates) != 1 || result.Candidates[0].Root != "" || setAsideKind(result, "hosted-functions") == nil {
			t.Fatalf("hosted functions = %#v, %#v", result.Candidates, result.SetAside)
		}
	})
}

func hasFinding(findings []PreflightFinding, code string, severity PreflightSeverity) bool {
	for _, item := range findings {
		if item.Code == code && item.Severity == severity {
			return true
		}
	}
	return false
}

func TestSelectedNotAServiceYieldsToAnExplicitStartCommand(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"package.json": `{"name":"tool","bin":{"tool":"cli.js"}}`, "package-lock.json": "{}",
		"docs/index.html": "<html></html>",
	})
	cli := candidateAtRoot(result, "", BuildRecipe)
	if cli == nil || cli.NotDeployable != "cli" {
		t.Fatalf("cli = %#v", result.Candidates)
	}
	result.SelectedID = cli.ID
	blocked := detectionOutcomeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
	if !hasFinding(blocked, "source_not_a_service", PreflightBlocked) {
		t.Fatalf("findings = %#v", blocked)
	}
	warned := detectionOutcomeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, StartCommand: "node cli.js serve --port 3000"}})
	if !hasFinding(warned, "source_not_a_service", PreflightWarning) {
		t.Fatalf("findings = %#v", warned)
	}
	if strings.Contains(blocked[0].Measured, "candidate-") {
		t.Fatalf("selection reason names an id: %q", blocked[0].Measured)
	}
}

// When nothing else is deployable the verdict still yields to the operator:
// a start command of their own, or another build method, turns the block
// into a warning, because detection can be wrong about what a source is.
func TestNotAServiceYieldsToTheOperatorWhenNothingElseIs(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"package.json":      `{"name":"lib","main":"dist/index.js","types":"dist/index.d.ts","exports":{".":"./dist/index.js"},"files":["dist"],"scripts":{"build":"tsup"}}`,
		"package-lock.json": "{}",
	})
	if len(result.Candidates) != 1 || result.Candidates[0].NotDeployable != "library" || result.SelectedID != "" {
		t.Fatalf("library = %#v", result)
	}
	for _, fixture := range []struct {
		name     string
		build    BuildPlanConfig
		selected bool
		want     PreflightSeverity
	}{
		{"as detected", BuildPlanConfig{Method: BuildRecipe}, false, PreflightBlocked},
		{"as detected and picked in the chooser", BuildPlanConfig{Method: BuildRecipe}, true, PreflightBlocked},
		{"the detected start command", BuildPlanConfig{Method: BuildRecipe, StartCommand: result.Candidates[0].StartCommand}, false, PreflightBlocked},
		{"an operator start command", BuildPlanConfig{Method: BuildRecipe, StartCommand: "node dist/server.js"}, false, PreflightWarning},
		{"an operator start command on the picked candidate", BuildPlanConfig{Method: BuildRecipe, StartCommand: "node dist/server.js"}, true, PreflightWarning},
		{"a Dockerfile instead", BuildPlanConfig{Method: BuildDockerfile}, false, PreflightWarning},
		{"a static site instead", BuildPlanConfig{Method: BuildStatic}, false, PreflightWarning},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			detection := result
			if fixture.selected {
				detection.SelectedID = result.Candidates[0].ID
			}
			findings := detectionOutcomeFindings(&detection, PlanConfiguration{Build: fixture.build})
			if len(findings) != 1 || !hasFinding(findings, "source_not_a_service", fixture.want) {
				t.Fatalf("findings = %#v", findings)
			}
		})
	}
}

// A verdict drawn from a file the walk did not find is not drawn when a bound
// stopped the walk: the service's src/main.rs, main.py or main package may be
// among the files it never reached.
func TestTruncatedWalkDrawsNoLibraryVerdict(t *testing.T) {
	// Manifests are read breadth-first, so nine of them use a four-file bound
	// up before the lexical pass reaches the service's entry point.
	crowd := func(files map[string]string) map[string]string {
		for index := 0; index < 9; index++ {
			files[fmt.Sprintf("pkgs%d/package.json", index)] = `{"name":"p"}`
		}
		return files
	}
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		maxFiles int
	}{
		{"rust crate with a library table", crowd(map[string]string{
			"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[lib]\nname = \"app\"\n", "src/main.rs": "fn main() {}\n", "src/lib.rs": "",
		}), 4},
		{"python project with a build backend", crowd(map[string]string{
			"pyproject.toml": "[project]\nname = \"app\"\n\n[build-system]\nrequires = [\"hatchling\"]\n", "main.py": "print(1)\n",
		}), 4},
		// The lexical pass reads a/lib.go, then stops at the file bound on
		// b/app.py before c/main.go.
		{"go module with its main package last", map[string]string{
			"go.mod": "module example.test/app\n\ngo 1.26\n", "a/lib.go": "package lib\n", "b/app.py": "",
			"c/main.go": "package main\n\nfunc main() {}\n",
		}, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{Limits: DetectionLimits{MaxFiles: fixture.maxFiles}}).DetectPath(t.Context(), root, SourceIdentity{})
			if err != nil || !result.Truncated {
				t.Fatalf("detection was not truncated: %#v, %v", result, err)
			}
			candidate := candidateAtRoot(result, "", BuildRecipe)
			if candidate == nil || candidate.NotDeployable != "" {
				t.Fatalf("a truncated walk called the service a library: %#v", result.Candidates)
			}
		})
	}
	t.Run("a pruned directory under the root", func(t *testing.T) {
		deep := strings.Repeat("d/", 11)
		result := detectShapeFixture(t, map[string]string{
			"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[lib]\nname = \"app\"\n", "src/lib.rs": "",
			deep + "notes.txt": "",
		})
		if candidate := candidateAtRoot(result, "", BuildRecipe); candidate == nil || candidate.NotDeployable != "" {
			t.Fatalf("a depth-pruned walk called the crate a library: %#v", result.Candidates)
		}
	})
}
