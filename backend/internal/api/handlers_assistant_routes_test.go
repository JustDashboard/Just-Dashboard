package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/go-chi/chi/v5"
)

// A route that upgrades to a WebSocket cannot be held by a recorded request, so
// every handler that does — itself or through a helper of this package — is kept
// from the assistant. The handlers' own source says which they are.
func TestEveryStreamingHandlerIsKeptFromTheAssistant(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]map[string]bool{}
	upgrades := map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			called := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.SelectorExpr:
					if f.Sel.Name == "Upgrade" || f.Sel.Name == "UpgradeProtocol" {
						upgrades[fn.Name.Name] = true
					}
					called[f.Sel.Name] = true
				case *ast.Ident:
					called[f.Name] = true
				}
				return true
			})
			calls[fn.Name.Name] = called
		}
	}
	for changed := true; changed; {
		changed = false
		for fn, called := range calls {
			if upgrades[fn] {
				continue
			}
			for callee := range called {
				if upgrades[callee] {
					upgrades[fn], changed = true, true
					break
				}
			}
		}
	}
	s := testServer(t)
	root := s.Routes().(chi.Routes)
	cat := s.apiCatalogue()
	found := 0
	_ = chi.Walk(root, func(method, pattern string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
		name := handlerName(h)
		if name == "" || !upgrades["handle"+strings.ToUpper(name[:1])+name[1:]] {
			return nil
		}
		found++
		route := cat.byKey[method+" "+strings.TrimPrefix(pattern, "/api/v1")]
		if route == nil || route.Excluded == "" {
			t.Errorf("%s %s (%s) streams and the assistant may make it: add it to agentStreamHandlers", method, pattern, name)
		}
		return nil
	})
	if found < 10 {
		t.Fatalf("only %d streaming routes were found; the source reading is broken", found)
	}
}

// find_routes looks the API up by words and by area, lists what the assistant
// cannot make with the reason, and an empty query lists the areas; the prompt's
// areas are the API's own.
func TestFindRoutesLooksTheAPIUp(t *testing.T) {
	s := testServer(t)
	s.Routes()
	cat := s.apiCatalogue()
	if len(cat.areas) < 20 {
		t.Fatalf("areas = %+v", cat.areas)
	}
	areas := map[string]assistant.APIArea{}
	for _, area := range cat.areas {
		areas[area.Name] = area
	}
	for _, want := range []string{"databases", "backups", "docker", "files", "firewall", "proxy", "deploy", "logs"} {
		if areas[want].Routes == 0 || areas[want].Blurb == "" {
			t.Errorf("the %s area = %+v", want, areas[want])
		}
	}
	if _, listed := areas["auth"]; listed {
		t.Error("the sign-in routes are an area the assistant is told about")
	}
	result := cat.findRoutes(assistant.FindInput{Query: "backup run"})
	if !result.ok || !strings.Contains(result.text, "POST /backups/{id}/run") {
		t.Fatalf("backup run = %s", result.text)
	}
	result = cat.findRoutes(assistant.FindInput{Query: "query", Method: "POST"})
	if !strings.Contains(result.text, "POST /databases/{id}/query") || strings.Contains(result.text, "\n- GET ") {
		t.Fatalf("query = %s", result.text)
	}
	result = cat.findRoutes(assistant.FindInput{Query: "logout"})
	if !strings.Contains(result.text, "cannot make") || !strings.Contains(result.text, "signing in and out") {
		t.Fatalf("logout = %s", result.text)
	}
	if result := cat.findRoutes(assistant.FindInput{}); !strings.Contains(result.text, "/databases (") {
		t.Fatalf("areas = %s", result.text)
	}
	if words := handlerWords("DBConnList", "GET", "/databases"); words != "list database connections" {
		t.Fatalf("words = %q", words)
	}
	route, methods := s.lookupRoute(http.MethodDelete, "/backups")
	if route != nil || len(methods) == 0 || methods[0] != http.MethodGet {
		t.Fatalf("a method the path does not take = %v %v", route, methods)
	}
	if route, _ := s.lookupRoute(http.MethodGet, "/backups"); route == nil || route.Path != "/backups" {
		t.Fatalf("a list without its slash = %v", route)
	}
}

// Every page open_page may name has an address, in a deployment's conversation and
// in the server's, and a label reads as a title.
func TestEveryPageTheAssistantNamesHasAnAddress(t *testing.T) {
	for _, page := range assistant.AgentPages {
		if _, ok := agentOpenablePages[page]; !ok {
			t.Errorf("a deployment's page %q has no address", page)
		}
	}
	for _, page := range assistant.ServerPages {
		if _, ok := agentServerPages[page]; !ok {
			t.Errorf("the server's page %q has no address", page)
		}
	}
	if len(agentServerPages) != len(assistant.ServerPages) || len(agentOpenablePages) != len(assistant.AgentPages) {
		t.Error("an address has no page the model may name")
	}
	if got := routeLabel("List an environment's variables: name, sensitivity, scopes; never a value."); got != "List an environment's variables" {
		t.Fatalf("label = %q", got)
	}
	if got := routeLabel("backup run now"); got != "Backup run now" {
		t.Fatalf("label = %q", got)
	}
}

// A route found by its handler's name is labelled as a person would say it.
func TestARoutesHandlerNameReadsAsWords(t *testing.T) {
	for name, want := range map[string]string{
		"handleDBConnList":      "list database connections",
		"handleDBQuery":         "query database",
		"handleBackupJobList":   "list backup jobs",
		"handleBackupRunNow":    "run backup now",
		"handleListDeployments": "list deployments",
		"handleDBPolicyList":    "list database policies",
	} {
		if got := handlerWords(strings.TrimPrefix(name, "handle"), "GET", "/x"); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// What a hub session is refused — a credential made, changed or read back, a
// link, the dashboard's own settings — is what outlives whoever made it
// (hubSessionRefused). The assistant reaches most of it, as the person does.
// Outside full access every one of those waits for the person: none is a POST
// read as a read, so nothing that makes lasting access runs on a read's terms.
// Full access runs them unasked, which is the mode's recorded meaning (security
// invariant 13): holding these routes alone would not keep an instruction the
// model read from leaving access behind, since the shell and the files it also
// runs unasked there can add a key or a sudo rule too. Changing that is changing
// what full access is, in the invariant and where the person chooses it.
func TestWhatOutlivesTheChatWaitsForThePersonOutsideFullAccess(t *testing.T) {
	s := testServer(t)
	s.Routes()
	server := &agentScope{kind: agentScopeServer}
	reached, closed := 0, 0
	for _, key := range hubSessionRefused {
		method, pattern, _ := strings.Cut(key, " ")
		under := strings.TrimPrefix(pattern, "/api/v1")
		route := s.apiCatalogue().byKey[method+" "+under]
		switch {
		case route == nil:
			t.Errorf("%s is refused a hub session and is not a route the assistant's table has", key)
			continue
		case route.Excluded != "":
			closed++
			continue
		case method == http.MethodGet:
			// A stored secret read back, masked before the provider is sent it.
			continue
		}
		reached++
		view := agentCallView{Kind: s.apiRequestKind(t.Context(), route, under, nil)}
		if view.Kind == agentKindRead {
			t.Errorf("%s makes what outlives the chat, and is read as a read", key)
		}
		for _, mode := range []assistant.Mode{assistant.ModeAsk, assistant.ModeChanges} {
			if !agentHolds(server, mode, view) {
				t.Errorf("%s runs without the person's answer in %q", key, mode)
			}
		}
		if agentHolds(server, assistant.ModeFull, view) {
			t.Errorf("%s waits in full access, which runs everything the person's role allows (security invariant 13)", key)
		}
	}
	if reached == 0 || closed == 0 {
		t.Fatalf("%d reached and %d closed: the walk read nothing", reached, closed)
	}
}
