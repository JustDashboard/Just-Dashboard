package deploy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lockfiles are read here as data and compared with package.json, so that
// detection, preflight and the recipe all know which committed lockfile a
// frozen install would accept. Nothing in a checkout is executed: npm's lock
// is streamed as JSON, Bun's text lock is JSONC, pnpm's is YAML up to its
// package list, Yarn's is read line by line, and Bun's binary lock is only
// searched for dependency names. Each reader follows the manager's own frozen
// check — npm ci and Bun accept a changed range the locked version still
// satisfies, pnpm and Yarn compare the range text — so "stale" always means
// the frozen install would refuse.

const (
	// nodeLockfileMaxBytes bounds one lockfile; a larger one is reported as
	// not compared rather than read in part.
	nodeLockfileMaxBytes = 16 << 20
	// nodeReadBudgetBytes is what one detection or one build preparation may
	// read for Node installs, apart from detection's own limits: comparing
	// lockfiles is the install's evidence, never a reason to call a scan
	// truncated.
	nodeReadBudgetBytes = 32 << 20
	nodeListedNames     = 16
	nodeMaxImporters    = 64
	nodeConfigMaxBytes  = 64 << 10
	nodeManifestMax     = 512 << 10
)

const (
	LockfileInSync  = "in_sync"
	LockfileStale   = "stale"
	LockfileUnknown = "unknown"
)

// nodeReadBudget bounds what one detection or one build preparation reads
// for Node installs, and remembers what it has parsed: every workspace
// member is compared against its workspace root's lockfile and siblings'
// manifests, which are read and parsed once rather than once per member.
type nodeReadBudget struct {
	remaining int64
	locks     map[string]nodeLockfileComparer
	manifests map[string]nodeManifestReading
}

// nodeLockfileComparer compares a parsed lockfile with the package at own,
// a path under the lockfile's directory.
type nodeLockfileComparer func(own string) nodeLockfileReading

type nodeManifestReading struct {
	specs nodeDependencySpecs
	ok    bool
}

func newNodeReadBudget() *nodeReadBudget { return &nodeReadBudget{remaining: nodeReadBudgetBytes} }

func (b *nodeReadBudget) take(size int64) bool {
	if b == nil || size > b.remaining {
		return false
	}
	b.remaining -= size
	return true
}

var (
	errNodeFileTooLarge = errors.New("file exceeds its read limit")
	errNodeBudget       = errors.New("the lockfile read budget is spent")
)

// nodeFiles reads one directory of a checkout as bounded data: regular files
// only, never through a symlink, never outside the checkout.
type nodeFiles struct {
	root   *os.Root
	dir    string
	budget *nodeReadBudget
}

func (f nodeFiles) name(rel string) string {
	return path.Join(f.dir, rel)
}

func (f nodeFiles) sub(rel string) nodeFiles {
	return nodeFiles{root: f.root, dir: path.Join(f.dir, rel), budget: f.budget}
}

func (f nodeFiles) stat(rel string) (fs.FileInfo, bool) {
	if f.root == nil || !safeRelativePath(rel) && rel != "." {
		return nil, false
	}
	info, err := f.root.Lstat(f.name(rel))
	if err != nil {
		return nil, false
	}
	return info, true
}

func (f nodeFiles) exists(rel string) bool {
	info, ok := f.stat(rel)
	return ok && info.Mode().IsRegular()
}

func (f nodeFiles) dirExists(rel string) bool {
	info, ok := f.stat(rel)
	return ok && info.IsDir()
}

func (f nodeFiles) open(rel string, limit int64) (io.ReadCloser, error) {
	info, ok := f.stat(rel)
	if !ok || !info.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	if info.Size() > limit {
		return nil, errNodeFileTooLarge
	}
	if !f.budget.take(info.Size()) {
		return nil, errNodeBudget
	}
	file, err := f.root.Open(f.name(rel))
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(file, limit), file}, nil
}

// openHead opens a file of which only the beginning may be parsed, and
// charges the budget for the bytes actually read.
func (f nodeFiles) openHead(rel string, limit int64) (io.ReadCloser, error) {
	info, ok := f.stat(rel)
	if !ok || !info.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	if info.Size() > limit {
		return nil, errNodeFileTooLarge
	}
	file, err := f.root.Open(f.name(rel))
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{nodeBudgetReader{io.LimitReader(file, limit), f.budget}, file}, nil
}

type nodeBudgetReader struct {
	reader io.Reader
	budget *nodeReadBudget
}

func (r nodeBudgetReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	if !r.budget.take(int64(count)) {
		return 0, errNodeBudget
	}
	return count, err
}

func (f nodeFiles) read(rel string, limit int64) ([]byte, error) {
	file, err := f.open(rel, limit)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// nodeLockfileNames are the lockfiles a frozen install can pin, with their
// manager, in the order a manager's own files are preferred: npm honours a
// shrinkwrap over package-lock.json, and Bun reads bun.lock over bun.lockb.
var nodeLockfileNames = []struct{ path, manager string }{
	{"bun.lock", "bun"}, {"bun.lockb", "bun"},
	{"npm-shrinkwrap.json", "npm"}, {"package-lock.json", "npm"},
	{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
}

func nodeLockfileManager(name string) string {
	for _, lock := range nodeLockfileNames {
		if lock.path == name {
			return lock.manager
		}
	}
	return ""
}

// nodeDependencySpecs are one package.json's dependency ranges by name.
type nodeDependencySpecs map[string]string

// nodeManifestSpecs reads the dependency kinds a manager records for a
// workspace. Peer dependencies are recorded by npm and Bun only.
func nodeManifestSpecs(content []byte, withPeers bool) (nodeDependencySpecs, bool) {
	var manifest struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if json.Unmarshal(content, &manifest) != nil {
		return nil, false
	}
	specs := nodeDependencySpecs{}
	kinds := []map[string]string{manifest.PeerDependencies, manifest.DevDependencies, manifest.OptionalDependencies, manifest.Dependencies}
	if !withPeers {
		kinds = kinds[1:]
	}
	// Later kinds win, so a name listed twice keeps its dependencies range,
	// which is what every manager installs.
	for _, kind := range kinds {
		for name, spec := range kind {
			specs[name] = spec
		}
	}
	return specs, true
}

type nodePeerConflict struct {
	Package, Peer, Range, Version string
}

// nodeOptionalBinary is a platform package an entry's optionalDependencies
// name for Linux on one architecture and one C library: musl or glibc.
type nodeOptionalBinary struct {
	Name, Version, Arch, Libc string
}

// nodeLockfileReading is what one lockfile says about the install.
type nodeLockfileReading struct {
	DetectedLockfile
	format  string
	version string
	// npm only: peers the lock leaves unsatisfied (it was written with
	// --legacy-peer-deps), platform binaries its optional dependencies name
	// but the lock never recorded, and registry hosts it resolves from.
	peers    []nodePeerConflict
	optional []nodeOptionalBinary
	hosts    []string
	// git lists packages the lock resolves from a Git repository, which the
	// install clones; gitSSH says one of them is fetched over SSH.
	git    []string
	gitSSH bool
	// names are the packages the lock installs, when the format lists them.
	names map[string]string
}

// nodeLockComparison accumulates one importer's differences. extrasAccepted
// is npm's rule: npm ci installs what package.json asks for from the lock
// and leaves out a locked dependency package.json no longer lists, where
// Bun, pnpm and Yarn refuse the lock.
type nodeLockComparison struct {
	missing, extra, changed []string
	missingTotal            int
	stale, unknown          bool
	extrasAccepted          bool
}

func (c *nodeLockComparison) add(list *[]string, name string) {
	if len(*list) < nodeListedNames {
		*list = append(*list, nodeListedName(name))
	}
}

// nodeListedName is a dependency name as a reading lists it: names are keys
// of a repository's JSON and YAML, so one is cut to the bound a saved draft
// validates and loses any line break that would make the draft malformed.
func nodeListedName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, name)
	if cut, truncated := truncateUTF8(name, 256); truncated {
		return cut + "..."
	}
	return name
}

// compare checks one importer's locked specs against its manifest. locked
// reports the version a name resolved to, for managers whose frozen install
// accepts a changed range the locked version still satisfies.
func (c *nodeLockComparison) compare(
	prefix string,
	manifest, lock nodeDependencySpecs,
	textual bool,
	locked func(name string) string,
) {
	names := make([]string, 0, len(manifest))
	for name := range manifest {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := manifest[name]
		recorded, ok := lock[name]
		switch {
		case !ok:
			c.missingTotal++
			c.add(&c.missing, prefix+name)
			c.stale = true
		case recorded == spec:
		case textual:
			c.add(&c.changed, prefix+name)
			c.stale = true
		default:
			satisfied, known := nodeRangeSatisfies(locked(name), spec)
			switch {
			case known && satisfied:
			case known:
				c.add(&c.changed, prefix+name)
				c.stale = true
			default:
				c.add(&c.changed, prefix+name)
				c.unknown = true
			}
		}
	}
	extras := []string{}
	for name := range lock {
		if _, ok := manifest[name]; !ok {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	for _, name := range extras {
		c.add(&c.extra, prefix+name)
		c.stale = c.stale || !c.extrasAccepted
	}
}

func (c *nodeLockComparison) reading(path, manager, format, version string) nodeLockfileReading {
	reading := nodeLockfileReading{
		DetectedLockfile: DetectedLockfile{
			Path: path, Manager: manager, State: LockfileInSync,
			Missing: c.missing, Extra: c.extra, Changed: c.changed,
		},
		format: format, version: version,
	}
	switch {
	case c.stale:
		reading.State = LockfileStale
		reading.Note = nodeStaleSentence(path, c.missingTotal, c.missing, len(c.extra), len(c.changed))
	case c.unknown:
		reading.State = LockfileUnknown
		reading.Note = path + " records ranges this reader cannot evaluate (" + nodeNameList(c.changed, len(c.changed)) + ")"
	case len(c.extra) > 0:
		reading.Note = path + " matches package.json; it still lists " + nodeCount(len(c.extra), "dependency", "dependencies") +
			" package.json no longer has (" + nodeNameList(c.extra, len(c.extra)) + "), which npm ci leaves out"
	default:
		reading.Note = path + " matches package.json"
	}
	reading.Note = boundedFindingText(reading.Note)
	return reading
}

// nodeStaleSentence names the drift the way an operator fixes it.
func nodeStaleSentence(path string, missingTotal int, missing []string, extra, changed int) string {
	parts := []string{}
	if missingTotal > 0 {
		parts = append(parts, fmt.Sprintf("is missing %s (%s)", nodeCount(missingTotal, "dependency", "dependencies"), nodeNameList(missing, missingTotal)))
	}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("does not satisfy %s package.json asks for now", nodeCount(changed, "range", "ranges")))
	}
	if extra > 0 {
		parts = append(parts, fmt.Sprintf("still lists %s package.json no longer has", nodeCount(extra, "dependency", "dependencies")))
	}
	return path + " " + strings.Join(parts, "; ")
}

func nodeCount(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

func nodeNameList(names []string, total int) string {
	shown := names
	if len(shown) > 3 {
		shown = shown[:3]
	}
	text := strings.Join(shown, ", ")
	if total > len(shown) {
		text += fmt.Sprintf(" and %d more", total-len(shown))
	}
	return text
}

func nodeUnreadLockfile(path, manager, note string) nodeLockfileReading {
	return nodeLockfileReading{DetectedLockfile: DetectedLockfile{Path: path, Manager: manager, State: LockfileUnknown, Note: note}}
}

// readNodeLockfile reads the lockfile at the root of files (a workspace's
// install root) and compares every workspace it lists with that workspace's
// package.json. importer is the package being deployed, relative to files;
// its differences are listed by name, a sibling's are prefixed with its path.
func readNodeLockfile(files nodeFiles, lockfile, importer string, arch string) nodeLockfileReading {
	budget, key := files.budget, arch+"\x00"+files.name(lockfile)
	if budget == nil {
		return parseNodeLockfile(files, lockfile, arch)(importer)
	}
	compare, ok := budget.locks[key]
	if !ok {
		compare = parseNodeLockfile(files, lockfile, arch)
		if budget.locks == nil {
			budget.locks = map[string]nodeLockfileComparer{}
		}
		budget.locks[key] = compare
	}
	return compare(importer)
}

func parseNodeLockfile(files nodeFiles, lockfile, arch string) nodeLockfileComparer {
	switch lockfile {
	case "package-lock.json", "npm-shrinkwrap.json":
		return readNPMLockfile(files, lockfile, arch)
	case "bun.lock":
		return readBunLockfile(files)
	case "bun.lockb":
		return readBunBinaryLockfile(files)
	case "pnpm-lock.yaml":
		return readPNPMLockfile(files)
	case "yarn.lock":
		return readYarnLockfile(files)
	}
	return nodeFixedReading(nodeUnreadLockfile(lockfile, nodeLockfileManager(lockfile), "not a recognised lockfile"))
}

// nodeFixedReading is a lockfile that reads the same for every package: one
// that could not be read.
func nodeFixedReading(reading nodeLockfileReading) nodeLockfileComparer {
	return func(string) nodeLockfileReading { return reading }
}

// withFacts gives a comparison's reading what its lockfile records whichever
// package is compared. The lists are shared, and never modified.
func (r nodeLockfileReading) withFacts(facts nodeLockfileReading) nodeLockfileReading {
	r.names, r.peers, r.optional, r.hosts, r.git, r.gitSSH = facts.names, facts.peers, facts.optional, facts.hosts, facts.git, facts.gitSSH
	return r
}

func nodeReadFailure(path, manager string, err error) nodeLockfileReading {
	switch {
	case errors.Is(err, errNodeFileTooLarge):
		return nodeUnreadLockfile(path, manager, fmt.Sprintf("%s is larger than %d MiB and was not compared", path, nodeLockfileMaxBytes>>20))
	case errors.Is(err, errNodeBudget):
		return nodeUnreadLockfile(path, manager, path+" was not compared: the lockfile read budget is spent")
	}
	return nodeUnreadLockfile(path, manager, path+" could not be read as data")
}

// importerManifest reads a workspace's package.json, once per detection.
func importerManifest(files nodeFiles, importer string, withPeers bool) (nodeDependencySpecs, bool) {
	name := "package.json"
	if importer != "" && importer != "." {
		if !safeRelativePath(importer) {
			return nil, false
		}
		name = path.Join(importer, "package.json")
	}
	budget, key := files.budget, strconv.FormatBool(withPeers)+"\x00"+files.name(name)
	if budget != nil {
		if cached, ok := budget.manifests[key]; ok {
			return cached.specs, cached.ok
		}
	}
	var reading nodeManifestReading
	if content, err := files.read(name, nodeManifestMax); err == nil {
		reading.specs, reading.ok = nodeManifestSpecs(content, withPeers)
	}
	if budget != nil {
		if budget.manifests == nil {
			budget.manifests = map[string]nodeManifestReading{}
		}
		budget.manifests[key] = reading
	}
	return reading.specs, reading.ok
}

func importerPrefix(importer, own string) string {
	if importer == own || (importer == "" && own == ".") || (importer == "." && own == "") {
		return ""
	}
	if importer == "" {
		importer = "."
	}
	return importer + ": "
}

// orderedImporters puts the deployed package's workspace first and caps the
// count, so its differences are the ones listed.
func orderedImporters(importers []string, own string) []string {
	sort.Strings(importers)
	for index, importer := range importers {
		if importer == own || (own == "" && importer == ".") {
			importers[0], importers[index] = importers[index], importers[0]
			break
		}
	}
	if len(importers) > nodeMaxImporters {
		importers = importers[:nodeMaxImporters]
	}
	return importers
}

type npmLockEntry struct {
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Link                 bool              `json:"link"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
}

// Platform packages name their C library: rollup-linux-x64-musl and
// sharp-linuxmusl-x64 for musl, rollup-linux-x64-gnu and
// watcher-linux-x64-glibc for glibc. A name that says neither is left alone.
var (
	nodeMuslBinaryRE  = regexp.MustCompile(`-linux(?:musl-(x64|arm64)|-(x64|arm64)-musl)$`)
	nodeGlibcBinaryRE = regexp.MustCompile(`-linux-(x64|arm64)-(?:gnu|glibc)$`)
)

func nodePlatformBinary(name, version string) (nodeOptionalBinary, bool) {
	if match := nodeMuslBinaryRE.FindStringSubmatch(name); match != nil {
		return nodeOptionalBinary{Name: name, Version: version, Arch: match[1] + match[2], Libc: "musl"}, true
	}
	if match := nodeGlibcBinaryRE.FindStringSubmatch(name); match != nil {
		return nodeOptionalBinary{Name: name, Version: version, Arch: match[1], Libc: "glibc"}, true
	}
	return nodeOptionalBinary{}, false
}

// readNPMLockfile streams package-lock.json (or npm-shrinkwrap.json) entry by
// entry: the workspace entries are compared with their manifests, and every
// entry contributes the versions, peers, platform binaries and registry
// hosts the other checks need.
func readNPMLockfile(files nodeFiles, lockfile, arch string) nodeLockfileComparer {
	file, err := files.open(lockfile, nodeLockfileMaxBytes)
	if err != nil {
		return nodeFixedReading(nodeReadFailure(lockfile, "npm", err))
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	version := ""
	workspaces := map[string]npmLockEntry{}
	versions := map[string]string{}
	v1 := map[string]string{}
	peers := []struct {
		path  string
		specs map[string]string
	}{}
	optional := []nodeOptionalBinary{}
	present := map[string]bool{}
	hosts := map[string]bool{}
	git := map[string]bool{}
	gitSSH := false
	fail := func() nodeLockfileComparer {
		return nodeFixedReading(nodeUnreadLockfile(lockfile, "npm", lockfile+" is not valid JSON"))
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return fail()
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fail()
		}
		key, _ := token.(string)
		switch key {
		case "lockfileVersion":
			var number json.Number
			if decoder.Decode(&number) != nil {
				return fail()
			}
			version = number.String()
		case "packages":
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return fail()
			}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return fail()
				}
				entryPath, _ := token.(string)
				var entry npmLockEntry
				if decoder.Decode(&entry) != nil {
					return fail()
				}
				if !strings.HasPrefix(entryPath, "node_modules/") && !strings.Contains(entryPath, "/node_modules/") {
					workspaces[entryPath] = entry
					continue
				}
				versions[entryPath] = entry.Version
				_, name, _ := strings.Cut(entryPath[strings.LastIndex(entryPath, "node_modules/"):], "node_modules/")
				present[name] = true
				if entry.Link {
					// A workspace package or a file: directory: its resolved
					// is a path in the checkout, and nothing is downloaded.
					continue
				}
				if host := npmResolvedHost(entry.Resolved); host != "" {
					hosts[host] = true
				}
				if cloned, ssh := nodeGitURL(entry.Resolved); cloned {
					git[name] = true
					gitSSH = gitSSH || ssh
				}
				if len(entry.PeerDependencies) > 0 {
					required := map[string]string{}
					for peer, spec := range entry.PeerDependencies {
						if !entry.PeerDependenciesMeta[peer].Optional {
							required[peer] = spec
						}
					}
					if len(required) > 0 {
						peers = append(peers, struct {
							path  string
							specs map[string]string
						}{entryPath, required})
					}
				}
				for dependency, spec := range entry.OptionalDependencies {
					if binary, ok := nodePlatformBinary(dependency, spec); ok {
						optional = append(optional, binary)
					}
				}
			}
			if _, err := decoder.Token(); err != nil {
				return fail()
			}
		case "dependencies":
			// Version 1 locks keep the tree here; later versions repeat it for
			// old npm clients, and the packages section already said it all.
			var tree map[string]struct {
				Version string `json:"version"`
			}
			if decoder.Decode(&tree) != nil {
				return fail()
			}
			for name, entry := range tree {
				v1[name] = entry.Version
				// A version 1 lock records a Git dependency's source as its
				// version, as a URL or a github: shorthand.
				if cloned, ssh := nodeGitURL(entry.Version); cloned {
					git[name] = true
					gitSSH = gitSSH || ssh
				}
			}
		default:
			var skipped json.RawMessage
			if decoder.Decode(&skipped) != nil {
				return fail()
			}
		}
	}
	if version == "" {
		return nodeFixedReading(nodeUnreadLockfile(lockfile, "npm", lockfile+" records no lockfileVersion"))
	}
	facts := nodeLockfileReading{git: sortedNames(git), gitSSH: gitSSH}
	if len(workspaces) == 0 {
		facts.names = v1
		return func(own string) nodeLockfileReading {
			// A version 1 lock records resolved versions, not the ranges
			// asked for, so a range is judged by whether its locked
			// version fits it.
			manifest, ok := importerManifest(files, own, false)
			if !ok {
				return nodeUnreadLockfile(lockfile, "npm", "package.json could not be read")
			}
			comparison := nodeLockComparison{extrasAccepted: true}
			comparison.compareV1(manifest, v1)
			return comparison.reading(lockfile, "npm", "npm", version).withFacts(facts)
		}
	}
	facts.names = map[string]string{}
	for entryPath, version := range versions {
		if strings.Count(entryPath, "node_modules/") == 1 && strings.HasPrefix(entryPath, "node_modules/") {
			facts.names[strings.TrimPrefix(entryPath, "node_modules/")] = version
		}
	}
	for _, entry := range peers {
		for peer, spec := range entry.specs {
			resolved := npmResolvePeer(versions, entry.path, peer)
			if resolved == "" {
				continue
			}
			if satisfied, known := nodeRangeSatisfies(resolved, spec); known && !satisfied {
				name := entry.path[strings.LastIndex(entry.path, "node_modules/")+len("node_modules/"):]
				facts.peers = append(facts.peers, nodePeerConflict{Package: name, Peer: peer, Range: spec, Version: resolved})
			}
		}
	}
	sort.Slice(facts.peers, func(i, j int) bool {
		if facts.peers[i].Package != facts.peers[j].Package {
			return facts.peers[i].Package < facts.peers[j].Package
		}
		return facts.peers[i].Peer < facts.peers[j].Peer
	})
	seen := map[string]bool{}
	for _, binary := range optional {
		if present[binary.Name] || seen[binary.Name] || (arch != "" && binary.Arch != arch) {
			continue
		}
		seen[binary.Name] = true
		facts.optional = append(facts.optional, binary)
	}
	sort.Slice(facts.optional, func(i, j int) bool { return facts.optional[i].Name < facts.optional[j].Name })
	for host := range hosts {
		facts.hosts = append(facts.hosts, host)
	}
	sort.Strings(facts.hosts)
	importers := make([]string, 0, len(workspaces))
	for importer := range workspaces {
		importers = append(importers, importer)
	}
	return func(own string) nodeLockfileReading {
		if own == "." {
			own = ""
		}
		comparison := nodeLockComparison{extrasAccepted: true}
		for _, importer := range orderedImporters(slices.Clone(importers), own) {
			entry := workspaces[importer]
			if entry.Link {
				continue
			}
			manifest, ok := importerManifest(files, importer, true)
			if !ok {
				comparison.unknown = true
				continue
			}
			lock := nodeDependencySpecs{}
			for _, kind := range []map[string]string{entry.PeerDependencies, entry.DevDependencies, entry.OptionalDependencies, entry.Dependencies} {
				for name, spec := range kind {
					lock[name] = spec
				}
			}
			base := ""
			if importer != "" {
				base = importer + "/"
			}
			comparison.compare(importerPrefix(importer, own), manifest, lock, false, func(name string) string {
				if version, ok := versions[base+"node_modules/"+name]; ok {
					return version
				}
				return versions["node_modules/"+name]
			})
		}
		return comparison.reading(lockfile, "npm", "npm", version).withFacts(facts)
	}
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compareV1 judges a version 1 lock, which records no ranges: a name it
// lacks is missing, and a locked version the range no longer allows is a
// change npm ci refuses.
func (c *nodeLockComparison) compareV1(manifest nodeDependencySpecs, locked map[string]string) {
	names := make([]string, 0, len(manifest))
	for name := range manifest {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		version, ok := locked[name]
		if !ok {
			c.missingTotal++
			c.add(&c.missing, name)
			c.stale = true
			continue
		}
		satisfied, known := nodeRangeSatisfies(version, manifest[name])
		switch {
		case known && !satisfied:
			c.add(&c.changed, name)
			c.stale = true
		case !known:
			c.unknown = true
		}
	}
}

// npmResolvePeer finds the version a package's peer resolves to the way
// Node's module resolution does: the nearest node_modules above it.
func npmResolvePeer(versions map[string]string, entryPath, peer string) string {
	current := entryPath
	for {
		if version, ok := versions[current+"/node_modules/"+peer]; ok {
			return version
		}
		index := strings.LastIndex(current, "/node_modules/")
		if index < 0 {
			return versions["node_modules/"+peer]
		}
		current = current[:index]
	}
}

// npmResolvedHost names a registry host a lock entry downloads from when it
// is one the build cannot be expected to reach: a loopback or private
// address, a single-label name, or an intranet suffix.
func npmResolvedHost(resolved string) string {
	if !strings.HasPrefix(resolved, "http://") && !strings.HasPrefix(resolved, "https://") {
		return ""
	}
	parsed, err := url.Parse(resolved)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return host
		}
		return ""
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return host
	}
	switch host[strings.LastIndex(host, ".")+1:] {
	case "local", "internal", "lan", "corp", "home", "intranet", "localdomain":
		return host
	}
	return ""
}

// readBunLockfile reads Bun's text lock: JSON with trailing commas, whose
// workspaces section records each package.json's ranges and whose packages
// section records what each name resolved to.
func readBunLockfile(files nodeFiles) nodeLockfileComparer {
	content, err := files.read("bun.lock", nodeLockfileMaxBytes)
	if err != nil {
		return nodeFixedReading(nodeReadFailure("bun.lock", "bun", err))
	}
	var lock struct {
		LockfileVersion json.Number `json:"lockfileVersion"`
		Workspaces      map[string]struct {
			Dependencies         map[string]string `json:"dependencies"`
			DevDependencies      map[string]string `json:"devDependencies"`
			OptionalDependencies map[string]string `json:"optionalDependencies"`
			PeerDependencies     map[string]string `json:"peerDependencies"`
		} `json:"workspaces"`
		Packages map[string]json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(denoJSONWithoutComments(content), &lock) != nil || lock.Workspaces == nil {
		return nodeFixedReading(nodeUnreadLockfile("bun.lock", "bun", "bun.lock is not a readable Bun text lockfile"))
	}
	resolved := func(key string) string {
		raw, ok := lock.Packages[key]
		if !ok {
			return ""
		}
		var entry []json.RawMessage
		var identity string
		if json.Unmarshal(raw, &entry) != nil || len(entry) == 0 || json.Unmarshal(entry[0], &identity) != nil {
			return ""
		}
		return identity[strings.LastIndex(identity, "@")+1:]
	}
	importers := make([]string, 0, len(lock.Workspaces))
	recorded := map[string]nodeDependencySpecs{}
	for importer, workspace := range lock.Workspaces {
		importers = append(importers, importer)
		specs := nodeDependencySpecs{}
		for _, kind := range []map[string]string{workspace.PeerDependencies, workspace.DevDependencies, workspace.OptionalDependencies, workspace.Dependencies} {
			for name, spec := range kind {
				specs[name] = spec
			}
		}
		recorded[importer] = specs
	}
	facts := nodeLockfileReading{names: map[string]string{}}
	for key := range lock.Packages {
		// Nested entries are keyed "<dependent>/<name>"; only hoisted ones
		// name what the application imports.
		if !strings.Contains(key, "/") || (strings.HasPrefix(key, "@") && strings.Count(key, "/") == 1) {
			facts.names[key] = resolved(key)
		}
	}
	version := lock.LockfileVersion.String()
	return func(own string) nodeLockfileReading {
		if own == "." {
			own = ""
		}
		comparison := nodeLockComparison{}
		for _, importer := range orderedImporters(slices.Clone(importers), own) {
			manifest, ok := importerManifest(files, importer, true)
			if !ok {
				comparison.unknown = true
				continue
			}
			comparison.compare(importerPrefix(importer, own), manifest, recorded[importer], false, func(name string) string { return facts.names[name] })
		}
		return comparison.reading("bun.lock", "bun", "bun", version).withFacts(facts)
	}
}

// readBunBinaryLockfile can only prove absence: bun.lockb stores every
// package name as raw bytes, so a dependency whose name is not in it is
// certainly not locked. Presence of every name is not proof of agreement.
func readBunBinaryLockfile(files nodeFiles) nodeLockfileComparer {
	content, err := files.read("bun.lockb", nodeLockfileMaxBytes)
	if err != nil {
		return nodeFixedReading(nodeReadFailure("bun.lockb", "bun", err))
	}
	return func(own string) nodeLockfileReading {
		manifest, ok := importerManifest(files, own, true)
		if !ok {
			return nodeUnreadLockfile("bun.lockb", "bun", "package.json could not be read")
		}
		comparison := nodeLockComparison{}
		names := make([]string, 0, len(manifest))
		for name := range manifest {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !bytes.Contains(content, []byte(name)) {
				comparison.missingTotal++
				comparison.add(&comparison.missing, name)
				comparison.stale = true
			}
		}
		if comparison.stale {
			return comparison.reading("bun.lockb", "bun", "bun-binary", "")
		}
		reading := nodeUnreadLockfile("bun.lockb", "bun", "bun.lockb is binary: every dependency name is in it, but its ranges cannot be compared")
		reading.format = "bun-binary"
		return reading
	}
}

// pnpmDependency is an importer entry: version 5 locks record the resolved
// version alone, later ones {specifier, version}.
type pnpmDependency struct {
	specifier, version string
}

func (d *pnpmDependency) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		d.version = node.Value
		return nil
	}
	var entry struct {
		Specifier string `yaml:"specifier"`
		Version   string `yaml:"version"`
	}
	if err := node.Decode(&entry); err != nil {
		return err
	}
	d.specifier, d.version = entry.Specifier, entry.Version
	return nil
}

type pnpmImporter struct {
	Specifiers           map[string]string         `yaml:"specifiers"`
	Dependencies         map[string]pnpmDependency `yaml:"dependencies"`
	DevDependencies      map[string]pnpmDependency `yaml:"devDependencies"`
	OptionalDependencies map[string]pnpmDependency `yaml:"optionalDependencies"`
}

func (i pnpmImporter) specs() nodeDependencySpecs {
	specs := nodeDependencySpecs{}
	for _, kind := range []map[string]pnpmDependency{i.DevDependencies, i.OptionalDependencies, i.Dependencies} {
		for name, dependency := range kind {
			specs[name] = dependency.specifier
			if dependency.specifier == "" {
				specs[name] = i.Specifiers[name]
			}
		}
	}
	return specs
}

// readPNPMLockfile parses pnpm-lock.yaml up to its package list, which is
// all the frozen check reads: pnpm refuses unless every importer's
// specifiers equal its package.json ranges exactly.
func readPNPMLockfile(files nodeFiles) nodeLockfileComparer {
	file, err := files.openHead("pnpm-lock.yaml", nodeLockfileMaxBytes)
	if err != nil {
		return nodeFixedReading(nodeReadFailure("pnpm-lock.yaml", "pnpm", err))
	}
	defer file.Close()
	var head bytes.Buffer
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "packages:" || line == "snapshots:" {
			break
		}
		head.WriteString(line)
		head.WriteByte('\n')
	}
	if err := scanner.Err(); errors.Is(err, errNodeBudget) {
		return nodeFixedReading(nodeReadFailure("pnpm-lock.yaml", "pnpm", err))
	}
	var lock struct {
		LockfileVersion yaml.Node               `yaml:"lockfileVersion"`
		Importers       map[string]pnpmImporter `yaml:"importers"`
		pnpmImporter    `yaml:",inline"`
	}
	if scanner.Err() != nil || yaml.Unmarshal(head.Bytes(), &lock) != nil || lock.LockfileVersion.Value == "" {
		return nodeFixedReading(nodeUnreadLockfile("pnpm-lock.yaml", "pnpm", "pnpm-lock.yaml is not a readable pnpm lockfile"))
	}
	version := lock.LockfileVersion.Value
	importers := lock.Importers
	if importers == nil {
		importers = map[string]pnpmImporter{".": lock.pnpmImporter}
	}
	names := make([]string, 0, len(importers))
	recorded := map[string]nodeDependencySpecs{}
	facts := nodeLockfileReading{names: map[string]string{}}
	for importer, entry := range importers {
		names = append(names, importer)
		recorded[importer] = entry.specs()
		for _, kind := range []map[string]pnpmDependency{entry.Dependencies, entry.DevDependencies, entry.OptionalDependencies} {
			for name, dependency := range kind {
				facts.names[name] = dependency.version
			}
		}
	}
	return func(own string) nodeLockfileReading {
		if own == "" {
			own = "."
		}
		comparison := nodeLockComparison{}
		for _, importer := range orderedImporters(slices.Clone(names), own) {
			manifest, ok := importerManifest(files, importer, false)
			if !ok {
				comparison.unknown = true
				continue
			}
			comparison.compare(importerPrefix(importer, own), manifest, recorded[importer], true, nil)
		}
		return comparison.reading("pnpm-lock.yaml", "pnpm", "pnpm", version).withFacts(facts)
	}
}

// yarnDescriptor splits "name@range", where a scoped name has an @ of its
// own and an alias's range names another package.
func yarnDescriptor(descriptor string) (string, string, bool) {
	start := 0
	if strings.HasPrefix(descriptor, "@") {
		start = 1
	}
	at := strings.Index(descriptor[start:], "@")
	if at < 0 {
		return "", "", false
	}
	return descriptor[:start+at], descriptor[start+at+1:], true
}

func yarnHeaderDescriptors(line string) []string {
	line = strings.TrimSuffix(line, ":")
	descriptors := []string{}
	for _, part := range strings.Split(line, ",") {
		part = strings.Trim(strings.TrimSpace(part), `"`)
		if part != "" {
			descriptors = append(descriptors, part)
		}
	}
	return descriptors
}

// yarnBerryRange is how Berry writes a package.json range in its lock:
// a bare npm range gains the npm: protocol, anything with a protocol stays.
var yarnProtocolRE = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

func yarnBerryRange(spec string) string {
	if yarnProtocolRE.MatchString(spec) {
		return spec
	}
	return "npm:" + spec
}

// readYarnLockfile reads the headers of a classic lock, or a Berry lock's
// headers and workspace blocks. Classic Yarn's --frozen-lockfile refuses when
// a package.json range has no entry; Berry's --immutable when a workspace's
// recorded dependencies differ from its manifest.
func readYarnLockfile(files nodeFiles) nodeLockfileComparer {
	file, err := files.open("yarn.lock", nodeLockfileMaxBytes)
	if err != nil {
		return nodeFixedReading(nodeReadFailure("yarn.lock", "yarn", err))
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	descriptors := map[string]bool{}
	byName := map[string]bool{}
	workspaces := map[string]nodeDependencySpecs{}
	berry, classic := false, false
	metadata := ""
	current, section := "", ""
	inMetadata := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#"):
			if strings.Contains(trimmed, "yarn lockfile v1") {
				classic = true
			}
			continue
		case line == "__metadata:":
			berry, inMetadata, current = true, true, ""
			continue
		case !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":"):
			inMetadata, section, current = false, "", ""
			for _, descriptor := range yarnHeaderDescriptors(line) {
				descriptors[descriptor] = true
				name, spec, ok := yarnDescriptor(descriptor)
				if !ok {
					continue
				}
				byName[name] = true
				if strings.HasPrefix(spec, "workspace:") {
					current = strings.TrimPrefix(spec, "workspace:")
					workspaces[current] = nodeDependencySpecs{}
				}
			}
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if inMetadata && indent == 2 && strings.HasPrefix(trimmed, "version:") {
			metadata = strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
			continue
		}
		if current == "" {
			continue
		}
		if indent == 2 {
			section = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if indent == 4 && section == "dependencies" {
			key, value, found := strings.Cut(trimmed, ": ")
			if found {
				workspaces[current][strings.Trim(key, `"`)] = strings.Trim(value, `"`)
			}
		}
	}
	if scanner.Err() != nil {
		return nodeFixedReading(nodeUnreadLockfile("yarn.lock", "yarn", "yarn.lock could not be read"))
	}
	if berry {
		importers := make([]string, 0, len(workspaces))
		for importer := range workspaces {
			importers = append(importers, importer)
		}
		if len(importers) == 0 {
			return nodeFixedReading(nodeUnreadLockfile("yarn.lock", "yarn", "yarn.lock records no workspace"))
		}
		if _, err := strconv.Atoi(metadata); err != nil {
			metadata = ""
		}
		return func(own string) nodeLockfileReading {
			if own == "" {
				own = "."
			}
			comparison := nodeLockComparison{}
			for _, importer := range orderedImporters(slices.Clone(importers), own) {
				manifest, ok := importerManifest(files, importer, false)
				if !ok {
					comparison.unknown = true
					continue
				}
				normalized := nodeDependencySpecs{}
				for name, spec := range manifest {
					normalized[name] = yarnBerryRange(spec)
				}
				comparison.compare(importerPrefix(importer, own), normalized, workspaces[importer], true, nil)
			}
			return comparison.reading("yarn.lock", "yarn", "yarn-berry", metadata)
		}
	}
	if !classic && len(descriptors) == 0 {
		return nodeFixedReading(nodeUnreadLockfile("yarn.lock", "yarn", "yarn.lock is empty"))
	}
	return func(own string) nodeLockfileReading {
		comparison := nodeLockComparison{}
		for _, importer := range []string{own, ""} {
			manifest, ok := importerManifest(files, importer, false)
			if !ok {
				comparison.unknown = true
				continue
			}
			names := make([]string, 0, len(manifest))
			for name := range manifest {
				names = append(names, name)
			}
			sort.Strings(names)
			prefix := importerPrefix(importer, own)
			for _, name := range names {
				switch {
				case descriptors[name+"@"+manifest[name]]:
				case byName[name]:
					comparison.add(&comparison.changed, prefix+name)
					comparison.stale = true
				case strings.HasPrefix(manifest[name], "workspace:") || strings.HasPrefix(manifest[name], "file:") || strings.HasPrefix(manifest[name], "link:"):
					// Classic Yarn links local packages without a lock entry.
				default:
					comparison.missingTotal++
					comparison.add(&comparison.missing, prefix+name)
					comparison.stale = true
				}
			}
			if own == "" || own == "." {
				break
			}
		}
		return comparison.reading("yarn.lock", "yarn", "yarn-classic", "1")
	}
}
