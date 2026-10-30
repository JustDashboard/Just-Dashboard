package blueprint

import "testing"

// The constants in catalogue_counts.go are what the competitive report and the
// deployments feature page quote. They are checked against the shipped definitions so a template
// cannot join or leave the catalogue without the numbers being bumped on
// purpose in the same change.
func TestCatalogueCountsMatchTheShippedDefinitions(t *testing.T) {
	t.Parallel()
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	offered, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	deployable, compose := 0, 0
	for _, entry := range offered {
		supported, _ := DeploymentSupport(entry)
		if !supported {
			continue
		}
		deployable++
		if entry.IsCompose() {
			compose++
		}
	}
	if len(all) != CatalogueBuiltIn {
		t.Errorf("builtin/ ships %d definitions, CatalogueBuiltIn says %d; bump the constant with the change that added or removed one", len(all), CatalogueBuiltIn)
	}
	if deployable != CatalogueDeployable {
		t.Errorf("%d offered definitions deploy, CatalogueDeployable says %d", deployable, CatalogueDeployable)
	}
	if compose != CatalogueCompose {
		t.Errorf("%d deployable definitions are Compose templates, CatalogueCompose says %d", compose, CatalogueCompose)
	}
}
