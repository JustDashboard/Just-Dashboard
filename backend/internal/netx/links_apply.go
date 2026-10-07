package netx

import "strings"

// renderLinks renders the ip batch file the boot unit restores the devices,
// addresses, routes, rules and namespaces from.
func renderLinks(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	return b.String()
}
