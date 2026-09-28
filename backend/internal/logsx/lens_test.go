package logsx

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
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
		checkDeclared(t, id, &l)
		out = append(out, l)
	}
	return out
}

// checkDeclared fails a test whose lens names an event or records an attr its
// registration does not declare. The golden file is written from the
// declarations, and the frontend labels only what the golden file lists, so
// an undeclared name reaches the page as a bare id. A line a composite handed
// on is held to the vocabulary of the lens that read it as well. given lists
// keys that were on the line before the lens saw it, as a journal entry's are.
func checkDeclared(t *testing.T, id string, l *Line, given ...string) {
	t.Helper()
	var vocab []*Lens
	for _, name := range []string{id, l.Lens} {
		if lens, _ := LensByID(name); lens != nil {
			vocab = append(vocab, lens)
		}
	}
	declares := func(list func(*Lens) []string, name string) bool {
		for _, lens := range vocab {
			if slices.Contains(list(lens), name) {
				return true
			}
		}
		return false
	}
	if l.Event != "" && !declares(func(lens *Lens) []string { return lens.Events }, l.Event) {
		t.Errorf("%s: event %q is not declared by %s (%q)", id, l.Event, l.Lens, l.Text)
	}
	for key := range l.Attrs {
		if !slices.Contains(given, key) && !declares(func(lens *Lens) []string { return lens.Attrs }, key) {
			t.Errorf("%s: attr %q is not declared by %s (%q)", id, key, l.Lens, l.Text)
		}
	}
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
