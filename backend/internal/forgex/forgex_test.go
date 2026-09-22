package forgex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

const testSHA = "0123456789012345678901234567890123456789"

func fixture(t *testing.T, kind string, handler http.HandlerFunc) (*Service, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, sealer)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	path := t.TempDir()
	if _, err := s.Configure(context.Background(), path, Setup{Kind: kind, URL: server.URL, Project: "owner/repo", Token: "private-test-token"}); err != nil {
		t.Fatal(err)
	}
	return s, path
}
func TestProviderRequestsAndEncryptedAccounts(t *testing.T) {
	for _, kind := range []string{"gitlab", "gitea"} {
		t.Run(kind, func(t *testing.T) {
			writes := []map[string]any{}
			methods := []string{}
			row := map[string]any{"number": 7, "title": "Change", "state": "open", "head": map[string]string{"ref": "feature", "sha": testSHA}, "base": map[string]string{"ref": "main"}, "html_url": "https://example.com/request/7"}
			if kind == "gitlab" {
				row = map[string]any{"iid": 7, "title": "Change", "state": "opened", "sha": testSHA, "source_branch": "feature", "target_branch": "main", "web_url": "https://example.com/request/7"}
			}
			s, path := fixture(t, kind, func(w http.ResponseWriter, r *http.Request) {
				if kind == "gitlab" && r.Header.Get("PRIVATE-TOKEN") != "private-test-token" {
					t.Error("missing GitLab token")
				}
				if kind == "gitea" && r.Header.Get("Authorization") != "token private-test-token" {
					t.Error("missing Gitea token")
				}
				if r.Method != "GET" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					writes = append(writes, body)
					methods = append(methods, r.Method)
					_ = json.NewEncoder(w).Encode(row)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/user"):
					fmt.Fprint(w, `{"login":"operator","username":"operator"}`)
				case strings.HasSuffix(r.URL.Path, "/owner/repo"), strings.HasSuffix(r.URL.Path, "/projects/owner/repo"):
					fmt.Fprint(w, `{"default_branch":"main"}`)
				case strings.HasSuffix(r.URL.Path, "/7"):
					_ = json.NewEncoder(w).Encode(row)
				case strings.HasSuffix(r.URL.Path, "/diffs"):
					fmt.Fprint(w, `[{"new_path":"a.txt","old_path":"a.txt","diff":"@@ -1 +1 @@\n-old\n+new"}]`)
				case strings.HasSuffix(r.URL.Path, "/7.diff"):
					fmt.Fprint(w, "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-old\n+new")
				case strings.HasSuffix(r.URL.Path, "/notes"), strings.HasSuffix(r.URL.Path, "/comments"):
					fmt.Fprint(w, `[{"id":1,"body":"Discuss","author":{"username":"operator"},"user":{"login":"operator"}}]`)
				default:
					_ = json.NewEncoder(w).Encode([]any{row})
				}
			})
			ctx := context.Background()
			public, err := s.Status(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(public)
			if strings.Contains(string(encoded), "token") {
				t.Fatal("public token leak")
			}
			k, _ := key(path)
			stored, _, _ := s.store.Setting(ctx, k)
			if strings.Contains(stored, "private-test-token") || strings.Contains(stored, "operator") {
				t.Fatal("plaintext account in database")
			}
			other, err := s.Status(ctx, t.TempDir())
			if err != nil || other.Configured {
				t.Fatal("account leaked to another checkout")
			}
			list, err := s.List(ctx, path, "open", 1)
			if err != nil || len(list.Requests) != 1 || list.Requests[0].SHA != testSHA {
				t.Fatal(list, err)
			}
			files, err := s.Files(ctx, path, 7, 1, testSHA)
			if err != nil || (len(files.Files) == 0 && files.Body == "") {
				t.Fatal(files, err)
			}
			conversation, err := s.Conversation(ctx, path, 7, 1)
			if err != nil || len(conversation.Comments) != 1 {
				t.Fatal(conversation, err)
			}
			if err := s.Act(ctx, path, 7, Action{Action: "approve", SHA: strings.Repeat("a", 40)}); err == nil {
				t.Fatal("accepted stale approval")
			}
			if len(writes) != 0 {
				t.Fatal("stale approval sent")
			}
			if err := s.Act(ctx, path, 7, Action{Action: "approve", SHA: testSHA}); err != nil {
				t.Fatal(err)
			}
			if kind == "gitlab" && writes[0]["sha"] != testSHA {
				t.Fatal(writes)
			}
			if kind == "gitea" && writes[0]["commit_id"] != testSHA {
				t.Fatal(writes)
			}
			if err := s.Act(ctx, path, 7, Action{Action: "merge", SHA: testSHA, Method: "squash"}); err != nil {
				t.Fatal(err)
			}
			if kind == "gitlab" && (methods[1] != "PUT" || writes[1]["sha"] != testSHA) {
				t.Fatal(methods, writes)
			}
			if kind == "gitea" && writes[1]["head_commit_id"] != testSHA {
				t.Fatal(writes)
			}
			if _, err := s.Create(ctx, path, NewRequest{Title: "New", Head: "feature", Base: "main", Body: "literal\n$(true)"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Disconnect(ctx, path); err != nil {
				t.Fatal(err)
			}
			public, _ = s.Status(ctx, path)
			if public.Configured {
				t.Fatal("disconnect retained token")
			}
		})
	}
}

func TestProviderRejectsRedirectsAndInsecureSetup(t *testing.T) {
	for _, address := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/?token=x", "https://example.com/../other"} {
		req := Setup{Kind: "gitlab", URL: address, Project: "owner/repo", Token: "secret"}
		if validateSetup(&req) == nil {
			t.Fatal(address)
		}
	}
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	s := New(nil, nil)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer source.Close()
	_, _, err := s.raw(context.Background(), &credential{Config: Config{Kind: "gitea", URL: source.URL}, Token: "private-test-token"}, "GET", "/user", nil)
	if err == nil || leaked {
		t.Fatal("provider followed an authenticated redirect")
	}
}
