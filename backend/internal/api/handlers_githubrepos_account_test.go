package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos/githubfake"
)

// These drive the connected account's own reads (profile, pull requests and
// issues everywhere, activity) through the whole chain against a stand-in
// GitHub. Each test calls only the routes it names.

// ghAsked counts the requests the stand-in saw for one method and path.
func ghAsked(fake *githubfake.Fake, method, path string) int {
	n := 0
	for _, request := range fake.Requests() {
		if request.Method == method && request.Path == path {
			n++
		}
	}
	return n
}

func ghDecode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v in %s", err, body)
	}
	return out
}

func newGHAccountEnv(t *testing.T) (*ghEnv, string) {
	t.Helper()
	e := newGHEnv(t)
	e.createApp()
	connection := e.connect(e.admin, 1001)
	return e, "/api/v1/github/connections/" + connection
}

func TestTheAccountReadsNeedASessionAndTheConnectionsOwner(t *testing.T) {
	e, base := newGHAccountEnv(t)
	e.fake.Reset()
	anonymous := &client{t: t, h: e.s.Routes()}
	paths := []string{"/profile", "/pulls", "/issues", "/events", "/repos/octo-dev/app/readme", "/repos/octo-dev/app/languages"}
	for _, path := range paths {
		if w := anonymous.do(http.MethodGet, base+path, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d", path, w.Code)
		}
		// Another dashboard account's connection is a missing one, an
		// administrator's included, and a read-only account that owns none
		// is told the same.
		for _, c := range []*ghClient{e.other, e.readonly} {
			w := c.call(http.MethodGet, base+path, "", nil)
			if code, _ := ghErrorCode(t, w); w.Code != http.StatusNotFound || code != "github_connection" {
				t.Errorf("GET %s as another account = %d %s", path, w.Code, w.Body)
			}
		}
	}
	if w := e.admin.call(http.MethodGet, "/api/v1/github/connections/nope/profile", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("a connection id that is not one = %d", w.Code)
	}
	if n := len(e.fake.Requests()); n != 0 {
		t.Fatalf("GitHub was asked %d times", n)
	}
}

func TestTheProfileAnswersWithItsCalendarAndIsKeptFiveMinutes(t *testing.T) {
	e, base := newGHAccountEnv(t)
	user := e.fake.Users[1001]
	user.Bio, user.Location, user.Blog, user.Followers, user.Following, user.PublicRepos = "Builds things", "Berlin", "octo.example", 12, 3, 40
	e.fake.Reset()

	w := e.admin.call(http.MethodGet, base+"/profile", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("profile = %d %s", w.Code, w.Body)
	}
	got := ghDecode(t, w.Body.String())
	if got["login"] != "octo-dev" || got["name"] != "Octo Dev" || got["bio"] != "Builds things" || got["location"] != "Berlin" ||
		got["blog"] != "octo.example" || got["htmlUrl"] != "https://github.com/octo-dev" || got["followers"] != 12.0 ||
		got["following"] != 3.0 || got["publicRepos"] != 40.0 || got["createdAt"] != "2019-05-06T07:08:09Z" {
		t.Fatalf("profile = %s", w.Body)
	}
	// What GitHub holds as empty is absent, never an empty string.
	for _, absent := range []string{"company", "contributionsMessage"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s is present: %s", absent, w.Body)
		}
	}
	calendar, _ := got["contributions"].(map[string]any)
	weeks, _ := calendar["weeks"].([]any)
	if calendar["total"] != 9.0 || len(weeks) != 2 {
		t.Fatalf("contributions = %v", got["contributions"])
	}
	last := weeks[1].(map[string]any)["days"].([]any)[2].(map[string]any)
	if last["date"] != "2026-11-24" || last["count"] != 4.0 || last["level"] != 4.0 || last["weekday"] != 2.0 {
		t.Fatalf("last day = %v", last)
	}
	if ghAsked(e.fake, "GET", "/user") != 1 || ghAsked(e.fake, "POST", "/graphql") != 1 {
		t.Fatalf("first read asked %v", e.fake.Requests())
	}

	// A second visit is answered from memory; refresh=1 asks again.
	if w := e.admin.call(http.MethodGet, base+"/profile", "", nil); w.Code != http.StatusOK || ghAsked(e.fake, "GET", "/user") != 1 {
		t.Fatalf("second read = %d, asked /user %d times", w.Code, ghAsked(e.fake, "GET", "/user"))
	}
	if w := e.admin.call(http.MethodGet, base+"/profile?refresh=1", "", nil); w.Code != http.StatusOK || ghAsked(e.fake, "GET", "/user") != 2 || ghAsked(e.fake, "POST", "/graphql") != 2 {
		t.Fatalf("refresh = %d, asked %v", w.Code, e.fake.Requests())
	}
	if w := e.admin.call(http.MethodGet, base+"/profile?refresh=perhaps", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("refresh=perhaps = %d", w.Code)
	}
	// Refreshing the connection drops what was kept for it.
	if w := e.admin.call(http.MethodPost, base+"/refresh", "", nil); w.Code != http.StatusOK {
		t.Fatalf("connection refresh = %d %s", w.Code, w.Body)
	}
	before := ghAsked(e.fake, "POST", "/graphql")
	if w := e.admin.call(http.MethodGet, base+"/profile", "", nil); w.Code != http.StatusOK || ghAsked(e.fake, "POST", "/graphql") != before+1 {
		t.Fatalf("after a connection refresh the profile was not read again")
	}
}

func TestTheProfileStandsWhenGitHubWillNotAnswerForTheCalendar(t *testing.T) {
	e, base := newGHAccountEnv(t)
	e.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/graphql" {
			return false
		}
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}]}`))
		return true
	}
	w := e.admin.call(http.MethodGet, base+"/profile", "", nil)
	got := ghDecode(t, w.Body.String())
	if _, has := got["contributions"]; w.Code != http.StatusOK || has || got["login"] != "octo-dev" ||
		got["contributionsMessage"] != "GitHub did not share the contribution calendar." {
		t.Fatalf("profile = %d %s", w.Code, w.Body)
	}

	// A sign-in GitHub no longer accepts is the whole answer, as for every
	// other read.
	e.fake.Override = nil
	e.fake.RevokeAll()
	w = e.admin.call(http.MethodGet, base+"/profile?refresh=1", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusConflict || code != "github_reconnect" {
		t.Fatalf("revoked = %d %s", w.Code, w.Body)
	}
	// And nothing kept from before is answered for a connection that needs
	// connecting again.
	w = e.admin.call(http.MethodGet, base+"/profile", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusConflict || code != "github_reconnect" {
		t.Fatalf("revoked, from memory = %d %s", w.Code, w.Body)
	}
}

func ghSearchItem(repository string, number int, extra map[string]any) map[string]any {
	item := map[string]any{
		"repository_url": "https://api.github.com/repos/" + repository, "number": number, "title": "Item", "state": "open",
		"user": map[string]any{"login": "octo-dev"}, "labels": []map[string]any{{"name": "bug", "color": "d73a4a"}}, "comments": 2,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z", "closed_at": nil,
		"html_url": "https://github.com/" + repository + "/issues/1",
	}
	for key, value := range extra {
		item[key] = value
	}
	return item
}

// ghSearches is the q of every search the stand-in was asked, in order.
func ghSearches(fake *githubfake.Fake) []string {
	var out []string
	for _, request := range fake.Requests() {
		if request.Path == "/search/issues" {
			values, _ := url.ParseQuery(request.Query)
			out = append(out, values.Get("q")+" page="+values.Get("page")+" per_page="+values.Get("per_page")+" "+values.Get("sort")+"/"+values.Get("order"))
		}
	}
	return out
}

func TestTheInboxSearchesAsTheConnectionAndKeepsEachPageAMinute(t *testing.T) {
	e, base := newGHAccountEnv(t)
	e.fake.PageSize = 2
	e.fake.Search = []map[string]any{
		ghSearchItem("octo-dev/app", 7, map[string]any{"state": "closed", "closed_at": "2026-11-02T09:00:00Z", "draft": false, "pull_request": map[string]any{"merged_at": "2026-11-02T09:00:00Z"}}),
		ghSearchItem("acme-org/api", 8, map[string]any{"draft": true, "pull_request": map[string]any{"merged_at": nil}}),
		ghSearchItem("acme-org/api", 9, map[string]any{"pull_request": map[string]any{}}),
	}
	e.fake.Reset()

	w := e.admin.call(http.MethodGet, base+"/pulls", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pulls = %d %s", w.Code, w.Body)
	}
	got := ghDecode(t, w.Body.String())
	items, _ := got["items"].([]any)
	if got["total"] != 3.0 || got["hasMore"] != true || got["incomplete"] != false || len(items) != 2 {
		t.Fatalf("pulls = %s", w.Body)
	}
	merged, draft := items[0].(map[string]any), items[1].(map[string]any)
	if merged["repository"] != "octo-dev/app" || merged["number"] != 7.0 || merged["state"] != "merged" || merged["draft"] != false ||
		merged["author"] != "octo-dev" || merged["comments"] != 2.0 || merged["closedAt"] != "2026-11-02T09:00:00Z" ||
		merged["htmlUrl"] != "https://github.com/octo-dev/app/issues/1" || len(merged["labels"].([]any)) != 1 {
		t.Fatalf("merged = %v", merged)
	}
	if _, has := draft["closedAt"]; draft["repository"] != "acme-org/api" || draft["state"] != "open" || draft["draft"] != true || has {
		t.Fatalf("draft = %v", draft)
	}
	if _, has := merged["stateReason"]; has {
		t.Fatalf("a pull request has no state reason: %v", merged)
	}

	// Defaults are spelled out to GitHub; the login is the connection's.
	want := []string{"is:pr is:open archived:false author:octo-dev page=1 per_page=30 updated/desc"}
	if searches := ghSearches(e.fake); strings.Join(searches, "|") != strings.Join(want, "|") {
		t.Fatalf("searches = %q", searches)
	}
	// The same page again, spelled with its defaults or not, is from memory.
	for _, query := range []string{"/pulls", "/pulls?filter=created&state=open&page=1"} {
		if w := e.admin.call(http.MethodGet, base+query, "", nil); w.Code != http.StatusOK || len(ghSearches(e.fake)) != 1 {
			t.Fatalf("%s = %d after %q", query, w.Code, ghSearches(e.fake))
		}
	}
	// Another page, filter, state or listing is another search, and
	// refresh=1 asks again.
	for _, query := range []string{
		"/pulls?page=2", "/pulls?filter=review", "/pulls?filter=assigned&state=closed", "/issues?filter=mentioned", "/pulls?refresh=1",
	} {
		if w := e.admin.call(http.MethodGet, base+query, "", nil); w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", query, w.Code, w.Body)
		}
	}
	want = append(want,
		"is:pr is:open archived:false author:octo-dev page=2 per_page=30 updated/desc",
		"is:pr is:open archived:false review-requested:octo-dev page=1 per_page=30 updated/desc",
		"is:pr is:closed archived:false assignee:octo-dev page=1 per_page=30 updated/desc",
		"is:issue is:open archived:false mentions:octo-dev page=1 per_page=30 updated/desc",
		"is:pr is:open archived:false author:octo-dev page=1 per_page=30 updated/desc",
	)
	if searches := ghSearches(e.fake); strings.Join(searches, "|") != strings.Join(want, "|") {
		t.Fatalf("searches = %q", searches)
	}
	// The second page is the last.
	w = e.admin.call(http.MethodGet, base+"/pulls?page=2", "", nil)
	if got := ghDecode(t, w.Body.String()); got["hasMore"] != false || len(got["items"].([]any)) != 1 || len(ghSearches(e.fake)) != len(want) {
		t.Fatalf("page 2 = %s", w.Body)
	}
}

func TestTheInboxRefusesWhatGitHubsSearchWouldNotTake(t *testing.T) {
	e, base := newGHAccountEnv(t)
	e.fake.Reset()
	for _, query := range []string{
		"/issues?filter=review",
		"/pulls?filter=involves",
		"/pulls?filter=created+author%3Ahubot",
		"/issues?filter=author%3Ahubot",
		"/pulls?state=merged",
		"/issues?state=all",
		"/pulls?page=0",
		"/pulls?page=-3",
		"/pulls?page=35",
		"/issues?page=two",
		"/issues?page=1.5",
	} {
		w := e.admin.call(http.MethodGet, base+query, "", nil)
		if code, _ := ghErrorCode(t, w); w.Code != http.StatusBadRequest || code != "bad_request" {
			t.Errorf("GET %s = %d %s", query, w.Code, w.Body)
		}
	}
	if n := len(e.fake.Requests()); n != 0 {
		t.Fatalf("GitHub was asked %d times", n)
	}
	// The last page GitHub's search answers is still a page.
	if w := e.admin.call(http.MethodGet, base+"/issues?page=34", "", nil); w.Code != http.StatusOK {
		t.Fatalf("page 34 = %d %s", w.Code, w.Body)
	}
	// Search's own rate limit is answered as a rate limit, with when it lifts.
	e.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/search/issues" {
			return false
		}
		w.Header().Set("X-RateLimit-Limit", "30")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "4102444800")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		return true
	}
	w := e.admin.call(http.MethodGet, base+"/issues?filter=assigned", "", nil)
	if code, _ := ghErrorCode(t, w); w.Code != http.StatusTooManyRequests || code != "github_rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limited = %d %s (%v)", w.Code, w.Body, w.Header())
	}
}

func TestTheAccountsEventsAreItsOwnAndKeptAMinute(t *testing.T) {
	e, base := newGHAccountEnv(t)
	e.fake.Events = []map[string]any{
		{"id": "31", "type": "PushEvent", "repo": map[string]any{"name": "octo-dev/app"}, "created_at": "2026-11-24T10:00:00Z",
			"payload": map[string]any{"ref": "refs/heads/main", "size": 2}},
		{"id": "30", "type": "GollumEvent", "repo": map[string]any{"name": "octo-dev/app"}, "created_at": "2026-11-24T09:30:00Z", "payload": map[string]any{}},
		{"id": "29", "type": "PullRequestEvent", "repo": map[string]any{"name": "acme-org/api"}, "created_at": "2026-11-24T09:00:00Z",
			"payload": map[string]any{"action": "closed", "number": 4, "pull_request": map[string]any{"number": 4, "title": "Ship it", "merged": true, "html_url": "https://github.com/acme-org/api/pull/4"}}},
	}
	e.fake.Reset()
	w := e.admin.call(http.MethodGet, base+"/events", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("events = %d %s", w.Code, w.Body)
	}
	want := `{"events":[` +
		`{"id":"31","kind":"push","repository":"octo-dev/app","createdAt":"2026-11-24T10:00:00Z","ref":"main","commits":2,"htmlUrl":"` + e.fake.URL + `/octo-dev/app/tree/main"},` +
		`{"id":"29","kind":"pull","repository":"acme-org/api","createdAt":"2026-11-24T09:00:00Z","action":"merged","number":4,"title":"Ship it","htmlUrl":"https://github.com/acme-org/api/pull/4"}]}`
	if strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("events = %s\nwant     %s", w.Body, want)
	}
	requests := e.fake.Requests()
	if len(requests) != 1 || requests[0].Path != "/users/octo-dev/events" || requests[0].Query != "per_page=30" {
		t.Fatalf("asked %+v", requests)
	}
	if w := e.admin.call(http.MethodGet, base+"/events", "", nil); w.Code != http.StatusOK || len(e.fake.Requests()) != 1 {
		t.Fatalf("second read asked GitHub again")
	}
	if w := e.admin.call(http.MethodGet, base+"/events?refresh=1", "", nil); w.Code != http.StatusOK || len(e.fake.Requests()) != 2 {
		t.Fatalf("refresh = %d after %d requests", w.Code, len(e.fake.Requests()))
	}
}
