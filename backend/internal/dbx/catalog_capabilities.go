package dbx

import "slices"

// What the schema surface offers per engine, stated beside the functions that
// decide it: the catalogue's groups (CatalogGroups) and the structure forms
// (DDLOperations). The driver catalogue reads these rows, so a page never
// draws a branch or a form the routes would refuse.
func init() {
	group := func(names ...string) CapabilityRule {
		return func(d Driver, flavor string) any {
			groups := CatalogGroups(d, flavor)
			return slices.ContainsFunc(names, func(name string) bool { return slices.Contains(groups, name) })
		}
	}
	operation := func(op string) CapabilityRule {
		return func(d Driver, flavor string) any { return slices.Contains(DDLOperations(d, flavor), op) }
	}
	RegisterCapabilities(
		Capability{"catalog", func(d Driver, flavor string) any { return len(CatalogGroups(d, flavor)) > 0 }},
		Capability{"catalogGroups", func(d Driver, flavor string) any { return listOf(CatalogGroups(d, flavor)) }},
		Capability{"ddlOperations", func(d Driver, flavor string) any { return listOf(DDLOperations(d, flavor)) }},
		Capability{"materializedViews", group(GroupMaterializedViews)},
		Capability{"routines", group(GroupFunctions, GroupProcedures)},
		Capability{"triggers", group(GroupTriggers)},
		Capability{"sequences", group(GroupSequences)},
		Capability{"enums", operation(OpEnumTypes)},
		Capability{"comments", operation(OpCommentTable)},
	)
}
