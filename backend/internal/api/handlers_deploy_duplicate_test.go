package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// TestDeploymentDuplicateCreatesAResumableDraft checks the route's exact
// contract: 201 with the draft body, pre-filled from the source project, and
// that the same draft is reachable afterwards through the ordinary draft
// routes (GET .../drafts/{id} and the caller's draft list) — nothing about
// resuming a duplicate is special-cased.
func TestDeploymentDuplicateCreatesAResumableDraft(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	projectID, _ := seedCommittedDeployment(t, s, "dup-source-app",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "alpine:3"},
		deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "docker.io/library/alpine:3", Digest: "sha256:" + strings.Repeat("0", 64)},
	)

	created := doDeployJSON(t, client, http.MethodPost,
		"/api/v1/deploy/"+strconv.FormatInt(projectID, 10)+"/duplicate", map[string]any{"name": "dup-target-app"})
	if created.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d %s", created.Code, created.Body.String())
	}
	var draft deploy.Draft
	decodeDeployResponse(t, created.Body.Bytes(), &draft)
	if draft.ID == "" {
		t.Fatalf("duplicate draft has no id: %#v", draft)
	}
	if draft.Data.Intent == nil || draft.Data.Intent.Name != "dup-target-app" {
		t.Fatalf("duplicate intent = %#v", draft.Data.Intent)
	}
	// canonicalSourceConfig normalizes the image reference to its fully
	// qualified form (docker.io/library/alpine:3), the same as saving any
	// draft source does.
	if draft.Data.Source == nil || draft.Data.Source.Kind != deploy.SourceImage || draft.Data.Source.Image != "docker.io/library/alpine:3" {
		t.Fatalf("duplicate source = %#v", draft.Data.Source)
	}
	if draft.Data.Configuration == nil || len(draft.Data.Configuration.Domains) != 0 {
		t.Fatalf("duplicate configuration = %#v, want no domains", draft.Data.Configuration)
	}

	resumed := client.do(http.MethodGet, "/api/v1/deploy/drafts/"+draft.ID, "", nil)
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume duplicate draft = %d %s", resumed.Code, resumed.Body.String())
	}
	var resumedDraft deploy.Draft
	decodeDeployResponse(t, resumed.Body.Bytes(), &resumedDraft)
	if resumedDraft.ID != draft.ID || resumedDraft.Data.Intent.Name != "dup-target-app" {
		t.Fatalf("resumed draft = %#v", resumedDraft)
	}

	listed := client.do(http.MethodGet, "/api/v1/deploy/drafts", "", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), draft.ID) {
		t.Fatalf("draft list = %d %s, want it to include %s", listed.Code, listed.Body.String(), draft.ID)
	}
}

func TestDeploymentDuplicateRefusesAnUnknownProjectAndRequiresSystemAdmin(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	missing := doDeployJSON(t, admin, http.MethodPost, "/api/v1/deploy/999999/duplicate", map[string]any{"name": "x"})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("duplicate of an unknown project = %d %s, want 404", missing.Code, missing.Body.String())
	}

	projectID, _ := seedCommittedDeployment(t, s, "dup-source-app2",
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "alpine:3"},
		deploy.SourceIdentity{Kind: deploy.SourceImage, Repository: "docker.io/library/alpine:3", Digest: "sha256:" + strings.Repeat("0", 64)},
	)
	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader2", auth.RoleReadOnly)}
	resp := doDeployJSON(t, readonly, http.MethodPost,
		"/api/v1/deploy/"+strconv.FormatInt(projectID, 10)+"/duplicate", map[string]any{"name": "nope"})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("read-only duplicate = %d %s, want 403", resp.Code, resp.Body.String())
	}
}
