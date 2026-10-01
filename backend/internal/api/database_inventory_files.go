package api

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

// Finding the databases that are files.
//
// This is the one collector that costs something: the others read a list the
// kernel or Docker already keeps, and this one walks directories. So it runs
// on its own slow cadence, apart from the poll, and it is bounded three ways —
// how deep it goes under each root, how many entries it looks at, and how long
// it takes — and says so when it stopped early. A partial answer that admits
// it is partial beats a complete one the page waited a minute for.
//
// Where it looks is chosen here, not by a client: the directories mounted into
// containers, where deployments and compose stacks live, Docker's volumes, and
// the handful of roots applications are installed under. Each still goes
// through files.Resolve, so narrowing JD_FILE_ROOTS narrows the scan with it —
// a database the dashboard may not open is not one it should go looking for.
//
// What it does to a file it finds is read its first sixteen bytes. The SQLite
// driver is never pointed at a file to learn whether it is a database: the
// driver takes locks and writes -wal and -shm files beside what it opens, and
// that is not a read of another application's data.

const (
	// dbFileScanFresh is how long a scan answers for before the next request
	// starts another behind it.
	dbFileScanFresh = 10 * time.Minute
	// dbFileScanBudget and dbFileScanVisits are where a scan stops whatever
	// is left.
	dbFileScanBudget = 12 * time.Second
	dbFileScanVisits = 400_000
	// dbFileScanFirstWait is how long the first request waits for the first
	// scan, so an ordinary server answers whole and a vast one answers now.
	dbFileScanFirstWait = 1500 * time.Millisecond

	scanDepthOwned  = 6
	scanDepthVolume = 7
	scanDepthRoot   = 5
	// One root may not spend the whole scan: a directory holding fifty
	// checkouts of a repository is a single root, and the volumes after it
	// are where the databases are.
	scanVisitsOwned = 60_000
	scanVisitsRoot  = 150_000

	// A container's own layer is asked of Docker, which takes it a second or
	// so per container to answer. These bound how many are asked, how many at
	// once, how long each and all of them may take, and how many files of one
	// container are looked into.
	maxEmbeddedContainers = 200
	maxEmbeddedFiles      = 24
	embeddedWorkers       = 3
	embeddedDiffTimeout   = 6 * time.Second
	embeddedBudget        = 20 * time.Second
)

type dbFileScanResult struct {
	files    []dbx.FileFacts
	dirs     []dbx.DataDirFacts
	embedded []dbx.EmbeddedFile
	scan     inventoryScan
}

type dbFileScanState struct {
	mu      sync.Mutex
	last    *dbFileScanResult
	running chan struct{}
	// layers is what each stopped container's own layer was last found to
	// hold, by container id. A container that is not running cannot change
	// its layer, so it is asked once.
	layers map[string][]dbx.EmbeddedFile
}

// fileScan returns the last scan's findings, starting a new scan behind them
// when they are stale. force waits for a fresh one; otherwise only the very
// first request waits, and only briefly.
func (s *Server) fileScan(ctx context.Context, force bool) (dbFileScanResult, bool) {
	state := &s.dbInventory.files
	state.mu.Lock()
	stale := state.last == nil || time.Since(state.last.scan.CheckedAt) > dbFileScanFresh
	if state.running == nil && (force || stale) {
		done := make(chan struct{})
		state.running = done
		go s.runFileScan(done)
	}
	running, first := state.running, state.last == nil
	state.mu.Unlock()

	if running != nil && (force || first) {
		wait := dbFileScanFirstWait
		if force {
			wait = embeddedBudget + 3*time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-running:
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.last == nil {
		return dbFileScanResult{scan: inventoryScan{
			Source: "files", OK: true, Running: true, CheckedAt: time.Now().UTC(),
			Reason: "the first scan for database files is still running",
		}}, true
	}
	out := *state.last
	out.scan.Running = state.running != nil
	return out, state.running != nil
}

// runFileScan walks once and keeps what it found. It runs on its own context:
// the request that started it may be long gone, and the budget is what ends it.
func (s *Server) runFileScan(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), embeddedBudget+2*time.Second)
	defer cancel()
	result := s.scanDatabaseFiles(ctx)

	state := &s.dbInventory.files
	state.mu.Lock()
	state.last = &result
	state.running = nil
	state.mu.Unlock()
	// What was built from the scan before this one is stale.
	s.dropInventory()
	close(done)
}

type scanRoot struct {
	path   string
	depth  int
	visits int
}

// scanDatabaseFiles chooses the roots and walks them, nearest owner first so a
// file under a container's volume is reached with depth to spare before the
// broad roots get to it.
func (s *Server) scanDatabaseFiles(ctx context.Context) dbFileScanResult {
	started := time.Now()
	result := dbFileScanResult{
		files: []dbx.FileFacts{}, dirs: []dbx.DataDirFacts{},
		scan: inventoryScan{Source: "files", CheckedAt: started.UTC()},
	}
	if s.modules.files == nil {
		result.scan.Reason = "file roots are not configured, so nothing can be scanned"
		return result
	}
	host := s.inventoryHost()
	_, containers, _ := s.collectContainers(ctx, false)
	// The containers' own layers are asked of Docker beside the walk, on a
	// budget of their own: neither is allowed to starve the other.
	type layerScan struct {
		found     []dbx.EmbeddedFile
		truncated bool
	}
	layers := make(chan layerScan, 1)
	go func() {
		found, truncated := s.scanContainerLayers(ctx, containers)
		layers <- layerScan{found, truncated}
	}()
	sc := &dbFileScanner{
		deadline: started.Add(dbFileScanBudget), ctx: ctx, storePath: s.dashboardStorePath(),
		seenDirs: map[string]bool{}, seenFiles: map[string]bool{},
	}
	// The dashboard's own store is listed under its own name whatever the
	// walk reaches it by, so the mark that keeps it from being connected
	// cannot be missed by a second spelling of its path.
	if info, err := os.Stat(sc.storePath); err == nil && info.Mode().IsRegular() {
		sc.file(sc.storePath, info)
	}
	walk := func(roots []scanRoot) {
		for _, root := range roots {
			if sc.over() {
				return
			}
			resolved, err := s.modules.files.Resolve(hostVisible(host.hostRoot, root.path))
			switch {
			case errors.Is(err, files.ErrOutsideRoot):
				sc.outside++
				continue
			case err != nil:
				sc.unreadable++
				continue
			}
			sc.root(resolved, root.depth, root.visits)
		}
	}

	// Nearest owner first. What containers mount and what they keep inside
	// themselves is where applications put their databases; the broad roots
	// come last and take whatever budget is left.
	owned := []scanRoot{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Source != "" && (m.Type == "volume" || ownedBindSource(m.Source)) {
				owned = append(owned, scanRoot{m.Source, scanDepthOwned, scanVisitsOwned})
			}
		}
	}
	walk(owned)

	projects := []scanRoot{}
	for _, place := range s.deploymentPlaces(ctx) {
		projects = append(projects, scanRoot{place.Path, scanDepthOwned, scanVisitsOwned})
	}
	if _, stacks, scan := s.collectDeclared(ctx); scan.OK {
		for _, stack := range stacks {
			// A compose file that was never brought up is a file in a
			// directory; the broad roots reach it like any other.
			if stack.WorkingDir != "" && stack.Containers > 0 {
				projects = append(projects, scanRoot{stack.WorkingDir, scanDepthOwned, scanVisitsOwned})
			}
		}
	}
	walk(projects)

	broad := []scanRoot{}
	if host.volumes != "" {
		broad = append(broad, scanRoot{host.volumes, scanDepthVolume, scanVisitsRoot})
	}
	for _, root := range host.roots {
		broad = append(broad, scanRoot{root, scanDepthRoot, scanVisitsRoot})
	}
	walk(broad)

	inside := <-layers
	result.embedded = inside.found
	sc.truncated = sc.truncated || inside.truncated
	result.files, result.dirs = sc.files, sc.dirs
	result.scan.OK = true
	result.scan.Truncated = sc.truncated
	result.scan.Count = len(sc.files) + len(sc.dirs) + len(result.embedded)
	result.scan.DurationMs = time.Since(started).Milliseconds()
	notes := []string{}
	if sc.truncated {
		notes = append(notes, "the scan stopped at its budget, so some directories were not reached")
	}
	if sc.unreadable > 0 {
		notes = append(notes, counted(sc.unreadable, "directory", "directories")+" could not be read")
	}
	if sc.outside > 0 {
		notes = append(notes, counted(sc.outside, "directory is", "directories are")+" outside the file roots and were not scanned")
	}
	result.scan.Reason = strings.Join(notes, "; ")
	return result
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// scanContainerLayers finds the database files applications keep inside their
// own containers, where no walk of the host can follow.
//
// A container's writable layer belongs to the storage driver, and where it is
// on disk is the driver's business — with the containerd image store Docker
// does not say at all. So Docker is asked instead: which files a container
// changed, and then the first bytes of the ones named like a database. Both
// answers come for a stopped container as readily as for a running one.
func (s *Server) scanContainerLayers(ctx context.Context, containers []dockerx.Container) ([]dbx.EmbeddedFile, bool) {
	if s.modules.docker == nil {
		return []dbx.EmbeddedFile{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, embeddedBudget)
	defer cancel()
	state := &s.dbInventory.files
	state.mu.Lock()
	kept := state.layers
	state.mu.Unlock()

	truncated := false
	if len(containers) > maxEmbeddedContainers {
		containers, truncated = containers[:maxEmbeddedContainers], true
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		next  = map[string][]dbx.EmbeddedFile{}
		slots = make(chan struct{}, embeddedWorkers)
	)
	for _, c := range containers {
		if cand, _ := dbx.Detect(c.Name, c.Image, nil, nil, nil); cand != nil {
			// A database server keeps its data in a volume, which the walk
			// has its own way to.
			continue
		}
		if found, ok := kept[c.ID]; ok && c.State != "running" {
			mu.Lock()
			next[c.ID] = found
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(c dockerx.Container) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				mu.Lock()
				truncated = true
				mu.Unlock()
				return
			}
			found, ok := s.containerLayerFiles(ctx, c)
			mu.Lock()
			defer mu.Unlock()
			if !ok {
				truncated = truncated || ctx.Err() != nil
				return
			}
			next[c.ID] = found
		}(c)
	}
	wg.Wait()

	state.mu.Lock()
	state.layers = next
	state.mu.Unlock()
	out := []dbx.EmbeddedFile{}
	for _, c := range containers {
		out = append(out, next[c.ID]...)
	}
	return out, truncated
}

// containerLayerFiles asks Docker what one container changed and reads the
// first bytes of what is named like a database. It reports false when Docker
// did not answer, so a container that could not be asked is asked again
// rather than remembered as holding nothing.
func (s *Server) containerLayerFiles(ctx context.Context, c dockerx.Container) ([]dbx.EmbeddedFile, bool) {
	diffCtx, cancel := context.WithTimeout(ctx, embeddedDiffTimeout)
	changed, err := s.modules.docker.ChangedFiles(diffCtx, c.ID)
	cancel()
	if err != nil {
		return nil, false
	}
	out := []dbx.EmbeddedFile{}
	looked := 0
	for _, path := range changed {
		if !dbx.FileCandidate(filepath.Base(path)) || mounted(path, c.Mounts) {
			continue
		}
		if looked++; looked > maxEmbeddedFiles || ctx.Err() != nil {
			break
		}
		head, size, modified, err := s.modules.docker.ContainerFileHead(ctx, c.ID, path, dbx.MagicLen)
		if err != nil {
			continue
		}
		if engine, ok := dbx.FileMagic(head); ok {
			out = append(out, dbx.EmbeddedFile{Container: c.Name, Path: path, Engine: engine, Size: size, Modified: modified})
		}
	}
	return out, true
}

// mounted reports a container path that is inside one of its mounts, which
// is a file on the host or in a volume rather than in the container's layer.
func mounted(path string, mounts []dockerx.MountPoint) bool {
	for _, m := range mounts {
		if m.Destination != "" && (path == m.Destination || strings.HasPrefix(path, strings.TrimSuffix(m.Destination, "/")+"/")) {
			return true
		}
	}
	return false
}

// ownedBindSource reports a bind mount worth scanning as an application's own
// directory. A mount of the root, of a system directory or of one of the broad
// roots is not somebody's data: the first two hold no application database,
// and the last is walked on its own, depth-capped.
func ownedBindSource(source string) bool {
	clean := filepath.Clean(source)
	if strings.Count(clean, "/") < 2 {
		return false
	}
	for _, system := range []string{"/proc", "/sys", "/dev", "/run", "/var/run", "/etc", "/usr", "/lib", "/bin", "/sbin", "/boot", "/tmp", "/var/log", "/var/lib/docker"} {
		if clean == system || strings.HasPrefix(clean, system+"/") {
			return false
		}
	}
	return true
}

// hostVisible names a host path as this process can open it. A containerised
// dashboard has a few of the host's directories mounted at their real names
// and the whole of it under hostRoot; a path that is not one of the former is
// only there under the latter.
func hostVisible(hostRoot, path string) string {
	if hostRoot == "" {
		return path
	}
	viaHost, err := os.Stat(filepath.Join(hostRoot, path))
	if err != nil {
		return path
	}
	if plain, err := os.Stat(path); err == nil && os.SameFile(plain, viaHost) {
		return path
	}
	return filepath.Join(hostRoot, path)
}

// scanSkipPaths are directories never walked into, as the host names them:
// container runtimes' own stores, which hold every image layer on the machine,
// and package managers' state.
var scanSkipPaths = map[string]bool{
	"/var/lib/docker": true, "/var/lib/containerd": true, "/var/lib/kubelet": true, "/var/lib/snapd": true,
	"/var/lib/lxcfs": true, "/var/lib/flatpak": true, "/var/lib/containers": true, "/var/lib/rancher": true,
	"/host": true, "/proc": true, "/sys": true, "/dev": true,
}

type dbFileScanner struct {
	ctx       context.Context
	deadline  time.Time
	storePath string
	visits    int
	// rootStop is the visit count at which the root being walked has had its
	// share; done says the whole scan is over.
	rootStop  int
	done      bool
	truncated bool
	// unreadable and outside count what was skipped, so the answer can say
	// what it does not cover.
	unreadable int
	outside    int
	seenDirs   map[string]bool
	seenFiles  map[string]bool
	files      []dbx.FileFacts
	dirs       []dbx.DataDirFacts
}

// over reports the whole scan spent: its visits, its time, or its caller.
func (sc *dbFileScanner) over() bool {
	if sc.done {
		return true
	}
	if sc.visits >= dbFileScanVisits || sc.ctx.Err() != nil || time.Now().After(sc.deadline) {
		sc.done, sc.truncated = true, true
	}
	return sc.done
}

// spent reports the scan over, or the root being walked out of its share.
func (sc *dbFileScanner) spent() bool {
	if sc.over() {
		return true
	}
	if sc.visits >= sc.rootStop {
		sc.truncated = true
		return true
	}
	return false
}

// root walks one chosen root. A root may be a single file: a bind mount of
// one database into a container is exactly that.
func (sc *dbFileScanner) root(path string, depth, visits int) {
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	sc.rootStop = sc.visits + visits
	switch {
	case info.IsDir():
		sc.walk(path, depth)
	case info.Mode().IsRegular() && dbx.FileCandidate(info.Name()):
		sc.file(path, info)
	}
}

// walk reads one directory and goes into the ones below it. Symbolic links are
// never followed, so everything reached is inside the root it was reached
// from.
func (sc *dbFileScanner) walk(dir string, depth int) {
	if sc.seenDirs[dir] || sc.spent() {
		return
	}
	sc.seenDirs[dir] = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		sc.unreadable++
		return
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if engine, versionFile, ok := dbx.DataDirMarker(names); ok {
		// An engine's own data directory: recorded as that, and not walked.
		// Nothing in it is a file database, and it can hold a great deal.
		facts := dbx.DataDirFacts{Path: dir, Engine: engine}
		if info, err := os.Stat(dir); err == nil {
			facts.Modified = info.ModTime().UTC()
		}
		if versionFile != "" {
			if raw, err := readBounded(filepath.Join(dir, versionFile), 32); err == nil {
				facts.Version = strings.TrimSpace(string(raw))
			}
		}
		sc.dirs = append(sc.dirs, facts)
		return
	}
	for _, entry := range entries {
		sc.visits++
		if sc.visits%256 == 0 && sc.spent() {
			return
		}
		name := entry.Name()
		child := filepath.Join(dir, name)
		switch {
		case entry.Type()&os.ModeSymlink != 0:
		case entry.IsDir():
			if depth > 0 && !dbx.SkipScanDir(name) && !scanSkipPaths[child] && !scanSkipPaths[dbx.HostView(child)] {
				sc.walk(child, depth-1)
			}
		case entry.Type().IsRegular() && dbx.FileCandidate(name):
			if info, err := entry.Info(); err == nil {
				sc.file(child, info)
			}
		}
	}
}

// file reads the first bytes of a candidate and records it when they are a
// database's. It opens read-only, refuses to follow a link that replaced the
// file since the directory was read, and never blocks on something that is
// not a regular file after all.
func (sc *dbFileScanner) file(path string, info os.FileInfo) {
	if sc.seenFiles[path] {
		return
	}
	if path != sc.storePath && dbx.HostView(path) == sc.storePath {
		// The store, reached a second time under the host's root.
		return
	}
	sc.seenFiles[path] = true
	engine, ok := fileEngine(path)
	if !ok {
		return
	}
	facts := dbx.FileFacts{Path: path, Engine: engine, Size: info.Size(), Modified: info.ModTime().UTC()}
	if _, err := os.Lstat(path + "-wal"); err == nil {
		facts.WAL = true
	}
	sc.files = append(sc.files, facts)
}

// fileEngine says which database format a file starts with.
func fileEngine(path string) (string, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	header := make([]byte, dbx.MagicLen)
	n, err := io.ReadFull(f, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", false
	}
	return dbx.FileMagic(header[:n])
}
