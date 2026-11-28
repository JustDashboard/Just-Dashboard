package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// stackSourcesSchema is two of the pinned Prowlarr's schema entries, cut to
// what the routes read: the generic Torznab, which asks for an address and a
// key, and a public site that asks for nothing.
const stackSourcesSchema = `[
 {"definitionName":"Torznab","name":"Generic Torznab","description":"A Newznab-like api for torrents.","language":"en-US",
  "privacy":"private","protocol":"torrent","implementation":"Torznab","implementationName":"Torznab","configContract":"TorznabSettings",
  "indexerUrls":[""],"capabilities":{"categories":[]},"presets":[],"tags":[],"enable":true,"appProfileId":0,
  "fields":[
   {"order":0,"name":"baseUrl","label":"Url","value":"","type":"textbox","advanced":false,"privacy":"normal","isFloat":false},
   {"order":1,"name":"apiPath","label":"API Path","value":"/api","type":"textbox","advanced":true,"privacy":"normal","isFloat":false},
   {"order":2,"name":"apiKey","label":"API Key","helpText":"Site API Key","type":"textbox","advanced":false,"privacy":"apiKey","isFloat":false}]},
 {"definitionName":"examplepublic","name":"Example Public","description":"A public site.","language":"en-US",
  "privacy":"public","protocol":"torrent","implementation":"Cardigann","implementationName":"Cardigann","configContract":"CardigannSettings",
  "indexerUrls":["https://public.example.org/"],"capabilities":{"categories":[]},"presets":[],"tags":[],"enable":true,"appProfileId":0,
  "fields":[
   {"order":0,"name":"definitionFile","value":"examplepublic","type":"textbox","advanced":false,"hidden":"hidden","privacy":"normal","isFloat":false},
   {"order":1,"name":"baseUrl","label":"Base Url","type":"select","advanced":false,"selectOptionsProviderAction":"getUrls","privacy":"normal","isFloat":false}]},
 {"definitionName":"Newznab","name":"Generic Newznab","protocol":"usenet","privacy":"private","fields":[]}
]`

// stackSourcesProwlarr is a stand-in for the stack's Prowlarr: the schema, the
// app profiles, the indexers it has, its test (which fails for a key starting
// "wrong", echoing it back), the save, the sync and the removal.
type stackSourcesProwlarr struct {
	key     string
	mu      sync.Mutex
	listed  []map[string]any
	schemas atomic.Int32
	server  *httptest.Server
}

func newStackSourcesProwlarr(t *testing.T) *stackSourcesProwlarr {
	t.Helper()
	fake := &stackSourcesProwlarr{key: strings.Repeat("k", 32)}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != fake.key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		answer := func(status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if value != nil {
				_ = json.NewEncoder(w).Encode(value)
			}
		}
		var body map[string]any
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		switch path := strings.TrimPrefix(r.URL.Path, "/api/v1/"); {
		case path == "indexer/schema":
			fake.schemas.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, stackSourcesSchema)
		case path == "appprofile":
			answer(http.StatusOK, []map[string]any{{"id": 1, "name": "Standard"}})
		case path == "indexer" && r.Method == http.MethodGet:
			answer(http.StatusOK, fake.listed)
		case path == "indexer/test":
			key := ""
			for _, raw := range body["fields"].([]any) {
				if field := raw.(map[string]any); field["name"] == "apiKey" {
					key, _ = field["value"].(string)
				}
			}
			if strings.HasPrefix(key, "wrong") {
				answer(http.StatusBadRequest, []map[string]any{{"isWarning": false, "propertyName": "",
					"errorMessage": "Indexer returned result for RSS URL, Credentials appears to be invalid. Response: the key " + key + " is not known"}})
				return
			}
			answer(http.StatusOK, map[string]any{})
		case path == "indexer" && r.Method == http.MethodPost:
			body["id"] = 20 + len(fake.listed)
			fake.listed = append(fake.listed, body)
			answer(http.StatusCreated, body)
		case path == "tag":
			// A stack without FlareSolverr: Prowlarr has no tags.
			answer(http.StatusOK, []any{})
		case path == "command":
			answer(http.StatusCreated, map[string]any{"id": 3, "status": "completed"})
		case strings.HasPrefix(path, "indexer/"):
			for index, entry := range fake.listed {
				if fmt.Sprint(entry["id"]) != strings.TrimPrefix(path, "indexer/") {
					continue
				}
				if r.Method == http.MethodDelete {
					fake.listed = append(fake.listed[:index], fake.listed[index+1:]...)
					answer(http.StatusOK, nil)
					return
				}
				answer(http.StatusOK, entry)
				return
			}
			answer(http.StatusNotFound, map[string]string{"message": "NotFound"})
		default:
			t.Errorf("Prowlarr: unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// seedSourcesStack is a media stack of a preset, under a name of its own,
// whose environment serves nothing yet.
func seedSourcesStack(t *testing.T, s *Server, preset string) (projectID, environmentID int64) {
	t.Helper()
	var count int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_projects`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("media-%d", count+1)
	project, err := s.Store.DB.Exec(`INSERT INTO deploy_projects (name, repo_path, hook_secret, hook_id, created_at) VALUES (?, '', 'x', ?, 1)`, name, "hook-"+name)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ = project.LastInsertId()
	environment, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, created_at, updated_at) VALUES(?, 'production', 'production', 'production', 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ = environment.LastInsertId()
	if err := s.modules.stackStore.Register(context.Background(), projectID, environmentID, media.ID, preset, "tester"); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// seedLiveStack is a stack of a preset whose environment serves a live
// Compose release of the given template.
func seedLiveStack(t *testing.T, s *Server, preset, template string) (projectID, environmentID int64) {
	t.Helper()
	projectID, environmentID = seedSourcesStack(t, s, preset)
	release, err := s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, source_identity_json, created_at) VALUES(?, ?, 1, ?, 1)`,
		projectID, environmentID, `{"template":"`+template+`@1.0.0"}`)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at) VALUES(?, ?, 'compose', ?, 1, 1)`,
		releaseID, environmentID, fmt.Sprintf("jd-media-%d", projectID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// The source routes answer only for a stack whose live release runs
// Prowlarr, say why in words otherwise, hold to the capabilities their
// verbs carry, and reach Prowlarr through the stack's own environment: the
// catalogue read once, an add tested, saved, synced and audited by its
// definition alone, a failed test answered with Prowlarr's reason and none of
// what was typed, a password kept from the assistant, a removal by id.
func TestStackSourceRoutesGateAndAnswerInWords(t *testing.T) {
	s := testServer(t)
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	s.modules.stackSources = media.NewSourceCache()
	prowlarr := newStackSourcesProwlarr(t)
	var envs atomic.Int32
	reachable := true
	original := stackSourceEnv
	stackSourceEnv = func(_ context.Context, _ *Server, _ *stacks.Manifest, preset stacks.Preset, _ *stacks.Record, _ stacks.Live) (stacks.Env, error) {
		envs.Add(1)
		address := prowlarr.server.URL
		if !reachable {
			address = "http://127.0.0.1:1"
		}
		return &stackstest.Fake{PresetID: preset.ID, Services: map[string]string{media.SourcesService: address},
			Vars: map[string]string{media.VariableProwlarrAPIKey: prowlarr.key}}, nil
	}
	t.Cleanup(func() { stackSourceEnv = original })

	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signIn(t, s)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	refused := func(c *client, method, path, body string, status int, code string) string {
		t.Helper()
		response := c.do(method, "/api/v1"+path, body, nil)
		if response.Code != status || !strings.Contains(response.Body.String(), `"`+code+`"`) {
			t.Fatalf("%s %s = %d %s; want %d %s", method, path, response.Code, response.Body.String(), status, code)
		}
		return response.Body.String()
	}
	ok := func(c *client, method, path, body string, status int, out any) {
		t.Helper()
		response := c.do(method, "/api/v1"+path, body, nil)
		if response.Code != status {
			t.Fatalf("%s %s = %d %s; want %d", method, path, response.Code, response.Body.String(), status)
		}
		if out != nil {
			if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Not a stack; a stack serving nothing yet; one serving another template;
	// one whose preset has no Prowlarr.
	refused(admin, http.MethodGet, "/deploy/999999/stack/sources", "", http.StatusNotFound, "stack_not_found")
	waiting, _ := seedSourcesStack(t, s, "complete")
	refused(admin, http.MethodGet, fmt.Sprintf("/deploy/%d/stack/sources", waiting), "", http.StatusConflict, "stack_waiting")
	other, _ := seedLiveStack(t, s, "complete", "whoami")
	refused(admin, http.MethodGet, fmt.Sprintf("/deploy/%d/stack/sources/catalogue", other), "", http.StatusConflict, "stack_not_preset")
	essential, _ := seedLiveStack(t, s, "essential", media.TemplateEssential)
	refused(admin, http.MethodPost, fmt.Sprintf("/deploy/%d/stack/sources", essential), `{"definition":"examplepublic"}`, http.StatusConflict, "stack_no_sources")
	if envs.Load() != 0 {
		t.Fatalf("Prowlarr was reached for a stack that refused: %d", envs.Load())
	}

	project, _ := seedLiveStack(t, s, "complete", media.TemplateComplete)
	base := fmt.Sprintf("/deploy/%d/stack/sources", project)
	reachable = false
	refused(admin, http.MethodGet, base, "", http.StatusBadGateway, "stack_app_unreachable")
	reachable = true

	var listed stackSourcesAnswer
	ok(reader, http.MethodGet, base, "", http.StatusOK, &listed)
	if listed.Sources == nil || len(listed.Sources) != 0 {
		t.Fatalf("sources = %+v", listed)
	}
	var catalogue media.MediaSourceCatalogue
	ok(reader, http.MethodGet, base+"/catalogue", "", http.StatusOK, &catalogue)
	if len(catalogue.Definitions) != 2 || catalogue.Definitions[0].Definition != "examplepublic" || catalogue.Definitions[0].NeedsInput ||
		catalogue.Definitions[1].Definition != "Torznab" || !catalogue.Definitions[1].NeedsInput || catalogue.FetchedAt.IsZero() {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	var detail media.MediaSourceDetail
	ok(reader, http.MethodGet, base+"/catalogue/"+url.PathEscape("Torznab"), "", http.StatusOK, &detail)
	if detail.Name != "Generic Torznab" || len(detail.Fields) != 3 || detail.Fields[2].Type != media.SourceFieldPassword {
		t.Fatalf("detail = %+v", detail)
	}
	refused(reader, http.MethodGet, base+"/catalogue/nothing", "", http.StatusNotFound, "source_definition_not_found")
	if prowlarr.schemas.Load() != 1 {
		t.Fatalf("the schema was read %d times for three requests", prowlarr.schemas.Load())
	}

	// A read-only account reads and does not change.
	refused(reader, http.MethodPost, base, `{"definition":"examplepublic"}`, http.StatusForbidden, "forbidden")
	refused(reader, http.MethodDelete, base+"/20", "", http.StatusForbidden, "forbidden")

	refused(admin, http.MethodPost, base, `{}`, http.StatusBadRequest, "bad_request")
	refused(admin, http.MethodPost, base, `{"definition":"nothing"}`, http.StatusUnprocessableEntity, "source_invalid")
	if body := refused(admin, http.MethodPost, base, `{"definition":"examplepublic","fields":{"baseUrl":"https://elsewhere.example.org/"}}`, http.StatusUnprocessableEntity, "source_invalid"); !strings.Contains(body, `"field":"fields.baseUrl"`) {
		t.Fatalf("an invalid value does not name its field: %s", body)
	}

	// A failed test: Prowlarr's reason, without the key typed.
	typed := "wrong-key-typed-by-the-person"
	body := refused(admin, http.MethodPost, base, `{"definition":"Torznab","fields":{"baseUrl":"http://torznab.example.org","apiKey":" `+typed+` "}}`,
		http.StatusUnprocessableEntity, "source_test_failed")
	if strings.Contains(body, typed) || !strings.Contains(body, "Credentials appears to be invalid") {
		t.Fatalf("the failure = %s", body)
	}

	// The assistant may add one that needs no password, never one that does.
	user, err := s.Auth.CreateUser(t.Context(), "helper", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	assistant := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1"+base, strings.NewReader(body))
		request = request.WithContext(httpx.WithAssistant(request.Context(), &httpx.Principal{User: user, Role: auth.RoleAdmin, Kind: "session", Elevated: true}))
		request.RemoteAddr = "127.0.0.1:5555"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(httpx.CSRFHeader, "1")
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		return response
	}
	if response := assistant(`{"definition":"Torznab","fields":{"baseUrl":"http://torznab.example.org","apiKey":"a-key-the-model-saw"}}`); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "source_needs_person") {
		t.Fatalf("the assistant adding a key = %d %s", response.Code, response.Body.String())
	}
	if len(prowlarr.listed) != 0 {
		t.Fatal("a source was saved")
	}
	var added stackSourceAddAnswer
	if response := assistant(`{"definition":"examplepublic"}`); response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &added) != nil {
		t.Fatalf("the assistant adding a public source = %d %s", response.Code, response.Body.String())
	}
	if added.Source.Name != "Example Public" || !added.Synced || added.Source.ID != 20 {
		t.Fatalf("added = %+v", added)
	}
	refused(admin, http.MethodPost, base, `{"definition":"examplepublic"}`, http.StatusConflict, "source_exists")

	// The person adds one with a key; the stack is asked to look again.
	if _, err := s.Store.DB.Exec(`UPDATE deploy_stacks SET requested_at = 0 WHERE project_id = ?`, project); err != nil {
		t.Fatal(err)
	}
	key := "the-person-typed-this-key"
	ok(admin, http.MethodPost, base, `{"definition":"Torznab","fields":{"baseUrl":"http://torznab.example.org","apiKey":"`+key+`"}}`, http.StatusCreated, &added)
	if added.Source.Name != "Generic Torznab" || added.Source.Definition != "Torznab" || !added.Synced {
		t.Fatalf("added = %+v", added)
	}
	var requested int64
	if err := s.Store.DB.QueryRow(`SELECT requested_at FROM deploy_stacks WHERE project_id = ?`, project).Scan(&requested); err != nil || requested == 0 {
		t.Fatalf("no pass was requested after an add: %d %v", requested, err)
	}
	ok(reader, http.MethodGet, base, "", http.StatusOK, &listed)
	if len(listed.Sources) != 2 || listed.Sources[0].Name != "Example Public" || listed.Sources[1].Name != "Generic Torznab" {
		t.Fatalf("sources = %+v", listed.Sources)
	}

	var adds, failures int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(success = 0), 0) FROM audit_log WHERE action = 'deploy.stack.source.add'`).Scan(&adds, &failures); err != nil {
		t.Fatal(err)
	}
	if adds != 7 || failures != 5 {
		t.Fatalf("adds audited = %d, failed %d", adds, failures)
	}
	var leaked int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE ? OR detail LIKE ? OR detail LIKE ?`,
		"%"+key+"%", "%"+typed+"%", "%a-key-the-model-saw%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the audit log carries a typed key: %d %v", leaked, err)
	}

	// Removal is by Prowlarr's id.
	refused(admin, http.MethodDelete, base+"/x", "", http.StatusBadRequest, "bad_request")
	refused(admin, http.MethodDelete, base+"/99", "", http.StatusNotFound, "source_not_found")
	if response := admin.do(http.MethodDelete, "/api/v1"+base+"/20", "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", response.Code, response.Body.String())
	}
	ok(reader, http.MethodGet, base, "", http.StatusOK, &listed)
	if len(listed.Sources) != 1 || listed.Sources[0].ID != 21 {
		t.Fatalf("sources after a removal = %+v", listed.Sources)
	}
	var removal string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.stack.source.remove' AND success = 1`).Scan(&removal); err != nil ||
		!strings.Contains(removal, "Example Public") {
		t.Fatalf("the removal's audit entry = %q %v", removal, err)
	}
}
