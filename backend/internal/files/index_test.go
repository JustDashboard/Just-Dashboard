package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func indexFixture(t *testing.T) (*Service, *NameIndex, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	data := filepath.Join(base, "data")
	for _, dir := range []string{root, data} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := New([]string{root})
	return svc, NewNameIndex(svc, data, time.Hour), root
}

func hitPaths(r *FindResult) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path)
	}
	return out
}

func TestNameIndexFindsWhatTheWalkWouldAndRanksItTheSameWay(t *testing.T) {
	svc, index, root := indexFixture(t)
	for _, p := range []string{
		"srv/app/src/components/Promo.tsx",
		"srv/app/deep/a/b/c/d/e/f/g/h/i/j/k/l/m/promo-banner.png",
		"srv/app/proxy-tls-monitor.spec.ts",
		"etc/nginx/sites-available/example.conf",
		".config/promo-secret.txt",
	} {
		writeFile(t, filepath.Join(root, p), "x", 0o644)
	}
	if _, _, err := index.Find(context.Background(), FindOptions{Root: root, Query: "promo"}); err != ErrNoIndex {
		t.Fatalf("before a build: %v, want ErrNoIndex", err)
	}
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(index.dir, indexFileName)); info.Mode().Perm() != 0o600 {
		t.Fatalf("the index is %o: it holds every path on the server", info.Mode().Perm())
	}
	if info, _ := os.Stat(index.dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("the index folder is %o", info.Mode().Perm())
	}

	got, info, err := index.Find(context.Background(), FindOptions{Root: root, Query: "promo"})
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || !info.Complete || info.Entries == 0 {
		t.Fatalf("index info: %+v", info)
	}
	paths := hitPaths(got)
	// Deeper than the walk's twelve levels, still found.
	if !strings.Contains(strings.Join(paths, "\n"), "promo-banner.png") {
		t.Fatalf("the deep file was not found: %v", paths)
	}
	for _, p := range paths {
		if strings.Contains(p, ".config") {
			t.Fatalf("a hidden path was returned without hidden: %v", paths)
		}
	}
	// find.go's scoring: the shallow word match outranks the deep one and
	// the scattered letters.
	walked, err := svc.Find(context.Background(), FindOptions{Root: root, Query: "promo", MaxDepth: 32})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(hitPaths(walked)) != fmt.Sprint(paths) {
		t.Fatalf("the index ranks differently from the walk:\nindex %v\nwalk  %v", paths, hitPaths(walked))
	}
	for i := range walked.Hits {
		if walked.Hits[i].Score != got.Hits[i].Score || fmt.Sprint(walked.Hits[i].Matches) != fmt.Sprint(got.Hits[i].Matches) {
			t.Fatalf("scores differ: %+v vs %+v", walked.Hits[i], got.Hits[i])
		}
	}
	hidden, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "promo", Hidden: true})
	if !strings.Contains(strings.Join(hitPaths(hidden), "\n"), "promo-secret") {
		t.Fatal("hidden=true did not reach the hidden file")
	}
	// A search of a folder below the root reads only that folder's part.
	sub, _, _ := index.Find(context.Background(), FindOptions{Root: filepath.Join(root, "etc"), Query: "conf"})
	if len(sub.Hits) == 0 || sub.Hits[0].Rel != filepath.Join("nginx", "sites-available", "example.conf") {
		t.Fatalf("search below the root: %+v", sub.Hits)
	}
}

func TestNameIndexRefreshSeesAddedAndRemovedFiles(t *testing.T) {
	_, index, root := indexFixture(t)
	writeFile(t, filepath.Join(root, "old-report.csv"), "x", 0o644)
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "new-report.csv"), "x", 0o644)
	got, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "report"})
	if len(got.Hits) != 1 {
		t.Fatalf("before the refresh: %v", hitPaths(got))
	}
	// Removed since the build: dropped before it is returned.
	os.Remove(filepath.Join(root, "old-report.csv"))
	got, _, _ = index.Find(context.Background(), FindOptions{Root: root, Query: "report"})
	if len(got.Hits) != 0 || got.Total != 0 {
		t.Fatalf("a vanished file was returned: %v (total %d)", hitPaths(got), got.Total)
	}
	if !index.Refresh(nil) {
		t.Fatal("refresh did not start")
	}
	deadline := time.Now().Add(10 * time.Second)
	for index.building.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got, _, _ = index.Find(context.Background(), FindOptions{Root: root, Query: "report"})
	if fmt.Sprint(hitPaths(got)) != fmt.Sprint([]string{filepath.Join(root, "new-report.csv")}) {
		t.Fatalf("after the refresh: %v", hitPaths(got))
	}
	index.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if !index.Stale() {
		t.Fatal("an index two hours old is not stale")
	}
}

func TestNameIndexNeverAnswersOutsideTheRoots(t *testing.T) {
	_, index, root := indexFixture(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret-notes.txt"), "x", 0o644)
	writeFile(t, filepath.Join(root, "app", "secret-notes.txt"), "x", 0o644)
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	// An index line naming a folder outside the roots, as one built under a
	// wider JD_FILE_ROOTS would hold.
	f, err := os.OpenFile(filepath.Join(index.dir, indexFileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "\x01%s\nfsecret-notes.txt\n", outside)
	f.Close()
	got, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "secret"})
	if fmt.Sprint(hitPaths(got)) != fmt.Sprint([]string{filepath.Join(root, "app", "secret-notes.txt")}) {
		t.Fatalf("hits: %v", hitPaths(got))
	}
	if _, _, err := index.Find(context.Background(), FindOptions{Root: outside, Query: "secret"}); err == nil {
		t.Fatal("a search rooted outside the roots was answered")
	}
	// A folder replaced since the build by a link out of the roots: what the
	// index lists under it is not returned.
	os.RemoveAll(filepath.Join(root, "app"))
	if err := os.Symlink(outside, filepath.Join(root, "app")); err != nil {
		t.Fatal(err)
	}
	got, _, _ = index.Find(context.Background(), FindOptions{Root: root, Query: "secret"})
	if len(got.Hits) != 0 {
		t.Fatalf("a hit through a link out of the roots: %v", hitPaths(got))
	}
}

func TestNameIndexSkipsWhatIsNotARealTree(t *testing.T) {
	_, index, root := indexFixture(t)
	writeFile(t, filepath.Join(root, "kept", "needle-a.txt"), "x", 0o644)
	writeFile(t, filepath.Join(root, TrashFolderName, "0123456789abcdef0123456789abcdef", "needle-trash.txt"), "x", 0o644)
	writeFile(t, filepath.Join(root, "app", "node_modules", "pkg", "needle-dep.js"), "x", 0o644)
	writeFile(t, filepath.Join(root, "share", "needle-nfs.txt"), "x", 0o644)
	writeFile(t, filepath.Join(root, "kernel", "needle-proc.txt"), "x", 0o644)
	previous := readMountTable
	readMountTable = func() ([]mountPoint, error) {
		return []mountPoint{
			{ID: 1, Path: "/", FSType: "ext4"},
			{ID: 2, Path: filepath.Join(root, "share"), FSType: "nfs4"},
			{ID: 3, Path: filepath.Join(root, "kernel"), FSType: "proc"},
		}, nil
	}
	t.Cleanup(func() { readMountTable = previous })
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "needle", Hidden: true})
	if fmt.Sprint(hitPaths(got)) != fmt.Sprint([]string{filepath.Join(root, "kept", "needle-a.txt")}) {
		t.Fatalf("hits: %v", hitPaths(got))
	}
	for _, p := range []string{"/proc", "/sys", "/dev", "/run", "/var/lib/docker/overlay2"} {
		if !indexSkips(index.dir).paths[p] {
			t.Errorf("%s is walked", p)
		}
	}
	if !indexSkips(index.dir).paths[index.dir] {
		t.Error("the index walks its own folder")
	}
}

func TestMergedFindPrefersTheLiveWalkForTheFolderOnScreen(t *testing.T) {
	svc, index, root := indexFixture(t)
	writeFile(t, filepath.Join(root, "work", "draft-one.md"), "x", 0o644)
	writeFile(t, filepath.Join(root, "elsewhere", "draft-two.md"), "x", 0o644)
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "work", "draft-new.md"), "x", 0o644)
	indexed, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "draft"})
	live, _ := svc.Find(context.Background(), FindOptions{Root: filepath.Join(root, "work"), Query: "draft"})
	merged := MergeFind(RebaseFind(live, root, "draft"), indexed, 60)
	paths := strings.Join(hitPaths(merged), "\n")
	for _, want := range []string{"draft-one.md", "draft-two.md", "draft-new.md"} {
		if strings.Count(paths, want) != 1 {
			t.Fatalf("%s is not in the merged answer once:\n%s", want, paths)
		}
	}
	for _, h := range merged.Hits {
		if !strings.HasPrefix(h.Rel, "work") && !strings.HasPrefix(h.Rel, "elsewhere") {
			t.Fatalf("a hit not measured from the search's root: %+v", h)
		}
	}
}

// Half a million names on disk, found in well under a second, by a search
// whose memory does not grow with the index.
func TestNameIndexAnswersHalfAMillionNamesInBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("makes a tree of half a million files")
	}
	_, index, root := indexFixture(t)
	const dirs, perDir = 500, 1000
	var wg sync.WaitGroup
	work := make(chan int)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range work {
				dir := filepath.Join(root, fmt.Sprintf("project-%03d", d), "src")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Error(err)
					return
				}
				for i := 0; i < perDir; i++ {
					name := fmt.Sprintf("module-%04d.ts", i)
					if d == 417 && i == 512 {
						name = "invoice-renderer.ts"
					}
					f, err := os.Create(filepath.Join(dir, name))
					if err != nil {
						t.Error(err)
						return
					}
					f.Close()
				}
			}
		}()
	}
	for d := 0; d < dirs; d++ {
		work <- d
	}
	close(work)
	wg.Wait()
	built := time.Now()
	if err := index.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("built an index of %d names in %v", index.Info().Entries, time.Since(built))
	if index.Info().Entries < dirs*perDir {
		t.Fatalf("index holds %d names", index.Info().Entries)
	}
	size, _ := os.Stat(filepath.Join(index.dir, indexFileName))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	got, _, err := index.Find(context.Background(), FindOptions{Root: root, Query: "invoice rend"})
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].Name != "invoice-renderer.ts" {
		t.Fatalf("hits: %+v", got.Hits)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("searched %d lines (%d bytes) in %v, allocating %d bytes", got.Visited, size.Size(), elapsed, allocated)
	if elapsed > time.Second {
		t.Fatalf("a search of half a million names took %v", elapsed)
	}
	if allocated > 4<<20 || int64(allocated) > size.Size()/4 {
		t.Fatalf("a search allocated %d bytes over an index of %d: memory is not bounded", allocated, size.Size())
	}
	// A query that matches many keeps only its limit.
	runtime.ReadMemStats(&before)
	many, _, _ := index.Find(context.Background(), FindOptions{Root: root, Query: "module", Limit: 60})
	runtime.ReadMemStats(&after)
	if len(many.Hits) != 60 || many.Total < dirs*perDir-1 {
		t.Fatalf("many: %d hits of %d", len(many.Hits), many.Total)
	}
	t.Logf("a search matching %d names allocated %d bytes", many.Total, after.TotalAlloc-before.TotalAlloc)
}

func BenchmarkNameIndexFind(b *testing.B) {
	base := b.TempDir()
	root := filepath.Join(base, "root")
	for d := 0; d < 100; d++ {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", d))
		os.MkdirAll(dir, 0o755)
		for i := 0; i < 1000; i++ {
			f, _ := os.Create(filepath.Join(dir, fmt.Sprintf("file-%04d.go", i)))
			f.Close()
		}
	}
	index := NewNameIndex(New([]string{root}), filepath.Join(base, "data"), time.Hour)
	if err := index.Build(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := index.Find(context.Background(), FindOptions{Root: root, Query: "fl0999"}); err != nil {
			b.Fatal(err)
		}
	}
}
