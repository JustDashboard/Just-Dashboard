package netx

import (
	"sort"
	"strings"
)

// renderSysctl renders the kernel settings drop-in systemd-sysctl reads at
// boot.
func renderSysctl(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	keys := make([]string, 0, len(sp.Sysctls))
	for k := range sp.Sysctls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k + " = " + sp.Sysctls[k] + "\n")
	}
	return b.String()
}
