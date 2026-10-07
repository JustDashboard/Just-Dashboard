package netx

import "strings"

// renderShaping renders the tc batch file the boot unit restores the speed
// limits and queue disciplines from.
func renderShaping(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	return b.String()
}
