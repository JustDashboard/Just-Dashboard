package files

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// NameIndex is Find's index of every name under the file roots, so a name
// typed in Find is found wherever it is in well under a second, however big
// the tree — where the walk find.go does stops on its budget long before it
// has seen a server's whole disk.
//
// It is a file in the data directory, never a structure in memory: one line
// per folder, followed by a line per entry in it, written by a walk at the
// lowest CPU and I/O priority and published by a rename, and read back as a
// stream for each search, keeping only the best few matches. Memory is bounded
// by the search's limit and the largest single folder, never by how many files
// the server has. It holds every path on the server, so it is root's alone
// (0700 folder, 0600 file).
//
// It answers for the tree and the walk still answers for the folder on
// screen, so a file made a moment ago is found at once. Every result from the
// index is checked to exist, and to be inside the roots, before it is
// returned; ranking is find.go's own scoring.
type NameIndex struct {
	svc    *Service
	dir    string
	maxAge time.Duration

	building atomic.Bool
	mu       sync.Mutex
	// lastErr is the last build's failure, for the log.
	lastErr error
	// now and mounts are the clock and the mount table, for tests.
	now func() time.Time
}

// IndexInfo is what the page is told about the index that answered: how old
// it is, whether its walk saw everything, and whether a new one is being made.
type IndexInfo struct {
	BuiltAt    time.Time `json:"builtAt,omitzero"`
	AgeSeconds int       `json:"ageSeconds"`
	Entries    int       `json:"entries"`
	Complete   bool      `json:"complete"`
	Building   bool      `json:"building"`
}

// indexMeta is written beside the index once it is published.
type indexMeta struct {
	Version   int       `json:"version"`
	BuiltAt   time.Time `json:"builtAt,omitzero"`
	Entries   int       `json:"entries"`
	Complete  bool      `json:"complete"`
	Roots     []string  `json:"roots"`
	ElapsedMS int64     `json:"elapsedMs"`
}

const (
	indexVersion  = 1
	indexFileName = "names.idx"
	indexMetaName = "names.json"
	// IndexFolderName is the index's folder in the data directory.
	IndexFolderName = "files-index"

	// indexMaxEntries and indexMaxWalk bound one build: past them the index
	// is published as incomplete and the page says its answers may be.
	indexMaxEntries = 50_000_000
	indexMaxWalk    = 2 * time.Hour
	indexMaxDepth   = 64
	// indexReadBatch is how many entries of one folder are read at a time,
	// so a folder of a million files is never held whole.
	indexReadBatch = 4096

	dirMarker = '\x01'
)

// NewNameIndex keeps its index under dataDir. An index older than maxAge is
// stale: a search answered from it starts a new build in the background.
func NewNameIndex(svc *Service, dataDir string, maxAge time.Duration) *NameIndex {
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	return &NameIndex{svc: svc, dir: filepath.Join(dataDir, IndexFolderName), maxAge: maxAge, now: time.Now}
}

func (x *NameIndex) on(fn func(s *Service)) {
	if x.svc.run != nil {
		x.svc.run(func() { fn(x.svc.inPlace()) })
		return
	}
	fn(x.svc)
}

// Info describes the published index, or nil when there is none yet.
func (x *NameIndex) Info() *IndexInfo {
	var meta *indexMeta
	x.on(func(*Service) { meta = x.readMeta() })
	if meta == nil {
		return nil
	}
	return x.info(meta)
}

func (x *NameIndex) info(meta *indexMeta) *IndexInfo {
	age := x.now().Sub(meta.BuiltAt)
	if age < 0 {
		age = 0
	}
	return &IndexInfo{
		BuiltAt: meta.BuiltAt.UTC(), AgeSeconds: int(age.Seconds()), Entries: meta.Entries,
		Complete: meta.Complete, Building: x.building.Load(),
	}
}

func (x *NameIndex) readMeta() *indexMeta {
	raw, err := os.ReadFile(filepath.Join(x.dir, indexMetaName))
	if err != nil {
		return nil
	}
	var meta indexMeta
	if json.Unmarshal(raw, &meta) != nil || meta.Version != indexVersion {
		return nil
	}
	if _, err := os.Stat(filepath.Join(x.dir, indexFileName)); err != nil {
		return nil
	}
	return &meta
}

// Stale reports whether the index is missing or older than it should be.
func (x *NameIndex) Stale() bool {
	info := x.Info()
	return info == nil || time.Duration(info.AgeSeconds)*time.Second > x.maxAge
}

// Refresh starts a build in the background unless one is running. It returns
// at once; the build is published when it finishes.
func (x *NameIndex) Refresh(log func(err error)) bool {
	if !x.building.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer x.building.Store(false)
		err := x.Build(context.Background())
		x.mu.Lock()
		x.lastErr = err
		x.mu.Unlock()
		if err != nil && log != nil {
			log(err)
		}
	}()
	return true
}

// Build walks the roots and publishes a new index. It runs on a thread of its
// own at the lowest CPU and I/O priority, which ends with it.
func (x *NameIndex) Build(ctx context.Context) error {
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The thread stays locked to the goroutine and so dies with it: the
		// priorities set on it below never reach a thread anything else uses.
		runtime.LockOSThread()
		x.on(func(s *Service) {
			lowerPriority()
			err = x.build(ctx, s)
		})
	}()
	<-done
	return err
}

// lowerPriority makes the calling thread the last to get the CPU and the disk.
func lowerPriority() {
	tid := unix.Gettid()
	_ = unix.Setpriority(unix.PRIO_PROCESS, tid, 19)
	// IOPRIO_WHO_PROCESS with a thread id names that thread; the idle class
	// gets the disk only when nothing else wants it.
	const ioprioClassIdle, ioprioClassShift, ioprioWhoProcess = 3, 13, 1
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioClassIdle<<ioprioClassShift)
}

func (x *NameIndex) build(ctx context.Context, s *Service) error {
	started := x.now()
	if err := os.MkdirAll(x.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(x.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(x.dir, ".names-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriterSize(tmp, 1<<20)
	walker := &indexWalker{
		ctx: ctx, w: w, deadline: time.Now().Add(indexMaxWalk), skip: indexSkips(x.dir),
	}
	walker.complete = true
	for _, root := range nestedRootsRemoved(s.roots) {
		if err := walker.walk(root); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(x.dir, indexFileName)); err != nil {
		return err
	}
	meta := indexMeta{
		Version: indexVersion, BuiltAt: started.UTC(), Entries: walker.entries, Complete: walker.complete,
		Roots: s.roots, ElapsedMS: time.Since(started).Milliseconds(),
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaTmp := filepath.Join(x.dir, ".names-meta")
	if err := os.WriteFile(metaTmp, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(metaTmp, filepath.Join(x.dir, indexMetaName))
}

// nestedRootsRemoved drops a root that sits inside another, which the walk of
// the outer one already covers.
func nestedRootsRemoved(roots []string) []string {
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	var out []string
	for _, root := range sorted {
		covered := false
		for _, kept := range out {
			if pathWithin(root, kept) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, root)
		}
	}
	return out
}

// indexSkip is what the walk leaves out: paths, and mount points whose
// filesystems are not trees of files.
type indexSkip struct {
	paths  map[string]bool
	mounts map[string]string
}

// indexSkippedPaths are not real trees: the kernel's views of itself, the
// runtime state under /run, and the layers Docker and containerd unpack
// images into, which hold a copy of every image's filesystem and would bury
// every real result under thousands of duplicates.
var indexSkippedPaths = []string{
	"/proc", "/sys", "/dev", "/run", "/var/run",
	"/var/lib/docker/overlay2", "/var/lib/docker/image", "/var/lib/docker/containers",
	"/var/lib/docker/buildkit", "/var/lib/docker/aufs", "/var/lib/docker/btrfs",
	"/var/lib/docker/zfs", "/var/lib/docker/vfs", "/var/lib/docker/fuse-overlayfs",
	"/var/lib/containerd", "/var/snap/docker/common/var-lib-docker/overlay2",
}

// indexSkippedStores are the dashboard's own stores beside the index in the
// data directory: kept contents named by their hash (file-versions), small
// copies of pictures (thumbnails) and the records of uploads in progress
// (file-uploads). None is a file anybody is looking for, and the first two
// would bury a search for a few hex digits under thousands of names.
var indexSkippedStores = []string{"file-versions", "thumbnails", "file-uploads"}

func indexSkips(indexDir string) indexSkip {
	skip := indexSkip{paths: map[string]bool{}, mounts: map[string]string{}}
	for _, p := range indexSkippedPaths {
		skip.paths[p] = true
	}
	skip.paths[indexDir] = true
	for _, store := range indexSkippedStores {
		skip.paths[filepath.Join(filepath.Dir(indexDir), store)] = true
	}
	mounts, _ := readMountTable()
	for _, m := range mounts {
		if pseudoFS[m.FSType] || networkFS[m.FSType] || m.FSType == "tmpfs" && strings.HasPrefix(m.Path, "/run") {
			skip.mounts[m.Path] = m.FSType
		}
	}
	return skip
}

type indexWalker struct {
	ctx      context.Context
	w        *bufio.Writer
	deadline time.Time
	skip     indexSkip
	entries  int
	complete bool
	stopped  bool
}

// walk writes root and everything under it, depth first, each folder's line
// before its entries. Links are recorded as links and never followed.
func (iw *indexWalker) walk(root string) error {
	type pending struct {
		path  string
		depth int
	}
	stack := []pending{{path: root}}
	for len(stack) > 0 && !iw.stopped {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if iw.skipped(dir.path) {
			continue
		}
		f, err := os.Open(dir.path)
		if err != nil {
			continue
		}
		if err := iw.writeDir(dir.path); err != nil {
			f.Close()
			return err
		}
		for {
			batch, err := f.ReadDir(indexReadBatch)
			for _, d := range batch {
				name := d.Name()
				if strings.ContainsAny(name, "\n\x01") {
					// Names that cannot be one line are left to the walk.
					continue
				}
				if IsTrashFolderName(name) || IsStagingName(name) {
					// A transfer still arriving has no name of its own yet,
					// as the listing, find and search already hold.
					continue
				}
				kind := byte('f')
				switch {
				case d.Type()&fs.ModeSymlink != 0:
					kind = 'l'
				case d.IsDir():
					kind = 'd'
				}
				if err := iw.writeEntry(kind, name); err != nil {
					f.Close()
					return err
				}
				if kind == 'd' && !skipDirs[name] && dir.depth+1 < indexMaxDepth {
					stack = append(stack, pending{path: filepath.Join(dir.path, name), depth: dir.depth + 1})
				}
			}
			if iw.entries >= indexMaxEntries || iw.entries%8192 == 0 && iw.overdue() {
				iw.complete, iw.stopped = false, true
				break
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
	return nil
}

func (iw *indexWalker) overdue() bool {
	return iw.ctx.Err() != nil || time.Now().After(iw.deadline)
}

func (iw *indexWalker) skipped(path string) bool {
	if iw.skip.paths[path] {
		return true
	}
	_, mounted := iw.skip.mounts[path]
	return mounted
}

func (iw *indexWalker) writeDir(path string) error {
	if err := iw.w.WriteByte(dirMarker); err != nil {
		return err
	}
	if _, err := iw.w.WriteString(path); err != nil {
		return err
	}
	return iw.w.WriteByte('\n')
}

func (iw *indexWalker) writeEntry(kind byte, name string) error {
	iw.entries++
	if err := iw.w.WriteByte(kind); err != nil {
		return err
	}
	if _, err := iw.w.WriteString(name); err != nil {
		return err
	}
	return iw.w.WriteByte('\n')
}

// ErrNoIndex is a search asked of an index that has not been built yet.
var ErrNoIndex = errors.New("the name index has not been built yet")

// Find answers opts from the index: every name under opts.Root, scored by
// find.go's own rules, the best opts.Limit kept as the file streams past.
// Each result is checked to still exist and to resolve inside the roots.
func (x *NameIndex) Find(ctx context.Context, opts FindOptions) (result *FindResult, info *IndexInfo, err error) {
	x.on(func(s *Service) { result, info, err = x.find(ctx, s, opts) })
	return result, info, err
}

func (x *NameIndex) find(ctx context.Context, s *Service, opts FindOptions) (*FindResult, *IndexInfo, error) {
	meta := x.readMeta()
	if meta == nil {
		return nil, nil, ErrNoIndex
	}
	root, err := s.Resolve(opts.Root)
	if err != nil {
		return nil, nil, err
	}
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = findDefaultLimit
	}
	f, err := os.Open(filepath.Join(x.dir, indexFileName))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	started := time.Now()
	terms := compileTerms(opts.Query)
	result := &FindResult{Root: root, Hits: []FindHit{}}
	if len(terms) == 0 {
		return result, x.info(meta), nil
	}
	lowerTerms := make([][]byte, len(terms))
	for i, t := range terms {
		lowerTerms[i] = []byte(t.text)
	}
	// More are kept than asked for, so results that vanished since the build
	// can be dropped without leaving the answer short.
	keep := opts.Limit*2 + 20
	best := &hitHeap{}
	reader := bufio.NewReaderSize(f, 256<<10)
	var dir string
	dirIn := false
	var relDir string
	hiddenDir := false
	lower := make([]byte, 0, 512)
	scanned := 0
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A line longer than the buffer: a path no shell would type. Skip it.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}
			continue
		}
		if len(line) == 0 && err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, err
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(line) == 0 {
			continue
		}
		scanned++
		if scanned%65536 == 0 && ctx.Err() != nil {
			result.Truncated = true
			break
		}
		if line[0] == dirMarker {
			dir = string(line[1:])
			dirIn = pathWithin(dir, root)
			if dirIn {
				relDir, _ = filepath.Rel(root, dir)
				if relDir == "." {
					relDir = ""
				}
				hiddenDir = !opts.Hidden && hasHiddenPart(relDir)
			}
			continue
		}
		if !dirIn || hiddenDir {
			continue
		}
		kind, name := line[0], line[1:]
		if !opts.Hidden && len(name) > 0 && name[0] == '.' {
			continue
		}
		// A cheap test before the scoring: every term's letters must appear
		// in order somewhere in the folder and name, lower-cased.
		lower = appendLower(lower[:0], relDir)
		lower = append(lower, '/')
		lower = appendLowerBytes(lower, name)
		if !allSubsequences(lowerTerms, lower) {
			continue
		}
		nameStr := string(name)
		rel := nameStr
		if relDir != "" {
			rel = relDir + string(filepath.Separator) + nameStr
		}
		score, matches, ok := scoreCandidate(terms, nameStr, rel)
		if !ok {
			continue
		}
		isDir := kind == 'd'
		if isDir {
			score += 8
		}
		result.Total++
		hit := FindHit{
			Path: filepath.Join(dir, nameStr), Name: nameStr, Rel: rel, Dir: filepath.Dir(rel),
			IsDir: isDir, Score: score, Matches: matches,
		}
		if best.Len() < keep {
			heap.Push(best, hit)
		} else if better(hit, (*best)[0]) {
			(*best)[0] = hit
			heap.Fix(best, 0)
		}
		if err != nil {
			break
		}
	}
	hits := make([]FindHit, best.Len())
	for i := len(hits) - 1; i >= 0; i-- {
		hits[i] = heap.Pop(best).(FindHit)
	}
	for _, hit := range hits {
		if len(result.Hits) >= opts.Limit {
			break
		}
		// Gone since the build, moved out of the roots since, or reached
		// through a link that now leads out of them: not an answer.
		full, err := s.ResolveEntry(hit.Path)
		if err != nil || full != hit.Path {
			result.Total--
			continue
		}
		st, err := os.Lstat(hit.Path)
		if err != nil {
			result.Total--
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Stat(hit.Path); err == nil {
				hit.IsDir = target.IsDir()
			}
		} else {
			hit.IsDir = st.IsDir()
		}
		hit.Size, hit.Modified = st.Size(), st.ModTime().UTC()
		result.Hits = append(result.Hits, hit)
	}
	if result.Total < len(result.Hits) {
		result.Total = len(result.Hits)
	}
	result.Visited = scanned
	result.ElapsedMS = int(time.Since(started).Milliseconds())
	return result, x.info(meta), nil
}

func hasHiddenPart(rel string) bool {
	if rel == "" {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func appendLower(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

func appendLowerBytes(dst, s []byte) []byte {
	for _, c := range s {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

// allSubsequences is the pre-filter: each term's bytes in order in hay. A
// term with letters outside ASCII is passed to the scorer, which lower-cases
// by rune.
func allSubsequences(terms [][]byte, hay []byte) bool {
	for _, t := range terms {
		k := 0
		for i := 0; i < len(hay) && k < len(t); i++ {
			if t[k] >= 0x80 {
				k = len(t)
				break
			}
			if hay[i] == t[k] {
				k++
			}
		}
		if k < len(t) {
			return false
		}
	}
	return true
}

// better is find.go's order: higher score, then the shorter, then the earlier
// relative path.
func better(a, b FindHit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if len(a.Rel) != len(b.Rel) {
		return len(a.Rel) < len(b.Rel)
	}
	return a.Rel < b.Rel
}

// hitHeap keeps the worst kept hit on top, so a better one replaces it.
type hitHeap []FindHit

func (h hitHeap) Len() int           { return len(h) }
func (h hitHeap) Less(i, j int) bool { return better(h[j], h[i]) }
func (h hitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *hitHeap) Push(v any)        { *h = append(*h, v.(FindHit)) }
func (h *hitHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// MergeFind puts a live walk's answer and the index's together: the walk's
// hits first where both found a path (they are the newer reading), then
// ranked by find.go's order and cut to limit.
func MergeFind(live, indexed *FindResult, limit int) *FindResult {
	if limit <= 0 || limit > 500 {
		limit = findDefaultLimit
	}
	out := &FindResult{Root: indexed.Root, Hits: []FindHit{}}
	seen := map[string]bool{}
	var all []FindHit
	if live != nil {
		for _, h := range live.Hits {
			seen[h.Path] = true
			all = append(all, h)
		}
	}
	for _, h := range indexed.Hits {
		if !seen[h.Path] {
			all = append(all, h)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return better(all[i], all[j]) })
	if len(all) > limit {
		all = all[:limit]
	}
	out.Hits = all
	out.Total = indexed.Total
	if live != nil && live.Total > out.Total {
		out.Total = live.Total
	}
	if out.Total < len(out.Hits) {
		out.Total = len(out.Hits)
	}
	out.Visited = indexed.Visited
	if live != nil {
		out.Visited += live.Visited
	}
	out.ElapsedMS = indexed.ElapsedMS
	if live != nil && live.ElapsedMS > out.ElapsedMS {
		out.ElapsedMS = live.ElapsedMS
	}
	return out
}

// String is for logs.
func (i *IndexInfo) String() string {
	return fmt.Sprintf("%d names, %ds old, complete=%v", i.Entries, i.AgeSeconds, i.Complete)
}

// RebaseFind re-reads a walk's hits as if the walk had started at root:
// relative paths, and so scores, measured from root, and a hit outside it
// dropped. The folder on screen is walked live, the tree is answered from the
// index, and the two can only be ranked together measured from one place.
func RebaseFind(r *FindResult, root, query string) *FindResult {
	if r == nil {
		return nil
	}
	terms := compileTerms(query)
	out := *r
	out.Root = root
	out.Hits = []FindHit{}
	for _, hit := range r.Hits {
		if !pathWithin(hit.Path, root) || hit.Path == root {
			continue
		}
		rel, err := filepath.Rel(root, hit.Path)
		if err != nil {
			continue
		}
		score, matches, ok := scoreCandidate(terms, hit.Name, rel)
		if !ok {
			continue
		}
		if hit.IsDir {
			score += 8
		}
		hit.Rel, hit.Dir, hit.Score, hit.Matches = rel, filepath.Dir(rel), score, matches
		out.Hits = append(out.Hits, hit)
	}
	return &out
}
