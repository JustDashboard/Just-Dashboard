package dockerx

import (
	"sort"
	"testing"
)

// Alphabetical order put three leftover test compose files — none of them ever
// deployed — above the stacks actually serving traffic, because their names
// happened to sort first. The first screenful is the one an operator reads.
func TestStacksSortRunningFirst(t *testing.T) {
	out := []ComposeStack{
		{Name: "Just-Dashboard"},
		{Name: "Just-Dashboard-main"},
		{Name: "Just-Dashboard-sidebar"},
		{Name: "zzz-stopped", Containers: 2},
		{Name: "bet-bot", Containers: 1, Running: 1},
		{Name: "epgjauto", Containers: 1, Running: 1},
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := stackRank(out[i]), stackRank(out[j]); a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})

	want := []string{
		"bet-bot", "epgjauto", // running
		"zzz-stopped",                                                     // deployed but down
		"Just-Dashboard", "Just-Dashboard-main", "Just-Dashboard-sidebar", // never deployed
	}
	for i, name := range want {
		if out[i].Name != name {
			t.Fatalf("position %d = %q, want %q (full order: %v)", i, out[i].Name, name, names(out))
		}
	}
}

func names(stacks []ComposeStack) []string {
	out := make([]string, len(stacks))
	for i, st := range stacks {
		out[i] = st.Name
	}
	return out
}
