// Package catalog is the list of smart stacks the dashboard ships. Adding a
// stack is adding its manifest here; the test beside this file holds each to
// the Compose templates its presets name.
package catalog

import (
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

var manifests = []*stacks.Manifest{media.Manifest()}

// All is every stack, in the order the Stacks tab offers them.
func All() []*stacks.Manifest { return manifests }

// ByID returns a stack by its id.
func ByID(id string) (*stacks.Manifest, bool) {
	for _, manifest := range manifests {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return nil, false
}

// ForTemplate returns the stack and preset a template id deploys.
func ForTemplate(templateID string) (*stacks.Manifest, stacks.Preset, bool) {
	for _, manifest := range manifests {
		if preset, found := manifest.PresetForTemplate(templateID); found {
			return manifest, preset, true
		}
	}
	return nil, stacks.Preset{}, false
}
