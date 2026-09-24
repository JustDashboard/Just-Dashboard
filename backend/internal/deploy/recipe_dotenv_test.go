package deploy

import (
	"context"
	"strings"
	"testing"
)

// A Go or Rust binary that exits without .env gets an empty one in its
// runtime stage, created as root before the stage drops privileges; one that
// ignores the load error, or never loads the file, gets nothing.
func TestRecipesProvideAnEmptyDotenvWhenTheSourceRequiresIt(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		recipe string
		files  map[string]string
		want   bool
	}{
		{"godotenv with log.Fatal", "go", map[string]string{
			"go.mod":          "module example.com/app\n\ngo 1.25\n",
			"cmd/api/main.go": "package main\nimport (\"log\"; \"github.com/joho/godotenv\")\nfunc main() {\n\terr := godotenv.Load()\n\tif err != nil {\n\t\tlog.Fatal(\"Error loading .env file\")\n\t}\n}\n",
		}, true},
		{"godotenv ignored", "go", map[string]string{
			"go.mod":  "module example.com/app\n\ngo 1.25\n",
			"main.go": "package main\nimport \"github.com/joho/godotenv\"\nfunc main() { _ = godotenv.Load() }\n",
		}, false},
		{"dotenvy expect", "rust", map[string]string{
			"Cargo.toml":  "[package]\nname = \"api\"\nversion = \"0.1.0\"\n",
			"Cargo.lock":  "version = 4\n",
			"src/main.rs": "fn main() { dotenvy::dotenv().expect(\".env\"); }\n",
		}, true},
		{"dotenvy ok", "rust", map[string]string{
			"Cargo.toml":  "[package]\nname = \"api\"\nversion = \"0.1.0\"\n",
			"Cargo.lock":  "version = 4\n",
			"src/main.rs": "fn main() { dotenvy::dotenv().ok(); }\n",
		}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
				BuildPlanConfig{Method: BuildRecipe, Recipe: fixture.recipe}, false, "just-dashboard/test:run-1")
			if err != nil {
				t.Fatal(err)
			}
			dockerfile := prepared.DockerfilePreview
			touch := strings.Index(dockerfile, "RUN touch /.env")
			if got := touch >= 0; got != fixture.want {
				t.Fatalf("empty .env = %v, want %v:\n%s", got, fixture.want, dockerfile)
			}
			if fixture.want && touch > strings.LastIndex(dockerfile, "USER app") {
				t.Fatalf("the file must be created before the stage drops to its user:\n%s", dockerfile)
			}
			if fixture.want && touch < strings.LastIndex(dockerfile, "FROM ") {
				t.Fatalf("the file belongs to the runtime stage:\n%s", dockerfile)
			}
		})
	}
}

func TestWithEmptyDotenvFollowsTheRuntimeWorkdir(t *testing.T) {
	t.Parallel()
	got := withEmptyDotenv([]string{"FROM golang AS build", "WORKDIR /src", "FROM alpine", "RUN adduser -D app", "USER app", "WORKDIR /home/app", `ENTRYPOINT ["/app"]`})
	want := []string{"FROM golang AS build", "WORKDIR /src", "FROM alpine", "RUN adduser -D app",
		"RUN mkdir -p /home/app && touch /home/app/.env", "USER app", "WORKDIR /home/app", `ENTRYPOINT ["/app"]`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	got = withEmptyDotenv([]string{"FROM alpine", `CMD ["/app"]`})
	if got[1] != "RUN touch /.env" {
		t.Fatalf("got %v", got)
	}
}
