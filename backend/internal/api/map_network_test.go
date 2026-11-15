package api

import (
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A network's detail is a page of its own, addressed by name. The map sent a
// press to /docker/networks?network=<id>, the address of the sheet the page
// replaced.
func TestMapNetworkLinksToTheNetworksOwnPage(t *testing.T) {
	for name, want := range map[string]string{
		"shop_backend": "/docker/networks/shop_backend",
		"odd name/x":   "/docker/networks/odd%20name%2Fx",
	} {
		if got := mapNetwork(dockerx.Network{ID: "1a2b3c", Name: name}).Href; got != want {
			t.Errorf("mapNetwork(%q).Href = %q, want %q", name, got, want)
		}
	}
}
