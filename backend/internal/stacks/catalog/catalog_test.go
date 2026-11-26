package catalog

import (
	"slices"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// Every preset names a reviewed Compose template that says it belongs to the
// stack and runs exactly the preset's services, and no template claims a
// stack that is not here: the adapters trust the services to be the ones
// they were written for.
func TestEveryPresetIsATemplateOfItsStack(t *testing.T) {
	claimed := map[string]bool{}
	for _, manifest := range All() {
		if err := manifest.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, preset := range manifest.Presets {
			definition, err := blueprint.Get(preset.Template)
			if err != nil {
				t.Fatalf("%s/%s: %v", manifest.ID, preset.ID, err)
			}
			if definition.Stack != manifest.ID || !definition.IsCompose() {
				t.Fatalf("%s is not a Compose template of stack %s", preset.Template, manifest.ID)
			}
			names := []string{}
			for _, service := range definition.Services {
				names = append(names, service.Name)
			}
			if !slices.Equal(names, preset.Services) {
				t.Fatalf("%s runs %v, preset %s says %v", preset.Template, names, preset.ID, preset.Services)
			}
			claimed[preset.Template] = true
		}
	}
	everything, err := blueprint.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range everything {
		if summary.Stack != "" && !claimed[summary.ID] {
			t.Fatalf("template %s claims stack %q, which has no preset for it", summary.ID, summary.Stack)
		}
	}
}
