package media

import "testing"

func TestManifestIsWellFormed(t *testing.T) {
	if err := Manifest().Validate(); err != nil {
		t.Fatal(err)
	}
}
