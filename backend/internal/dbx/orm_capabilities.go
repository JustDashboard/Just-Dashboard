package dbx

// Which generators an engine can be pointed at, for the driver catalogue.
// The Generate page is gated on `orm`; this is the list it offers, read from
// the same catalogue the generators are checked against.
func init() {
	RegisterCapabilities(Capability{"ormTargets", func(d Driver, _ string) any {
		targets := []string{}
		for _, target := range ORMTargets() {
			if ORMUnsupported(target, d) == "" {
				targets = append(targets, string(target))
			}
		}
		return targets
	}})
}
