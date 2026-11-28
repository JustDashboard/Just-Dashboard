package api

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestListGETRoutes writes every GET route the router mounts to the file
// JD_LIST_GET_ROUTES names, one pattern a line, and is skipped without it.
// scripts/verify-distros/run.sh opens each one that takes no parameter on a
// freshly installed machine, so the walk follows the routes as they are.
func TestListGETRoutes(t *testing.T) {
	out := os.Getenv("JD_LIST_GET_ROUTES")
	if out == "" {
		t.Skip("set JD_LIST_GET_ROUTES to a file to list the GET routes into")
	}
	r, ok := testServer(t).Routes().(chi.Routes)
	if !ok {
		t.Fatal("Routes() no longer returns something chi can walk")
	}
	var gets []string
	err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet {
			gets = append(gets, pattern)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(gets)
	if err := os.WriteFile(out, []byte(strings.Join(gets, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
