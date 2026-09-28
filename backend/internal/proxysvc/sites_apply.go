package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// SiteResult reports what happened to a site, in order.
type SiteResult struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	Content    string            `json:"content"`
	Warnings   []string          `json:"warnings"`
	Validation *ValidationResult `json:"validation,omitempty"`
	// Conflicts are this site's names that nginx found another server block
	// already answering on the same address. A save is refused over them
	// unless it allows them, since nginx serves each name from one block
	// only and "ignores" the other with nothing but a warning.
	Conflicts []ServerNameConflict `json:"conflicts,omitempty"`
	// TestWarnings are the test's warnings placed in this site's own file.
	TestWarnings []Diagnostic `json:"testWarnings,omitempty"`
	Enabled      bool         `json:"enabled"`
	// TestedAsEnabled says a site saved disabled was tested with its link in
	// place for the length of the test. Its Validation, Conflicts and
	// TestWarnings are then what enabling it would meet, and none of them
	// refused the save: nginx does not read the file while it is disabled.
	TestedAsEnabled bool `json:"testedAsEnabled,omitempty"`
	// ServedCopy says sites-enabled/<name> is a file of its own rather than
	// a link: nginx serves that file under the site's name and does not read
	// this one, so the save changed nothing nginx serves and was not tested.
	ServedCopy bool `json:"servedCopy,omitempty"`
	// Backup is where a file this dashboard did not write was kept before the
	// save replaced it with what the form produces.
	Backup   string `json:"backup,omitempty"`
	Reloaded bool   `json:"reloaded"`
	// ReloadError is why nginx did not reload a configuration that tested
	// clean. The site is written and in place, and nginx has not picked it
	// up: a running nginx serves what it served before, and one that is not
	// running — the usual cause — serves nothing until it is started.
	ReloadError string `json:"reloadError,omitempty"`
	Output      string `json:"output,omitempty"`
}

// ServerNameConflict is one of a site's names that another server block
// also claims on the same address.
type ServerNameConflict struct {
	Domain string `json:"domain"`
	// Listen is the address as nginx names it: 0.0.0.0:80, [::]:443.
	Listen string `json:"listen"`
	// Site is the listed site behind the other claim; empty when the other
	// block is not one of the listed sites, such as one in nginx.conf.
	Site string `json:"site,omitempty"`
	// Effect is which of the two nginx answers the name from: ConflictIgnored,
	// ConflictTakes or ConflictKeeps. Empty when the order nginx reads the
	// two in could not be read, and then the conflict says only that both
	// claim the name.
	Effect string `json:"effect,omitempty"`
}

// nginx answers a name on an address from the first server block it reads
// that claims it, in include order, and ignores the rest.
const (
	// ConflictIgnored: the other block comes first and keeps the name; this
	// site's claim is ignored.
	ConflictIgnored = "ignored"
	// ConflictTakes: this site comes first and takes the name from the
	// other block, which answered it until now.
	ConflictTakes = "takes"
	// ConflictKeeps: this site already answered the name and still does;
	// the other block's claim was ignored before the save and still is.
	ConflictKeeps = "keeps"
)

// ErrServerNameConflict refuses a save that changes which block answers a
// name, or whose own claim nginx would ignore.
var ErrServerNameConflict = errors.New("another server block already answers to one of this site's names")

// errSiteReloadFailed marks a save that tested clean and is in place, and
// that nginx did not pick up.
var errSiteReloadFailed = errors.New("reload failed")

// SiteSave is how a site is saved.
type SiteSave struct {
	// Enable links the site into sites-enabled. Without it the link is left
	// as it was, so saving a disabled site keeps it disabled — the form used
	// to enable every site it saved. Such a site is still tested as it would
	// be enabled, and the result says what that test found.
	Enable bool
	Reload bool
	// Overwrite replaces a site of the same name; without it one is refused.
	Overwrite bool
	// AllowConflict saves a site even when another server block claims one
	// of its names on the same address and the save changes who answers it
	// or leaves this site's claim ignored.
	AllowConflict bool
	// BaseDigest is the ContentDigest of the file the form read. When set,
	// the save is refused unless the file is still exactly that, since
	// writing the form's reading of an older version would silently undo
	// whatever changed it since.
	BaseDigest string
}

// ApplySite is SaveSite without allowing a server-name conflict.
func (s *Service) ApplySite(ctx context.Context, spec *SiteSpec, enable, reload, overwrite bool) (*SiteResult, error) {
	return s.SaveSite(ctx, spec, SiteSave{Enable: enable, Reload: reload, Overwrite: overwrite})
}

// SaveSite writes a site, enables it when asked, tests the whole
// configuration and puts everything back if the test fails.
//
// The order matters and is different from the plain config editor's. A brand
// new file in sites-available is not in nginx's include tree, so `nginx -t`
// has nothing to say about it — the existing editor documents that gap. Here
// the symlink goes in *before* the test, which is what makes the test mean
// something, and both the file and the link are undone together if it fails.
//
// A reload that fails after a clean test is not an error here: the site was
// saved, and the result says what nginx did not do. Nor is a site saved
// disabled refused over its test: nginx does not read it, and the result
// carries what enabling it would meet.
func (s *Service) SaveSite(ctx context.Context, spec *SiteSpec, opts SiteSave) (*SiteResult, error) {
	content, err := RenderNginx(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.saveSiteLocked(ctx, spec, content, opts)
	if errors.Is(err, errSiteReloadFailed) {
		return res, nil
	}
	return res, err
}

// applySiteLocked performs ApplySite after rendering. The caller holds s.mu;
// deployment cutovers use this form so snapshot, apply, and recovery are one
// serialized proxy transaction. They are not refused over a server-name
// conflict, and a failed reload is their error: their recovery is built on
// nginx's own test and reload.
func (s *Service) applySiteLocked(ctx context.Context, spec *SiteSpec, content string, enable, reload, overwrite bool) (*SiteResult, error) {
	return s.saveSiteLocked(ctx, spec, content, SiteSave{
		Enable: enable, Reload: reload, Overwrite: overwrite, AllowConflict: true,
	})
}

func (s *Service) saveSiteLocked(ctx context.Context, spec *SiteSpec, content string, opts SiteSave) (*SiteResult, error) {
	full, confd, err := s.siteTarget(spec.Name)
	if err != nil {
		return nil, err
	}
	original, existed := readIfPresent(full)
	if existed && !opts.Overwrite {
		return nil, fmt.Errorf("a site called %s already exists", spec.Name)
	}
	if opts.BaseDigest != "" && (!existed || ContentDigest(original) != opts.BaseDigest) {
		if !existed {
			return nil, fmt.Errorf("%w: %s is not there any more", ErrSiteChanged, full)
		}
		return nil, fmt.Errorf("%w: %s is not the version the form read", ErrSiteChanged, full)
	}
	link := filepath.Join(s.nginxDir, "sites-enabled", spec.Name)
	// read is whether nginx reads the file as it stands: a conf.d file whose
	// name ends in .conf, or one linked into sites-enabled — under its own
	// name, or under another such as 010-app, which enables it all the same.
	// Taking the second for disabled staged a second link, so nginx read the
	// file twice, reported the site conflicting with itself, and the edit
	// was never reloaded.
	read := false
	elsewhere, via := "", ""
	if confd {
		read = strings.HasSuffix(full, ".conf")
		if !read && opts.Enable {
			return nil, fmt.Errorf("conf.d/%s is off: nginx reads only the conf.d files whose names end in .conf, so it is enabled by renaming it", filepath.Base(full))
		}
	} else {
		// A link of this name that enables another file is that site's.
		// Linking this one in its place unlinks it before nginx -t runs, so
		// the test never sees the two claim one name and the site just stops.
		elsewhere = enabledElsewhere(link, full)
		if elsewhere != "" && (!opts.Overwrite || opts.Enable) {
			return nil, fmt.Errorf("%s — pick another name, or move that link aside first", elsewhere)
		}
		via = enablingLink(link, full)
		read = via != ""
	}
	// What nginx read of this file before the save: a name the site wins
	// after it is only taken from someone if the site did not answer it
	// already.
	var previous []Directive
	if existed && read {
		previous, _ = ParseNginxFile(full, original, []string{"http"})
	}

	res := &SiteResult{
		Name: spec.Name, Path: full, Content: content, Warnings: SpecWarnings(spec),
	}
	s.keepLoaded(full, filepath.Join(s.nginxDir, "sites-enabled", spec.Name))
	undoLink, undoBackup := func() {}, func() {}
	rollback := func() {
		undoLink()
		restoreConfig(full, original, existed)
		undoBackup()
	}
	// A file this dashboard did not write is replaced by what the form reads
	// of it, and anything else it held — a comment, a directive the form has
	// no field for — went with no way back while the form said the previous
	// version was kept as .bak. It is kept there now, before the write.
	if existed && !strings.Contains(original, managedMarker) {
		undo, err := keepBackup(full, original)
		if err != nil {
			return nil, fmt.Errorf("%s was written by hand and could not be kept as %s.bak, so it was not replaced: %w",
				filepath.Base(full), filepath.Base(full), err)
		}
		undoBackup, res.Backup = undo, full+".bak"
	}
	if err := writeAtomic(full, content); err != nil {
		undoBackup()
		return nil, err
	}
	// trial is the copy of the configuration a site saved disabled is tested
	// in, with it enabled.
	var trial *trialConfig
	untested := ""
	switch {
	case confd && read, via != "" && via != spec.Name:
		// nginx reads the file where it is: every conf.d file ending in
		// .conf, or one linked under another name. There is no link to make.
		res.Enabled = true
	case read || opts.Enable:
		// A site with its own link keeps it, relinked so that it names the
		// file being saved.
		undo, err := linkEnabled(link, full)
		if err != nil {
			// The write is undone rather than left standing: a file in
			// sites-available that nginx does not include is invisible
			// everywhere except the next person to wonder why the site is
			// not serving.
			rollback()
			return nil, err
		}
		undoLink = undo
		res.Enabled = true
	case servedCopy(link):
		// nginx reads the file of its own under this name, so this one is
		// neither served nor tested, and "disabled" was not true of the site.
		res.ServedCopy = true
		untested = fmt.Sprintf("nginx serves sites-enabled/%s, a file of its own, and not this one, so it did not test this file and the save changes nothing it serves.", spec.Name)
	case elsewhere != "":
		untested = elsewhere + trialUntested
	default:
		// A site saved disabled is tested as it would be enabled: in
		// sites-enabled under its name, or in conf.d with the .conf its name
		// lacks. Without that `nginx -t` never read the file, and "saved"
		// said nothing about the day somebody enables it.
		from, entry := filepath.Dir(link), spec.Name
		if confd {
			from, entry = filepath.Dir(full), filepath.Base(full)+".conf"
		}
		trial, untested = s.stageTrial(from, entry, full)
		res.TestedAsEnabled = trial != nil
	}

	if trial != nil {
		res.Validation = trial.validate(ctx)
	} else {
		res.Validation = runValidator(ctx, "nginx", "-t")
	}
	if !res.Validation.Valid && trial == nil {
		rollback()
		res.Enabled, res.Backup = false, ""
		return res, ErrInvalidConf
	}
	// Only a test that read the file says anything about its names.
	if res.Validation.Valid && (res.Enabled || trial != nil) {
		// nginx passes a second server block claiming a name on an address
		// the first already answers it on, and serves only the first in
		// include order. A save that takes a domain from a working site, or
		// whose own claim is ignored, is refused unless allowed; one that
		// leaves a name with the site that already answered it is not,
		// since refusing it blocks an edit to the site that is serving.
		res.Conflicts = s.serverNameConflicts(res.Validation, spec.Domains, content, full)
		if len(res.Conflicts) > 0 {
			dump := s.dumpNginx
			if trial != nil {
				dump = trial.dump
			}
			if files, err := dump(ctx); err == nil {
				s.orderConflicts(res.Conflicts, full, previous, files)
			}
		}
	}
	res.TestWarnings = warningsIn(res.Validation, full)
	if trial != nil {
		// Whatever the test said: a failing test or a contested name is what
		// enabling the site would meet, and the result says so, but it is no
		// reason to refuse a file nginx does not read.
		trial.remove()
	} else if refusesConflicts(res.Conflicts) && !opts.AllowConflict {
		rollback()
		res.Enabled, res.Backup = false, ""
		return res, ErrServerNameConflict
	}
	if untested != "" {
		res.Validation.Note = untested
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	// Only a configuration that passed its test is reloaded. A disabled
	// site's test failing with it enabled says nothing about the files
	// nginx reads now, which have not been tested without it.
	if opts.Reload && res.Validation.Valid {
		raw, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
		out := strings.TrimSpace(string(raw))
		res.Output = out
		if err != nil {
			// The config tested clean, so a reload failure is about the
			// running process rather than the file. Undoing the write would
			// lose the operator's work for a problem it did not cause.
			res.ReloadError = out
			if res.ReloadError == "" {
				res.ReloadError = err.Error()
			}
			return res, fmt.Errorf("%w: %s", errSiteReloadFailed, res.ReloadError)
		}
		res.Reloaded = true
	}
	return res, nil
}

// conflictingNameRe is nginx's warning for a server name a second block
// claims on an address the first already answers it on.
var conflictingNameRe = regexp.MustCompile(`^conflicting server name "([^"]*)" on (\S+), ignored$`)

var siteListenRe = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)

// serverNameConflicts reads nginx's own conflict warnings for this site's
// names on the addresses its file listens on. nginx's verdict rather than a
// comparison of listings: it already knows that 127.0.0.1:80 and a wildcard
// :80 are separate, and that a name is compared lowercased.
func (s *Service) serverNameConflicts(v *ValidationResult, domains []string, content, full string) []ServerNameConflict {
	names := map[string]bool{}
	for _, domain := range domains {
		names[strings.ToLower(domain)] = true
	}
	addresses := map[string]bool{}
	for _, m := range siteListenRe.FindAllStringSubmatch(content, -1) {
		addresses[listenAddress(m[1])] = true
	}
	out := []ServerNameConflict{}
	seen := map[string]bool{}
	var others []VHost
	listed := false
	for _, d := range v.Diagnostics {
		m := conflictingNameRe.FindStringSubmatch(d.Message)
		if d.Level != "warn" || m == nil || !names[m[1]] || !addresses[m[2]] {
			continue
		}
		// nginx warns once per address, so a site on 0.0.0.0:80 and [::]:80
		// is told once per port.
		key := m[1] + " " + listenPort(m[2])
		if seen[key] {
			continue
		}
		seen[key] = true
		if !listed {
			others, listed = s.nginxVHosts(), true
		}
		out = append(out, ServerNameConflict{
			Domain: m[1], Listen: m[2], Site: servingSite(others, full, m[1], m[2]),
		})
	}
	return out
}

// servingSite is the enabled site, other than the one at full, that lists
// name on address.
func servingSite(sites []VHost, full, name, address string) string {
	for _, v := range sites {
		if !v.Enabled || resolvedFile(v.Path) == full {
			continue
		}
		named := false
		for _, n := range v.ServerNames {
			named = named || strings.EqualFold(n, name)
		}
		if !named {
			continue
		}
		for _, listen := range v.Listen {
			if listenAddress(listen) == address {
				return v.Name
			}
		}
	}
	return ""
}

// listenAddress is a listen value's address the way nginx names it in a
// warning: `listen 80` binds 0.0.0.0:80, and an address with no port is on
// 80.
func listenAddress(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	address := fields[0]
	if _, err := strconv.Atoi(address); err == nil {
		return "0.0.0.0:" + address
	}
	host, port := address, "80"
	if i := strings.LastIndex(address, ":"); i >= 0 && !strings.HasSuffix(address, "]") {
		host, port = address[:i], address[i+1:]
	}
	if host == "*" {
		host = "0.0.0.0"
	}
	return host + ":" + port
}

// listenPort is the port of an address as listenAddress names it.
func listenPort(address string) string {
	return address[strings.LastIndex(address, ":")+1:]
}

// orderConflicts says for each conflict which of the two claims nginx
// answers from, reading the order from `nginx -T`: nginx keeps the first
// server block it reads that claims a name on an address, and the dump's
// tree is that order, includes and all — sites-enabled/* sorted, a conf.d
// file before it or after it as nginx.conf includes them. It names the other
// claim's site from the same tree, which beats the listing: the listing
// cannot tell the site that answers from the one that is ignored.
//
// files is that dump — of the live configuration, or of the trial one a
// disabled site is tested in — and previous is the file as nginx read it
// before the save, nil when it did not read it at all. A conflict whose
// order cannot be read is left without an Effect, and with the site the
// listing gave it.
func (s *Service) orderConflicts(conflicts []ServerNameConflict, full string, previous []Directive, files []ConfigFile) {
	tree, err := NginxTree(files)
	if err != nil {
		return
	}
	blocks := serverBlocks(tree)
	self := resolvedFile(full)
	var sites []VHost
	listed := false
	for i := range conflicts {
		c := &conflicts[i]
		mineFirst, mineSeen := false, false
		var other *Directive
		for j := range blocks {
			if !claimsName(blocks[j], c.Domain, c.Listen) {
				continue
			}
			mine := resolvedFile(blocks[j].File) == self
			if !mineSeen && other == nil {
				mineFirst = mine
			}
			if mine {
				mineSeen = true
			} else if other == nil {
				other = &blocks[j]
			}
		}
		if !mineSeen || other == nil {
			continue
		}
		if !listed {
			sites, listed = s.nginxVHosts(), true
		}
		c.Site = siteOfFile(sites, other.File)
		switch {
		case !mineFirst:
			c.Effect = ConflictIgnored
		case claimsIn(serverBlocks(previous), c.Domain, c.Listen):
			c.Effect = ConflictKeeps
		default:
			c.Effect = ConflictTakes
		}
	}
}

// refusesConflicts says whether a save is refused over its conflicts: every
// one that is not the site keeping a name it already answered.
func refusesConflicts(conflicts []ServerNameConflict) bool {
	for _, c := range conflicts {
		if c.Effect != ConflictKeeps {
			return true
		}
	}
	return false
}

// serverBlocks are a tree's http server blocks, in the order nginx reads them.
func serverBlocks(directives []Directive) []Directive {
	var out []Directive
	for _, d := range directives {
		if d.Block == nil {
			continue
		}
		if d.Name == "server" {
			if len(d.Context) > 0 && d.Context[len(d.Context)-1] == "http" {
				out = append(out, d)
			}
			continue
		}
		out = append(out, serverBlocks(d.Block)...)
	}
	return out
}

// claimsName says whether a server block claims name on address. A block
// with no listen is on *:80, or *:8000 when nginx is not run as root; and
// ".example.com" claims example.com as well as its subdomains.
func claimsName(block Directive, name, address string) bool {
	named, listens := false, false
	onAddress := false
	for _, d := range block.Block {
		switch d.Name {
		case "server_name":
			for _, arg := range d.Args {
				arg = strings.ToLower(arg)
				named = named || arg == name || arg == "."+name || (strings.HasPrefix(arg, ".") && name == "*"+arg)
			}
		case "listen":
			listens = true
			onAddress = onAddress || listenAddress(strings.Join(d.Args, " ")) == address
		}
	}
	if !listens {
		onAddress = address == "0.0.0.0:80" || address == "0.0.0.0:8000"
	}
	return named && onAddress
}

func claimsIn(blocks []Directive, name, address string) bool {
	for _, b := range blocks {
		if claimsName(b, name, address) {
			return true
		}
	}
	return false
}

// siteOfFile is the listed site whose file is file, or "" for a block that is
// not one of the listed sites.
func siteOfFile(sites []VHost, file string) string {
	resolved := resolvedFile(file)
	for _, v := range sites {
		if resolvedFile(v.Path) == resolved {
			return v.Name
		}
	}
	return ""
}

// ConflictSummary says, for each conflicting name, which claim nginx answers
// and what saving anyway would do, in sentences the form shows as they are.
func ConflictSummary(conflicts []ServerNameConflict) string {
	type group struct{ domain, site, effect string }
	var order []group
	listens := map[group][]string{}
	for _, c := range conflicts {
		key := group{c.Domain, c.Site, c.Effect}
		if _, ok := listens[key]; !ok {
			order = append(order, key)
		}
		listens[key] = append(listens[key], c.Listen)
	}
	sentences := make([]string, 0, len(order))
	for _, key := range order {
		site := key.site
		if site == "" {
			site = "another server block"
		}
		at := key.domain + " on " + strings.Join(listens[key], " and ")
		switch key.effect {
		case ConflictIgnored:
			sentences = append(sentences, fmt.Sprintf(
				"nginx answers %s from %s, which it reads first, and ignores this site's claim.", at, site))
		case ConflictTakes:
			sentences = append(sentences, fmt.Sprintf(
				"Saving anyway takes %s from %s, since nginx reads this site first.", at, site))
		case ConflictKeeps:
			sentences = append(sentences, fmt.Sprintf(
				"This site goes on answering %s; nginx ignores %s's claim to it, as before.", at, site))
		default:
			sentences = append(sentences, fmt.Sprintf(
				"%s is also claimed by %s, and nginx answers it from only one of the two.", at, site))
		}
	}
	return strings.Join(sentences, " ")
}

// warningsIn are the test's warnings placed in file.
func warningsIn(v *ValidationResult, file string) []Diagnostic {
	out := []Diagnostic{}
	for _, d := range v.Diagnostics {
		if d.Level == "warn" && d.File == file {
			out = append(out, d)
		}
	}
	return out
}

// siteTarget is the file a site named name is saved in: sites-available on a
// Debian layout, and conf.d on a host that keeps everything there, where
// confd says there is no link to make and nginx reads a file whose name ends
// in .conf. The suffix is added by confdSite rather than here, so editing a
// site the listing calls app.conf writes back over it instead of creating
// app.conf.conf.
func (s *Service) siteTarget(name string) (full string, confd bool, err error) {
	available := filepath.Join(s.nginxDir, "sites-available", name)
	if _, err := os.Stat(filepath.Dir(available)); err != nil {
		available, confd = s.confdSite(name), true
		if _, err := os.Stat(filepath.Dir(available)); err != nil {
			// Neither layout is present, which on a host that really runs
			// nginx means JD_NGINX_DIR points at the wrong place. Saying
			// which directory was looked for beats the "no such file or
			// directory" the write would otherwise fail with.
			return "", false, fmt.Errorf("%s has neither a sites-available nor a conf.d directory — set JD_NGINX_DIR to where this host keeps its nginx configuration", s.nginxDir)
		}
	}
	full, err = s.allowedPath(available)
	return full, confd, err
}

// confdSite is the conf.d file of the site named name. A file of exactly
// that name that nginx does not read — app.conf.disabled, or app, the usual
// ways to switch a conf.d site off — is the site the listing names so, and
// saving writes it where it is: confdPath's app.conf.disabled.conf beside it
// was a second copy of the site that nginx reads.
func (s *Service) confdSite(name string) string {
	exact := filepath.Join(s.nginxDir, "conf.d", name)
	if info, err := os.Stat(exact); err == nil && info.Mode().IsRegular() {
		return exact
	}
	return s.confdPath(name)
}

// SiteFileInfo is where saving a site of some name writes, and what already
// holds that name.
type SiteFileInfo struct {
	Path string
	// Exists is a file at Path: a site of this name.
	Exists bool
	// EnabledElsewhere says what holds the name's sites-enabled link when
	// that is not Path — another site's file, which a save would unlink.
	EnabledElsewhere string
	// Enabled is a file at Path that nginx reads: linked into sites-enabled
	// under its own name or another, or in conf.d with a name ending in .conf.
	Enabled bool
	// ServedCopy says sites-enabled/<name> is a file of its own, not a link:
	// nginx serves that file under the name, and not Path — usually a copy
	// made where a link was meant. Enabled is false, and so is "disabled":
	// the name is served, from the other file.
	ServedCopy bool
	// Confd says the host keeps its sites in conf.d, where there is no link
	// to make: a file is on while its name ends in .conf, and one without
	// the suffix is off until it is renamed.
	Confd bool
}

// SiteFile is the file saving a site named name writes, and what already
// holds the name. The form shows the first as the site's file name and
// refuses a new site over the rest before the save does, since a derived
// name can land on another site without the operator ever typing it.
func (s *Service) SiteFile(name string) (SiteFileInfo, error) {
	if !siteNameRe.MatchString(name) {
		return SiteFileInfo{}, fmt.Errorf("invalid site name")
	}
	full, confd, err := s.siteTarget(name)
	if err != nil {
		return SiteFileInfo{}, err
	}
	_, err = os.Stat(full)
	file := SiteFileInfo{Path: full, Exists: err == nil, Confd: confd}
	if confd {
		file.Enabled = file.Exists && strings.HasSuffix(full, ".conf")
	} else {
		link := filepath.Join(s.nginxDir, "sites-enabled", name)
		file.EnabledElsewhere = enabledElsewhere(link, full)
		file.ServedCopy = servedCopy(link)
		file.Enabled = file.Exists && enablingLink(link, full) != ""
	}
	return file, nil
}

// servedCopy says the sites-enabled entry link is a file of its own, which
// nginx includes and serves like any site, rather than a link to one.
func servedCopy(link string) bool {
	info, err := os.Lstat(link)
	return err == nil && info.Mode().IsRegular()
}

// enablingLink is the entry of sites-enabled through which nginx reads full:
// link, the name's own, when it names full, or else any other entry that
// resolves to full — a site linked under another name, such as 010-app, is
// enabled all the same. Empty when nothing there enables it. An entry
// starting with a dot is passed over, as nginx's include passes it over.
func enablingLink(link, full string) string {
	if _, err := os.Lstat(link); err == nil && enabledElsewhere(link, full) == "" {
		return filepath.Base(link)
	}
	if links := linksTo(filepath.Dir(link), full); len(links) > 0 {
		return links[0]
	}
	return ""
}

// linksTo are the entries of dir that resolve to full, passing over those
// starting with a dot as nginx's include passes them over.
func linksTo(dir, full string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	target := resolvePath(full)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name())); err == nil && resolved == target {
			out = append(out, e.Name())
		}
	}
	return out
}

// enabledElsewhere says what holds the sites-enabled link a site saved as
// full would use, when that is not full: a link to another file, or a file of
// its own sitting where the link belongs. Empty when there is no link, or it
// already names full — spelled relatively, or through another link, or
// before the file it names has been written.
func enabledElsewhere(link, full string) string {
	info, err := os.Lstat(link)
	if err != nil {
		return ""
	}
	name := filepath.Base(link)
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Sprintf("sites-enabled/%s is a file of its own, not a link", name)
	}
	target, err := os.Readlink(link)
	if err != nil {
		return fmt.Sprintf("sites-enabled/%s cannot be read: %v", name, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	target = resolvePath(target)
	if target == resolvePath(full) {
		return ""
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Sprintf("sites-enabled/%s links to %s, which is not there", name, target)
	}
	return fmt.Sprintf("sites-enabled/%s already enables %s", name, target)
}

// resolvePath follows the links in a path as far as they go. A file that is
// not there yet is placed by its directory, which is how a new site's path
// compares with a link written ahead of it.
func resolvePath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(dir, filepath.Base(path))
	}
	return path
}

// ReadSiteSpec reads a site the listing names back into the form: its spec,
// whether this dashboard wrote the file, and the file itself.
//
// Through ReadConfig's allowlist rather than a joined path, since a name that
// turns out to be a path is exactly what that check exists for. On a conf.d
// host the listing names a site app.conf; its spec is called app, the name
// saving it writes back to, where app.conf rendered its logs as
// app.conf.access.log and an edit moved them.
func (s *Service) ReadSiteSpec(name string) (*SiteSpec, bool, string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, false, "", fmt.Errorf("invalid site name")
	}
	candidates := []struct {
		path, spec string
	}{
		{filepath.Join(s.nginxDir, "sites-available", name), name},
		// Both spellings of the conf.d layout: the listing on such a host
		// reports a name that already ends in .conf, and a host that was set
		// up by hand may have a file without it.
		{filepath.Join(s.nginxDir, "conf.d", name), s.confdSpecName(name)},
		{filepath.Join(s.nginxDir, "conf.d", name+".conf"), name},
	}
	var err error
	for _, c := range candidates {
		var content string
		content, err = s.ReadConfig(c.path)
		if err == nil {
			spec, managed := ParseSiteSpec(c.spec, content)
			return spec, managed, content, nil
		}
	}
	return nil, false, "", err
}

// confdSpecName is the spec name of the conf.d file called name: without its
// .conf when confdSite writes that shorter name back to this file, and as it
// is when a file of the shorter name is there too and would be written
// instead.
func (s *Service) confdSpecName(name string) string {
	short := strings.TrimSuffix(name, ".conf")
	if s.confdSite(short) == filepath.Join(s.nginxDir, "conf.d", name) {
		return short
	}
	return name
}

// linkEnabled points sites-enabled at this file, and returns the undo.
//
// Three cases rather than one, because two of them used to be reported as
// success and were not. A link that already points somewhere else is replaced:
// leaving it meant the new file was never in nginx's include tree while the
// page said the site was enabled. And a symlink that could not be created at
// all is an error now — the earlier version swallowed it and set Enabled true
// regardless, so a read-only or missing sites-enabled produced a site that had
// been "enabled" and was serving nothing.
func linkEnabled(link, target string) (func(), error) {
	previous := ""
	restore := func() {
		_ = os.Remove(link)
		if previous != "" {
			_ = os.Symlink(previous, link)
		}
	}
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return func() {}, nil
		}
		previous = existing
		if err := os.Remove(link); err != nil {
			return nil, err
		}
	} else if _, err := os.Lstat(link); err == nil {
		// Not a symlink: a real file sitting where the link belongs. Removing
		// somebody's configuration is not this function's decision.
		return nil, fmt.Errorf("%s already exists and is not a symlink — move it aside first", link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		restore()
		return nil, err
	}
	if err := os.Symlink(target, link); err != nil {
		restore()
		return nil, err
	}
	return restore, nil
}

// keepBackup writes original beside full as <full>.bak, and returns the undo
// for a save that is then refused: the .bak that was there before, or none.
func keepBackup(full, original string) (func(), error) {
	backup := full + ".bak"
	before, existed := readIfPresent(backup)
	if err := writeAtomic(backup, original); err != nil {
		return nil, err
	}
	return func() { restoreConfig(backup, before, existed) }, nil
}

func readIfPresent(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func restoreConfig(path, original string, existed bool) {
	if existed {
		writeAtomic(path, original)
		return
	}
	os.Remove(path)
}

// DeleteSite removes a site's file and every symlink in sites-enabled that
// enables it: its own, and one of another name such as 010-app.
//
// Links first: one left behind points nginx at a file that no longer exists,
// which takes every site on the box down at the next reload — a site linked
// only as 010-app did that, since only sites-enabled/<name> was removed.
// A sites-enabled/<name> that enables another file is that site's, not this
// one's — a hand-written site linked under its domain — and stays: removing it
// took a site nobody asked to delete off the air.
func (s *Service) DeleteSite(ctx context.Context, name string) error {
	if !siteNameRe.MatchString(name) {
		return fmt.Errorf("invalid site name")
	}
	if isBackupFile(name) {
		// Unreachable from the listing, which hides these — but a second
		// delete of the same name must never be able to produce
		// <name>.bak.bak, which is the shape the old bug took.
		return fmt.Errorf("%s is a backup of a deleted site, not a site — remove it from the file manager if you no longer want it", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSiteDelete(name); err != nil {
		return err
	}
	removedLink := false
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	keptLink := ""
	if info, err := os.Lstat(link); err == nil {
		keptLink = enabledElsewhere(link, filepath.Join(s.nginxDir, "sites-available", name))
		if _, err := os.Stat(link); err != nil && info.Mode()&os.ModeSymlink != 0 {
			// A link to nothing enables nothing, so removing it cannot stop
			// a site, and left behind it fails the next reload.
			keptLink = ""
		}
		if keptLink == "" {
			if err := os.Remove(link); err != nil {
				return err
			}
			removedLink = true
			s.forgetEffective()
		}
	}
	// The conf.d file the listing names: app.conf.disabled itself, where
	// confdPath's app.conf.disabled.conf was not there, and app rather than
	// the app.conf beside it.
	for _, candidate := range []string{
		filepath.Join(s.nginxDir, "sites-available", name),
		s.confdSite(name),
	} {
		full, err := s.allowedPath(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(full); err != nil {
			continue
		}
		for _, entry := range linksTo(filepath.Join(s.nginxDir, "sites-enabled"), full) {
			if err := os.Remove(filepath.Join(s.nginxDir, "sites-enabled", entry)); err != nil {
				return err
			}
			s.forgetEffective()
		}
		// Kept as .bak for the same reason a compose file is: validation
		// catches a broken config, not a correct one that says the wrong
		// thing, and the only cure for the second is the previous version.
		// The listing skips these, so the copy is a file on disk rather than
		// a site that comes back the moment the one it replaced is deleted.
		b, readErr := os.ReadFile(full)
		if readErr == nil {
			os.WriteFile(full+".bak", b, 0o644)
		}
		if err := os.Remove(full); err != nil {
			return err
		}
		s.recordChange(ctx, Change{Path: full, Action: ChangeDelete, Before: b, BeforeExisted: true})
		return nil
	}
	if removedLink {
		// A link with nothing behind it is exactly what takes every site on
		// the box down at the next reload, so removing it is the whole job
		// and reporting failure afterwards would be wrong.
		return nil
	}
	if keptLink != "" {
		return fmt.Errorf("no such site: %s — %s, and stays", name, keptLink)
	}
	return fmt.Errorf("no such site: %s", name)
}
