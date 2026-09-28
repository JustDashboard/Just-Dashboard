package deploy

import (
	"errors"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Detection reads a checkout in two passes. The first lists directories
// breadth-first and visits only the files whose names say what a directory
// is — package.json, go.mod, a Dockerfile, a Gemfile — and the second walks
// everything else in lexical order. A single lexical walk used to spend its
// file and byte budget on whatever sorted first: ten thousand images under
// assets/, or generated Go under api/, were read before the root's
// package.json or go.mod, and the scan ended truncated with no candidate at
// all. Breadth-first, the root's own manifests are always among the first
// things read, and nothing a repository holds in bulk can hide them.

// walkDetectionPassEntries bounds the listing pass itself. It lists names
// only, so the bound exists for a pathological tree, not a large one.
const walkDetectionPassEntries = 400_000

// walkDetectionTree calls visit exactly as filepath.WalkDir would — once for
// every directory it enters (SkipDir prunes it) and once for every file —
// except that the files `first` names are visited in the breadth-first pass
// and are not visited again. A directory is judged once: the lexical pass
// reuses the breadth-first pass's answer instead of asking again, so what
// the callback records about a directory (a pruned manifest, the workflows
// .github holds) is recorded once.
func walkDetectionTree(root string, first func(lowerName string) bool, visit fs.WalkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return visit(root, nil, err)
	}
	judged := map[string]error{}
	if err := visit(root, fs.FileInfoToDirEntry(info), nil); err != nil {
		if errors.Is(err, filepath.SkipDir) || errors.Is(err, filepath.SkipAll) {
			return nil
		}
		return err
	}
	judged[root] = nil
	queue := []string{root}
	listed := 0
	for len(queue) > 0 && listed < walkDetectionPassEntries {
		directory := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		listed += len(entries)
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			if entry.IsDir() {
				if err := visit(path, entry, nil); err != nil {
					if errors.Is(err, filepath.SkipDir) {
						judged[path] = filepath.SkipDir
						continue
					}
					if errors.Is(err, filepath.SkipAll) {
						return nil
					}
					return err
				}
				judged[path] = nil
				queue = append(queue, path)
				continue
			}
			if !first(strings.ToLower(entry.Name())) {
				continue
			}
			if err := visit(path, entry, nil); err != nil {
				if errors.Is(err, filepath.SkipDir) {
					continue
				}
				if errors.Is(err, filepath.SkipAll) {
					return nil
				}
				return err
			}
		}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry != nil && entry.IsDir() {
			if decision, ok := judged[path]; ok {
				return decision
			}
		}
		if err == nil && entry != nil && !entry.IsDir() && first(strings.ToLower(entry.Name())) {
			return nil
		}
		return visit(path, entry, err)
	})
}

// Go source is read for three facts — whether it imports "C", whether it is
// a main package, and the environment names it reads — all of which sit at
// the top of a file. Reading only the head, under a budget of its own, keeps
// a module's generated protobuf or entgo code from spending the byte budget
// the manifests need; generated files are skipped outright, since nobody
// writes cgo or configuration reads into them by hand.
const (
	goScanMaxFiles = 3000
	goScanMaxBytes = 24 << 20
	goScanHead     = 64 << 10
)

var goGeneratedRE = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

type goSourceScan struct {
	files     int
	bytes     int64
	truncated bool
	// mains holds the directories with a hand-written main package, and
	// packages every directory with any Go package, both relative to the
	// detection root.
	mains    map[string]bool
	packages map[string]bool
}

func newGoSourceScan() *goSourceScan {
	return &goSourceScan{mains: map[string]bool{}, packages: map[string]bool{}}
}

// read returns the head of a Go source file, or false when the file is
// skipped: generated, a protobuf binding, or past the scan's budget.
func (s *goSourceScan) read(path, rel, lowerName string) ([]byte, bool) {
	if strings.HasSuffix(lowerName, ".pb.go") || strings.HasSuffix(lowerName, ".pb.gw.go") {
		return nil, false
	}
	if s.files >= goScanMaxFiles || s.bytes >= goScanMaxBytes {
		s.truncated = true
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, goScanHead))
	s.files++
	s.bytes += int64(len(content))
	if err != nil || generatedGoSource(content) {
		return nil, false
	}
	directory := filepath.ToSlash(filepath.Dir(rel))
	if parsed, err := parser.ParseFile(token.NewFileSet(), "source.go", content, parser.PackageClauseOnly); err == nil {
		s.packages[directory] = true
		if parsed.Name.Name == "main" {
			s.mains[directory] = true
		}
	}
	return content, true
}

// generatedGoSource follows the Go convention (go help generate): a line
// `// Code generated ... DO NOT EDIT.` before the package clause.
func generatedGoSource(content []byte) bool {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if goGeneratedRE.MatchString(line) {
			return true
		}
	}
	return false
}

// libraryModule reports whether a module rooted at root holds Go packages but
// no hand-written main package outside example and test directories, leaving
// out any nested module, and where its only main packages are. It is a
// verdict only when the scan was not cut short and found packages at all.
func (s *goSourceScan) libraryModule(root string, moduleRoots []string) (bool, []string) {
	if s.truncated || !s.anyUnder(s.packages, root, moduleRoots) {
		return false, nil
	}
	main, examples := s.hasMain(root, moduleRoots)
	return !main, examples
}

func (s *goSourceScan) anyUnder(directories map[string]bool, root string, moduleRoots []string) bool {
	for directory := range directories {
		if (root == "" || directory == root || strings.HasPrefix(directory, rootPrefix(root))) && !nestedModule(directory, root, moduleRoots) {
			return true
		}
	}
	return false
}

func nestedModule(directory, root string, moduleRoots []string) bool {
	for _, other := range moduleRoots {
		if other != root && (root == "" || strings.HasPrefix(other, rootPrefix(root))) && (directory == other || strings.HasPrefix(directory, rootPrefix(other))) {
			return true
		}
	}
	return false
}

func (s *goSourceScan) hasMain(root string, moduleRoots []string) (bool, []string) {
	prefix := rootPrefix(root)
	examples := []string{}
	for directory := range s.mains {
		relative := directory
		if root != "" {
			if directory != root && !strings.HasPrefix(directory, prefix) {
				continue
			}
			relative = strings.TrimPrefix(strings.TrimPrefix(directory, root), "/")
		}
		if nestedModule(directory, root, moduleRoots) {
			continue
		}
		if decoySegment(relative) != "" {
			examples = append(examples, directory)
			continue
		}
		return true, nil
	}
	return false, examples
}

// detectionToolingDirectories hold an editor's, a CI system's or a tool's own
// files, never the application: a .devcontainer Dockerfile is a development
// image that serves nothing, and caches repeat a dependency tree the walk
// would otherwise read file by file.
var detectionToolingDirectories = map[string]string{
	".devcontainer":    "development container definition, not deployed",
	".github":          "GitHub workflow and repository settings",
	".gitlab":          "GitLab settings",
	".circleci":        "CI configuration",
	".vscode":          "editor settings",
	".idea":            "editor settings",
	".fleet":           "editor settings",
	".husky":           "Git hook scripts",
	".tox":             "tool cache",
	".nox":             "tool cache",
	".mypy_cache":      "tool cache",
	".pytest_cache":    "tool cache",
	".ruff_cache":      "tool cache",
	".gradle":          "tool cache",
	".terraform":       "tool cache",
	".serverless":      "tool cache",
	".vercel":          "tool cache",
	".netlify":         "tool cache",
	".svelte-kit":      "build output",
	".nuxt":            "build output",
	".output":          "build output",
	".turbo":           "tool cache",
	".angular":         "tool cache",
	".expo":            "tool cache",
	".docusaurus":      "build output",
	".parcel-cache":    "tool cache",
	".dart_tool":       "tool cache",
	".stack-work":      "build output",
	".zig-cache":       "build output",
	"zig-cache":        "build output",
	"zig-out":          "build output",
	"elm-stuff":        "build output",
	"_opam":            "tool cache",
	"bower_components": "installed dependencies",
	"jspm_packages":    "installed dependencies",
	"pods":             "installed dependencies",
	"deriveddata":      "build output",
	"htmlcov":          "coverage report",
	"coverage":         "coverage report",
	"lcov-report":      "coverage report",
}

// virtualenvDirectory reports whether a directory is a committed Python
// environment, whatever it is called: `python -m venv env` writes
// pyvenv.cfg, and conda writes conda-meta/.
func virtualenvDirectory(path string) bool {
	for _, marker := range []string{"pyvenv.cfg", "conda-meta"} {
		if _, err := os.Lstat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}
