package files

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Places are where a file manager should open, and where it should be able to
// get back to in one click.
//
// The old page opened at "/", which is the one directory on a Linux server
// where nothing an operator owns lives: every visit began with the same two
// clicks past bin, boot, dev and proc. The answer is not a configuration
// setting — the machine already knows where the interesting directories are,
// and the ones that matter on a server are the same everywhere.
//
// Everything here is checked against the roots before it is offered. A place
// that does not exist, or that JD_FILE_ROOTS puts out of reach, is simply not
// in the list: a shortcut that lands on "outside the permitted roots" is worse
// than no shortcut.
type Place struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Kind groups the rail: home, root (a configured JD_FILE_ROOTS entry),
	// user (a home directory under /home) and notable (the handful of places
	// a server keeps its work in).
	Kind string `json:"kind"`
	Hint string `json:"hint,omitempty"`
}

// notablePlaces is deliberately short and deliberately server-shaped. These
// are the directories somebody administering one Linux box actually opens;
// a longer list would be a menu to read rather than a row of shortcuts.
var notablePlaces = []struct{ path, hint string }{
	{"/etc", "System configuration"},
	{"/var/www", "Web roots"},
	{"/var/log", "Log files"},
	{"/opt", "Optional software"},
	{"/srv", "Served data"},
	{"/usr/local", "Locally installed software"},
	{"/tmp", "Temporary files"},
}

// Home is where the page opens when nothing else says otherwise.
//
// $HOME first, because that is the account this process runs as and the one a
// shell opened from this dashboard lands in; then /root, then a single home
// directory under /home if there is exactly one — a one-tenant VPS is the
// common case and "the only account on the machine" is not a guess. Anything
// unreachable falls through to the first configured root, which always
// resolves by definition.
func (s *Service) Home() string {
	candidates := []string{}
	if h := os.Getenv("HOME"); h != "" {
		candidates = append(candidates, h)
	}
	candidates = append(candidates, "/root")
	if users := s.homeDirs(); len(users) == 1 {
		candidates = append(candidates, users[0])
	}
	for _, c := range candidates {
		if full, err := s.Resolve(c); err == nil {
			if st, err := os.Stat(full); err == nil && st.IsDir() {
				return full
			}
		}
	}
	return s.roots[0]
}

// homeDirs lists the account home directories under /home, sorted. It reads
// the directory rather than /etc/passwd on purpose: this is a shortcut list,
// not an account inventory, and a home with no login shell is still a folder
// worth opening.
func (s *Service) homeDirs() []string {
	entries, err := os.ReadDir("/home")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join("/home", e.Name())
		if resolved, err := s.Resolve(full); err == nil {
			out = append(out, resolved)
		}
	}
	sort.Strings(out)
	return out
}

// Places is the rail: home, then the configured roots, then the accounts, then
// the notable directories — each one checked, and each one listed once.
func (s *Service) Places() []Place {
	seen := map[string]bool{}
	out := []Place{}
	add := func(p Place) {
		if p.Path == "" || seen[p.Path] {
			return
		}
		seen[p.Path] = true
		out = append(out, p)
	}

	home := s.Home()
	add(Place{Name: filepath.Base(home), Path: home, Kind: "home", Hint: "Where this dashboard starts"})

	for _, root := range s.roots {
		name := root
		if root != "/" {
			name = filepath.Base(root)
		}
		add(Place{Name: name, Path: root, Kind: "root", Hint: "Permitted root"})
	}
	for _, dir := range s.homeDirs() {
		add(Place{Name: filepath.Base(dir), Path: dir, Kind: "user", Hint: "Account home"})
	}
	for _, n := range notablePlaces {
		full, err := s.Resolve(n.path)
		if err != nil {
			continue
		}
		if st, err := os.Stat(full); err != nil || !st.IsDir() {
			continue
		}
		add(Place{Name: n.path, Path: full, Kind: "notable", Hint: n.hint})
	}
	return out
}

// Complete answers the path bar: everything under the typed prefix's directory
// whose name starts with the typed fragment.
//
// It is the other half of typing a path by hand. A text field with no
// completion is a field you have to be right in first time, on a server whose
// directory names you are trying to remember — which is the reason people go
// back to a shell, where tab has worked since 1983.
func (s *Service) Complete(prefix string, limit int) []Entry {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	dir, fragment := prefix, ""
	// A prefix ending in a separator means "inside this directory"; anything
	// else means the last component is what is being typed.
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		dir, fragment = filepath.Dir(prefix), filepath.Base(prefix)
		if prefix == "" {
			dir, fragment = s.Home(), ""
		}
	}
	full, err := s.Resolve(dir)
	if err != nil {
		return []Entry{}
	}
	names, err := os.ReadDir(full)
	if err != nil {
		return []Entry{}
	}
	lower := strings.ToLower(fragment)
	out := []Entry{}
	owners := newEntryNames()
	for _, d := range names {
		if len(out) >= limit {
			break
		}
		name := d.Name()
		if !strings.HasPrefix(strings.ToLower(name), lower) {
			continue
		}
		// A typed dot is a request for the dotfiles; without one they stay out
		// of the way, exactly as the listing's own hidden switch has it.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(fragment, ".") {
			continue
		}
		e, err := s.entryWithNames(filepath.Join(full, name), name, owners)
		if err != nil {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
