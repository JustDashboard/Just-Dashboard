package proxysvc

import (
	"slices"
	"testing"
)

// Only a plain domain no remaining route answers for names a directory of
// Caddy's certificates to remove.
func TestOrphanedCaddyNamesKeepsWhatAnotherRouteServes(t *testing.T) {
	served := []string{"Shop.example.com", "shop.example.com", "www.example.com", "*.example.com", "..", "a/b.example.com"}
	got := orphanedCaddyNames(served, map[string]bool{"www.example.com": true})
	if want := []string{"shop.example.com"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
