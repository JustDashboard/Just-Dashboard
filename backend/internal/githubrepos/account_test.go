package githubrepos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testWeb = "https://github.com"

func TestGraphQLIsFoundBesideTheRESTRoot(t *testing.T) {
	for root, want := range map[string]string{
		"https://api.github.com":             "https://api.github.com/graphql",
		"https://api.github.com/":            "https://api.github.com/graphql",
		"":                                   "https://api.github.com/graphql",
		"https://ghe.example.test/api/v3":    "https://ghe.example.test/api/graphql",
		"https://ghe.example.test/api/v3/":   "https://ghe.example.test/api/graphql",
		"http://127.0.0.1:4010":              "http://127.0.0.1:4010/graphql",
		"https://ghe.example.test/v3/api/v3": "https://ghe.example.test/v3/api/graphql",
	} {
		if got := GraphQLURL(root); got != want {
			t.Errorf("GraphQLURL(%q) = %q, want %q", root, got, want)
		}
	}
}

func TestContributionLevelsAreGitHubsQuartiles(t *testing.T) {
	for name, want := range map[string]int{
		"NONE": 0, "FIRST_QUARTILE": 1, "SECOND_QUARTILE": 2, "THIRD_QUARTILE": 3, "FOURTH_QUARTILE": 4, "": 0, "FIFTH_QUARTILE": 0,
	} {
		if got := contributionLevel(name); got != want {
			t.Errorf("contributionLevel(%q) = %d, want %d", name, got, want)
		}
	}
}

const calendarAnswer = `{"data":{"viewer":{"contributionsCollection":{"contributionCalendar":{"totalContributions":7,"weeks":[
	{"contributionDays":[{"date":"2026-11-21","contributionCount":0,"contributionLevel":"NONE","weekday":6}]},
	{"contributionDays":[{"date":"2026-11-22","contributionCount":3,"contributionLevel":"SECOND_QUARTILE","weekday":0},
	                     {"date":"2026-11-23","contributionCount":4,"contributionLevel":"FOURTH_QUARTILE","weekday":1},
	                     {"date":"not a date","contributionCount":9,"contributionLevel":"FOURTH_QUARTILE","weekday":2}]}]}}}}}`

func TestTheProfileLeavesOutWhatGitHubHoldsEmpty(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /user", 200, `{"login":"mona","name":null,"bio":"","company":"  ","location":"Berlin","blog":"mona.example",
		"html_url":"https://github.com/mona","followers":12,"following":3,"public_repos":40,"created_at":"2019-05-06T07:08:09Z"}`)
	fake.on("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_test" {
			t.Errorf("GraphQL Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(calendarAnswer))
	})
	profile, err := readProfile(context.Background(), testAccess(t, fake).Client)
	if err != nil {
		t.Fatal(err)
	}
	if query, _ := fake.body("POST /graphql")["query"].(string); query != contributionsQuery {
		t.Fatalf("query = %q", query)
	}
	raw, _ := json.Marshal(profile)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	for _, absent := range []string{"name", "bio", "company", "contributionsMessage"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s is present in %s", absent, raw)
		}
	}
	if got["login"] != "mona" || got["location"] != "Berlin" || got["blog"] != "mona.example" || got["htmlUrl"] != "https://github.com/mona" ||
		got["followers"] != 12.0 || got["following"] != 3.0 || got["publicRepos"] != 40.0 || got["createdAt"] != "2019-05-06T07:08:09Z" {
		t.Fatalf("profile = %s", raw)
	}
	want := &Contributions{Total: 7, Weeks: []ContributionWeek{
		{Days: []ContributionDay{{Date: "2026-11-21", Count: 0, Level: 0, Weekday: 6}}},
		{Days: []ContributionDay{{Date: "2026-11-22", Count: 3, Level: 2, Weekday: 0}, {Date: "2026-11-23", Count: 4, Level: 4, Weekday: 1}}},
	}}
	if !reflect.DeepEqual(profile.Contributions, want) {
		t.Fatalf("contributions = %+v", profile.Contributions)
	}
}

// The calendar is the lesser half: without it the profile still answers and
// says why. What is about the account itself — a refused sign-in, a spent
// rate limit — is the whole answer.
func TestTheProfileStandsWithoutItsCalendar(t *testing.T) {
	user := `{"login":"mona","html_url":"https://github.com/mona"}`
	stands := map[string]func(w http.ResponseWriter, r *http.Request){
		"errors": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}]}`))
		},
		"errors beside data": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"viewer":null},"errors":[{"message":"timeout"}]}`))
		},
		"no calendar": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":{"viewer":{}}}`)) },
		"forbidden": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		},
		"unavailable": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) },
		"not JSON":    func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
	}
	for name, answer := range stands {
		fake := newFake(t)
		fake.json("GET /user", 200, user)
		fake.on("POST /graphql", answer)
		profile, err := readProfile(context.Background(), testAccess(t, fake).Client)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if profile.Login != "mona" || profile.Contributions != nil || !strings.HasSuffix(profile.ContributionsMessage, ".") {
			t.Fatalf("%s: profile = %+v", name, profile)
		}
		wantMessage := "GitHub did not share the contribution calendar."
		if name == "unavailable" || name == "not JSON" {
			wantMessage = "The contribution calendar could not be read from GitHub just now."
		}
		if profile.ContributionsMessage != wantMessage {
			t.Errorf("%s: message = %q", name, profile.ContributionsMessage)
		}
		raw, _ := json.Marshal(profile)
		if strings.Contains(string(raw), `"contributions"`) {
			t.Errorf("%s: %s", name, raw)
		}
	}

	whole := map[Kind]func(w http.ResponseWriter, r *http.Request){
		KindUnauthorized: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		},
		KindRateLimited: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1900000000")
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		},
	}
	for kind, answer := range whole {
		fake := newFake(t)
		fake.json("GET /user", 200, user)
		fake.on("POST /graphql", answer)
		if _, err := readProfile(context.Background(), testAccess(t, fake).Client); KindOf(err) != kind {
			t.Fatalf("%s: err = %v", kind, err)
		}
	}

	// A sign-in the connection layer already knows is gone is the whole
	// answer too, whichever half met it.
	if !accountFailure(&ReconnectError{Login: "mona"}) || accountFailure(errors.New("other")) || accountFailure(&GraphQLError{}) {
		t.Fatal("accountFailure")
	}
}

func TestInboxQueriesNameTheConnectionsOwnLogin(t *testing.T) {
	cases := []struct {
		kind, filter, state string
		page                int
		want                string
	}{
		{InboxPulls, "created", "open", 1, "is:pr is:open archived:false author:mona"},
		{InboxPulls, "assigned", "closed", 2, "is:pr is:closed archived:false assignee:mona"},
		{InboxPulls, "mentioned", "open", 34, "is:pr is:open archived:false mentions:mona"},
		{InboxPulls, "review", "open", 1, "is:pr is:open archived:false review-requested:mona"},
		{InboxIssues, "created", "closed", 1, "is:issue is:closed archived:false author:mona"},
		{InboxIssues, "assigned", "open", 3, "is:issue is:open archived:false assignee:mona"},
		{InboxIssues, "mentioned", "open", 1, "is:issue is:open archived:false mentions:mona"},
	}
	for _, c := range cases {
		path, err := InboxQuery{Kind: c.kind, Filter: c.filter, State: c.state, Page: c.page}.searchPath("mona")
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		parsed, err := url.Parse(path)
		if err != nil || parsed.Path != "/search/issues" {
			t.Fatalf("path = %q %v", path, err)
		}
		q := parsed.Query()
		if q.Get("q") != c.want || q.Get("sort") != "updated" || q.Get("order") != "desc" || q.Get("per_page") != "30" || q.Get("page") != jsonInt(c.page) || len(q) != 5 {
			t.Errorf("%+v: query = %v", c, q)
		}
	}
	// A login that would read as more than one qualifier never reaches a
	// query, and an enterprise-managed login (an underscore) does.
	for _, login := range []string{"", "mona author:hubot", "mona\"", "mona/x", "-mona"} {
		_, err := InboxQuery{Kind: InboxPulls, Filter: "created", State: "open", Page: 1}.searchPath(login)
		if KindOf(err) != KindUnavailable {
			t.Errorf("login %q: err = %v", login, err)
		}
	}
	if _, err := (InboxQuery{Kind: InboxPulls, Filter: "created", State: "open", Page: 1}).searchPath("mona_acme"); err != nil {
		t.Fatal(err)
	}
}

func jsonInt(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestInboxQueriesAreCheckedBeforeGitHubIsAsked(t *testing.T) {
	query, err := ParseInboxQuery(InboxIssues, "", "", "")
	if err != nil || query != (InboxQuery{Kind: InboxIssues, Filter: "created", State: "open", Page: 1}) {
		t.Fatalf("defaults = %+v %v", query, err)
	}
	query, err = ParseInboxQuery(InboxPulls, "review", "closed", "34")
	if err != nil || query != (InboxQuery{Kind: InboxPulls, Filter: "review", State: "closed", Page: 34}) {
		t.Fatalf("review = %+v %v", query, err)
	}
	for _, bad := range [][4]string{
		{InboxIssues, "review", "", ""},
		{InboxPulls, "involves", "", ""},
		{InboxPulls, "created author:hubot", "", ""},
		{InboxPulls, "", "merged", ""},
		{InboxPulls, "", "all", ""},
		{InboxPulls, "", "", "0"},
		{InboxPulls, "", "", "-1"},
		{InboxPulls, "", "", "35"},
		{InboxPulls, "", "", "two"},
		{InboxPulls, "", "", "1.5"},
		{InboxPulls, "", "", " 2"},
		{"commits", "", "", ""},
	} {
		_, err := ParseInboxQuery(bad[0], bad[1], bad[2], bad[3])
		wantInvalid(t, err)
	}
	// The service checks again whatever it is handed.
	_, err = InboxQuery{Kind: InboxIssues, Filter: "review", State: "open", Page: 1}.searchPath("mona")
	wantInvalid(t, err)
	_, err = InboxQuery{Kind: InboxPulls, Filter: "created", State: "open", Page: 0}.searchPath("mona")
	wantInvalid(t, err)
}

func TestInboxItemsAreMappedFromGitHubsSearch(t *testing.T) {
	fake := newFake(t)
	query := InboxQuery{Kind: InboxPulls, Filter: "created", State: "closed", Page: 2}
	path, _ := query.searchPath("mona")
	fake.on("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://api.github.com/search/issues?page=3>; rel="next", <https://api.github.com/search/issues?page=5>; rel="last"`)
		_, _ = w.Write([]byte(`{"total_count":131,"incomplete_results":true,"items":[
			{"repository_url":"https://api.github.com/repos/octo-org/widgets","number":7,"title":"Merged one","state":"closed","draft":false,
			 "user":{"login":"mona"},"labels":[{"name":"bug","color":"d73a4a","description":null}],"comments":4,
			 "created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T10:00:00Z","closed_at":"2026-11-02T09:00:00Z",
			 "html_url":"https://github.com/octo-org/widgets/pull/7","state_reason":null,
			 "pull_request":{"url":"x","merged_at":"2026-11-02T09:00:00Z"}},
			{"repository_url":"https://api.github.com/repos/mona/dot.files","number":8,"title":"Draft","state":"open","draft":true,
			 "user":{"login":"mona"},"labels":[],"comments":0,"created_at":"2026-11-03T10:00:00Z","updated_at":"2026-11-03T10:00:00Z","closed_at":null,
			 "html_url":"https://github.com/mona/dot.files/pull/8","pull_request":{"merged_at":null}},
			{"repository_url":"https://ghe.example.test/api/v3/repos/acme_corp/api","number":9,"title":"Not planned","state":"closed",
			 "user":null,"comments":1,"created_at":"2026-11-03T10:00:00Z","updated_at":"2026-11-04T10:00:00Z","closed_at":"2026-11-04T10:00:00Z",
			 "html_url":"https://ghe.example.test/acme_corp/api/issues/9","state_reason":"not_planned","draft":true},
			{"repository_url":"https://api.github.com/somewhere/else","number":10,"title":"No repository","state":"open"},
			{"repository_url":"https://api.github.com/repos/octo-org/widgets/extra","number":11,"title":"Not a repository","state":"open"}]}`))
	})
	page, err := searchInbox(context.Background(), testAccess(t, fake).Client, "mona", query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 131 || !page.HasMore || !page.Incomplete || len(page.Items) != 3 {
		t.Fatalf("page = %+v", page)
	}
	merged, draft, issue := page.Items[0], page.Items[1], page.Items[2]
	if merged.Repository != "octo-org/widgets" || merged.Number != 7 || merged.State != "merged" || merged.Draft || merged.Author != "mona" ||
		len(merged.Labels) != 1 || merged.Labels[0].Name != "bug" || merged.Comments != 4 || merged.ClosedAt.IsZero() || merged.StateReason != "" ||
		merged.HTMLURL != "https://github.com/octo-org/widgets/pull/7" {
		t.Fatalf("merged = %+v", merged)
	}
	if draft.Repository != "mona/dot.files" || draft.State != "open" || !draft.Draft || !draft.ClosedAt.IsZero() || draft.Labels == nil {
		t.Fatalf("draft = %+v", draft)
	}
	// An issue has a reason and is never a draft, whatever the item said.
	if issue.Repository != "acme_corp/api" || issue.State != "closed" || issue.StateReason != "not_planned" || issue.Draft || issue.Author != "" {
		t.Fatalf("issue = %+v", issue)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "0001-01-01") || strings.Count(string(raw), `"stateReason"`) != 1 || strings.Count(string(raw), `"draft"`) != 3 ||
		strings.Contains(string(raw), `"labels":null`) {
		t.Fatalf("page as JSON = %s", raw)
	}

	// The last page GitHub serves has no next, whatever a header says, and an
	// answer with nothing is an empty list, not null.
	last := newFake(t)
	query = InboxQuery{Kind: InboxIssues, Filter: "mentioned", State: "open", Page: maxInboxPage}
	path, _ = query.searchPath("mona")
	last.on("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://api.github.com/search/issues?page=35>; rel="next"`)
		_, _ = w.Write([]byte(`{"total_count":5000,"incomplete_results":false,"items":[]}`))
	})
	page, err = searchInbox(context.Background(), testAccess(t, last).Client, "mona", query)
	if err != nil || page.HasMore || page.Total != 5000 || page.Incomplete {
		t.Fatalf("last = %+v %v", page, err)
	}
	if raw, _ := json.Marshal(page); !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("empty page as JSON = %s", raw)
	}
}

const eventFixture = `[
 {"id":"1","type":"PushEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T10:00:00Z",
  "payload":{"ref":"refs/heads/feature/x","size":3,"commits":[{},{}],
             "before":"1111111111111111111111111111111111111111","head":"2222222222222222222222222222222222222222"}},
 {"id":"2","type":"PushEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T09:00:00Z",
  "payload":{"ref":"refs/heads/new branch","commits":[{},{}],
             "before":"0000000000000000000000000000000000000000","head":"2222222222222222222222222222222222222222"}},
 {"id":"3","type":"PullRequestEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T08:00:00Z",
  "payload":{"action":"closed","number":12,"pull_request":{"number":12,"title":"Add it","merged":true,"html_url":"https://github.com/octo-org/widgets/pull/12"}}},
 {"id":"4","type":"PullRequestEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T07:00:00Z",
  "payload":{"action":"opened","number":13,"pull_request":{"number":13,"url":"https://api.github.com/repos/octo-org/widgets/pulls/13"}}},
 {"id":"5","type":"IssuesEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T06:00:00Z",
  "payload":{"action":"reopened","issue":{"number":5,"title":"It broke","html_url":"https://github.com/octo-org/widgets/issues/5"}}},
 {"id":"6","type":"IssueCommentEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T05:00:00Z",
  "payload":{"action":"created","issue":{"number":5,"title":"It broke","html_url":"https://github.com/octo-org/widgets/issues/5"},
             "comment":{"html_url":"https://github.com/octo-org/widgets/issues/5#issuecomment-99"}}},
 {"id":"7","type":"PullRequestReviewEvent","repo":{"name":"octo-org/widgets"},"created_at":"2026-11-24T04:00:00Z",
  "payload":{"action":"created","review":{"state":"APPROVED","html_url":"https://github.com/octo-org/widgets/pull/12#pullrequestreview-1"},
             "pull_request":{"number":12,"title":"Add it"}}},
 {"id":"8","type":"CreateEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T03:00:00Z","payload":{"ref":null,"ref_type":"repository"}},
 {"id":"9","type":"CreateEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T02:30:00Z","payload":{"ref":"v1.0","ref_type":"tag"}},
 {"id":"10","type":"DeleteEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T02:00:00Z","payload":{"ref":"old","ref_type":"branch"}},
 {"id":"11","type":"WatchEvent","repo":{"name":"someone/liked"},"created_at":"2026-11-24T01:00:00Z","payload":{"action":"started"}},
 {"id":"12","type":"ForkEvent","repo":{"name":"someone/liked"},"created_at":"2026-11-24T00:30:00Z",
  "payload":{"forkee":{"full_name":"mona/liked","html_url":"https://github.com/mona/liked"}}},
 {"id":"13","type":"ReleaseEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:20:00Z",
  "payload":{"action":"published","release":{"name":"","tag_name":"v1.0","html_url":"https://github.com/mona/new/releases/tag/v1.0"}}},
 {"id":"14","type":"ReleaseEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:15:00Z",
  "payload":{"action":"published","release":{"name":"First","tag_name":"v0.9","html_url":"javascript:alert(1)"}}},
 {"id":"15","type":"PublicEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:10:00Z","payload":{}},
 {"id":"16","type":"MemberEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:05:00Z","payload":{"action":"added","member":{"login":"hubot"}}},
 {"id":"17","type":"GollumEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:04:00Z","payload":{"pages":[]}},
 {"id":"18","type":"SponsorshipEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:03:00Z"},
 {"id":"19","type":"WatchEvent","repo":{"name":"not a repository"},"created_at":"2026-11-24T00:02:00Z","payload":{}},
 {"id":"20","type":"PushEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:01:00Z","payload":{"ref":"refs/tags/v1.0","size":0}},
 {"id":"21","type":"PullRequestEvent","repo":{"name":"mona/new"},"created_at":"2026-11-24T00:00:30Z","payload":"not an object"}
]`

func TestEventsAreFoldedIntoOneShapePerKind(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /users/mona/events?per_page=30", 200, eventFixture)
	list, err := readEvents(context.Background(), testAccess(t, fake).Client, "mona", testWeb+"/")
	if err != nil {
		t.Fatal(err)
	}
	widgets := "https://github.com/octo-org/widgets"
	want := []Event{
		{ID: "1", Kind: "push", Repository: "octo-org/widgets", Ref: "feature/x", Commits: 3,
			HTMLURL: widgets + "/compare/1111111111111111111111111111111111111111...2222222222222222222222222222222222222222"},
		// A branch's first push has nothing to compare with, and a push
		// without GitHub's count is counted from its commits.
		{ID: "2", Kind: "push", Repository: "octo-org/widgets", Ref: "new branch", Commits: 2, HTMLURL: widgets + "/tree/new%20branch"},
		{ID: "3", Kind: "pull", Repository: "octo-org/widgets", Action: "merged", Number: 12, Title: "Add it", HTMLURL: widgets + "/pull/12"},
		// GitHub may send a pull request without its title or page address.
		{ID: "4", Kind: "pull", Repository: "octo-org/widgets", Action: "opened", Number: 13, HTMLURL: widgets + "/pull/13"},
		{ID: "5", Kind: "issue", Repository: "octo-org/widgets", Action: "reopened", Number: 5, Title: "It broke", HTMLURL: widgets + "/issues/5"},
		{ID: "6", Kind: "comment", Repository: "octo-org/widgets", Action: "created", Number: 5, Title: "It broke", HTMLURL: widgets + "/issues/5#issuecomment-99"},
		{ID: "7", Kind: "review", Repository: "octo-org/widgets", Action: "approved", Number: 12, Title: "Add it", HTMLURL: widgets + "/pull/12#pullrequestreview-1"},
		{ID: "8", Kind: "create", Repository: "mona/new", RefType: "repository"},
		{ID: "9", Kind: "create", Repository: "mona/new", RefType: "tag", Ref: "v1.0"},
		{ID: "10", Kind: "delete", Repository: "mona/new", RefType: "branch", Ref: "old"},
		{ID: "11", Kind: "star", Repository: "someone/liked"},
		{ID: "12", Kind: "fork", Repository: "someone/liked", HTMLURL: "https://github.com/mona/liked"},
		{ID: "13", Kind: "release", Repository: "mona/new", Action: "published", Ref: "v1.0", Title: "v1.0", HTMLURL: "https://github.com/mona/new/releases/tag/v1.0"},
		// An address a browser would not open as a page is not a link.
		{ID: "14", Kind: "release", Repository: "mona/new", Action: "published", Ref: "v0.9", Title: "First"},
		{ID: "15", Kind: "public", Repository: "mona/new"},
		{ID: "16", Kind: "member", Repository: "mona/new", Action: "added"},
		{ID: "20", Kind: "push", Repository: "mona/new", Ref: "v1.0", HTMLURL: "https://github.com/mona/new/tree/v1.0"},
		// A payload that does not read leaves who, where and when.
		{ID: "21", Kind: "pull", Repository: "mona/new"},
	}
	if len(list.Events) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(list.Events), len(want), list.Events)
	}
	for i, event := range list.Events {
		if event.CreatedAt.IsZero() {
			t.Errorf("event %s has no time", event.ID)
		}
		event.CreatedAt = want[i].CreatedAt
		if event != want[i] {
			t.Errorf("event %d\n got %+v\nwant %+v", i, event, want[i])
		}
	}
	raw, _ := json.Marshal(list.Events[10])
	if string(raw) != `{"id":"11","kind":"star","repository":"someone/liked","createdAt":"2026-11-24T01:00:00Z"}` {
		t.Fatalf("a star as JSON = %s", raw)
	}

	none := newFake(t)
	none.json("GET /users/mona/events?per_page=30", 200, `[]`)
	list, err = readEvents(context.Background(), testAccess(t, none).Client, "mona", testWeb)
	if raw, _ := json.Marshal(list); err != nil || string(raw) != `{"events":[]}` {
		t.Fatalf("none = %s %v", raw, err)
	}
	if _, err := readEvents(context.Background(), testAccess(t, newFake(t)).Client, "mona/../orgs", testWeb); KindOf(err) != KindUnavailable {
		t.Fatalf("a login that is not one: %v", err)
	}
}

func TestKeptAnswersExpireAndStayBounded(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	kept := answers[int, string]{}
	kept.put(1, "one", now, time.Minute)
	if value, ok := kept.get(1, now.Add(59*time.Second)); !ok || value != "one" {
		t.Fatalf("within its time = %q %v", value, ok)
	}
	if _, ok := kept.get(1, now.Add(time.Minute)); ok {
		t.Fatal("answered past its time")
	}
	if _, ok := kept.get(2, now); ok {
		t.Fatal("answered what was never kept")
	}
	// A full map makes room, what has expired first.
	for i := 2; i <= maxCached; i++ {
		kept.put(i, "fresh", now.Add(2*time.Minute), time.Minute)
	}
	if len(kept) != maxCached {
		t.Fatalf("%d kept", len(kept))
	}
	kept.put(maxCached+1, "new", now.Add(2*time.Minute), time.Minute)
	if _, ok := kept[1]; ok || len(kept) != maxCached {
		t.Fatalf("the expired answer stayed; %d kept", len(kept))
	}
	kept.put(maxCached+2, "newer", now.Add(2*time.Minute), time.Minute)
	if _, ok := kept.get(maxCached+2, now.Add(2*time.Minute)); !ok || len(kept) > maxCached {
		t.Fatalf("%d kept", len(kept))
	}
	// Replacing an answer takes nobody's place.
	kept.put(maxCached+2, "again", now.Add(2*time.Minute), time.Minute)
	if len(kept) != maxCached {
		t.Fatalf("%d kept after a replacement", len(kept))
	}
}
