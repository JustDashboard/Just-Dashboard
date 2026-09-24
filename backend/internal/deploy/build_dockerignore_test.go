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
		[]string{"package.json", "bun.lock", "next.config.ts", "src", "README.md"}, "node")
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

	static, _ := recipeDockerignore(nil, []string{"index.html"}, "static")
	rules = parseDockerignore([]byte(static))
	for _, path := range []string{".env", ".env.production", ".git/HEAD"} {
		if excluded, _ := dockerignoreExcludes(rules, path); !excluded {
			t.Errorf("a static site would publish %s", path)
		}
	}
	// Toolchains that stamp builds from Git keep the repository metadata.
	golang, _ := recipeDockerignore(nil, []string{"go.mod"}, "go")
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
	if err := writeGeneratedDockerignore(root, "x\n"); err == nil {
		t.Fatal("the ignore file followed a checkout symlink out of the context")
	}
}
