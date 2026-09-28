package deploy

import (
	"path"
	"strings"
)

// The JVM and .NET recipes build a project from the directory that owns it —
// a Maven reactor, a Gradle settings root, the folder of a solution's
// shared props — so what they read reaches beyond the root a candidate
// names. Detection and the recipe read those files through the same reader,
// over the checkout, so they decide the same thing: bounded, as data, never
// through a symlink, and never executed.

// buildFilesBudget bounds what one reader reads in total; a file past it
// reads as absent, which costs a fact, never a wrong plan.
const buildFilesBudget = 32 << 20

type buildFiles struct {
	tree  detectionTree
	cache map[string][]byte
	spent int64
}

func newBuildFiles(checkout string) *buildFiles {
	return &buildFiles{tree: openDetectionTree(checkout), cache: map[string][]byte{}}
}

func (f *buildFiles) close() { f.tree.close() }

// read returns a regular file's content when it is at most limit bytes.
// Paths are relative to the checkout, slash-separated.
func (f *buildFiles) read(rel string, limit int64) ([]byte, bool) {
	rel = cleanBuildPath(rel)
	if rel == "" {
		return nil, false
	}
	if content, ok := f.cache[rel]; ok {
		return content, content != nil && int64(len(content)) <= limit
	}
	if f.spent >= buildFilesBudget {
		return nil, false
	}
	content, ok := f.tree.read(rel, limit)
	if !ok {
		f.cache[rel] = nil
		return nil, false
	}
	f.spent += int64(len(content))
	content = manifestText(content)
	f.cache[rel] = content
	return content, true
}

func (f *buildFiles) regular(rel string) bool {
	rel = cleanBuildPath(rel)
	if rel == "" {
		return false
	}
	_, ok := f.tree.regular(rel)
	return ok
}

func (f *buildFiles) directory(rel string) bool {
	info, ok := f.tree.lstat(rel)
	return ok && info.IsDir()
}

// names lists a directory's entries, bounded by the tree.
func (f *buildFiles) names(rel string) []string {
	return f.tree.entries(rel)
}

// cleanBuildPath is a checkout-relative path in slash form, or "" for one
// that leaves the checkout. "." is the checkout itself.
func cleanBuildPath(rel string) string {
	rel = path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "."
	}
	return rel
}

// joinBuildPath joins a relative reference onto a checkout directory and
// says whether the result stays inside the checkout.
func joinBuildPath(dir, reference string) (string, bool) {
	reference = strings.ReplaceAll(strings.TrimSpace(reference), "\\", "/")
	if reference == "" || path.IsAbs(reference) || strings.ContainsAny(reference, "\x00\r\n$") {
		return "", false
	}
	joined := path.Clean(path.Join(dir, reference))
	if joined == ".." || strings.HasPrefix(joined, "../") || path.IsAbs(joined) {
		return "", false
	}
	return joined, true
}

// ancestorDirs lists dir and each directory above it up to the checkout's
// top, nearest first, in slash form with "." for the top.
func ancestorDirs(dir string) []string {
	dir = cleanBuildPath(dir)
	dirs := []string{}
	for {
		dirs = append(dirs, dir)
		if dir == "." || len(dirs) > 32 {
			return dirs
		}
		dir = path.Dir(dir)
	}
}

// commonDir is the deepest directory that contains every one of dirs.
func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return "."
	}
	common := strings.Split(cleanBuildPath(dirs[0]), "/")
	if common[0] == "." {
		return "."
	}
	for _, dir := range dirs[1:] {
		parts := strings.Split(cleanBuildPath(dir), "/")
		if parts[0] == "." {
			return "."
		}
		keep := 0
		for keep < len(common) && keep < len(parts) && common[keep] == parts[keep] {
			keep++
		}
		common = common[:keep]
		if keep == 0 {
			return "."
		}
	}
	return strings.Join(common, "/")
}

// relativeBuildPath is target's path under dir, "" when it is dir itself;
// both are checkout-relative and target lies inside dir.
func relativeBuildPath(dir, target string) string {
	dir, target = cleanBuildPath(dir), cleanBuildPath(target)
	if dir == target {
		return ""
	}
	if dir == "." {
		return target
	}
	return strings.TrimPrefix(target, dir+"/")
}

// checkoutRoot turns a candidate root ("" for the top) into the reader's form.
func checkoutRoot(root string) string {
	if root == "" {
		return "."
	}
	return cleanBuildPath(root)
}

// contextLabel is a checkout directory as evidence and plans name it.
func contextLabel(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}
