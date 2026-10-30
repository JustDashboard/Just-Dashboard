package deploy

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The images are ones a Docker host running this suite usually has, so the
// stack starts without a pull; the harness removes only what it pulled.
const liveUserTemplateCompose = `services:
  web:
    image: nginx:alpine
    ports: ["8088:80"]
    environment:
      CACHE_HOST: cache
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1/"]
      interval: 2s
      timeout: 2s
      retries: 30
    depends_on:
      cache: {condition: service_healthy}
  cache:
    image: redis:7-alpine
    command: redis-server --appendonly yes
    volumes: ["cache-data:/data"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 2s
      timeout: 2s
      retries: 30
`

// A template added on this dashboard from a pasted Compose file — nginx in
// front, Redis behind it — made by the call the POST /deploy/templates route
// makes, chosen by the call every source save makes, and brought up by the
// harness the catalogue's own sweep brings every built-in stack up with.
func TestLiveUserComposeTemplateStartsAndAnswersItsOwnChecks(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to start a template added from a Compose file")
	}
	ctx := context.Background()
	fixture := newTemplateFixture(t)
	created, err := fixture.planning.CreateComposeTemplate(ctx, ComposeTemplateRequest{
		Name: "Live nginx and Redis", Compose: liveUserTemplateCompose, HealthPath: "/",
	}, "live-test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "user-") || len(created.Definition.Services) != 2 {
		t.Fatalf("created = %+v", created.UserTemplate)
	}
	source, err := fixture.planning.WithUserTemplate(ctx, DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: created.ID,
		BlueprintVersion: created.Version, BlueprintInputs: created.Definition.Fixtures[0].Inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	newLiveComposeHarness(t).startsAndAnswersItsOwnChecks(t, created.Definition, source)
}
