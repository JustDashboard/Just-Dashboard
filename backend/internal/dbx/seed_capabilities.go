package dbx

// Seed data is written through the import's writer, so it is offered where
// that writer works: every engine behind the dialect layer.
func init() {
	RegisterCapabilities(Capability{"seed", sqlEngines})
}
