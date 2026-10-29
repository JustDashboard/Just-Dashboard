package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A masked value keeps its shape and nothing else: letters for letters,
// digits for digits, the rest where it was, the same value the same way
// under one key and another way under the next.
func TestMaskShapeKeepsAValuesShapeAndNothingElse(t *testing.T) {
	key, other := []byte(strings.Repeat("k", 32)), []byte(strings.Repeat("o", 32))
	for _, value := range []struct{ value, kind, shape string }{
		{"alice.smith@example.com", dbx.KindText, `^[a-z]{5}\.[a-z]{5}@[a-z]{7}\.[a-z]{3}$`},
		{"Alice O'Neil", dbx.KindText, `^[A-Z][a-z]{4} [A-Z]'[A-Z][a-z]{3}$`},
		{"+44 20 7946 0958", dbx.KindText, `^\+[0-9]{2} [0-9]{2} [0-9]{4} [0-9]{4}$`},
		{"9d5ed678-fe57-bcca-8a2c-56fa90ab0d2c", dbx.KindUUID, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`},
		{"4012888888881881", dbx.KindInteger, `^[1-9][0-9]{15}$`},
		{"-1250.75", dbx.KindDecimal, `^-[1-9][0-9]{3}\.[0-9]{2}$`},
	} {
		masked, err := maskShape(key, value.value, value.kind)
		if err != nil {
			t.Fatal(err)
		}
		if masked == value.value || !regexp.MustCompile(value.shape).MatchString(masked) {
			t.Fatalf("%q masked to %q, want %s", value.value, masked, value.shape)
		}
		again, _ := maskShape(key, value.value, value.kind)
		elsewhere, _ := maskShape(other, value.value, value.kind)
		if again != masked || elsewhere == masked {
			t.Fatalf("%q: the same key gave %q then %q; another gave %q", value.value, masked, again, elsewhere)
		}
	}
	if _, err := maskShape(key, "12 apples", dbx.KindInteger); err == nil {
		t.Fatal("a number with words in it was masked")
	}
	if _, present := maskCellText(nil); present {
		t.Fatal("NULL was masked")
	}
	if text, _ := maskCellText([]byte("bytes")); text != "bytes" {
		t.Fatal(text)
	}
}

type previewBranchAPIFixture struct {
	*pullRequestFixture
	connectionID int64
}

func newPreviewBranchAPIFixture(t *testing.T, name string) *previewBranchAPIFixture {
	t.Helper()
	f := newPullRequestFixture(t, name)
	sealed, err := f.s.Sealer.Seal("postgres://app:production-password@127.0.0.1:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?, 'postgres', ?, 1)`, name+"-db", sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := result.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_database_networks(environment_id, network_name, owner_key, created_at) VALUES(?, 'jd-e-test-db', 'owner', 1)`,
		fmt.Sprintf(`INSERT INTO deploy_database_bindings(environment_id, connection_id, container_name, container_id, status, checked_at) VALUES(?, %d, 'production-db', 'production-db', 'connected', 1)`, connectionID),
	} {
		if _, err := f.s.Store.DB.Exec(statement, f.environmentID); err != nil {
			t.Fatal(err)
		}
	}
	return &previewBranchAPIFixture{pullRequestFixture: f, connectionID: connectionID}
}

func (f *previewBranchAPIFixture) policyPath() string {
	return fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links/%d/previews", f.projectID, f.environmentID, f.connectionID)
}

// What a linked database gives previews is an administrator's word, in a
// session, audited; the links report carries it.
func TestPreviewBranchPolicyIsAnAdministratorsInASession(t *testing.T) {
	f := newPreviewBranchAPIFixture(t, "branch-policy")
	body := `{"mode":"masked","maskedColumns":["users.email","users.phone"]}`
	if res := f.limited.do(http.MethodPut, f.policyPath(), body, nil); res.Code != http.StatusForbidden {
		t.Fatalf("a limited account = %d %s", res.Code, res.Body.String())
	}
	if res := f.admin.do(http.MethodPut, f.policyPath(), body, map[string]string{"Authorization": "Bearer " + f.token}); res.Code == http.StatusOK {
		t.Fatalf("an API key = %d %s", res.Code, res.Body.String())
	}
	if res := f.admin.do(http.MethodPut, f.policyPath(), `{"mode":"masked","maskedColumns":["email; drop"]}`, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a column that is not a name = %d %s", res.Code, res.Body.String())
	}
	res := f.admin.do(http.MethodPut, f.policyPath(), body, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("policy = %d %s", res.Code, res.Body.String())
	}
	if got := auditCount(t, f.s, `action='deploy.preview_branch.policy' AND success=1 AND detail LIKE '%users.email%'`); got != 1 {
		t.Fatalf("policy audit = %d", got)
	}
	links := f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links", f.projectID, f.environmentID), "", nil)
	var listed []struct {
		ConnectionID int64                      `json:"connectionId"`
		Previews     deploy.PreviewBranchPolicy `json:"previews"`
	}
	decodeJSON(t, links.Body.Bytes(), &listed)
	if len(listed) != 1 || listed[0].Previews.Mode != deploy.PreviewBranchMasked || strings.Join(listed[0].Previews.MaskedColumns, ",") != "users.email,users.phone" {
		t.Fatalf("links = %s", links.Body.String())
	}
}

// Testing a pull request gives its preview a branch the listing shows; the
// preview's database reference resolves to the branch once it is ready, and
// a refresh keeps its address. A fork's preview gets none.
func TestPreviewBranchFromTestToRefresh(t *testing.T) {
	f := newPreviewBranchAPIFixture(t, "branch-flow")
	ctx := t.Context()
	if res := f.admin.do(http.MethodPut, f.policyPath(), `{"mode":"branch"}`, nil); res.Code != http.StatusOK {
		t.Fatalf("policy = %d %s", res.Code, res.Body.String())
	}
	result := f.testPreview(t, 9, fmt.Sprintf(`{"revision":%q,"copyVariables":false}`, f.head))
	environmentID := int64(result["preview"].(map[string]any)["environmentId"].(float64))
	store := f.s.modules.deployRuns
	branch, err := store.ActivePreviewBranch(ctx, environmentID, f.connectionID)
	if err != nil || branch.State != deploy.PreviewBranchWaiting {
		t.Fatalf("branch = %+v, %v", branch, err)
	}
	var listed projectPullRequests
	decodeJSON(t, f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/pull-requests", f.projectID), "", nil).Body.Bytes(), &listed)
	if len(listed.Previews) != 1 || len(listed.Previews[0].Branches) != 1 || listed.Previews[0].Branches[0].ID != branch.ID {
		t.Fatalf("listed previews = %+v", listed.Previews)
	}
	// A fork's preview gets no branch.
	fork := f.testPreview(t, 10, fmt.Sprintf(`{"revision":%q,"copyVariables":false,"acceptFork":true}`, strings.Repeat("f", 40)))
	forkEnvironment := int64(fork["preview"].(map[string]any)["environmentId"].(float64))
	if _, err := store.ActivePreviewBranch(ctx, forkEnvironment, f.connectionID); !errors.Is(err, deploy.ErrPreviewBranchNotFound) {
		t.Fatalf("a fork's preview got a branch: %v", err)
	}

	// Made ready the way the owner does it.
	if claimed, err := store.ClaimPreviewBranch(ctx, branch.ID); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	branchURL := fmt.Sprintf("postgres://app:branch-password@%s:5432/app", deploy.PreviewBranchHost(f.connectionID))
	raw, _ := json.Marshal(previewBranchSecret{User: "app", Password: "branch-password", URL: branchURL})
	sealed, err := f.s.Sealer.Seal(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PreviewBranchReady(ctx, branch.ID, deploy.PreviewBranchReadiness{
		Container: previewBranchContainer(environmentID, branch.ID), Network: "jd-preview-test", Host: deploy.PreviewBranchHost(f.connectionID),
		Port: 5432, Database: "app", Engine: "postgres", SecretSealed: sealed, Source: deploy.RehearsalSource{Kind: "dump"},
	}); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := f.s.Store.DB.QueryRow(`SELECT desired_revision FROM deploy_environments WHERE id=?`, environmentID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	resolver := f.s.modules.deployDatabases
	for target, want := range map[string]string{
		fmt.Sprint(f.connectionID):            branchURL,
		fmt.Sprintf("%d.url", f.connectionID): branchURL,
		"branch-flow-db":                      branchURL,
	} {
		got, err := resolver.ResolveVariable(ctx, environmentID, revision, target)
		if err != nil || got != want {
			t.Fatalf("%s resolved to %q, %v", target, got, err)
		}
	}
	if got, err := resolver.ResolveVariable(ctx, environmentID, revision, fmt.Sprintf("%d.jdbc", f.connectionID)); err != nil || !strings.HasPrefix(got, "jdbc:postgresql://"+deploy.PreviewBranchHost(f.connectionID)) || strings.Contains(got, "production-password") {
		t.Fatalf("jdbc = %q, %v", got, err)
	}
	if _, err := resolver.ResolveVariable(ctx, environmentID, revision, fmt.Sprintf("%d.pooled", f.connectionID)); err == nil || !strings.Contains(err.Error(), "no connection pooler") {
		t.Fatalf("pooled = %v", err)
	}
	// The fork's preview still meets production's isolation.
	if _, err := resolver.ResolveVariable(ctx, forkEnvironment, revision, fmt.Sprint(f.connectionID)); !errors.Is(err, deploy.ErrPreviewIsolation) {
		t.Fatalf("a preview without a branch = %v", err)
	}

	refresh := fmt.Sprintf("/api/v1/deploy/%d/pull-requests/9/preview/branches/%d/refresh", f.projectID, f.connectionID)
	if res := f.limited.do(http.MethodPost, refresh, "", nil); res.Code != http.StatusForbidden {
		t.Fatalf("a limited refresh = %d", res.Code)
	}
	if res := f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/pull-requests/10/preview/branches/%d/refresh", f.projectID, f.connectionID), "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("refreshing a branch the fork's preview never had = %d %s", res.Code, res.Body.String())
	}
	res := f.admin.do(http.MethodPost, refresh, "", nil)
	if res.Code != http.StatusAccepted {
		t.Fatalf("refresh = %d %s", res.Code, res.Body.String())
	}
	next, err := store.ActivePreviewBranch(ctx, environmentID, f.connectionID)
	if err != nil || next.ID == branch.ID || next.State != deploy.PreviewBranchWaiting {
		t.Fatalf("after refresh = %+v, %v", next, err)
	}
	if carried, _ := store.PreviewBranchSecret(ctx, next.ID); carried != sealed {
		t.Fatal("the refreshed branch lost its sign-in")
	}
	if got := auditCount(t, f.s, `action='deploy.preview_branch.refresh' AND success=1`); got != 1 {
		t.Fatalf("refresh audit = %d", got)
	}
	if res := f.admin.do(http.MethodPost, refresh, "", nil); res.Code != http.StatusConflict {
		t.Fatalf("refreshing a branch still waiting = %d %s", res.Code, res.Body.String())
	}
}
