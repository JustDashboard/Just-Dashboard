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

// maxNginxVarLen is the longest variable name nginx takes at its default
// variables_hash_bucket_size of 64, in http and stream alike, on a 64-bit
// build: the hash stores a pointer, then the name and two bytes, rounded up
// to 8, and the bucket must still hold the pointer that ends it. A single
// longer name fails nginx -t for the whole configuration ("could not build
// variables_hash"), so every site stops reloading, not only its owner.
const maxNginxVarLen = 46

// longestVarSuffix is the longest suffix a site or stream puts after its
// ident to name a variable: the static cache's _asset_immutable.
const longestVarSuffix = len("_asset_immutable")

// NginxVarIdent is the ident an owner's variables are named from when
// NginxIdent(name) is too long for them: its start and a hash of the whole
// name, short enough for the longest suffix. A name whose NginxIdent already
// fits keeps it. The owner shortens all of its variables alike, never one at
// a time, because the readers find a variable's partner by swapping the
// suffix: _maint for _maint_ip, a zone's _req for its _limit_exempt.
func NginxVarIdent(name string) string {
	ident := NginxIdent(name)
	keep := maxNginxVarLen - longestVarSuffix
	if len(ident) <= keep {
		return ident
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	tag := fmt.Sprintf("_%08x", h.Sum32())
	return ident[:keep-len(tag)] + tag
}
