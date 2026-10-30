package blueprint

// The catalogue's size, as the competitive report quotes it. These are
// declared rather than derived so that a template added or retired by
// accident fails catalogue_counts_test.go instead of silently changing the
// numbers the report and docs/features/deployments.md repeat: whoever changes the catalogue
// bumps them deliberately in the same change, and the test confirms the
// shipped definitions agree.
const (
	// CatalogueBuiltIn is every definition in builtin/, retired ones included.
	CatalogueBuiltIn = 162
	// CatalogueDeployable is how many of the offered definitions
	// DeploymentSupport accepts: every one of them, the two Minecraft game
	// servers included.
	CatalogueDeployable = 157
	// CatalogueCompose is how many of those are multi-service Compose templates.
	CatalogueCompose = 56
)
