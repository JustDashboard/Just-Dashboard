package logsx

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/lenses.golden.json")

// readThrough runs lines through ParseLine and one reader of the lens, in
// order, the way a follow or a search would. Lens tests use it so the state a
// reader carries between lines is exercised exactly as it is in production.
func readThrough(t *testing.T, id string, texts ...string) []Line {
	t.Helper()
	lens, err := LensByID(id)
	if err != nil || lens == nil {
		t.Fatalf("lens %q not registered: %v", id, err)
	}
	r := lens.New()
	out := make([]Line, 0, len(texts))
	for _, text := range texts {
		l := ParseLine(text, "")
		r.Read(&l)
		out = append(out, l)
	}
	return out
}

// TestLensGolden writes the vocabulary every lens declares to a file the
// frontend's registry test reads, so an event the parsers emit cannot reach
// the page without a label. Run with -update after changing a lens.
func TestLensGolden(t *testing.T) {
	type entry struct {
		Events []string `json:"events"`
		Attrs  []string `json:"attrs"`
	}
	golden := map[string]entry{}
	for _, id := range LensIDs() {
		l := lenses[id]
		golden[id] = entry{Events: l.Events, Attrs: l.Attrs}
	}
	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "lenses.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/logsx -run TestLensGolden -update)", err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s is stale; run go test ./internal/logsx -run TestLensGolden -update", path)
	}
}

func TestFingerprintGroupsTheSameShape(t *testing.T) {
	a := Fingerprint("SELECT * FROM orders WHERE id = 42 AND state IN ('a','b')")
	b := Fingerprint("select *  from orders where id = 7 and state in ('c')  -- hot path")
	if a != b {
		t.Fatalf("same shape, different fingerprints: %s %s", a, b)
	}
	if c := Fingerprint("SELECT * FROM users WHERE id = 42"); c == a {
		t.Fatalf("different shapes share a fingerprint")
	}
	if len(a) != 12 {
		t.Fatalf("fingerprint %q is not twelve hex digits", a)
	}
}
