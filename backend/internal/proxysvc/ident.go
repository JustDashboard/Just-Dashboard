package proxysvc

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// NginxIdent names an nginx object a site or stream owns — a limit_req zone,
// an upstream, a map variable, a cache zone, a log format — after its owner.
//
// Those names are global to the whole configuration, and a second zone of the
// same name is an emergency that takes every site down at the next reload, so
// each owner gets its own. nginx accepts fewer characters in them than a site
// name has, and folding the rest to "_" alone would give a-b, a_b and a.b one
// zone between three sites; whenever anything was folded, a short hash of the
// original name keeps them apart.
func NginxIdent(name string) string {
	var b strings.Builder
	b.WriteString("jd_")
	folded := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
		folded = true
	}
	if folded {
		h := fnv.New32a()
		h.Write([]byte(name))
		fmt.Fprintf(&b, "_%06x", h.Sum32()>>8)
	}
	return b.String()
}
