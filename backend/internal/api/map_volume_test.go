package api

import (
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A volume's detail is a page of its own, addressed by name. The map sent a
// press to /docker/volumes?volume=, the address of the sheet the page replaced.
func TestMapVolumeLinksToTheVolumesOwnPage(t *testing.T) {
	for name, want := range map[string]string{
		"shop_pgdata": "/docker/volumes/shop_pgdata",
		"odd name/x":  "/docker/volumes/odd%20name%2Fx",
	} {
		if got := mapVolume(dockerx.Volume{Name: name}, false).Href; got != want {
			t.Errorf("mapVolume(%q).Href = %q, want %q", name, got, want)
		}
	}
}
