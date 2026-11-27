package requesthistory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// The caps bound the file, so what a cap cuts takes the strings only it named
// with it. Left to the retention's sweep, a busy route's unique query strings
// and addresses stayed thirty days after the rows naming them were cut, and
// the dictionary grew past what the caps promise the disk. A quiet route's
// month-old request beside it keeps its strings, and every kept request still
// reads back whole.
func TestACapTakesTheStringsOnlyTheRowsItCutNamed(t *testing.T) {
	a, c := openTest(t)
	ctx := context.Background()
	quiet := parse(t, caddyLine(c.Now().Add(-29*24*time.Hour), "/quiet?since=a-month", 200, "QuietBrowser/1"))
	if err := a.Append(ctx, "quiet", []accesslog.Entry{quiet}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	// A thousand requests, each with a path and a query of its own: a cache
	// buster or a tracking parameter on every one.
	busy := make([]accesslog.Entry, 1000)
	for i := range busy {
		at := c.Now().Add(-3 * time.Hour).Add(time.Duration(i) * time.Second)
		busy[i] = parse(t, caddyLine(at, fmt.Sprintf("/p/%d?t=%d", i, i), 200, "x"))
	}
	if err := a.Append(ctx, "busy", busy, Cursor{}); err != nil {
		t.Fatal(err)
	}
	// The cap is applied on a later hour's pass, as the hourly prune does.
	c.Advance(2 * time.Hour)
	if err := a.prune(ctx, caps{route: 100, total: 10_000}); err != nil {
		t.Fatal(err)
	}
	kept := scanAll(t, a, "busy")
	if len(kept) != 90 {
		t.Fatalf("busy keeps %d requests, want 90", len(kept))
	}
	var strings int
	if err := a.db.Reader.QueryRow(`SELECT count(*) FROM strings`).Scan(&strings); err != nil {
		t.Fatal(err)
	}
	// Each kept request names a path and a query of its own; everything else
	// is shared, and the quiet route's few.
	if limit := 2*len(kept) + 20; strings > limit {
		t.Fatalf("%d strings are kept for %d requests, want at most %d: the cut rows' strings stayed", strings, len(kept), limit)
	}
	for _, e := range kept {
		if e.Path == "" || e.Query == "" || e.UserAgent != "x" || e.RemoteIP == "" {
			t.Fatalf("a kept request lost a string: %+v", e)
		}
	}
	if got := scanAll(t, a, "quiet"); len(got) != 1 || got[0].Query != quiet.Query || got[0].UserAgent != quiet.UserAgent {
		t.Fatalf("the quiet route's request lost its strings: %+v", got)
	}
	// The writer's dictionary starts again: a request appended now names
	// strings that are there.
	fresh := parse(t, caddyLine(c.Now().Add(-time.Minute), "/p/1?t=1", 200, "x"))
	if err := a.Append(ctx, "busy", []accesslog.Entry{fresh}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	if got := scanAll(t, a, "busy"); got[len(got)-1].Query != fresh.Query || got[len(got)-1].Path != fresh.Path {
		t.Fatalf("a request appended after the sweep reads back as %+v", got[len(got)-1])
	}
}
