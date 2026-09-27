package updates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// The rest of what a package manager is for.
//
// This package began as "how far behind is this server", which answers one
// question and leaves the operator in an SSH session for every other one:
// what is actually installed, what is that thing for, how do I get rid of it,
// how do I add the tool I need. Those are the questions a package page is
// opened to answer, and each of them is spelled six different ways across the
// distributions this dashboard runs on — which is exactly the argument the
// manager interface already makes for upgrades.
//
// So the interface grew a second half rather than the frontend growing a
// switch on `manager`. Everything below is either a listing command parsed by
// a pure function, or an argv handed to a job. Nothing here runs a shell, and
// nothing here executes a package's own binaries — see usage.go for why that
// last one is a rule rather than an omission.

// InstalledPackage is one package present on this host.
type InstalledPackage struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"arch,omitempty"`
	// Summary is the one-line description the package ships. apk publishes
	// none in its listing, so it is empty there rather than invented.
	Summary string `json:"summary,omitempty"`
	// Size is the installed size in bytes, or 0 where the manager does not
	// report one. A sortable size column is most of the reason to look at a
	// list of two thousand packages at all.
	Size int64 `json:"size,omitempty"`
	// Section is the manager's own grouping — apt's section, rpm's group,
	// pacman's repository.
	Section string `json:"section,omitempty"`
	// Explicit distinguishes what somebody asked for from what came along as
	// a dependency. It is the difference between a list you can read and two
	// thousand rows, and it is what "installed by hand" filters on.
	Explicit bool `json:"explicit"`
	// Essential marks a package the host cannot be without. Removing one is
	// refused rather than confirmed.
	Essential bool `json:"essential,omitempty"`
	// Upgradable carries the pending version where there is one, joined on
	// from the upgrade report so the list answers both questions at once.
	Upgradable string `json:"upgradable,omitempty"`
	Security   bool   `json:"security,omitempty"`
}

// CatalogEntry is one result from searching what the repositories offer.
type CatalogEntry struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Repository string `json:"repository,omitempty"`
	// Installed and InstalledVersion are filled in by the service rather than
	// by the manager, from the same index the installed list is drawn from —
	// so "install" never appears next to something already on the machine,
	// whichever manager answered.
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion,omitempty"`
}

// PackageDetail is everything worth showing about one package.
type PackageDetail struct {
	Name             string   `json:"name"`
	Version          string   `json:"version,omitempty"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	Installed        bool     `json:"installed"`
	Summary          string   `json:"summary,omitempty"`
	Description      string   `json:"description,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	License          string   `json:"license,omitempty"`
	Section          string   `json:"section,omitempty"`
	Repository       string   `json:"repository,omitempty"`
	Architecture     string   `json:"arch,omitempty"`
	Maintainer       string   `json:"maintainer,omitempty"`
	Size             int64    `json:"size,omitempty"`
	Dependencies     []string `json:"dependencies,omitempty"`
	Essential        bool     `json:"essential,omitempty"`
	// Protected is why this dashboard will not remove it, empty when it will.
	Protected  string `json:"protected,omitempty"`
	Upgradable string `json:"upgradable,omitempty"`
}

// Inventory is the answer to "what is on this machine".
type Inventory struct {
	Available bool               `json:"available"`
	Manager   string             `json:"manager,omitempty"`
	Packages  []InstalledPackage `json:"packages"`
	// ExplicitCount is how many of them somebody asked for, which is the
	// number worth putting on screen next to a total of two thousand.
	ExplicitCount int   `json:"explicitCount"`
	TotalSize     int64 `json:"totalSize,omitempty"`
	UpgradeCount  int   `json:"upgradeCount"`
	SecurityCount int   `json:"securityCount"`
	CanInstall    bool  `json:"canInstall"`
	CanPurge      bool  `json:"canPurge"`
	// IndexAge is when the repository index was last refreshed, and CanRefresh
	// whether this dashboard will do it. Both are nil/false where the manager
	// cannot answer, which is said on screen rather than guessed at.
	IndexAge   *time.Time `json:"indexAge,omitempty"`
	CanRefresh bool       `json:"canRefresh"`
	ReadAt     time.Time  `json:"readAt"`
	Error      string     `json:"error,omitempty"`
}

// catalogue is the half of a manager that is about packages rather than about
// how far behind they are. It is part of `manager` rather than an optional
// extra: every one of the six can do all of it, and a compiler error is a
// better way to learn about the seventh than a page that renders empty.
type catalogue interface {
	// ListInstalled enumerates what is on the machine now. It reads the local
	// database only — no manager here touches the network for this.
	ListInstalled(ctx context.Context) ([]InstalledPackage, error)
	// Search asks the repositories what matches. The index is whatever is on
	// disk: a search that refreshed first would take a minute per keystroke.
	Search(ctx context.Context, query string) ([]CatalogEntry, error)
	// Info describes one package, installed or not.
	Info(ctx context.Context, name string) (*PackageDetail, error)
	// Files lists what a package owns, which is where usage.go finds the
	// commands, man pages and units it reports.
	Files(ctx context.Context, name string) ([]string, error)
	// InstallCommand and RemoveCommand return argv rather than running it,
	// for the reason UpgradeCommand does: both are watched as jobs.
	InstallCommand(names []string) (string, []string, []string)
	RemoveCommand(names []string, purge bool) (string, []string, []string)
	// SupportsPurge reports whether "and delete its configuration" means
	// anything here. RPM keeps a modified config file as .rpmsave whatever it
	// is asked, so the UI hides the switch there rather than offering one that
	// silently does nothing.
	SupportsPurge() bool
	// IndexAge is when the repository index was last refreshed, which is the
	// answer to "is this list of 76,000 packages current". Everything else
	// here reads that index without touching the network; a host nobody has
	// run an update on for three months is searching a three-month-old
	// catalogue and has no way to know it.
	IndexAge() (time.Time, bool)
	// RefreshCommand returns the argv that fetches a new index, and false
	// where refreshing on its own is not a safe thing to do — see pacman.
	RefreshCommand() (name string, args []string, env []string, ok bool)
}

// ErrUnknownPackage is a name no repository and no local database knows.
var ErrUnknownPackage = errors.New("no package by that name")

// maxSearchResults bounds what one query can return.
//
// `apt-cache search e` matches most of Debian; a list of nine thousand rows is
// not a search result, it is a denial of service against the browser that
// asked for it. The cap is applied after ranking, so the exact match a person
// typed is never the row that got cut.
const maxSearchResults = 60

// installedTTL is how long the name → version index is reused for.
//
// Search annotates every result against it, and it is polled by a page where
// somebody is typing: reading two thousand rows from dpkg per keystroke is the
// obvious way to make a search box feel broken. Anything that changes the set
// — an install, a removal — invalidates it explicitly, so the staleness this
// admits is only ever somebody else's change, seen a few seconds late.
const installedTTL = 30 * time.Second

// Service is stateless apart from the installed-package index it caches.
type Service struct {
	mu          sync.Mutex
	index       []InstalledPackage
	indexedAt   time.Time
	indexedWith string
	generation  uint64
	mutations   int
	flight      *installedRead
}

type installedRead struct {
	done       chan struct{}
	cancel     context.CancelFunc
	generation uint64
	manager    string
	list       []InstalledPackage
	err        error
}

func New() *Service { return &Service{} }

// Available reports whether *any* supported package manager was found. It used
// to mean "apt is installed", which made every RPM, Alpine and Arch host look
// like a machine with nothing to update rather than one that was never
// checked — the worst possible failure for a security signal.
func (s *Service) Available() bool { return detect() != nil }

// Manager names what runs this host, so the UI can say apt or dnf rather than
// "packages".
func (s *Service) Manager() string {
	if m := detect(); m != nil {
		return m.Name()
	}
	return ""
}

// Inventory lists what is installed, with the pending upgrades joined on.
//
// The join happens here rather than in the browser because the two lists are
// keyed differently on half these managers — apt reports `libssl3` where dpkg
// reports `libssl3:amd64` — and a client-side join would silently miss those,
// which is precisely the set most likely to carry a security update.
func (s *Service) Inventory(ctx context.Context) (*Inventory, error) {
	inv := &Inventory{Packages: []InstalledPackage{}, ReadAt: time.Now().UTC()}
	m := detect()
	if m == nil {
		return inv, ErrNotSupported
	}
	inv.Available = true
	inv.Manager = m.Name()
	inv.CanInstall = true
	inv.CanPurge = m.SupportsPurge()
	_, _, _, inv.CanRefresh = m.RefreshCommand()
	if at, ok := m.IndexAge(); ok {
		inv.IndexAge = &at
	}

	list, err := s.installed(ctx, m, false)
	if err != nil {
		inv.Error = strings.TrimSpace(err.Error())
		return inv, nil
	}

	pending := map[string]Package{}
	if rep, err := m.List(ctx); err == nil {
		for _, p := range rep {
			pending[p.Name] = p
		}
	}

	for _, p := range list {
		if up, ok := pending[baseName(p.Name)]; ok {
			p.Upgradable = up.Candidate
			p.Security = up.Security
			inv.UpgradeCount++
			if up.Security {
				inv.SecurityCount++
			}
		}
		if p.Explicit {
			inv.ExplicitCount++
		}
		inv.TotalSize += p.Size
		inv.Packages = append(inv.Packages, p)
	}
	sort.Slice(inv.Packages, func(i, j int) bool { return inv.Packages[i].Name < inv.Packages[j].Name })
	return inv, nil
}

// baseName strips the architecture qualifier dpkg puts on a multi-arch name,
// which is the only place the two spellings differ.
func baseName(name string) string {
	if i := strings.IndexByte(name, ':'); i > 0 {
		return name[:i]
	}
	return name
}

// installed returns the cached index's packages, refreshing when stale.
func (s *Service) installed(ctx context.Context, m manager, force bool) ([]InstalledPackage, error) {
	if force {
		s.Invalidate()
	}
	for ctx.Err() == nil {
		s.mu.Lock()
		if s.indexedWith == m.Name() && time.Since(s.indexedAt) < installedTTL && s.index != nil {
			out := append([]InstalledPackage{}, s.index...)
			s.mu.Unlock()
			return out, nil
		}
		flight := s.flight
		if flight == nil || flight.manager != m.Name() {
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			flight = &installedRead{done: make(chan struct{}), cancel: cancel, generation: s.generation, manager: m.Name()}
			s.flight = flight
			go s.readInstalled(readCtx, m, flight)
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			s.mu.Lock()
			valid := flight.generation == s.generation
			s.mu.Unlock()
			if valid {
				return append([]InstalledPackage{}, flight.list...), flight.err
			}
		}
	}
	return nil, ctx.Err()
}

func (s *Service) readInstalled(ctx context.Context, m manager, flight *installedRead) {
	defer flight.cancel()
	started := time.Now()
	list, err := m.ListInstalled(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	flight.list, flight.err = list, err
	if flight.generation == s.generation && s.mutations == 0 && err == nil {
		// Keep every architecture and return copies: a hit must describe the
		// same installed set as the original package-manager read.
		s.index = append([]InstalledPackage{}, list...)
		s.indexedAt, s.indexedWith = started, m.Name()
	}
	if s.flight == flight {
		s.flight = nil
	}
	close(flight.done)
}

// Invalidate forgets the installed index, so the next read is the truth.
// Called when a job that changes the set has finished.
func (s *Service) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked()
}

func (s *Service) invalidateLocked() {
	s.generation++
	s.index, s.indexedAt = nil, time.Time{}
	if s.flight != nil {
		s.flight.cancel()
		s.flight = nil
	}
}

// BeginMutation brackets a package job, including a failed or cancelled one:
// managers can change part of the installed set before returning an error.
func (s *Service) BeginMutation() func() {
	s.mu.Lock()
	s.mutations++
	s.invalidateLocked()
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.mutations--
		s.invalidateLocked()
	}
}

// Search asks the repositories, ranks the answer and says what is already here.
func (s *Service) Search(ctx context.Context, query string) ([]CatalogEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []CatalogEntry{}, nil
	}
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	m := detect()
	if m == nil {
		return nil, ErrNotSupported
	}
	found, err := m.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	// Annotating from the index rather than from each manager's own "is it
	// installed" marker keeps the answer the same everywhere: pacman prints
	// [installed], apt prints nothing, and zypper prints a status letter that
	// means four different things.
	installed, _ := s.installed(ctx, m, false)
	byName := make(map[string]InstalledPackage, len(installed))
	for _, p := range installed {
		byName[baseName(p.Name)] = p
	}
	for i := range found {
		if p, ok := byName[baseName(found[i].Name)]; ok {
			found[i].Installed = true
			found[i].InstalledVersion = p.Version
		}
	}
	rankResults(found, query)
	if len(found) > maxSearchResults {
		found = found[:maxSearchResults]
	}
	return found, nil
}

// rankResults puts what the operator most likely meant at the top.
//
// A repository search is a substring match over thousands of names, so
// `apt-cache search git` answers with `git` somewhere around row four hundred.
// Ordering by how the name matches — exact, then prefix, then contained, then
// summary-only — is the whole difference between a search box and a list.
//
// The last bucket needs a rule of its own. Everything that got there matched a
// description rather than a name, so they are all equally unmatched by name,
// and breaking the tie on name length puts `nd` and `h2o` at the top of a
// search for "web server". Counting how many of the typed words the summary
// actually carries puts nginx and lighttpd there instead.
func rankResults(entries []CatalogEntry, query string) {
	q := strings.ToLower(query)
	terms := searchTerms(q)
	score := func(e CatalogEntry) int {
		name := strings.ToLower(e.Name)
		switch {
		case name == q:
			return 0
		case strings.HasPrefix(name, q):
			return 1
		case strings.Contains(name, q):
			return 2
		default:
			return 3
		}
	}
	hits := func(e CatalogEntry) int {
		haystack := strings.ToLower(e.Name + " " + e.Summary)
		n := 0
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				n++
			}
		}
		return n
	}
	sort.SliceStable(entries, func(i, j int) bool {
		si, sj := score(entries[i]), score(entries[j])
		if si != sj {
			return si < sj
		}
		if hi, hj := hits(entries[i]), hits(entries[j]); hi != hj {
			return hi > hj
		}
		if len(entries[i].Name) != len(entries[j].Name) {
			return len(entries[i].Name) < len(entries[j].Name)
		}
		return entries[i].Name < entries[j].Name
	})
}

// searchTerms splits a query into the words worth counting. Anything shorter
// than three characters matches half the archive as a substring and tells you
// nothing about which half you wanted.
func searchTerms(q string) []string {
	terms := []string{}
	for _, word := range strings.FieldsFunc(q, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(word) >= 3 {
			terms = append(terms, word)
		}
	}
	return terms
}

// thinSearch is when a name-only search has found too little to be the answer.
//
// Matching names is what makes `nginx` return nginx rather than the four
// hundred packages whose description mentions it — but it also means "web
// server" finds *nothing*, because no package is called that. Somebody who
// knows the name types the name; somebody who does not types what the software
// does, and that person is the whole reason this page exists. So a thin result
// widens to the descriptions, which every one of these tools can search and
// none of them does by default.
const thinSearch = 5

// widen runs a second, broader search and appends what the first did not find.
// The name matches keep their position — rankResults sorts the merged list, and
// a name match outranks a description match by construction.
func widen(first []CatalogEntry, second []CatalogEntry) []CatalogEntry {
	seen := make(map[string]bool, len(first))
	for _, e := range first {
		seen[e.Name] = true
	}
	for _, e := range second {
		if !seen[e.Name] {
			first = append(first, e)
			seen[e.Name] = true
		}
	}
	return first
}

// Describe answers everything the detail sheet shows about one package.
func (s *Service) Describe(ctx context.Context, name string) (*PackageDetail, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	m := detect()
	if m == nil {
		return nil, ErrNotSupported
	}
	detail, err := m.Info(ctx, name)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, ErrUnknownPackage
	}
	installed, _ := s.installed(ctx, m, false)
	for _, p := range installed {
		if baseName(p.Name) != baseName(name) {
			continue
		}
		detail.Installed = true
		detail.InstalledVersion = p.Version
		detail.Essential = detail.Essential || p.Essential
		if detail.Size == 0 {
			detail.Size = p.Size
		}
		break
	}
	if detail.Installed {
		if pending, err := m.List(ctx); err == nil {
			for _, p := range pending {
				if baseName(p.Name) == baseName(name) {
					detail.Upgradable = p.Candidate
					break
				}
			}
		}
	}
	detail.Protected = protectedReason(baseName(name), detail.Essential)
	return detail, nil
}

// Usage answers "now that it is installed, how do I use it" — see usage.go.
func (s *Service) Usage(ctx context.Context, name string) (*Usage, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	m := detect()
	if m == nil {
		return nil, ErrNotSupported
	}
	files, err := m.Files(ctx, name)
	if err != nil {
		return nil, err
	}
	return buildUsage(ctx, baseName(name), files), nil
}

// RefreshCommand returns the argv that fetches a new repository index.
func (s *Service) RefreshCommand() (string, []string, []string, error) {
	m := detect()
	if m == nil {
		return "", nil, nil, ErrNotSupported
	}
	name, args, env, ok := m.RefreshCommand()
	if !ok {
		return "", nil, nil, fmt.Errorf(
			"%s cannot refresh its database without also upgrading: doing one without the other is the documented way to break the system, so the upgrade is the refresh here", m.Name())
	}
	return name, args, env, nil
}

// InstallCommand returns the argv that adds packages.
func (s *Service) InstallCommand(names []string) (string, []string, []string, error) {
	m := detect()
	if m == nil {
		return "", nil, nil, ErrNotSupported
	}
	clean, err := cleanNames(names)
	if err != nil {
		return "", nil, nil, err
	}
	name, args, env := m.InstallCommand(clean)
	return name, args, env, nil
}

// RemoveCommand returns the argv that takes packages away, having refused the
// ones that would take the machine with them.
func (s *Service) RemoveCommand(ctx context.Context, names []string, purge bool) (string, []string, []string, error) {
	m := detect()
	if m == nil {
		return "", nil, nil, ErrNotSupported
	}
	clean, err := cleanNames(names)
	if err != nil {
		return "", nil, nil, err
	}
	installed, _ := s.installed(ctx, m, false)
	essential := map[string]bool{}
	for _, p := range installed {
		if p.Essential {
			essential[baseName(p.Name)] = true
		}
	}
	for _, n := range clean {
		if reason := protectedReason(baseName(n), essential[baseName(n)]); reason != "" {
			return "", nil, nil, fmt.Errorf("%s cannot be removed from here: %s", n, reason)
		}
	}
	name, args, env := m.RemoveCommand(clean, purge)
	return name, args, env, nil
}

// cleanNames validates and de-duplicates the names a client asked for.
func cleanNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, errors.New("name at least one package")
	}
	if len(names) > 50 {
		return nil, errors.New("that is more packages than one operation should carry; do it in batches")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if err := validateName(n); err != nil {
			return nil, err
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// validateName refuses what a package name may not be.
//
// These go into an argv and never through a shell, so quoting is not the
// worry. The worry is that every one of these tools reads a leading dash as a
// flag and several of them accept a path to a package *file* or a `name=
// version` pin in the same position — so an unvalidated name is not a package
// name at all, it is whatever the caller wanted the command to do.
func validateName(name string) error {
	if name == "" {
		return errors.New("a package name is required")
	}
	if len(name) > 128 {
		return errors.New("that is not a package name")
	}
	if !isNameStart(name[0]) {
		return fmt.Errorf("%q is not a package name — they begin with a letter or a digit", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isNameStart(c) || strings.IndexByte("._+-:@", c) >= 0 {
			continue
		}
		return fmt.Errorf("%q is not a package name", name)
	}
	return nil
}

func isNameStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// validateQuery bounds what a search term may be. It is looser than a package
// name — people search for "web server" — but it is still an argument to a
// command, and apt-cache reads its query as a regular expression.
func validateQuery(q string) error {
	if len(q) > 96 {
		return errors.New("that search is too long")
	}
	if q[0] == '-' {
		return errors.New("a search cannot begin with a dash")
	}
	for _, r := range q {
		if r < 0x20 || r == 0x7f {
			return errors.New("that search contains a control character")
		}
	}
	return nil
}

// newestMtime is when any of these paths was last written, checking the host's
// filesystem through /host as well as this one's.
//
// A directory is read one level deep rather than trusted for its own mtime:
// /var/lib/apt/lists has a lock file in it that is touched by operations that
// fetch nothing, so the directory's own timestamp says "refreshed" on a host
// where the refresh failed. The files inside it are written only by a fetch
// that succeeded.
func newestMtime(paths ...string) (time.Time, bool) {
	var newest time.Time
	for _, base := range []string{"", "/host"} {
		for _, path := range paths {
			info, err := os.Stat(base + path)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				if info.ModTime().After(newest) {
					newest = info.ModTime()
				}
				continue
			}
			entries, err := os.ReadDir(base + path)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() || strings.Contains(entry.Name(), "lock") {
					continue
				}
				if ei, err := entry.Info(); err == nil && ei.ModTime().After(newest) {
					newest = ei.ModTime()
				}
			}
		}
	}
	return newest.UTC(), !newest.IsZero()
}

// protectedReason names the packages this dashboard refuses to remove.
//
// It is deliberately narrow, in the spirit of guardSSHLockout: the only
// entries are the ones whose removal certainly ends either the machine or the
// operator's ability to reach it, and everything else — including things that
// look important — goes through the ordinary confirmation. A guard that
// second-guessed every risky removal would be a guard nobody could work with,
// and the operator who wants one of these gone has a root shell two clicks
// away, where the tool's own warning is the right place to read it.
func protectedReason(name string, essential bool) string {
	if essential {
		return "the package database marks it essential, and removing it breaks the system"
	}
	switch name {
	case "apt", "apt-get", "apt-utils", "dpkg", "rpm", "dnf", "dnf5", "yum", "zypper", "pacman", "apk-tools":
		return "it is this host's package manager, and removing it leaves nothing that can put it back"
	case "systemd", "systemd-sysv", "init", "sysvinit-core", "openrc":
		return "it is the init system, and the machine does not boot without it"
	case "bash", "sh", "dash", "busybox", "coreutils", "util-linux", "libc6", "glibc", "musl", "base-files", "filesystem", "alpine-baselayout":
		return "the rest of the system is built on it"
	case "openssh-server", "openssh", "sshd":
		return "it is how you get back into this machine when the dashboard is what broke"
	case "docker", "docker-ce", "docker.io", "containerd", "containerd.io", "podman":
		return "this dashboard runs as a container on it, so removing it takes the dashboard down with it"
	case "sudo", "linux-image-generic", "kernel", "kernel-core", "linux", "linux-lts":
		return "removing it from a web page is not something that ends well; do it from a real shell if you mean it"
	}
	if strings.HasPrefix(name, "linux-image-") || strings.HasPrefix(name, "kernel-") {
		return "it is a kernel, and removing the running one leaves nothing to boot"
	}
	return ""
}
