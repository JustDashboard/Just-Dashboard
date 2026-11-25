package imagescan

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Which of an image's Node packages the application needs to run.
//
// Trivy reads every package.json under node_modules and reports a vulnerable
// package by its path, but it cannot say whether the application runs it or
// it came with a development tool. A recipe that copies the whole
// node_modules into the image ships both: in a Next.js release, the standard
// library of esbuild's Go binary was 148 of 183 findings, none of them in
// anything that served a request.
//
// So the archive the scanner read is read here too, as data: each layer in
// order, keeping only the package.json files and the links under
// node_modules, with whiteouts applied. Nothing is written to disk and
// nothing in the image is run. From every application folder (a package.json
// outside node_modules), dependencies, optional dependencies and required
// peers are followed the way Node resolves them, nearest node_modules first.
// What that reaches is what the application runs. devDependencies are
// followed afterwards, to name what brought in a package it does not run.

// A finding's Dependency, for a package installed in an image's
// node_modules.
const (
	// DependencyDirect is a package an application folder names in its
	// dependencies, or one installed at the top of a global or ownerless
	// node_modules.
	DependencyDirect = "direct"
	// DependencyIndirect is one a direct dependency needs to run.
	DependencyIndirect = "indirect"
	// DependencyDevelopment is one nothing the application runs needs:
	// reached only through devDependencies, or not reached at all.
	DependencyDevelopment = "development"
)

const (
	// nodeManifestMaxBytes bounds one package.json. A larger one still
	// counts as a package that exists; what it names is not read.
	nodeManifestMaxBytes = 1 << 20
	// nodeMaxManifests and nodeMaxLinks bound what one archive may hold for
	// this reading; an image past either is left unclassified.
	nodeMaxManifests = 200_000
	nodeMaxLinks     = 200_000
	// nodeMaxPrefixRemovals bounds the whiteouts that clear a folder, each a
	// walk over what is kept.
	nodeMaxPrefixRemovals = 5_000
	// nodeMaxReadBytes bounds what the layers may decompress to.
	nodeMaxReadBytes = 4 * MaxImageBytes
	nodeMaxLinkHops  = 40
)

// ErrNodeGraph is an archive whose Node packages could not be read. The
// findings are kept as Trivy reported them, without a dependency.
var ErrNodeGraph = errors.New("the image's Node packages could not be read")

// nodeManifest is the part of a package.json this reading follows.
type nodeManifest struct {
	runtime []string
	dev     []string
}

// NodeGraph is an image's Node packages: each package folder with the names
// it depends on, and the links a package manager made between folders.
type NodeGraph struct {
	manifests map[string]*nodeManifest
	links     map[string]string
	removals  int

	reached map[string]nodeReach
	walked  bool
}

type nodeReach struct {
	dependency string
	via        string
}

func newNodeGraph() *NodeGraph {
	return &NodeGraph{manifests: map[string]*nodeManifest{}, links: map[string]string{}}
}

// NeedsNodeGraph reports whether any finding is in a package under
// node_modules, the only ones a NodeGraph says anything about.
func NeedsNodeGraph(findings []Finding) bool {
	for _, finding := range findings {
		if inNodeModules(finding.Path) {
			return true
		}
	}
	return false
}

// ReadNodeGraph reads the Node packages of the one image in a `docker save`
// archive.
func ReadNodeGraph(ctx context.Context, archive string) (*NodeGraph, error) {
	file, err := os.Open(archive)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeGraph, err)
	}
	defer file.Close()
	entries, manifest, err := indexArchive(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeGraph, err)
	}
	var images []struct {
		Layers []string `json:"Layers"`
	}
	if err := json.Unmarshal(manifest, &images); err != nil || len(images) != 1 {
		return nil, fmt.Errorf("%w: the archive does not hold exactly one image", ErrNodeGraph)
	}
	graph := newNodeGraph()
	budget := int64(nodeMaxReadBytes)
	for _, layer := range images[0].Layers {
		entry, ok := entries[cleanEntry(layer)]
		if !ok {
			return nil, fmt.Errorf("%w: layer %s is not in the archive", ErrNodeGraph, layer)
		}
		if err := graph.readLayer(ctx, io.NewSectionReader(file, entry.offset, entry.size), &budget); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNodeGraph, err)
		}
	}
	return graph, nil
}

type archiveEntry struct{ offset, size int64 }

// indexArchive finds where each file of the archive starts, and reads its
// manifest.json, which may come after the layers it names.
func indexArchive(file *os.File) (map[string]archiveEntry, []byte, error) {
	entries := map[string]archiveEntry{}
	var manifest []byte
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := cleanEntry(header.Name)
		if name == "manifest.json" {
			if manifest, err = io.ReadAll(io.LimitReader(reader, 1<<20)); err != nil {
				return nil, nil, err
			}
			continue
		}
		// archive/tar reads a header in whole blocks and nothing ahead of
		// it, so the file's offset is where this entry's content starts.
		offset, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, nil, err
		}
		entries[name] = archiveEntry{offset: offset, size: header.Size}
	}
	if manifest == nil {
		return nil, nil, errors.New("the archive has no manifest.json")
	}
	return entries, manifest, nil
}

// budgetReader stops a layer that decompresses past what is left of the
// archive's budget.
type budgetReader struct {
	reader io.Reader
	left   *int64
}

func (r budgetReader) Read(buffer []byte) (int, error) {
	if *r.left <= 0 {
		return 0, errors.New("the layers decompress to more than this reading allows")
	}
	if int64(len(buffer)) > *r.left {
		buffer = buffer[:*r.left]
	}
	n, err := r.reader.Read(buffer)
	*r.left -= int64(n)
	return n, err
}

// readLayer applies one layer: its whiteouts first take away what a lower
// layer had, then its package.json files and node_modules links are kept.
func (g *NodeGraph) readLayer(ctx context.Context, layer io.Reader, budget *int64) error {
	buffered := bufio.NewReaderSize(layer, 64<<10)
	magic, _ := buffered.Peek(4)
	var stream io.Reader = buffered
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(buffered)
		if err != nil {
			return err
		}
		defer gz.Close()
		stream = gz
	case bytes.Equal(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		decoder, err := zstd.NewReader(buffered, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return err
		}
		defer decoder.Close()
		stream = decoder
	}
	reader := tar.NewReader(budgetReader{reader: stream, left: budget})
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := cleanEntry(header.Name)
		if name == "" {
			continue
		}
		dir, base := path.Dir(name), path.Base(name)
		if dir == "." {
			dir = ""
		}
		if base == ".wh..wh..opq" {
			if err := g.removeUnder(dir); err != nil {
				return err
			}
			continue
		}
		if hidden, ok := strings.CutPrefix(base, ".wh."); ok {
			if err := g.remove(path.Join(dir, hidden)); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeSymlink {
			delete(g.links, name)
		}
		switch header.Typeflag {
		case tar.TypeSymlink:
			delete(g.manifests, name)
			if !inNodeModules(name) {
				continue
			}
			if len(g.links) >= nodeMaxLinks {
				return errors.New("the image holds more node_modules links than this reading follows")
			}
			g.links[name] = header.Linkname
		case tar.TypeReg:
			if base != "package.json" {
				continue
			}
			if len(g.manifests) >= nodeMaxManifests {
				return errors.New("the image holds more package.json files than this reading follows")
			}
			raw, err := io.ReadAll(io.LimitReader(reader, nodeManifestMaxBytes+1))
			if err != nil {
				return err
			}
			g.manifests[dir] = parseNodeManifest(raw)
		case tar.TypeLink:
			// A hard link within the layer names an entry before it.
			if base != "package.json" {
				continue
			}
			if manifest, ok := g.manifests[path.Dir(cleanEntry(header.Linkname))]; ok {
				g.manifests[dir] = manifest
			}
		}
	}
}

// parseNodeManifest reads what a package needs. A field of the wrong shape
// reads as empty rather than failing the whole manifest: the folder is still
// a package that exists.
func parseNodeManifest(raw []byte) *nodeManifest {
	manifest := &nodeManifest{}
	if len(raw) > nodeManifestMaxBytes {
		return manifest
	}
	var fields struct {
		Dependencies         map[string]any `json:"dependencies"`
		OptionalDependencies map[string]any `json:"optionalDependencies"`
		PeerDependencies     map[string]any `json:"peerDependencies"`
		PeerDependenciesMeta map[string]any `json:"peerDependenciesMeta"`
		DevDependencies      map[string]any `json:"devDependencies"`
	}
	var loose map[string]json.RawMessage
	if json.Unmarshal(raw, &loose) != nil {
		return manifest
	}
	read := func(key string, into *map[string]any) {
		if value, ok := loose[key]; ok {
			_ = json.Unmarshal(value, into)
		}
	}
	read("dependencies", &fields.Dependencies)
	read("optionalDependencies", &fields.OptionalDependencies)
	read("peerDependencies", &fields.PeerDependencies)
	read("peerDependenciesMeta", &fields.PeerDependenciesMeta)
	read("devDependencies", &fields.DevDependencies)
	runtime := map[string]bool{}
	for name := range fields.Dependencies {
		runtime[name] = true
	}
	for name := range fields.OptionalDependencies {
		runtime[name] = true
	}
	// An optional peer is the host's to install or not (Next.js names
	// @playwright/test and sass); a development install of it is not
	// something the package runs.
	for name := range fields.PeerDependencies {
		meta, _ := fields.PeerDependenciesMeta[name].(map[string]any)
		if optional, _ := meta["optional"].(bool); !optional {
			runtime[name] = true
		}
	}
	for name := range runtime {
		if validNodeName(name) {
			manifest.runtime = append(manifest.runtime, name)
		}
	}
	for name := range fields.DevDependencies {
		if validNodeName(name) && !runtime[name] {
			manifest.dev = append(manifest.dev, name)
		}
	}
	sort.Strings(manifest.runtime)
	sort.Strings(manifest.dev)
	return manifest
}

// remove takes away one path a whiteout names, and what is under it.
func (g *NodeGraph) remove(target string) error {
	delete(g.links, target)
	delete(g.manifests, target)
	if path.Base(target) == "package.json" {
		delete(g.manifests, path.Dir(target))
	}
	return g.removeUnder(target)
}

// removeUnder takes away everything below a folder.
func (g *NodeGraph) removeUnder(dir string) error {
	g.removals++
	if g.removals > nodeMaxPrefixRemovals {
		return errors.New("the image's layers remove more folders than this reading follows")
	}
	prefix := dir + "/"
	if dir == "" {
		prefix = ""
	}
	for name := range g.manifests {
		if strings.HasPrefix(name, prefix) && name != dir {
			delete(g.manifests, name)
		}
	}
	for name := range g.links {
		if strings.HasPrefix(name, prefix) && name != dir {
			delete(g.links, name)
		}
	}
	return nil
}

// Classify marks each finding in a package under node_modules with whether
// the application runs it, and the dependency it came through.
func (g *NodeGraph) Classify(findings []Finding) []Finding {
	if g == nil {
		return findings
	}
	g.walk()
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		if dir, ok := g.owner(finding.Path); ok {
			if reach, ok := g.reached[dir]; ok {
				finding.Dependency, finding.Via = reach.dependency, reach.via
			} else {
				finding.Dependency, finding.Via = DependencyDevelopment, ""
			}
		}
		out[i] = finding
	}
	return out
}

// owner is the installed package a finding's file belongs to: the package
// folder of a package.json, or the nearest package above a binary.
func (g *NodeGraph) owner(file string) (string, bool) {
	file = cleanEntry(file)
	if !inNodeModules(file) {
		return "", false
	}
	dir := path.Dir(file)
	for dir != "." && dir != "" && inNodeModules(dir) {
		if _, ok := g.manifests[dir]; ok && path.Base(dir) != "node_modules" {
			return dir, true
		}
		dir = path.Dir(dir)
	}
	return "", false
}

type nodeStep struct {
	dir, dependency, via string
}

// walk follows every application folder's runtime dependencies, then its
// development ones; the first reach of a package is what it is.
func (g *NodeGraph) walk() {
	if g.walked {
		return
	}
	g.walked = true
	g.reached = map[string]nodeReach{}
	var roots, installed []string
	for dir := range g.manifests {
		if !inNodeModules(dir) {
			roots = append(roots, dir)
			continue
		}
		// The top of a node_modules with no package.json beside it: a
		// global install (/usr/local/lib/node_modules/npm), or an
		// application copied without its manifest. Either way it is there
		// to be run.
		owner, rest, _ := strings.Cut("/"+dir, "/node_modules/")
		owner = strings.TrimPrefix(owner, "/")
		if _, ok := g.manifests[owner]; ok || inNodeModules(owner) {
			continue
		}
		if topLevelPackage(rest) {
			installed = append(installed, dir)
		}
	}
	sort.Strings(roots)
	sort.Strings(installed)

	var queue []nodeStep
	visit := func(dir, dependency, via string) {
		if _, ok := g.reached[dir]; ok {
			return
		}
		g.reached[dir] = nodeReach{dependency: dependency, via: via}
		queue = append(queue, nodeStep{dir: dir, dependency: dependency, via: via})
	}
	drain := func(child string) {
		for len(queue) > 0 {
			step := queue[0]
			queue = queue[1:]
			for _, name := range g.manifests[step.dir].runtime {
				if dir, ok := g.resolve(step.dir, name); ok {
					visit(dir, child, step.via)
				}
			}
		}
	}
	for _, root := range roots {
		for _, name := range g.manifests[root].runtime {
			if dir, ok := g.resolve(root, name); ok {
				visit(dir, DependencyDirect, name)
			}
		}
	}
	for _, dir := range installed {
		visit(dir, DependencyDirect, packageNameOf(dir))
	}
	drain(DependencyIndirect)
	for _, root := range roots {
		for _, name := range g.manifests[root].dev {
			if dir, ok := g.resolve(root, name); ok {
				visit(dir, DependencyDevelopment, name)
			}
		}
	}
	drain(DependencyDevelopment)
}

// resolve finds the package a folder's require(name) loads: the nearest
// node_modules/name above the folder's real path, as Node looks.
func (g *NodeGraph) resolve(from, name string) (string, bool) {
	dir := g.real(from)
	for {
		if path.Base(dir) != "node_modules" {
			candidate := g.real(path.Join(dir, "node_modules", name))
			if _, ok := g.manifests[candidate]; ok {
				return candidate, true
			}
		}
		if dir == "" {
			return "", false
		}
		parent := path.Dir(dir)
		if parent == "." || parent == "/" {
			parent = ""
		}
		dir = parent
	}
}

// real follows the links on a path, each component in turn, as the kernel
// would inside the image: an absolute target starts from the image's root,
// and nothing climbs above it.
func (g *NodeGraph) real(name string) string {
	name = cleanEntry(name)
	if len(g.links) == 0 || name == "" {
		return name
	}
	parts := strings.Split(name, "/")
	current := ""
	for hops := 0; len(parts) > 0; {
		part := parts[0]
		parts = parts[1:]
		if part == "" {
			continue
		}
		next := path.Join(current, part)
		target, ok := g.links[next]
		if !ok || hops >= nodeMaxLinkHops {
			current = next
			continue
		}
		hops++
		if !strings.HasPrefix(target, "/") {
			target = path.Join("/"+current, target)
		}
		parts = append(strings.Split(cleanEntry(target), "/"), parts...)
		current = ""
	}
	return current
}

// cleanEntry is a path inside the image, relative to its root, with no way
// above it: "./app/../app/x" and "/app/x" are "app/x".
func cleanEntry(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}

func inNodeModules(name string) bool {
	return strings.Contains("/"+cleanEntry(name)+"/", "/node_modules/")
}

// topLevelPackage reports whether what follows a node_modules/ is one
// package: "next" or "@scope/name".
func topLevelPackage(rest string) bool {
	parts := strings.Split(rest, "/")
	if strings.HasPrefix(rest, "@") {
		return len(parts) == 2 && parts[1] != ""
	}
	return len(parts) == 1 && parts[0] != "" && !strings.HasPrefix(parts[0], ".")
}

// packageNameOf is the name a package folder is installed under.
func packageNameOf(dir string) string {
	base := path.Base(dir)
	if scope := path.Base(path.Dir(dir)); strings.HasPrefix(scope, "@") {
		return scope + "/" + base
	}
	return base
}

// validNodeName keeps a dependency name that is a package's: "next" or
// "@scope/name", never a path.
func validNodeName(name string) bool {
	if name == "" || len(name) > 214 || strings.ContainsAny(name, "\\\x00") || strings.Contains(name, "..") {
		return false
	}
	return topLevelPackage(name)
}
