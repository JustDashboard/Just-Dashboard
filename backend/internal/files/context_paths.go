package files

import (
	"os"
	"path/filepath"
	"strings"
)

// ContextPaths is the folder on screen as the server-context read compares
// it with what the other pages know: the folder through the roots like any
// client path, each entry by the path the listing gives it and what it
// resolves to, and the inventories' own paths resolved the same way, so a
// site whose root is spelled through a symlink still matches the folder it
// really is.
type ContextPaths struct {
	Folder   string
	Children []ContextChild
	// Known maps each inventory path asked about to what it resolves to.
	Known map[string]string
	// Truncated says the folder held more entries than were read.
	Truncated bool
}

// ContextChild is one entry: Path as the listing names it, Real what it is.
type ContextChild struct {
	Path string
	Real string
}

// maxContextLinks bounds how many symlinked entries are followed for one
// answer. A folder of a thousand links is unusual and each costs a resolve.
const maxContextLinks = 500

// ContextPaths resolves the folder and reads its entries without the stat
// and owner lookups a listing makes, so it costs a directory read and a
// resolve per symlink. An entry that resolves outside the roots keeps its own
// path and matches nothing it points at. The known paths are only compared,
// never opened, and are resolved from their nearest existing ancestor.
func (s *Service) ContextPaths(path string, showHidden bool, known []string, limit int) (*ContextPaths, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (*ContextPaths, error) {
			return s.ContextPaths(path, showHidden, known, limit)
		})
	}
	dir, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDir
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := &ContextPaths{Folder: dir, Children: []ContextChild{}, Known: map[string]string{}}
	// One more than the limit says whether there was more.
	entries, _ := f.ReadDir(limit + 1)
	if limit > 0 && len(entries) > limit {
		entries, out.Truncated = entries[:limit], true
	}
	links := 0
	for _, d := range entries {
		name := d.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		child := ContextChild{Path: filepath.Join(dir, name)}
		child.Real = child.Path
		if d.Type()&os.ModeSymlink != 0 && links < maxContextLinks {
			links++
			if real, err := s.Resolve(child.Path); err == nil {
				child.Real = real
			}
		}
		out.Children = append(out.Children, child)
	}
	for _, p := range known {
		if _, done := out.Known[p]; done || !filepath.IsAbs(p) {
			continue
		}
		clean := filepath.Clean(p)
		if real, err := resolveFromExistingAncestor(clean); err == nil {
			out.Known[p] = real
		} else {
			out.Known[p] = clean
		}
	}
	return out, nil
}
