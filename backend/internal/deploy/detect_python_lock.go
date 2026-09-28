package deploy

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The uv, Poetry and PDM locks are TOML their tools generate, one package per
// [[package]] table. What detection needs from them is small — each
// package's name, version and source, the dependencies it pulls in and from
// which group, and the file names of its wheels — so they are read line by
// line rather than parsed, and a lock too large for the walk's byte budget is
// streamed once and kept in that reduced form (compactPythonLock).

// pythonLock is one lock as detection reads it.
type pythonLock struct {
	file string
	// unreadable says the lock was present but not read; its packages are
	// then unknown, which is never the same as empty.
	unreadable bool
	packages   []*pythonLockedPackage
	byName     map[string]*pythonLockedPackage
	// root is the uv project itself (source virtual or editable ".").
	root           *pythonLockedPackage
	requiresPython string
	// members are a uv workspace's members, by name.
	members []string
}

// pythonLockEdge is one dependency as a lock records it: the package (and
// the version, where uv pins one of several), the extras it asks for, and
// the environment marker it is installed under.
type pythonLockEdge struct {
	name, version string
	extras        []string
	marker        string
}

type pythonLockedPackage struct {
	name, version string
	// source is registry, git, path, editable, virtual, directory or url;
	// sourceRef the URL or path it names.
	source, sourceRef string
	dependencies      []string
	edges             []pythonLockEdge
	extras            map[string][]pythonLockEdge
	dev               map[string][]string
	requiresDist      []pythonRequirement
	requiresDev       map[string][]string
	wheels            []string
	sdist             bool
	// groups are Poetry's and PDM's groups (Poetry 1's category), and
	// extraOnly marks a Poetry package installed only for an extra.
	groups    []string
	extraOnly bool
	// markers are the environments this version of the package is for
	// (uv's resolution-markers, Poetry's and PDM's own marker): a lock
	// that pins numpy twice pins each for its own Pythons.
	markers []string
}

func (l *pythonLock) find(name string) *pythonLockedPackage {
	if l == nil {
		return nil
	}
	return l.byName[normalizePythonName(name)]
}

var (
	lockNameRE      = regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
	lockExtraRE     = regexp.MustCompile(`extras?\s*=\s*\[([^\]]*)\]`)
	lockSpecifierRE = regexp.MustCompile(`specifier\s*=\s*"([^"]*)"`)
	lockVersionRE   = regexp.MustCompile(`\bversion\s*=\s*"([^"]+)"`)
	lockMarkerRE    = regexp.MustCompile(`marker\s*=\s*"([^"]*)"`)
	lockSourceRE    = regexp.MustCompile(`^source\s*=\s*\{\s*([a-z-]+)\s*=\s*(?:"([^"]*)"|(true))`)
	lockFileRE      = regexp.MustCompile(`(?:url|file|filename)\s*=\s*"([^"]+)"`)
	lockQuotedRE    = regexp.MustCompile(`"([^"]+)"`)
	// lockStringRE is a TOML basic string with its escapes.
	lockStringRE    = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	poetryMarkersRE = regexp.MustCompile(`markers\s*=\s*"((?:[^"\\]|\\.)*)"`)
)

// readPythonLock reads a uv.lock, poetry.lock or pdm.lock.
func readPythonLock(file string, content []byte) *pythonLock {
	lock := &pythonLock{file: file, byName: map[string]*pythonLockedPackage{}}
	if content == nil {
		return nil
	}
	if string(content) == "locked" {
		lock.unreadable = true
		return lock
	}
	lines := strings.Split(strings.ReplaceAll(string(manifestText(content)), "\r\n", "\n"), "\n")
	var current *pythonLockedPackage
	table := ""
	// A multi-line array is gathered before it is read.
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && !strings.Contains(line, "=") {
			header := strings.Trim(line, "[] ")
			switch {
			case line == "[[package]]":
				current = &pythonLockedPackage{}
				lock.packages = append(lock.packages, current)
				table = "package"
			case strings.HasPrefix(header, "package.") && current != nil:
				table = header
			default:
				current, table = nil, header
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") {
			var scan tomlArrayScan
			balanced := scan.feed(value)
			for !balanced && index+1 < len(lines) {
				index++
				next := strings.TrimSpace(lines[index])
				value += " " + next
				balanced = scan.feed(" " + next)
			}
		}
		switch {
		case current == nil && table == "" && key == "requires-python":
			lock.requiresPython = tomlScalar(value)
		case current == nil && table == "manifest" && key == "members":
			for _, member := range lockQuotedRE.FindAllStringSubmatch(value, -1) {
				lock.members = append(lock.members, normalizePythonName(member[1]))
			}
		case current == nil && table == "metadata.files":
			// Poetry 1.1 kept every package's files here, keyed by name.
			if pkg := lock.byName[normalizePythonName(strings.Trim(key, `"`))]; pkg != nil {
				readLockFiles(pkg, value)
			}
		case current == nil:
		case table == "package":
			switch key {
			case "name":
				current.name = normalizePythonName(tomlScalar(value))
				lock.byName[current.name] = current
			case "version":
				current.version = tomlScalar(value)
			case "category":
				current.groups = []string{tomlScalar(value)}
			case "groups":
				current.groups = nil
				for _, group := range lockQuotedRE.FindAllStringSubmatch(value, -1) {
					current.groups = append(current.groups, group[1])
				}
			case "optional":
				current.extraOnly = value == "true"
			case "source":
				if match := lockSourceRE.FindStringSubmatch(line); match != nil {
					current.source, current.sourceRef = match[1], match[2]
				}
			case "git":
				// PDM's VCS entries.
				current.source, current.sourceRef = "git", tomlScalar(value)
			case "path":
				current.source, current.sourceRef = "path", tomlScalar(value)
			case "dependencies":
				current.edges = lockDependencyEdges(value)
				current.dependencies = edgeNames(current.edges)
			case "resolution-markers":
				current.markers = nil
				for _, marker := range lockQuotedRE.FindAllStringSubmatch(value, -1) {
					current.markers = append(current.markers, marker[1])
				}
			case "markers", "marker":
				current.markers = []string{tomlScalar(value)}
			case "wheels", "files":
				readLockFiles(current, value)
			case "sdist":
				current.sdist = true
			}
		case table == "package.source" && key == "type":
			current.source = tomlScalar(value)
		case table == "package.source" && key == "url":
			current.sourceRef = tomlScalar(value)
		case table == "package.dependencies":
			// Poetry lists each dependency as a key, its value a version or an
			// inline table with the markers and extras.
			edge := pythonLockEdge{name: normalizePythonName(strings.Trim(key, `"`))}
			if marker := poetryMarkersRE.FindStringSubmatch(value); marker != nil {
				edge.marker = strings.ReplaceAll(marker[1], `\"`, `"`)
			}
			if extras := lockExtraRE.FindStringSubmatch(value); extras != nil {
				edge.extras = pythonExtras(strings.ReplaceAll(extras[1], `"`, ""))
			}
			current.edges = append(current.edges, edge)
			current.dependencies = append(current.dependencies, edge.name)
		case table == "package.optional-dependencies":
			if current.extras == nil {
				current.extras = map[string][]pythonLockEdge{}
			}
			current.extras[key] = lockDependencyEdges(value)
		case table == "package.dev-dependencies":
			if current.dev == nil {
				current.dev = map[string][]string{}
			}
			current.dev[key] = edgeNames(lockDependencyEdges(value))
		case table == "package.metadata" && key == "requires-dist":
			current.requiresDist = lockRequirements(value)
		case table == "package.metadata.requires-dev":
			if current.requiresDev == nil {
				current.requiresDev = map[string][]string{}
			}
			for _, requirement := range lockRequirements(value) {
				current.requiresDev[key] = append(current.requiresDev[key], requirement.name)
			}
		}
	}
	for _, pkg := range lock.packages {
		if (pkg.source == "virtual" || pkg.source == "editable") && pkg.sourceRef == "." {
			lock.root = pkg
		}
	}
	return lock
}

// lockDependencyEdges reads uv's `[{ name = "x", extra = […], marker = "…" }, …]`
// and PDM's `["x[extra]>=1; marker", …]` alike.
func lockDependencyEdges(value string) []pythonLockEdge {
	var edges []pythonLockEdge
	if strings.Contains(value, "{") {
		for _, item := range splitLockItems(value) {
			name := lockNameRE.FindStringSubmatch(item)
			if name == nil {
				continue
			}
			edge := pythonLockEdge{name: normalizePythonName(name[1])}
			if version := lockVersionRE.FindStringSubmatch(item); version != nil {
				edge.version = version[1]
			}
			if extras := lockExtraRE.FindStringSubmatch(item); extras != nil {
				edge.extras = pythonExtras(strings.ReplaceAll(extras[1], `"`, ""))
			}
			if marker := lockMarkerRE.FindStringSubmatch(item); marker != nil {
				edge.marker = marker[1]
			}
			edges = append(edges, edge)
		}
		return edges
	}
	for _, match := range lockStringRE.FindAllStringSubmatch(value, -1) {
		text := strings.ReplaceAll(match[1], `\"`, `"`)
		if requirement, ok := parsePythonRequirement(text); ok {
			edges = append(edges, pythonLockEdge{name: requirement.name, extras: requirement.extras, marker: requirement.marker})
		}
	}
	return edges
}

func edgeNames(edges []pythonLockEdge) []string {
	names := make([]string, 0, len(edges))
	for _, edge := range edges {
		names = append(names, edge.name)
	}
	return names
}

// lockRequirements reads uv's requires-dist, one inline table per
// requirement.
func lockRequirements(value string) []pythonRequirement {
	var result []pythonRequirement
	for _, item := range splitLockItems(value) {
		name := lockNameRE.FindStringSubmatch(item)
		if name == nil {
			continue
		}
		requirement := pythonRequirement{name: normalizePythonName(name[1]), file: "uv.lock"}
		if extras := lockExtraRE.FindStringSubmatch(item); extras != nil {
			requirement.extras = pythonExtras(strings.ReplaceAll(extras[1], `"`, ""))
		}
		if specifier := lockSpecifierRE.FindStringSubmatch(item); specifier != nil {
			requirement.specifier = specifier[1]
		}
		if marker := lockMarkerRE.FindStringSubmatch(item); marker != nil {
			requirement.marker = marker[1]
		}
		if strings.Contains(item, "git =") || strings.Contains(item, "path =") || strings.Contains(item, "editable =") {
			requirement.url = "source"
		}
		result = append(result, requirement)
	}
	return result
}

// splitLockItems splits an array of inline tables into the tables.
func splitLockItems(value string) []string {
	var items []string
	depth, start := 0, -1
	inString := false
	for index := 0; index < len(value); index++ {
		switch character := value[index]; {
		case inString:
			if character == '\\' {
				index++
			} else if character == '"' {
				inString = false
			}
		case character == '"':
			inString = true
		case character == '{':
			if depth == 0 {
				start = index
			}
			depth++
		case character == '}':
			depth--
			if depth == 0 && start >= 0 {
				items = append(items, value[start:index+1])
				start = -1
			}
		}
	}
	return items
}

func readLockFiles(pkg *pythonLockedPackage, value string) {
	for _, match := range lockFileRE.FindAllStringSubmatch(value, -1) {
		name := path.Base(strings.SplitN(match[1], "#", 2)[0])
		switch {
		case strings.HasSuffix(name, ".whl"):
			pkg.wheels = append(pkg.wheels, name)
		case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip"):
			pkg.sdist = true
		}
	}
}

// mainPackages says which packages a lock installs for production, on any
// platform: uv from the project's own dependencies and the extras each edge
// asks for (dev groups and the project's own extras left out), Poetry and
// PDM from each package's groups.
func (l *pythonLock) mainPackages() map[string]bool {
	result := map[string]bool{}
	for _, pkg := range l.reach(func(string) bool { return true }) {
		result[pkg.name] = true
	}
	return result
}

// installedPackages are the locked versions a Linux build on one CPython
// family installs: the edges whose markers hold there, and of a package
// the lock pins more than once, the version meant for it.
func (l *pythonLock) installedPackages(minor int, arch string) []*pythonLockedPackage {
	return l.reach(func(marker string) bool { return pythonMarkerAllows(marker, minor, arch) })
}

func (l *pythonLock) reach(allows func(marker string) bool) []*pythonLockedPackage {
	if l == nil || l.unreadable {
		return nil
	}
	holds := func(markers []string) bool {
		return len(markers) == 0 || slices.ContainsFunc(markers, allows)
	}
	var result []*pythonLockedPackage
	if l.root == nil {
		for _, pkg := range l.packages {
			if pkg.name == "" || pkg.extraOnly || !holds(pkg.markers) {
				continue
			}
			if len(pkg.groups) == 0 || slices.Contains(pkg.groups, "main") || slices.Contains(pkg.groups, "default") {
				result = append(result, pkg)
			}
		}
		return result
	}
	queue := append([]pythonLockEdge(nil), l.root.edges...)
	for _, member := range l.members {
		if pkg := l.byName[member]; pkg != nil && pkg != l.root {
			queue = append(queue, pythonLockEdge{name: member})
		}
	}
	versions := map[string][]*pythonLockedPackage{}
	for _, pkg := range l.packages {
		versions[pkg.name] = append(versions[pkg.name], pkg)
	}
	// Each package version and each of its extras is expanded once, so the
	// queue ends.
	visited := map[*pythonLockedPackage]bool{}
	expanded := map[string]bool{}
	for len(queue) > 0 {
		edge := queue[0]
		queue = queue[1:]
		if edge.marker != "" && !allows(edge.marker) {
			continue
		}
		for _, pkg := range versions[edge.name] {
			if (edge.version != "" && pkg.version != edge.version) || (edge.version == "" && !holds(pkg.markers)) {
				continue
			}
			if !visited[pkg] {
				visited[pkg] = true
				result = append(result, pkg)
				queue = append(queue, pkg.edges...)
			}
			for _, extra := range edge.extras {
				key := pkg.name + "==" + pkg.version + "[" + extra + "]"
				if !expanded[key] {
					expanded[key] = true
					queue = append(queue, pkg.extras[extra]...)
				}
			}
		}
	}
	return result
}

// compactPythonLock streams a lock too large to keep whole and keeps what
// readPythonLock reads: every structural line, and of the file arrays only
// the entries a Linux build can install, each reduced to its file name. A
// machine-learning uv.lock of several megabytes is mostly other platforms'
// wheels and their hashes. The result is bounded by keep.
func compactPythonLock(reader io.Reader, keep int) ([]byte, bool) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var out bytes.Buffer
	inFiles := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case inFiles:
			if strings.HasPrefix(trimmed, "]") {
				inFiles = false
				out.WriteString("]\n")
				continue
			}
			for _, match := range lockFileRE.FindAllStringSubmatch(trimmed, -1) {
				name := path.Base(strings.SplitN(match[1], "#", 2)[0])
				if strings.Contains(name, "linux") || strings.HasSuffix(name, "-any.whl") || !strings.HasSuffix(name, ".whl") {
					out.WriteString(`{ file = "` + name + "\" },\n")
				}
			}
		case strings.HasPrefix(trimmed, "wheels = [") || strings.HasPrefix(trimmed, "files = ["):
			key, _, _ := strings.Cut(trimmed, "=")
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed[len(key)+1:]), "["))
			out.WriteString(strings.TrimSpace(key) + " = [\n")
			if strings.HasSuffix(rest, "]") {
				for _, match := range lockFileRE.FindAllStringSubmatch(rest, -1) {
					out.WriteString(`{ file = "` + path.Base(match[1]) + "\" },\n")
				}
				out.WriteString("]\n")
				continue
			}
			inFiles = true
		case strings.HasPrefix(trimmed, "sdist = "):
			out.WriteString("sdist = true\n")
		default:
			out.WriteString(line + "\n")
		}
		if out.Len() > keep {
			return nil, false
		}
	}
	return out.Bytes(), scanner.Err() == nil
}

// readCompactPythonLock opens a lock under a root and keeps it compacted,
// reading at most 64 MiB of it.
func readCompactPythonLock(open func() (io.ReadCloser, error)) ([]byte, bool) {
	file, err := open()
	if err != nil {
		return nil, false
	}
	defer file.Close()
	return compactPythonLock(io.LimitReader(file, 64<<20), 4<<20)
}

// readCompactPythonLockAt is readCompactPythonLock for a lock under a build
// root, reached without leaving it.
func readCompactPythonLockAt(root, name string) ([]byte, bool) {
	realPath, err := containedRegularPath(root, name, 64<<20)
	if err != nil {
		return nil, false
	}
	return readCompactPythonLock(func() (io.ReadCloser, error) { return os.Open(realPath) })
}
