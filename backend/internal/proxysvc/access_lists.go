package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Reusable access lists.
//
// The site form puts an allow list and a password on one site. An office
// range or a VPN in front of ten staging sites was ten copies of the same
// lines, and the day the office moved it was ten edits and ten reloads, and
// whichever site was missed kept letting the old range in. A list is one file
// under jd-access that sites include, so an edit is one write, one `nginx -t`
// and one reload for every site that uses it.
//
// The file holds nothing but allow, deny, satisfy and the basic-auth pair,
// each legal in http, server and location context, so a site can take a list
// in at whichever level it wants the restriction.

var (
	// accessListNameRe is the file name without its .conf. No dots, so a
	// name can never read as a second suffix or as a backup.
	accessListNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

	ErrAccessListExists = errors.New("an access list with this name already exists")
	ErrNoAccessList     = errors.New("no such access list")
)

// maxAccessEntries bounds each side of a list. nginx checks the rules one by
// one on every request, and a list longer than this belongs in a firewall.
const maxAccessEntries = 512

// accessListHead is the first line of every list this dashboard writes.
const accessListHead = "# " + OwnedMarker + ": an access list, edited on the Sites page."

// AccessListSpec is what a list says, in the shape the page edits.
//
// Deny is checked first and Allow second; a non-empty Allow ends with
// `deny all`, so "only these addresses" means only these — the same order
// and fence the site form writes for a single site. Satisfy decides what an
// address and a password do together: "all" asks for both, "any" lets either
// one in, and it only exists where the list has both an allow list and a
// password file.
type AccessListSpec struct {
	Allow    []string `json:"allow"`
	Deny     []string `json:"deny"`
	AuthFile string   `json:"authFile,omitempty"`
	Realm    string   `json:"realm,omitempty"`
	Satisfy  string   `json:"satisfy"`
}

// AccessList is one list on disk: what it says, where it is, and the sites
// that include it.
type AccessList struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Include is the directive a site takes the list in with.
	Include string `json:"include"`
	AccessListSpec
	// AuthFileMissing is a password file the list names and jd-auth no
	// longer holds. nginx passes its test and reloads without it, and then
	// refuses every login.
	AuthFileMissing bool `json:"authFileMissing,omitempty"`
	// HandWritten says why the file is not in the shape this page writes —
	// a directive it does not write, rules in another order — so saving the
	// list from the form would change what it does, not only how it reads.
	HandWritten string          `json:"handWritten,omitempty"`
	UsedBy      []AccessListUse `json:"usedBy"`
	Modified    time.Time       `json:"modified"`
}

// AccessListUse is a site whose file includes a list, directly or through a
// file it includes.
type AccessListUse struct {
	Site    string `json:"site"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// AccessListInUseError refuses to delete a list a site still includes: nginx
// would refuse that site the moment it is loaded, and a disabled one the
// moment it is enabled again.
type AccessListInUseError struct {
	Name   string
	UsedBy []AccessListUse
}

func (e *AccessListInUseError) Error() string {
	names := make([]string, 0, len(e.UsedBy))
	for _, use := range e.UsedBy {
		name := use.Site
		if !use.Enabled {
			name += " (disabled)"
		}
		names = append(names, name)
	}
	if len(names) == 1 {
		return fmt.Sprintf("%s includes %s — take the include out of that site before deleting the list", names[0], e.Name)
	}
	return fmt.Sprintf("%s include %s — take the include out of those sites before deleting the list", joinNames(names), e.Name)
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// AccessListResult is what saving a list did. Validation is the test that
// passed with the new file in place; Reload is how the reload after it went.
type AccessListResult struct {
	List       AccessList        `json:"list"`
	Validation *ValidationResult `json:"validation"`
	Reload     *LinkReload       `json:"-"`
}

// AccessListDir is where the lists live, beside the configuration they are
// part of and under the configured nginx directory, like jd-auth.
func (s *Service) AccessListDir() string { return filepath.Join(s.nginxDir, "jd-access") }

// AccessListPath is the file a list called name lives in, which a site
// includes to use it.
func (s *Service) AccessListPath(name string) (string, error) {
	if !accessListNameRe.MatchString(name) {
		return "", fmt.Errorf("a list name is lowercase letters, digits, dashes or underscores, up to 63 of them")
	}
	return filepath.Join(s.AccessListDir(), name+".conf"), nil
}

// ValidateAccessList checks a list and returns it in the form it is written:
// every address in its canonical spelling, satisfy set, the login prompt
// defaulted where there is a password and dropped where there is not.
func ValidateAccessList(spec AccessListSpec) (AccessListSpec, error) {
	out := AccessListSpec{Allow: []string{}, Deny: []string{}, Satisfy: spec.Satisfy}
	for _, side := range []struct {
		label string
		in    []string
		out   *[]string
	}{{"allow", spec.Allow, &out.Allow}, {"deny", spec.Deny, &out.Deny}} {
		if len(side.in) > maxAccessEntries {
			return out, fmt.Errorf("the %s list has %d entries; the most a list takes is %d", side.label, len(side.in), maxAccessEntries)
		}
		// Keyed by the range each names, so 10.0.0.1 and 10.0.0.1/32 are one.
		seen := map[netip.Prefix]bool{}
		for _, raw := range side.in {
			entry, prefix, err := accessEntry(raw)
			if err != nil {
				return out, err
			}
			if seen[prefix] {
				return out, fmt.Errorf("%s is on the %s list twice", entry, side.label)
			}
			seen[prefix] = true
			*side.out = append(*side.out, entry)
		}
	}
	out.AuthFile = strings.TrimSpace(spec.AuthFile)
	if out.AuthFile != "" && !authFileRe.MatchString(out.AuthFile) {
		return out, fmt.Errorf("%q is not the name of a password file", out.AuthFile)
	}
	if out.AuthFile != "" {
		out.Realm = strings.TrimSpace(spec.Realm)
		if out.Realm == "" {
			out.Realm = "Restricted"
		}
		// nginx reads a variable in the prompt, and "off" as the prompt
		// turns the password off altogether.
		if strings.ContainsAny(out.Realm, "\"\\$;{}") || strings.ContainsFunc(out.Realm, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return out, fmt.Errorf("the login prompt may not contain quotes, backslashes, $, semicolons, braces or control characters")
		}
		if strings.EqualFold(out.Realm, "off") {
			return out, fmt.Errorf("a login prompt of \"off\" turns the password off — choose other words")
		}
		if len(out.Realm) > 100 {
			return out, fmt.Errorf("the login prompt is %d characters; keep it to 100", len(out.Realm))
		}
	}
	switch out.Satisfy {
	case "":
		out.Satisfy = "all"
	case "all":
	case "any":
		// With no allow list every address already passes the address
		// check, and "either one" would let everybody in without the
		// password.
		if out.AuthFile == "" || len(out.Allow) == 0 {
			return out, fmt.Errorf("letting in an allowed address or a password needs both an allow list and a password file")
		}
	default:
		return out, fmt.Errorf("satisfy must be all or any")
	}
	if len(out.Allow) == 0 && len(out.Deny) == 0 && out.AuthFile == "" {
		return out, fmt.Errorf("a list needs at least one address or a password file")
	}
	return out, nil
}

// accessEntry is one address or range in its canonical spelling. A range
// with bits set past its length is refused rather than masked: nginx takes
// 10.0.0.5/8 as 10.0.0.0/8 with a warning, and a list reading one thing and
// doing another is what this page exists to stop. The prefix is the range
// the entry names, a single address being its own /32 or /128.
func accessEntry(raw string) (string, netip.Prefix, error) {
	entry := strings.TrimSpace(raw)
	if entry == "all" {
		return "", netip.Prefix{}, fmt.Errorf("write addresses or ranges rather than all — an allow list is already closed with deny all")
	}
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return "", netip.Prefix{}, fmt.Errorf("%q is not an IP address or a range like 10.0.0.0/8", entry)
		}
		if masked := prefix.Masked(); masked != prefix {
			return "", netip.Prefix{}, fmt.Errorf("%s has bits set past its /%d — the range is %s", entry, prefix.Bits(), masked)
		}
		return prefix.String(), prefix, nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil || addr.Zone() != "" {
		return "", netip.Prefix{}, fmt.Errorf("%q is not an IP address or a range like 10.0.0.0/8", entry)
	}
	return addr.String(), netip.PrefixFrom(addr, addr.BitLen()), nil
}

// renderAccessList writes a validated list. authFile is the password file's
// full path, or empty.
func renderAccessList(path string, spec AccessListSpec, authFile string) string {
	l := &lines{}
	l.add("%s", accessListHead)
	l.add("# Sites take it in with: include %s;", path)
	l.add("# Every site that includes it changes with it.")
	l.blank()
	if authFile != "" {
		if len(spec.Allow) > 0 || len(spec.Deny) > 0 {
			l.add("# all: an allowed address and a password; any: either one.")
			l.add("satisfy %s;", spec.Satisfy)
		}
		l.add("auth_basic \"%s\";", spec.Realm)
		l.add("auth_basic_user_file %s;", authFile)
	}
	if len(spec.Allow) > 0 || len(spec.Deny) > 0 {
		if authFile != "" {
			l.blank()
		}
		l.add("# nginx stops at the first rule an address matches: the denials")
		l.add("# first, then the allowed, then the fence.")
		for _, entry := range spec.Deny {
			l.add("deny %s;", entry)
		}
		for _, entry := range spec.Allow {
			l.add("allow %s;", entry)
		}
		if len(spec.Allow) > 0 {
			l.add("deny all;")
		}
	}
	return l.String()
}

// readAccessList reads a list back from its file. Anything the form would
// not write the same way is named in HandWritten; the rules read are still
// returned, so the page can show what the form would start from.
func (s *Service) readAccessList(name, path, content string) AccessList {
	list := AccessList{
		Name: name, Path: path, Include: "include " + path + ";",
		AccessListSpec: AccessListSpec{Allow: []string{}, Deny: []string{}, Satisfy: "all"},
		UsedBy:         []AccessListUse{},
	}
	directives, err := ParseNginxFile(path, content, []string{"http", "server"})
	if err != nil {
		list.HandWritten = "nginx could not read it: " + err.Error()
		return list
	}
	unknown := []string{}
	var rules []Directive
	for _, d := range directives {
		arg := ""
		if len(d.Args) == 1 {
			arg = d.Args[0]
		}
		switch {
		case d.Block != nil || arg == "":
			unknown = appendNew(unknown, d.Name)
		case d.Name == "satisfy":
			list.Satisfy = arg
		case d.Name == "auth_basic":
			list.Realm = arg
		case d.Name == "auth_basic_user_file":
			if filepath.Dir(arg) == s.authDir() && authFileRe.MatchString(filepath.Base(arg)) {
				list.AuthFile = filepath.Base(arg)
			} else {
				list.HandWritten = "it takes its passwords from " + arg + ", which is not one of the password files on this page"
			}
		case d.Name == "allow" || d.Name == "deny":
			rules = append(rules, d)
		default:
			unknown = appendNew(unknown, d.Name)
		}
	}
	for i, rule := range rules {
		arg := rule.Args[0]
		if rule.Name == "deny" && arg == "all" && i == len(rules)-1 {
			continue
		}
		if rule.Name == "allow" {
			list.Allow = append(list.Allow, arg)
		} else {
			list.Deny = append(list.Deny, arg)
		}
	}
	if list.AuthFile != "" {
		if _, err := os.Stat(filepath.Join(s.authDir(), list.AuthFile)); err != nil {
			list.AuthFileMissing = true
		}
	}
	switch {
	case len(unknown) > 0:
		list.HandWritten = "it has " + strings.Join(unknown, ", ") + ", which the form does not write"
	case list.HandWritten != "":
	default:
		// What the form would write from what was read, compared rule for
		// rule with what the file says: an allow before a deny, a missing
		// fence or a stray satisfy all read the same into the fields and do
		// something else.
		spec, err := ValidateAccessList(list.AccessListSpec)
		if err != nil {
			list.HandWritten = "the form cannot hold it as it is: " + err.Error()
			break
		}
		authFile := ""
		if spec.AuthFile != "" {
			authFile = filepath.Join(s.authDir(), spec.AuthFile)
		}
		rendered, _ := ParseNginxFile(path, renderAccessList(path, spec, authFile), []string{"http", "server"})
		if !sameDirectives(directives, rendered) {
			list.HandWritten = "its rules are written in an order or form the page would change, and nginx stops at the first rule an address matches"
		}
	}
	return list
}

// sameDirectives compares two runs of simple directives by name and
// arguments, ignoring where they sit.
func sameDirectives(a, b []Directive) bool {
	return slices.EqualFunc(a, b, func(x, y Directive) bool {
		return x.Name == y.Name && slices.Equal(x.Args, y.Args) && x.Block == nil && y.Block == nil
	})
}

// ListAccessLists reads every list in jd-access and the sites using each.
func (s *Service) ListAccessLists() ([]AccessList, error) {
	out := []AccessList{}
	entries, err := os.ReadDir(s.AccessListDir())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".conf")
		if !ok || e.IsDir() || !accessListNameRe.MatchString(name) {
			continue
		}
		path := filepath.Join(s.AccessListDir(), e.Name())
		var list AccessList
		if b, err := os.ReadFile(path); err != nil {
			list = s.readAccessList(name, path, "")
			list.HandWritten = "it could not be read: " + err.Error()
		} else {
			list = s.readAccessList(name, path, string(b))
		}
		if info, err := e.Info(); err == nil {
			list.Modified = info.ModTime()
		}
		out = append(out, list)
		paths = append(paths, path)
	}
	uses := s.accessListUses(paths)
	for i := range out {
		if used := uses[out[i].Path]; used != nil {
			out[i].UsedBy = used
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// accessListUses finds the sites that include each of lists, by path. A site
// uses a list when its file includes it, or includes a file — a snippet —
// that does; the include may be a glob, as nginx allows. Disabled sites
// count: deleting a list one of them includes would make enabling it fail.
func (s *Service) accessListUses(lists []string) map[string][]AccessListUse {
	out := map[string][]AccessListUse{}
	if len(lists) == 0 {
		return out
	}
	resolved := make(map[string]string, len(lists))
	for _, list := range lists {
		resolved[list] = resolvedFile(list)
	}
	seen := map[string]bool{}
	for _, v := range s.nginxVHosts() {
		if v.Path == "" || seen[v.Name+"\x00"+v.Path] {
			continue
		}
		seen[v.Name+"\x00"+v.Path] = true
		patterns := s.includePatterns(v.Path)
		for _, list := range lists {
			if slices.ContainsFunc(patterns, func(p string) bool { return includeMatches(p, list, resolved[list]) }) {
				out[list] = append(out[list], AccessListUse{Site: v.Name, Path: v.Path, Enabled: v.Enabled})
			}
		}
	}
	for _, uses := range out {
		sort.Slice(uses, func(i, j int) bool {
			if uses[i].Site != uses[j].Site {
				return uses[i].Site < uses[j].Site
			}
			return uses[i].Path < uses[j].Path
		})
	}
	return out
}

// includePatterns is every include a site file writes, anywhere in it, and
// every include in the files those name — one level, as the listing follows
// a snippet — each an absolute, cleaned path or glob. Lists themselves and
// password files are not read.
func (s *Service) includePatterns(path string) []string {
	own := s.includesIn(path)
	out := append([]string{}, own...)
	self := resolvedFile(path)
	for _, pattern := range own {
		matches, _ := filepath.Glob(globToMatch(pattern))
		for _, match := range matches {
			// glob(3) leaves out a dotfile unless the pattern names one.
			if strings.HasPrefix(filepath.Base(match), ".") && !strings.HasPrefix(filepath.Base(pattern), ".") {
				continue
			}
			full, err := s.allowedPath(match)
			if err != nil || full == self || filepath.Dir(full) == s.AccessListDir() || s.isPasswordFile(full) {
				continue
			}
			out = append(out, s.includesIn(full)...)
		}
	}
	return out
}

// includesIn is every include in the file at path, resolved the way nginx
// resolves it: a relative path from the directory nginx.conf is in.
func (s *Service) includesIn(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	directives, err := ParseNginxFile(path, string(b), []string{"http"})
	if err != nil {
		return nil
	}
	var out []string
	var walk func([]Directive)
	walk = func(block []Directive) {
		for _, d := range block {
			if d.Name == "include" && len(d.Args) == 1 {
				pattern := d.Args[0]
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(s.nginxDir, pattern)
				}
				out = append(out, filepath.Clean(pattern))
			}
			walk(d.Block)
		}
	}
	walk(directives)
	return out
}

// includeMatches says whether an include pattern takes in the list at path,
// under its own name or with its links resolved.
func includeMatches(pattern, path, resolved string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == path || pattern == resolved || resolvedFile(pattern) == resolved
	}
	glob := globToMatch(pattern)
	for _, candidate := range []string{path, resolved} {
		if strings.HasPrefix(filepath.Base(candidate), ".") && !strings.HasPrefix(filepath.Base(pattern), ".") {
			continue
		}
		if ok, _ := filepath.Match(glob, candidate); ok {
			return true
		}
	}
	return false
}

// SaveAccessList writes a list, tests the whole configuration with it in
// place and puts the previous file back if nginx refuses it, then reloads
// nginx inside the same hold of the service lock, so every site that
// includes the list changes at once. overwrite false refuses a name that is
// already a list: a new list never replaces one sites depend on.
func (s *Service) SaveAccessList(ctx context.Context, name string, spec AccessListSpec, overwrite bool) (*AccessListResult, error) {
	path, err := s.AccessListPath(name)
	if err != nil {
		return nil, err
	}
	spec, err = ValidateAccessList(spec)
	if err != nil {
		return nil, err
	}
	authFile := ""
	if spec.AuthFile != "" {
		authFile = filepath.Join(s.authDir(), spec.AuthFile)
		if _, err := os.Stat(authFile); err != nil {
			return nil, fmt.Errorf("there is no password file called %s — add a login to it under Password files first", spec.AuthFile)
		}
	}
	if err := os.MkdirAll(s.AccessListDir(), 0o755); err != nil {
		return nil, err
	}
	full, err := s.allowedPath(path)
	if err != nil {
		return nil, err
	}
	content := renderAccessList(path, spec, authFile)

	s.mu.Lock()
	defer s.mu.Unlock()
	original, existed := readIfPresent(full)
	if existed && !overwrite {
		return nil, fmt.Errorf("%w: %s", ErrAccessListExists, name)
	}
	if err := writeAtomic(full, content); err != nil {
		return nil, err
	}
	res := runValidator(ctx, "nginx", "-t")
	if !res.Valid {
		restoreConfig(full, original, existed)
		refused := &RefusedError{Validation: res}
		before := runValidator(ctx, "nginx", "-t")
		switch {
		case !before.Valid && FailureHeadline(before) == FailureHeadline(res):
			refused.Lead = "nginx already refuses the configuration without this change to " + name
		case failsIn(res, full):
			// The list is fine on its own; it is a site that takes it in
			// that nginx refuses — one that already sets the same prompt,
			// or includes it where these directives are not allowed.
			refused.Lead = "nginx refuses " + name + " where a site includes it"
		default:
			refused.Lead = "nginx refuses the configuration with this change to " + name
		}
		return nil, refused
	}
	s.recordChange(ctx, Change{Path: full, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	list := s.readAccessList(name, full, content)
	if info, err := os.Stat(full); err == nil {
		list.Modified = info.ModTime()
	}
	if used := s.accessListUses([]string{full})[full]; used != nil {
		list.UsedBy = used
	}
	out := &AccessListResult{List: list, Validation: res}
	out.Reload = s.reloadLocked(ctx, true)
	return out, nil
}

// DeleteAccessList removes a list no site includes, keeping a copy beside it
// as <name>.conf.bak — nginx's include of *.conf never reads that — and puts
// it back if nginx refuses the configuration without it: something outside
// the sites, nginx.conf or a snippet of a snippet, may still include it.
// Nothing nginx has loaded changes, so nothing is reloaded.
func (s *Service) DeleteAccessList(ctx context.Context, name string) error {
	path, err := s.AccessListPath(name)
	if err != nil {
		return err
	}
	full, err := s.allowedPath(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	original, err := os.ReadFile(full)
	if os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrNoAccessList, name)
	}
	if err != nil {
		return err
	}
	if used := s.accessListUses([]string{full})[full]; len(used) > 0 {
		return &AccessListInUseError{Name: name, UsedBy: used}
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		if err := writeAtomic(full, string(original)); err != nil {
			return fmt.Errorf("nginx refused the configuration without %s, and putting it back failed: %w", name, err)
		}
		refused := &RefusedError{Validation: res, Lead: "nginx refuses the configuration without " + name}
		if before := runValidator(ctx, "nginx", "-t"); !before.Valid && FailureHeadline(before) == FailureHeadline(res) {
			refused.Lead = "nginx already refuses the configuration, with or without " + name
		}
		return refused
	}
	_ = os.WriteFile(full+".bak", original, 0o644)
	s.recordChange(ctx, Change{Path: full, Action: ChangeDelete, Before: original, BeforeExisted: true})
	return nil
}
