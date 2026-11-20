package fileversions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func newKeeper(t *testing.T, limits Limits) (*Keeper, string) {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := filepath.Join(t.TempDir(), "file-versions")
	k := New(st.DB, dir, limits)
	clock := time.Unix(1_700_000_000, 0)
	k.now = func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}
	return k, dir
}

// save is a save of after over before, as the files package hands it over.
func save(path, before, after string) Save {
	s := Save{Path: path, Written: []byte(after), By: "admin", Reason: ReasonSave}
	if before != "\x00none" {
		s.Existed, s.Content = true, []byte(before)
	}
	return s
}

func contents(t *testing.T, k *Keeper, path string) []string {
	t.Helper()
	h, err := k.History(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range h.Versions {
		content, _, err := k.Content(context.Background(), path, v.ID)
		if err != nil {
			t.Fatalf("version %d: %v", v.ID, err)
		}
		out = append(out, string(content))
	}
	return out
}

func TestEachReplacingSaveKeepsWhatItReplacedAndANewFileKeepsNothing(t *testing.T) {
	ctx := context.Background()
	k, dir := newKeeper(t, DefaultLimits)
	const path = "/srv/app/app.conf"
	if err := k.Record(ctx, save(path, "\x00none", "one")); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, k, path); len(got) != 0 {
		t.Fatalf("a new file kept a version: %q", got)
	}
	for _, step := range [][2]string{{"one", "two"}, {"two", "three"}} {
		if err := k.Record(ctx, save(path, step[0], step[1])); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(contents(t, k, path), ","); got != "two,one" {
		t.Fatalf("versions, newest first: %s", got)
	}
	h, _ := k.History(ctx, path)
	if h.Head == nil || h.Head.Revision != revisionOf([]byte("three")) || h.Versions[0].By != "admin" || h.Versions[0].Outside {
		t.Fatalf("history: %+v head %+v", h.Versions, h.Head)
	}
	// Saving the same bytes again replaces nothing worth keeping.
	if err := k.Record(ctx, save(path, "three", "three")); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, k, path); len(got) != 2 {
		t.Fatalf("an unchanged save kept a version: %q", got)
	}
	// Root's alone: the copies may be of keys and .env files.
	for _, p := range []string{dir, filepath.Join(dir, "objects")} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", p, info.Mode(), err)
		}
	}
	object, _ := k.objectPath(revisionOf([]byte("one")))
	if info, err := os.Stat(object); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("kept content mode: %v %v", info, err)
	}
}

func TestAnEditMadeOutsideTheDashboardIsCapturedAndLabelled(t *testing.T) {
	ctx := context.Background()
	k, _ := newKeeper(t, DefaultLimits)
	const path = "/etc/nginx/nginx.conf"
	if err := k.Record(ctx, save(path, "original", "saved by dashboard")); err != nil {
		t.Fatal(err)
	}
	// Somebody edits it in a shell, then the next save comes through here.
	if err := k.Record(ctx, save(path, "edited in a shell", "saved again")); err != nil {
		t.Fatal(err)
	}
	h, err := k.History(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Versions) != 3 {
		t.Fatalf("versions: %+v", h.Versions)
	}
	shell, displaced, first := h.Versions[0], h.Versions[1], h.Versions[2]
	if !shell.Outside || shell.Reason != ReasonSave || shell.Revision != revisionOf([]byte("edited in a shell")) {
		t.Fatalf("the shell's edit was not labelled: %+v", shell)
	}
	if displaced.Reason != ReasonDisplaced || displaced.Outside || displaced.Revision != revisionOf([]byte("saved by dashboard")) {
		t.Fatalf("the dashboard's own save that the shell replaced was lost: %+v", displaced)
	}
	if first.Outside || first.Revision != revisionOf([]byte("original")) {
		t.Fatalf("first save: %+v", first)
	}
	if got := strings.Join(contents(t, k, path), ","); got != "edited in a shell,saved by dashboard,original" {
		t.Fatal(got)
	}
}

func TestBoundsPruneOldestFirstAndRemoveOrphanedContents(t *testing.T) {
	ctx := context.Background()
	k, _ := newKeeper(t, Limits{PerFile: 3, TotalBytes: 1 << 20})
	const path = "/srv/notes.txt"
	previous := "v0"
	for i := 1; i <= 6; i++ {
		next := "v" + string(rune('0'+i))
		if err := k.Record(ctx, save(path, previous, next)); err != nil {
			t.Fatal(err)
		}
		previous = next
	}
	if got := strings.Join(contents(t, k, path), ","); got != "v5,v4,v3" {
		t.Fatalf("per-file bound kept %s", got)
	}
	for _, gone := range []string{"v0", "v1", "v2"} {
		object, _ := k.objectPath(revisionOf([]byte(gone)))
		if _, err := os.Stat(object); !os.IsNotExist(err) {
			t.Fatalf("%s's content outlived its version: %v", gone, err)
		}
	}

	// The total: three files of 400 KiB each under a 1 MiB budget keeps the
	// newest, oldest removed first across every file.
	k2, _ := newKeeper(t, Limits{PerFile: 50, TotalBytes: 1 << 20})
	big := func(c byte) string { return strings.Repeat(string(c), 400<<10) }
	for i, p := range []string{"/a", "/b", "/c"} {
		if err := k2.Record(ctx, Save{Path: p, Existed: true, Content: []byte(big(byte('a' + i))),
			WrittenRevision: revisionOf([]byte(p)), Reason: ReasonSave}); err != nil {
			t.Fatal(err)
		}
	}
	total, err := k2.total(ctx)
	if err != nil || total > 1<<20 {
		t.Fatalf("total %d over the bound: %v", total, err)
	}
	if got := contents(t, k2, "/a"); len(got) != 0 {
		t.Fatal("the oldest content was kept over the newest")
	}
	if got := contents(t, k2, "/c"); len(got) != 1 {
		t.Fatal("the newest content was pruned")
	}

	// Zero turns edit history off entirely.
	off, offDir := newKeeper(t, Limits{PerFile: 0})
	if err := off.Record(ctx, save(path, "a", "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(offDir); !os.IsNotExist(err) {
		t.Fatal("history off still kept content")
	}
}

func TestLargeContentIsNotKept(t *testing.T) {
	ctx := context.Background()
	k, _ := newKeeper(t, DefaultLimits)
	if err := k.Record(ctx, Save{Path: "/var/big.log", Existed: true, TooLarge: true,
		Written: []byte(strings.Repeat("x", MaxFileBytes+1)), Reason: ReasonSave}); err != nil {
		t.Fatal(err)
	}
	h, _ := k.History(ctx, "/var/big.log")
	if len(h.Versions) != 0 {
		t.Fatal("an oversized file kept a version")
	}
	if total, _ := k.total(ctx); total != 0 {
		t.Fatalf("an oversized save was kept whole: %d bytes", total)
	}
}

func TestHistoryFollowsAMoveAndAVersionBelongsToItsPath(t *testing.T) {
	ctx := context.Background()
	k, _ := newKeeper(t, DefaultLimits)
	for _, p := range []string{"/srv/site/conf/a.conf", "/srv/site2/b.conf"} {
		if err := k.Record(ctx, save(p, "old "+p, "new "+p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := k.Move(ctx, "/srv/site", "/srv/renamed"); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, k, "/srv/renamed/conf/a.conf"); len(got) != 1 || got[0] != "old /srv/site/conf/a.conf" {
		t.Fatalf("history did not follow the move: %q", got)
	}
	if got := contents(t, k, "/srv/site2/b.conf"); len(got) != 1 {
		t.Fatal("a sibling whose name starts the same moved too")
	}
	// The head moved with it: the next save is not an outside change.
	if err := k.Record(ctx, save("/srv/renamed/conf/a.conf", "new /srv/site/conf/a.conf", "newer")); err != nil {
		t.Fatal(err)
	}
	h, _ := k.History(ctx, "/srv/renamed/conf/a.conf")
	if h.Versions[0].Outside {
		t.Fatal("a moved file's next save was taken for an outside edit")
	}
	if _, _, err := k.Content(ctx, "/srv/site2/b.conf", h.Versions[0].ID); err != ErrNotFound {
		t.Fatalf("another file's version was served for this path: %v", err)
	}
}

func TestAFailureToKeepIsReportedNotHidden(t *testing.T) {
	ctx := context.Background()
	k, dir := newKeeper(t, DefaultLimits)
	// A file where the folder should be: nothing can be kept.
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := k.Record(ctx, save("/srv/x", "a", "b")); err == nil {
		t.Fatal("a version that could not be kept was reported kept")
	}
	if got := contents(t, k, "/srv/x"); len(got) != 0 {
		t.Fatalf("a row points at content that was never kept: %q", got)
	}
}
