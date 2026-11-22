package deploy

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// The language recipes' dependency order and caches: what each copies and
// installs before the source, what it mounts, and that a forced build
// renders no cache at all.

// prepareCachedAndForced prepares one recipe as an environment's build and
// again as a forced build of it, which must render no cache mount.
func prepareCachedAndForced(t *testing.T, boundary, root string, config BuildPlanConfig, variables ...string) (cached, forced string) {
	t.Helper()
	config.Method = BuildRecipe
	builder := NewArtifactBuilder(&artifactBackendFake{})
	prepared, err := builder.PrepareWithin(t.Context(), boundary, root, config, false, "just-dashboard/deployment-7:run-3", variables...)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	config.NoCache = true
	again, err := builder.PrepareWithin(t.Context(), boundary, root, config, false, "just-dashboard/deployment-7:run-4", variables...)
	if err != nil {
		t.Fatalf("prepare a forced build: %v", err)
	}
	if strings.Contains(again.DockerfilePreview, "type=cache") {
		t.Fatalf("a forced build mounts a cache:\n%s", again.DockerfilePreview)
	}
	return prepared.DockerfilePreview, again.DockerfilePreview
}

// assertBefore fails unless first occurs in dockerfile, and before second.
func assertBefore(t *testing.T, dockerfile, first, second string) {
	t.Helper()
	at, then := strings.Index(dockerfile, first), strings.Index(dockerfile, second)
	if at < 0 || then < 0 || at > then {
		t.Fatalf("Dockerfile does not have %q before %q:\n%s", first, second, dockerfile)
	}
}

// dockerfileLine is the first line of dockerfile that contains part.
func dockerfileLine(t *testing.T, dockerfile, part string) string {
	t.Helper()
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.Contains(line, part) {
			return line
		}
	}
	t.Fatalf("Dockerfile has no line with %q:\n%s", part, dockerfile)
	return ""
}

func cacheMountFor(name, target string) string {
	return "--mount=type=cache,id=jd-deployment-7-" + name + ",target=" + target + ",sharing=locked "
}

func TestRustCachesCratesAndTargetAndCopiesTheBinaryInTheBuildStep(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"api\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\naxum = \"0.8\"\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n\n[[package]]\nname = \"api\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"axum\"\nversion = \"0.8.4\"\n")
	writeBuildFixture(t, root, "src/main.rs", "fn main() {}\n")
	config := BuildPlanConfig{Recipe: "rust"}
	cached, forced := prepareCachedAndForced(t, root, root, config)
	registry := cacheMountFor("cargo-registry", "/usr/local/cargo/registry") + cacheMountFor("cargo-git", "/usr/local/cargo/git")
	assertGoDockerfile(t, cached, "RUN "+registry+"cargo fetch --locked\n")
	build := dockerfileLine(t, cached, "cargo build --release")
	for _, want := range []string{"RUN " + registry + cacheMountFor("cargo-target", "/src/target"),
		"rm -f /src/target/release/api && cargo build --release --locked --bin api && (mkdir -p /out && test -f /src/target/release/api && cp /src/target/release/api /out/app ||"} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
	// A forced build compiles from nothing, and still copies the binary out
	// of the step that built it.
	assertGoDockerfile(t, forced, "RUN cargo fetch --locked\n", "&& cp /src/target/release/api /out/app ||")
	if strings.Count(forced, "\nRUN ") != strings.Count(cached, "\nRUN ") {
		t.Fatalf("the forced build has other steps:\n%s\n---\n%s", forced, cached)
	}

	// A private registry's token is the fetch's alone, so the crates it
	// fetches stay in the image for the build that has no token.
	secret := config
	secret.Secrets = []BuildSecretConfig{{Variable: "CARGO_REGISTRIES_ACME_TOKEN", Step: "install"}}
	cached, _ = prepareCachedAndForced(t, root, root, secret, "CARGO_REGISTRIES_ACME_TOKEN")
	if strings.Contains(cached, "cargo-registry") || !strings.Contains(cached, cacheMountFor("cargo-target", "/src/target")) {
		t.Fatalf("a private registry's crates are cached:\n%s", cached)
	}

	// An absolute target-dir may name a directory of the image itself.
	writeBuildFixture(t, root, ".cargo/config.toml", "[build]\ntarget-dir = \"/opt/out\"\n")
	cached, _ = prepareCachedAndForced(t, root, root, config)
	if strings.Contains(cached, "cargo-target") || !strings.Contains(cached, "cp /opt/out/release/api /out/app") {
		t.Fatalf("an absolute target-dir is cached:\n%s", cached)
	}
	writeBuildFixture(t, root, ".cargo/config.toml", "[build]\ntarget-dir = \"build/cargo\"\n")
	cached, _ = prepareCachedAndForced(t, root, root, config)
	assertGoDockerfile(t, cached, cacheMountFor("cargo-target", "/src/build/cargo"), "cp /src/build/cargo/release/api /out/app")
}

func TestRustTargetIsNeverMountedOverTheSource(t *testing.T) {
	for _, recipe := range []rustRecipe{{targetDir: "."}, {targetDir: "crates", member: "crates/server"}, {targetDir: "/target"}} {
		if got := rustTargetPath(recipe); got != "" {
			t.Fatalf("target %+v mounts at %q", recipe, got)
		}
	}
	if got := rustTargetPath(rustRecipe{member: "crates/server"}); got != "/src/target" {
		t.Fatalf("a workspace target = %q", got)
	}
}

func TestLeptosAndTrunkCacheTheirBuildsApartFromTheMuslTarget(t *testing.T) {
	leptos := t.TempDir()
	writeBuildFixture(t, leptos, "Cargo.toml", "[package]\nname = \"site\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[lib]\ncrate-type = [\"cdylib\", \"rlib\"]\n\n[dependencies]\nleptos = { version = \"0.8\" }\nleptos_axum = { version = \"0.8\", optional = true }\naxum = { version = \"0.8\", optional = true }\n\n[features]\nhydrate = [\"leptos/hydrate\"]\nssr = [\"dep:axum\", \"dep:leptos_axum\", \"leptos/ssr\"]\n\n[package.metadata.leptos]\nsite-root = \"target/site\"\nhash-files = true\nbin-features = [\"ssr\"]\nlib-features = [\"hydrate\"]\n")
	writeBuildFixture(t, leptos, "src/main.rs", "fn main() {}\n")
	writeBuildFixture(t, leptos, "src/lib.rs", "\n")
	cached, forced := prepareCachedAndForced(t, leptos, leptos, BuildPlanConfig{Recipe: "rust"})
	build := dockerfileLine(t, cached, "cargo leptos build")
	for _, want := range []string{cacheMountFor("cargo-target-debian", "/src/target"), cacheMountFor("cargo-leptos-tools", "/root/.cache/cargo-leptos"),
		// The last build's site is removed, so the image holds this
		// build's hashed bundle alone.
		"rm -rf target/release/site target/release/hash.txt target/site && cargo leptos build --release && (mkdir -p /out"} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
	assertGoDockerfile(t, forced, "rm -rf target/release/site target/release/hash.txt target/site && cargo leptos build --release && (")

	trunk := t.TempDir()
	writeBuildFixture(t, trunk, "Cargo.toml", "[package]\nname = \"counter\"\n\n[dependencies]\nyew = { version = \"0.21\", features = [\"csr\"] }\n")
	writeBuildFixture(t, trunk, "src/main.rs", "fn main() { yew::Renderer::<App>::new().render(); }\n")
	writeBuildFixture(t, trunk, "index.html", "<!doctype html><html><head><link data-trunk rel=\"rust\" /></head><body></body></html>")
	cached, _ = prepareCachedAndForced(t, trunk, trunk, BuildPlanConfig{Recipe: "rust", OutputDirectory: "dist"})
	build = dockerfileLine(t, cached, "trunk build")
	for _, want := range []string{cacheMountFor("cargo-target-debian", "/src/target"), cacheMountFor("trunk-tools", "/root/.cache/trunk")} {
		if !strings.Contains(build, want) {
			t.Fatalf("build step %q lacks %q", build, want)
		}
	}
}

func TestGoDownloadsModulesBeforeTheSourceAndCachesTheBuild(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire github.com/go-chi/chi/v5 v5.2.1\n")
	writeBuildFixture(t, root, "go.sum", "github.com/go-chi/chi/v5 v5.2.1 h1:x\ngithub.com/go-chi/chi/v5 v5.2.1/go.mod h1:y\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nimport _ \"github.com/go-chi/chi/v5\"\n\nfunc main() {}\n")
	// A repository rule written for its own Dockerfile does not take the
	// root module files out of the context (recipeDockerignore keeps them).
	writeBuildFixture(t, root, ".dockerignore", "*\n!main.go\n")
	cached, forced := prepareCachedAndForced(t, root, root, BuildPlanConfig{Recipe: "go"})
	modules, compile := cacheMountFor("go-mod", "/go/pkg/mod"), cacheMountFor("go-mod", "/go/pkg/mod")+cacheMountFor("go-build", "/root/.cache/go-build")
	assertGoDockerfile(t, cached, "ENV CGO_ENABLED=0 GOTOOLCHAIN=local\nCOPY go.mod go.sum /src/\nRUN "+modules+"go mod download\nCOPY . .\n",
		"RUN "+compile+"go build -trimpath")
	assertGoDockerfile(t, forced, "COPY go.mod go.sum /src/\nRUN go mod download\nCOPY . .\n", "RUN go build -trimpath")

	// A private module's token is the download's alone, so the modules it
	// fetches stay in the image; the build cache holds no module and stays.
	private := BuildPlanConfig{Recipe: "go", Secrets: []BuildSecretConfig{{Variable: "GIT_TOKEN", Step: "install"}}}
	cached, _ = prepareCachedAndForced(t, root, root, private, "GIT_TOKEN")
	if strings.Contains(cached, "go-mod") || !strings.Contains(cached, cacheMountFor("go-build", "/root/.cache/go-build")+"go build") {
		t.Fatalf("a private download is cached:\n%s", cached)
	}
	assertBefore(t, cached, "--mount=type=secret,id=GIT_TOKEN", "COPY . .")

	// A replacement without a go.mod cannot be downloaded from module files
	// alone: the download stays after the source, as before.
	writeBuildFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n\nrequire example.com/lib v0.0.0\n\nreplace example.com/lib => ./lib\n")
	writeBuildFixture(t, root, "lib/lib.go", "package lib\n")
	cached, _ = prepareCachedAndForced(t, root, root, BuildPlanConfig{Recipe: "go"})
	assertBefore(t, cached, "COPY . .", "go mod download")
	writeBuildFixture(t, root, "lib/go.mod", "module example.com/lib\n\ngo 1.26\n")
	cached, _ = prepareCachedAndForced(t, root, root, BuildPlanConfig{Recipe: "go"})
	assertGoDockerfile(t, cached, "COPY go.mod go.sum /src/\nCOPY lib/go.mod /src/lib/\nRUN "+modules+"go mod download\nCOPY . .\n")

	// A vendored module downloads nothing; a workspace lists its imports
	// once the source is there.
	vendored := t.TempDir()
	writeBuildFixture(t, vendored, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeBuildFixture(t, vendored, "vendor/modules.txt", "# github.com/go-chi/chi/v5 v5.2.1\n")
	writeBuildFixture(t, vendored, "main.go", "package main\nfunc main() {}\n")
	cached, _ = prepareCachedAndForced(t, vendored, vendored, BuildPlanConfig{Recipe: "go"})
	if strings.Contains(cached, "go mod download") || strings.Contains(cached, "COPY go.mod") {
		t.Fatalf("a vendored module downloads:\n%s", cached)
	}
	workspace := t.TempDir()
	writeBuildFixture(t, workspace, "go.work", "go 1.26\n\nuse ./api\n")
	writeBuildFixture(t, workspace, "api/go.mod", "module example.com/api\n\ngo 1.26\n")
	writeBuildFixture(t, workspace, "api/main.go", "package main\nfunc main() {}\n")
	cached, _ = prepareCachedAndForced(t, workspace, workspace+"/api", BuildPlanConfig{Recipe: "go", RootDirectory: "api"})
	assertGoDockerfile(t, cached, "COPY . .\nWORKDIR /src/api\nRUN "+modules+"go list -e -deps ./... >/dev/null\n")
}

// sitePythonRun is the RUN a site build's Python install renders as, with
// the download caches of a build tagged like prepareSite's.
func sitePythonRun(command string) string {
	mounts, cached := pythonCachedRun(newRecipeCache("site:1", "reuse"), command)
	return "RUN " + mounts + cached + "\n"
}

func TestPythonInstallsDependenciesBeforeTheSourceWhereTheManifestsSayAll(t *testing.T) {
	pip := cacheMountFor("pip", "/root/.cache/pip")
	for _, fixture := range []struct {
		name  string
		files map[string]string
		start string
		// first is what installs before COPY . ., after what follows it;
		// an empty first keeps the whole install after the source.
		first, after string
	}{
		{name: "requirements with an include and a constraint",
			files: map[string]string{"requirements.txt": "-r requirements/base.txt\n-c constraints.txt\ngunicorn==23.0.0\n", "requirements/base.txt": "flask==3.1.0\n", "constraints.txt": "werkzeug<4\n"},
			start: "gunicorn app:app",
			first: "COPY requirements.txt constraints.txt /app/\nCOPY requirements/base.txt /app/requirements/\nRUN " + pip + "pip install --requirement requirements.txt\nCOPY . .\n"},
		{name: "a src layout installs the project after its source",
			files: map[string]string{"requirements.txt": "flask==3.1.0\ngunicorn==23.0.0\n", "pyproject.toml": "[build-system]\nrequires = [\"setuptools\"]\nbuild-backend = \"setuptools.build_meta\"\n[project]\nname = \"app\"\n[tool.setuptools.packages.find]\nwhere = [\"src\"]\n"},
			start: "gunicorn app:app",
			first: "RUN " + pip + "pip install --requirement requirements.txt\nCOPY . .\nRUN " + pip + "pip install --no-deps .\n"},
		{name: "Pipfile.lock",
			files: map[string]string{"Pipfile": "[packages]\nflask = \"==3.1.0\"\ngunicorn = \"*\"\n", "Pipfile.lock": `{"_meta": {"hash": {"sha256": "x"}}, "default": {"flask": {"version": "==3.1.0"}, "gunicorn": {"version": "==23.0.0"}}}`},
			start: "gunicorn app:app",
			first: "COPY Pipfile Pipfile.lock /app/\nRUN " + pip + cacheMountFor("pipenv", "/root/.cache/pipenv") + "pip install pipenv==" + pythonPipenvRelease + " && pipenv install --system --deploy\nCOPY . .\n"},
		{name: "an editable line needs the source", files: map[string]string{"requirements.txt": "-e .\nflask==3.1.0\n", "setup.py": "from setuptools import setup\nsetup(name='app')\n"},
			start: "gunicorn app:app", after: "COPY . .\nRUN " + pip + "pip install --requirement requirements.txt\n"},
		{name: "a local wheel needs the source", files: map[string]string{"requirements.txt": "flask==3.1.0\nvendor/lib-1.0-py3-none-any.whl\n"},
			start: "gunicorn app:app", after: "COPY . .\nRUN " + pip + "pip install --requirement requirements.txt\n"},
		{name: "a committed .venv would land on the installed one", files: map[string]string{"requirements.txt": "flask==3.1.0\n", ".venv/pyvenv.cfg": "home = /usr\n"},
			start: "gunicorn app:app", after: "COPY . .\nRUN " + pip + "pip install --requirement requirements.txt\n"},
		{name: "setup.py installs from its source", files: map[string]string{"setup.py": "from setuptools import setup\nsetup(name='app', install_requires=['flask'])\n"},
			start: "gunicorn app:app", after: "COPY . .\nRUN " + pip + "pip install .\n"},
		{name: "uv sources name directories of the checkout",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"lib\"]\n[tool.uv.sources]\nlib = { path = \"./lib\" }\n", "uv.lock": "[[package]]\nname = \"x\"\nversion = \"0\"\nsource = { virtual = \".\" }\ndependencies = [{ name = \"lib\" }]\n"},
			start: "uvicorn main:app", after: "COPY . .\nRUN " + pip + cacheMountFor("uv", "/root/.cache/uv") + "export UV_LINK_MODE=copy && pip install uv=="},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range fixture.files {
				writeBuildFixture(t, root, name, content)
			}
			cached, forced := prepareCachedAndForced(t, root, root, BuildPlanConfig{Recipe: "python", StartCommand: fixture.start})
			if fixture.first != "" {
				assertGoDockerfile(t, cached, fixture.first)
				// A forced build installs in the same order, with no cache
				// for pip to keep in the layer.
				if !strings.Contains(forced, "pip install --no-cache-dir") || strings.Index(forced, "RUN pip install --no-cache-dir") > strings.Index(forced, "COPY . .") {
					t.Fatalf("forced build:\n%s", forced)
				}
				return
			}
			assertGoDockerfile(t, cached, fixture.after)
			if strings.Contains(cached, "COPY requirements") || strings.Contains(cached, "COPY pyproject") {
				t.Fatalf("an install that needs the source copies its manifests first:\n%s", cached)
			}
		})
	}
}

func TestPythonInstallSecretsReachTheDependencyStep(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "requirements.txt", "--extra-index-url https://${PIP_TOKEN}@pypi.example.test/simple\nflask==3.1.0\n")
	config := BuildPlanConfig{Recipe: "python", StartCommand: "gunicorn app:app", Secrets: []BuildSecretConfig{{Variable: "PIP_TOKEN", Step: "install"}}}
	cached, _ := prepareCachedAndForced(t, root, root, config, "PIP_TOKEN")
	install := dockerfileLine(t, cached, "pip install --requirement requirements.txt")
	if !strings.Contains(install, "--mount=type=secret,id=PIP_TOKEN") {
		t.Fatalf("the dependency step lacks the install secret: %q", install)
	}
	assertBefore(t, cached, install, "COPY . .")
}

func TestJVMBuildsCacheTheirDownloadsOutsideTheTree(t *testing.T) {
	for _, fixture := range []struct{ name, recipe, want string }{
		{"java", "java", cacheMountFor("m2", "/root/.m2/repository")},
		{"gradle", "java", cacheMountFor("gradle-caches", "/root/.gradle/caches") + cacheMountFor("gradle-wrapper", "/root/.gradle/wrapper")},
		{"play", "scala", cacheMountFor("coursier", "/root/.cache/coursier") + cacheMountFor("ivy", "/root/.ivy2/cache")},
		{"clojure", "clojure", cacheMountFor("m2", "/root/.m2/repository")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			copyFrameworkFixture(t, fixture.name, root)
			cached, _ := prepareCachedAndForced(t, root, root, BuildPlanConfig{Recipe: fixture.recipe})
			if !strings.Contains(cached, fixture.want) {
				t.Fatalf("no download cache %q:\n%s", fixture.want, cached)
			}
			if fixture.recipe == "java" {
				return
			}
			// The fetch and the build read the same caches; with an install
			// secret the fetch keeps its downloads in the layer for the
			// build, which has no credential.
			if strings.Count(cached, fixture.want) != 2 {
				t.Fatalf("the fetch and the build do not share the caches:\n%s", cached)
			}
			secret := BuildPlanConfig{Recipe: fixture.recipe, Secrets: []BuildSecretConfig{{Variable: "REPO_TOKEN", Step: "install"}}}
			if cached, _ = prepareCachedAndForced(t, root, root, secret, "REPO_TOKEN"); strings.Contains(cached, fixture.want) {
				t.Fatalf("a credentialed fetch is cached:\n%s", cached)
			}
		})
	}
}

func TestDotnetRestoresFromTheProjectFilesBeforeTheSource(t *testing.T) {
	boundary := t.TempDir()
	copyFrameworkFixture(t, "dotnet-solution", boundary)
	detection, err := (Detector{}).DetectPath(t.Context(), boundary, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&detection)
	if candidate == nil || candidate.Recipe != "dotnet" {
		t.Fatalf("candidate = %+v", candidate)
	}
	root := filepath.Join(boundary, filepath.FromSlash(candidate.Root))
	config := BuildPlanConfig{Recipe: "dotnet", RootDirectory: candidate.Root}
	cached, forced := prepareCachedAndForced(t, boundary, root, config)
	nuget := cacheMountFor("nuget-http", "/root/.local/share/NuGet/http-cache")
	restore := dockerfileLine(t, cached, "dotnet restore")
	if !strings.HasPrefix(restore, "RUN "+nuget) {
		t.Fatalf("restore = %q", restore)
	}
	for _, input := range []string{"Directory.Build.props", "Directory.Packages.props", "global.json", ".csproj"} {
		assertBefore(t, cached, input, restore)
	}
	assertBefore(t, cached, restore, "COPY . .")
	assertBefore(t, forced, "dotnet restore", "COPY . .")

	// A project that imports a file of its own, or commits obj/, restores
	// from its source as before.
	project := dockerfileLine(t, cached, ".csproj /src/")
	file := strings.Fields(project)[1]
	content, err := os.ReadFile(filepath.Join(boundary, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, boundary, file, strings.Replace(string(content), "</Project>", "<Import Project=\"build/common.props\" /></Project>", 1))
	writeBuildFixture(t, boundary, "build/common.props", "<Project />")
	cached, _ = prepareCachedAndForced(t, boundary, root, config)
	assertBefore(t, cached, "COPY . .", "dotnet restore")
	writeBuildFixture(t, boundary, file, string(content))
	writeBuildFixture(t, boundary, path.Join(path.Dir(file), "obj/project.assets.json"), "{}")
	cached, _ = prepareCachedAndForced(t, boundary, root, config)
	assertBefore(t, cached, "COPY . .", "dotnet restore")
}

func TestRubyInstallsTheBundleBeforeTheSource(t *testing.T) {
	root := t.TempDir()
	copyFrameworkFixture(t, "sinatra", root)
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&detection)
	if candidate == nil || candidate.Recipe != "ruby" {
		t.Fatalf("candidate = %+v", candidate)
	}
	config := BuildPlanConfig{Recipe: "ruby", StartCommand: candidate.StartCommand}
	cached, forced := prepareCachedAndForced(t, root, root, config)
	for _, dockerfile := range []string{cached, forced} {
		assertBefore(t, dockerfile, "COPY Gemfile Gemfile.lock", "bundle install")
		assertBefore(t, dockerfile, "bundle install", "COPY . .")
	}
	// A gemspec or a path gem is part of the checkout: the bundle installs
	// from the source, as before.
	gemfile, err := os.ReadFile(filepath.Join(root, "Gemfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"gemspec\n", "gem \"local\", path: \"vendor/local\"\n", "eval_gemfile \"Gemfile.shared\"\n"} {
		writeBuildFixture(t, root, "Gemfile", string(gemfile)+line)
		cached, _ = prepareCachedAndForced(t, root, root, config)
		assertBefore(t, cached, "COPY . .", "bundle install")
	}
}

func TestPHPInstallsLockedPackagesBeforeTheSource(t *testing.T) {
	root := t.TempDir()
	files := laravelTree(map[string]string{
		"composer.json": `{"require":{"php":"^8.2","laravel/framework":"^12.0"},"scripts":{"post-autoload-dump":["@php artisan package:discover --ansi"]}}`,
		"composer.lock": composerLockJSON(t, map[string]map[string]string{"laravel/framework v12.1.0": {"php": "^8.2"}}),
	})
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	config := BuildPlanConfig{Recipe: "php", StartCommand: "frankenphp php-server --root /app/public"}
	cached, forced := prepareCachedAndForced(t, root, root, config)
	composer := cacheMountFor("composer", "/root/.cache/composer") + "COMPOSER_CACHE_DIR=/root/.cache/composer "
	assertGoDockerfile(t, cached, "COPY composer.json composer.lock /app/\nRUN "+composer+"composer install --no-dev --no-interaction --prefer-dist --no-scripts --no-autoloader\nCOPY . .\n")
	assertGoDockerfile(t, forced, "COPY composer.json composer.lock /app/\nRUN composer install --no-dev --no-interaction --prefer-dist --no-scripts --no-autoloader\nCOPY . .\n")
	// package:discover runs with the autoloader, once the source and the
	// cache directories it writes are there.
	assertBefore(t, cached, "bootstrap/cache database", "composer dump-autoload --no-dev --optimize")

	manifest, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{`"repositories": [{"type": "path", "url": "packages/*"}],`, `"extra": {"installer-paths": {"web/": ["type:x"]}},`, `"scripts": {"pre-install-cmd": "php check.php"},`} {
		writeBuildFixture(t, root, "composer.json", strings.Replace(string(manifest), "{", "{"+change, 1))
		cached, _ = prepareCachedAndForced(t, root, root, config)
		if strings.Contains(cached, "--no-autoloader") || !strings.Contains(cached, composer+"composer install --no-dev --optimize-autoloader") {
			t.Fatalf("%s installs before the source:\n%s", change, cached)
		}
	}
}

func TestElixirFetchesAndCompilesDependenciesBeforeTheSource(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "mix.exs", "defmodule Shop.MixProject do\n  use Mix.Project\n  def project, do: [app: :shop, version: \"0.1.0\", deps: deps(), releases: [shop: []]]\n  defp deps, do: [{:jason, \"~> 1.4\"}]\nend\n")
	writeBuildFixture(t, root, "mix.lock", "%{\"jason\": {:hex, :jason, \"1.4.4\"}}\n")
	writeBuildFixture(t, root, "config/config.exs", "import Config\nimport_config \"#{config_env()}.exs\"\n")
	writeBuildFixture(t, root, "config/prod.exs", "import Config\n")
	writeBuildFixture(t, root, "lib/shop.ex", "defmodule Shop do\nend\n")
	config := BuildPlanConfig{Recipe: "elixir", StartCommand: "/app/bin/shop start"}
	cached, forced := prepareCachedAndForced(t, root, root, config)
	hex := cacheMountFor("hex-packages", "/root/.hex/packages") + cacheMountFor("rebar3", "/root/.cache/rebar3")
	assertGoDockerfile(t, cached, "COPY config/ /app/config/\nCOPY mix.exs mix.lock /app/\nRUN "+hex+"mix deps.get --only prod --check-locked\nRUN mix deps.compile\nCOPY . .\n")
	assertGoDockerfile(t, forced, "COPY mix.exs mix.lock /app/\nRUN mix deps.get --only prod --check-locked\nRUN mix deps.compile\nCOPY . .\n")
	// A path dependency, or a version read from a file, needs the source.
	for _, manifest := range []string{"{:core, path: \"../core\"}", "File.read!(\"VERSION\")"} {
		writeBuildFixture(t, root, "mix.exs", "defmodule Shop.MixProject do\n  use Mix.Project\n  def project, do: [app: :shop, version: \"0.1.0\", deps: [{:jason, \"~> 1.4\"}], releases: [shop: []]]\n  # "+manifest+"\nend\n")
		cached, _ = prepareCachedAndForced(t, root, root, config)
		assertBefore(t, cached, "COPY . .", "mix deps.get")
	}
}

func TestOtherRecipesFetchFromTheirManifestsBeforeTheSource(t *testing.T) {
	for _, fixture := range []struct{ name, fetch, first string }{
		{"deno", "deno install", "COPY deno.json"},
		{"gleam", "gleam deps download", "COPY gleam.toml"},
		{"jekyll", "bundle install", "COPY Gemfile"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			copyFrameworkFixture(t, fixture.name, root)
			detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := selectedDetectionCandidate(&detection)
			if candidate == nil {
				t.Fatalf("no candidate: %+v", detection.Candidates)
			}
			config := BuildPlanConfig{Recipe: candidate.Recipe, BuildCommand: candidate.BuildCommand, StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory}
			cached, forced := prepareCachedAndForced(t, root, root, config)
			for _, dockerfile := range []string{cached, forced} {
				assertBefore(t, dockerfile, fixture.first, fixture.fetch)
				assertBefore(t, dockerfile, fixture.fetch, "COPY . .")
			}
		})
	}
	dart := t.TempDir()
	writeBuildFixture(t, dart, "pubspec.yaml", "name: server\nenvironment:\n  sdk: ^3.5.0\ndependencies:\n  shelf: ^1.4.0\n")
	writeBuildFixture(t, dart, "pubspec.lock", "packages:\n  shelf:\n    version: \"1.4.2\"\n")
	writeBuildFixture(t, dart, "bin/server.dart", "import 'package:shelf/shelf.dart';\nvoid main() {}\n")
	config := BuildPlanConfig{Recipe: "dart", StartCommand: "/app/server"}
	cached, _ := prepareCachedAndForced(t, dart, dart, config)
	assertGoDockerfile(t, cached, "COPY pubspec.yaml pubspec.lock /app/\nRUN dart pub get --enforce-lockfile\nCOPY . .\n")
	// A path dependency is part of the checkout.
	writeBuildFixture(t, dart, "pubspec.yaml", "name: server\nenvironment:\n  sdk: ^3.5.0\ndependencies:\n  shelf: ^1.4.0\n  core:\n    path: ../core\n")
	cached, _ = prepareCachedAndForced(t, dart, dart, config)
	assertBefore(t, cached, "COPY . .", "dart pub get")
}

// The early copies never bring back what the repository's .dockerignore
// leaves out: the install then runs after the source, as before.
func TestEarlyInstallsKeepToWhatTheBuildContextHolds(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "requirements.txt", "-r requirements/base.txt\n")
	writeBuildFixture(t, root, "requirements/base.txt", "flask==3.1.0\ngunicorn==23.0.0\n")
	config := BuildPlanConfig{Recipe: "python", StartCommand: "gunicorn app:app"}
	cached, _ := prepareCachedAndForced(t, root, root, config)
	assertBefore(t, cached, "COPY requirements/base.txt", "COPY . .")
	writeBuildFixture(t, root, ".dockerignore", "requirements/\n")
	cached, _ = prepareCachedAndForced(t, root, root, config)
	assertBefore(t, cached, "COPY . .", "pip install --requirement requirements.txt")
	ignore, err := os.ReadFile(filepath.Join(root, ".just-dashboard", "Dockerfile.dockerignore"))
	if err != nil || strings.Contains(string(ignore), "!requirements") {
		t.Fatalf("the ignore file brings back what the repository left out (%v):\n%s", err, ignore)
	}

	// Credentials beside the manifests stay with the source copy.
	ruby := t.TempDir()
	copyFrameworkFixture(t, "sinatra", ruby)
	writeBuildFixture(t, ruby, ".bundle/config", "BUNDLE_GEMS__EXAMPLE__TEST: \"token\"\n")
	cached, _ = prepareCachedAndForced(t, ruby, ruby, BuildPlanConfig{Recipe: "ruby", StartCommand: "bundle exec rackup"})
	assertBefore(t, cached, "COPY . .", "bundle install")
	php := t.TempDir()
	for name, content := range laravelTree(map[string]string{
		"composer.lock": composerLockJSON(t, map[string]map[string]string{"laravel/framework v12.1.0": {"php": "^8.2"}}),
		"auth.json":     `{"http-basic": {}}`,
	}) {
		writeBuildFixture(t, php, name, content)
	}
	cached, _ = prepareCachedAndForced(t, php, php, BuildPlanConfig{Recipe: "php", StartCommand: "frankenphp php-server --root /app/public"})
	if strings.Contains(cached, "--no-autoloader") {
		t.Fatalf("an auth.json project installs before its source:\n%s", cached)
	}
}
