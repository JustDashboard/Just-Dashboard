package deploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompiledRuntimeAssetsFollowTheConventionsAndTheSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":                   "module example.com/app\n\ngo 1.25\n",
		"main.go":                  "package main\nfunc main() { r.LoadHTMLGlob(\"web/tmpl/*\"); r.Static(\"/assets\", \"./frontend/dist\"); m, _ := migrate.New(\"file://db/migrations\", url); http.Dir(\"/etc\"); r.Static(\"/x\", \"../outside\") }\n",
		"templates/index.html":     "<h1>hi</h1>",
		"web/tmpl/page.html":       "x",
		"frontend/dist/app.js":     "x",
		"db/migrations/1_init.sql": "create table x();",
		"config.yaml":              "port: 8080",
		"config.local.toml":        "x = 1",
		"internal/handler.go":      "package internal\nvar t = template.Must(template.ParseGlob(\"views/*.gohtml\"))\n",
		"handler_test.go":          "package main\nvar _ = http.Dir(\"testdata-only\")\n",
		"testdata-only/x":          "x",
		"vendor/x/y.go":            "package y\nvar _ = http.Dir(\"from-vendor\")\n",
		"from-vendor/x":            "x",
		"README.md":                "x",
	} {
		writeBuildFixture(t, root, name, content)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "static")); err != nil {
		t.Fatal(err)
	}
	got := compiledRuntimeAssets(root, ".go")
	want := []string{"config.local.toml", "config.yaml", "db", "frontend", "templates", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assets = %q, want %q", got, want)
	}

	ignoring := t.TempDir()
	for name, content := range map[string]string{
		"main.go": "package main\n", "templates/a.html": "x", "config.yaml": "secret: x", "static/a.css": "x",
		"public/a": "x", "locales/en.json": "{}", "assets/a.png": "x",
		".dockerignore": "# local only\nconfig.yaml\n**/*.log\ntemplates/drafts\n/static/\n!public\nassets\n",
	} {
		writeBuildFixture(t, ignoring, name, content)
	}
	if got := compiledRuntimeAssets(ignoring, ".go"); !reflect.DeepEqual(got, []string{"locales", "templates"}) {
		t.Fatalf("assets despite .dockerignore = %q", got)
	}

	rust := t.TempDir()
	for name, content := range map[string]string{
		"Cargo.toml":           "[package]\nname = \"app\"\n",
		"src/main.rs":          "fn main() { let tera = Tera::new(\"tmpl/**/*\"); let app = Router::new().nest_service(\"/static\", ServeDir::new(\"public\")); Files::new(\"/f\", \"./files\"); }\n",
		"tmpl/base.html":       "x",
		"public/favicon.ico":   "x",
		"files/a":              "x",
		"migrations/0001.sql":  "x",
		"target/release/app":   "x",
		"src/templates/x.html": "x",
	} {
		writeBuildFixture(t, rust, name, content)
	}
	if got := compiledRuntimeAssets(rust, ".rs"); !reflect.DeepEqual(got, []string{"files", "migrations", "public", "tmpl"}) {
		t.Fatalf("rust assets = %q", got)
	}
}

func TestCompiledRecipesRunFromAWritableHomeWithTheirFiles(t *testing.T) {
	t.Parallel()
	goRoot := t.TempDir()
	writeBuildFixture(t, goRoot, "go.mod", "module example.com/app\n\ngo 1.25\n")
	writeBuildFixture(t, goRoot, "main.go", "package main\nfunc main() { r.LoadHTMLGlob(\"templates/*\") }\n")
	writeBuildFixture(t, goRoot, "templates/index.html", "<h1>hi</h1>")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), goRoot, BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := prepared.DockerfilePreview[strings.LastIndex(prepared.DockerfilePreview, "FROM "):]
	for _, want := range []string{
		"RUN apk add --no-cache tzdata && adduser -D -u 10001 app\nUSER app\nWORKDIR /home/app\nCOPY --from=build /out/app /app\n",
		"COPY --from=build --chown=app:app /src/templates /home/app/templates\n",
		"RUN mkdir -p /home/app/data && ln -s /app /home/app/app\n", `ENTRYPOINT ["/app"]`,
	} {
		if !strings.Contains(runtime, want) {
			t.Fatalf("Go runtime stage missing %q:\n%s", want, runtime)
		}
	}
	if prepared.RecipeVersion != "just-dashboard-recipes-v3" {
		t.Fatalf("recipe version = %q", prepared.RecipeVersion)
	}

	rustRoot := t.TempDir()
	writeBuildFixture(t, rustRoot, "Cargo.toml", "[package]\nname = \"svc\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\n")
	writeBuildFixture(t, rustRoot, "src/main.rs", "fn main() {}\n")
	writeBuildFixture(t, rustRoot, "static/app.css", "body{}")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), rustRoot, BuildPlanConfig{Method: BuildRecipe, Recipe: "rust", StartCommand: "/app serve"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WORKDIR /home/app", "COPY --from=build --chown=app:app /src/static /home/app/static", "RUN mkdir -p /home/app/data", `CMD ["/bin/sh","-c","/app serve"]`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Rust Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}

	// A root-level directory named app is copied as it is; nothing links over it.
	lines := strings.Join(compiledRuntimeLines(ResolvedImage{Reference: "alpine:3.22", Digest: "sha256:" + strings.Repeat("a", 64)}, compiledRuntime{assets: []string{"app", "templates"}}), "\n")
	if strings.Contains(lines, "ln -s") || !strings.Contains(lines, "/src/app /home/app/app") {
		t.Fatalf("runtime lines with an app directory:\n%s", lines)
	}
}

func TestDotnetRuntimeOwnsItsDirectoryDataAndKeyRing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "Api.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	want := "WORKDIR /app\n" +
		"RUN mkdir -p /app/data /home/app/.aspnet/DataProtection-Keys && chown app:app /app /app/data /home/app/.aspnet /home/app/.aspnet/DataProtection-Keys\n" +
		"COPY --from=build --chown=app:app /out /app\nUSER app\n"
	if !strings.Contains(prepared.DockerfilePreview, want) {
		t.Fatalf("runtime stage:\n%s", prepared.DockerfilePreview)
	}
}

func TestDotnetRecipeSeedsTheDataDirectoryWithACommittedDatabase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "Web.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Sqlite" Version="8.0.0" /></ItemGroup></Project>`)
	writeBuildFixture(t, root, "appsettings.json", `{"ConnectionStrings":{"DefaultConnection":"DataSource=Data/app.db;Cache=Shared","Audit":"Data Source=audit.db","Other":"Data Source=missing.db","Dup":"Data Source=elsewhere/app.db"}}`)
	writeBuildFixture(t, root, "Data/app.db", "SQLite format 3\x00")
	writeBuildFixture(t, root, "audit.db", "SQLite format 3\x00")
	writeBuildFixture(t, root, "elsewhere/app.db", "SQLite format 3\x00")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	want := "COPY --from=build --chown=app:app /out /app\n" +
		"COPY --from=build --chown=app:app /src/Data/app.db /app/data/app.db\n" +
		"COPY --from=build --chown=app:app /src/audit.db /app/data/audit.db\nUSER app\n"
	if !strings.Contains(prepared.DockerfilePreview, want) {
		t.Fatalf("runtime stage:\n%s", prepared.DockerfilePreview)
	}

	// A file the build context leaves out, a link, or a project without
	// SQLite seeds nothing.
	for name, setup := range map[string]func(string){
		"ignored": func(dir string) { writeBuildFixture(t, dir, ".dockerignore", "**/*.db\n") },
		"linked": func(dir string) {
			if err := os.Remove(filepath.Join(dir, "Data/app.db")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../audit.db", filepath.Join(dir, "Data/app.db")); err != nil {
				t.Fatal(err)
			}
			writeBuildFixture(t, dir, "appsettings.json", `{"ConnectionStrings":{"DefaultConnection":"DataSource=Data/app.db"}}`)
		},
		"no sqlite": func(dir string) {
			writeBuildFixture(t, dir, "Web.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
		},
	} {
		dir := t.TempDir()
		writeBuildFixture(t, dir, "Web.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Sqlite" Version="8.0.0" /></ItemGroup></Project>`)
		writeBuildFixture(t, dir, "appsettings.json", `{"ConnectionStrings":{"DefaultConnection":"DataSource=Data/app.db"}}`)
		writeBuildFixture(t, dir, "Data/app.db", "SQLite format 3\x00")
		writeBuildFixture(t, dir, "audit.db", "SQLite format 3\x00")
		setup(dir)
		project, err := selectDotnetRecipe(dir)
		if err != nil {
			t.Fatal(err)
		}
		if seeds := dotnetSQLiteSeeds(dir, project); len(seeds) != 0 {
			t.Errorf("%s: seeds = %q", name, seeds)
		}
	}
}

func TestDockerIgnoredPathsReadRulesConservatively(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// The last rule is thousands of "**" long: matching it stays linear.
	writeBuildFixture(t, root, ".dockerignore", "bin\n**/obj\nData/*.bak\nsrc/**/secret.db\n"+strings.Repeat("**/", 2000)+"x\n")
	ignored := dockerIgnoredPaths(root)
	for name, want := range map[string]bool{
		"bin": true, "bin/app.db": true, "obj": true, "Web/obj/app.db": true, "Data/a.bak": true,
		"src/secret.db": true, "src/a/b/secret.db": true, "deep/x": true, "x": true,
		"Data/app.db": false, "templates": false, "binary": false,
	} {
		if got := ignored(name); got != want {
			t.Errorf("%s ignored = %v, want %v", name, got, want)
		}
	}
	// A rule that cannot be read might exclude anything.
	writeBuildFixture(t, root, ".dockerignore", "[\n")
	if !dockerIgnoredPaths(root)("templates") {
		t.Fatal("an unreadable rule was taken to exclude nothing")
	}
}

func TestLaravelRecipeLinksThePublicDisk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "composer.json", `{"require":{"php":"^8.2","laravel/framework":"^11.0"}}`)
	writeBuildFixture(t, root, "composer.lock", "{}")
	writeBuildFixture(t, root, "artisan", "")
	writeBuildFixture(t, root, "public/index.php", "<?php")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "php", StartCommand: "php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public",
	}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"storage/app/public bootstrap/cache", "RUN if [ -d public ] && [ ! -e public/storage ]; then ln -s /app/storage/app/public public/storage; fi"} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Laravel Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
}
