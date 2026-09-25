package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerignoreMatchesBuildKitSemantics(t *testing.T) {
	t.Parallel()
	rules := parseDockerignore([]byte("# comment\n/dist\n**/*.log\n*.md\n!README.md\nsecrets/**\n.env*\n!.env.example\n"))
	for _, fixture := range []struct {
		path     string
		excluded bool
	}{
		{"dist", true}, {"dist/app.js", true}, {"src/dist", false},
		{"debug.log", true}, {"logs/a/b.log", true},
		{"CHANGELOG.md", true}, {"README.md", false}, {"docs/guide.md", false},
		{"secrets/key", true}, {".env", true}, {".env.production", true}, {".env.example", false},
		{"package.json", false},
	} {
		if excluded, _ := dockerignoreExcludes(rules, fixture.path); excluded != fixture.excluded {
			t.Errorf("%s excluded = %v, want %v", fixture.path, excluded, fixture.excluded)
		}
	}
}

func TestRecipeDockerignoreKeepsRepositoryRulesButNotRecipeInputs(t *testing.T) {
	t.Parallel()
	content, dropped := recipeDockerignore([]byte("*\n!dist\n!package.json\nnode_modules\n*.log\n"),
		[]string{"package.json", "bun.lock", "next.config.ts", "src", "README.md"}, "node", nil)
	if len(dropped) != 1 || dropped[0].Rule != "*" || dropped[0].Input != "bun.lock" {
		t.Fatalf("dropped = %+v", dropped)
	}
	rules := parseDockerignore([]byte(content))
	for path, excluded := range map[string]bool{
		"bun.lock": false, "next.config.ts": false, "src/app.tsx": false, "debug.log": true,
		".git/config": true, "node_modules/x/index.js": true, "apps/web/node_modules/y": true,
		".just-dashboard/Dockerfile": true, ".just-dashboard-build-metadata-123": true, ".env": false,
	} {
		if got, _ := dockerignoreExcludes(rules, path); got != excluded {
			t.Errorf("%s excluded = %v, want %v\n%s", path, got, excluded, content)
		}
	}

	static, _ := recipeDockerignore(nil, []string{"index.html"}, "static", nil)
	rules = parseDockerignore([]byte(static))
	for _, path := range []string{".env", ".env.production", ".git/HEAD"} {
		if excluded, _ := dockerignoreExcludes(rules, path); !excluded {
			t.Errorf("a static site would publish %s", path)
		}
	}
	// sbt's project/ is an input of the Scala recipe alone; a Node build
	// keeps the repository's rule leaving a directory of that name out.
	for kind, kept := range map[string]bool{"scala": true, "node": false} {
		content, _ := recipeDockerignore([]byte("project\n"), []string{"build.sbt", "package.json", "project"}, kind, nil)
		if excluded, _ := dockerignoreExcludes(parseDockerignore([]byte(content)), "project/plugins.sbt"); excluded == kept {
			t.Errorf("%s: project/ excluded = %v\n%s", kind, excluded, content)
		}
	}
	// The JVM and .NET recipes' version and MSBuild files are theirs alone
	// too: a static site keeps its repository's rule and does not serve them.
	for _, fixture := range []struct {
		kind, input string
		kept        bool
	}{
		{"java", ".tool-versions", true}, {"scala", ".sdkmanrc", true}, {"java", "gradle.properties", true},
		{"dotnet", "global.json", true}, {"dotnet", "Directory.Build.props", true},
		{"static", ".tool-versions", false}, {"static", "global.json", false}, {"node", "gradle.properties", false},
		// A site generator's configuration belongs to the recipe that runs it.
		{"site", "config.toml", true}, {"site", "Gemfile", true}, {"python", "mkdocs.yml", true}, {"deno", "_config.ts", true},
		{"static", "_config.yml", false}, {"static", "hugo.toml", false}, {"go", "config.toml", false},
	} {
		content, _ := recipeDockerignore([]byte(fixture.input+"\n"), []string{fixture.input, "index.html"}, fixture.kind, nil)
		if excluded, _ := dockerignoreExcludes(parseDockerignore([]byte(content)), fixture.input); excluded == fixture.kept {
			t.Errorf("%s: %s excluded = %v\n%s", fixture.kind, fixture.input, excluded, content)
		}
	}
	// Toolchains that stamp builds from Git keep the repository metadata.
	golang, _ := recipeDockerignore(nil, []string{"go.mod"}, "go", nil)
	if excluded, _ := dockerignoreExcludes(parseDockerignore([]byte(golang)), ".git/HEAD"); excluded {
		t.Error("the Go recipe lost .git")
	}
}

func TestPreparedRecipeAndStaticBuildsWriteTheirIgnoreFile(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		files  map[string]string
		config BuildPlanConfig
		want   []string
	}{
		{"static", map[string]string{"index.html": "<h1>x</h1>", ".dockerignore": "drafts\n"},
			BuildPlanConfig{Method: BuildStatic}, []string{"drafts", ".git", ".env"}},
		{"node", map[string]string{"package.json": `{"scripts":{"start":"node index.js"}}`, "package-lock.json": "{}"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "node index.js"}, []string{".git", "**/node_modules"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			fixture.config.Secrets = []BuildSecretConfig{}
			if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, fixture.config, false, "just-dashboard/release:1-2"); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(root, ".just-dashboard", "Dockerfile.dockerignore"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range fixture.want {
				if !strings.Contains(string(content), "\n"+want+"\n") {
					t.Fatalf("ignore file lacks %q:\n%s", want, content)
				}
			}
		})
	}

	root, outside := t.TempDir(), t.TempDir()
	writeBuildFixture(t, root, "index.html", "x")
	if err := os.Symlink(outside, filepath.Join(root, ".just-dashboard")); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFile(root, "Dockerfile.dockerignore", "x\n"); err == nil {
		t.Fatal("the ignore file followed a checkout symlink out of the context")
	}
}
