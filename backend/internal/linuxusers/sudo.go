package linuxusers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Reading sudo's own rules is the difference between "ubuntu is in the sudo
// group" and "ubuntu can run anything as root without a password, because
// cloud-init wrote one line in a file nobody opens". The page used to guess from
// group names and say in small print that it had not looked.

// SudoRule is one rule as its file holds it.
type SudoRule struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
	// Who is the rule's user list, aliases expanded: "ubuntu", "%sudo".
	Who      []string `json:"who"`
	RunAs    string   `json:"runAs"`
	Commands []string `json:"commands"`
	// NoPassword is NOPASSWD: anywhere in the rule.
	NoPassword bool `json:"noPassword"`
	// Full is any command as root; a rule that is not Full is limited to what
	// Commands names, or to another target account.
	Full bool `json:"full"`
	// AsRoot says the rule can reach root at all.
	AsRoot bool `json:"asRoot"`
	// Managed is the file this dashboard writes for the account.
	Managed bool `json:"managed"`

	endLine int
	// noAuth is who a `Defaults !authenticate` covers: for them no rule asks for a
	// password, whatever the rule says.
	noAuth []string
}

// RootRoute is one way an account reaches root.
type RootRoute struct {
	// Kind is "uid0", "sudo", or "group" for a group that is root by another
	// name: docker can mount the host's root into a container.
	Kind string `json:"kind"`
	// Via is the group that carries the route, when one does.
	Via        string `json:"via,omitempty"`
	NoPassword bool   `json:"noPassword,omitempty"`
	// Limited is a rule that names commands rather than granting everything.
	Limited bool   `json:"limited,omitempty"`
	RuleID  string `json:"ruleId,omitempty"`
	File    string `json:"file,omitempty"`
	// Usable is false for a rule that asks for a password the account does not
	// have: it reads like root access and cannot be used.
	Usable bool `json:"usable"`
}

// rootEquivalentGroups are the groups whose members can become root without
// sudo. Each hands over something that reaches the host's filesystem as root.
var rootEquivalentGroups = map[string]bool{
	"docker": true, "lxd": true, "lxc": true, "libvirt": true, "disk": true,
}

// sudoGroupsWhenBlind is what is left to go on when the sudoers files cannot be
// read: the conventional names, and a route that says it is a guess.
var sudoGroupsWhenBlind = map[string]bool{"sudo": true, "wheel": true, "admin": true}

type sudoAliases struct {
	users, commands, runas map[string][]string
}

type sudoLine struct {
	file       string
	start, end int
	text       string
}

// sudoRules reads every rule, following includes. It returns nil, rather than
// empty, when the main file cannot be read, so a caller can tell "no rules" from
// "could not look".
func (s *Service) sudoRules() []SudoRule {
	main := s.path("sudoers")
	lines, ok := readSudoLines(main, s.etc, 0, map[string]bool{})
	if !ok {
		// No file at all is a host without sudo, which is an answer.
		if _, err := os.Stat(main); os.IsNotExist(err) {
			return []SudoRule{}
		}
		return nil
	}
	return parseSudoLines(lines, s.path(filepath.Join("sudoers.d", "")))
}

// readSudoLines joins continuations and walks the includes. Depth and a seen set
// bound a sudoers tree that includes itself.
func readSudoLines(path, etc string, depth int, seen map[string]bool) ([]sudoLine, bool) {
	if depth > 8 || seen[path] {
		return nil, true
	}
	seen[path] = true
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	out := []sudoLine{}
	raw := strings.Split(string(b), "\n")
	for i := 0; i < len(raw); i++ {
		start := i
		line := raw[i]
		// Before the continuation, not after: a comment ends at its own line even
		// with a backslash on the end. Joining first swallowed the rule under it,
		// and sudo keeps that rule.
		if isSudoComment(strings.TrimSpace(line)) {
			continue
		}
		for strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") && i+1 < len(raw) {
			i++
			line = strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\") + " " + strings.TrimSpace(raw[i])
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if kind, target, ok := sudoInclude(trimmed); ok {
			target = strings.Trim(target, `"`)
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			target = rebase(target, etc)
			if kind == "includedir" {
				for _, file := range sudoDirFiles(target) {
					included, _ := readSudoLines(file, etc, depth+1, seen)
					out = append(out, included...)
				}
				continue
			}
			included, _ := readSudoLines(target, etc, depth+1, seen)
			out = append(out, included...)
			continue
		}
		out = append(out, sudoLine{file: strings.TrimPrefix(path, hostRootPrefix), start: start + 1, end: i + 1, text: trimmed})
	}
	return out, true
}

// isSudoComment knows the two lines that start with # and are not comments:
// "#1000 ALL=..." is a uid and "#includedir" a directive, sudoers says so in as many
// words.
func isSudoComment(line string) bool {
	if !strings.HasPrefix(line, "#") || sudoUIDLineRe.MatchString(line) {
		return false
	}
	_, _, directive := sudoInclude(line)
	return !directive
}

// hostRootPrefix is what hostexec puts in front of a path outside this container's
// mounts. It is how the file is opened and never how it is named to anyone.
const hostRootPrefix = "/proc/1/root"

// rebase maps a real /etc path in an include onto the root this service reads
// from, which is /etc itself outside a test. An include elsewhere is on the host
// and not in the mounts this container shares: read from here it would look like
// a file with no rules in it.
func rebase(path, etc string) string {
	if etc == "/etc" {
		if strings.HasPrefix(path, "/etc/") || strings.HasPrefix(path, hostRootPrefix) {
			return path
		}
		return hostexec.HostPath(path)
	}
	if rest, ok := strings.CutPrefix(path, "/etc/"); ok {
		return filepath.Join(etc, rest)
	}
	return path
}

// sudoInclude takes whatever whitespace sudo does: "#includedir<TAB>/x" is an
// include, and read as a comment it hides every rule in the directory.
func sudoInclude(line string) (kind, target string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	switch fields[0] {
	case "@includedir", "#includedir", "@include", "#include":
		return strings.TrimLeft(fields[0], "@#"), strings.TrimSpace(line[len(fields[0]):]), true
	}
	return "", "", false
}

// sudoDirFiles lists what sudo itself would read: it skips any name with a dot
// in it or ending in a tilde, which is how a package's backup stays inert.
func sudoDirFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.Contains(name, ".") || strings.HasSuffix(name, "~") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}

var sudoUIDLineRe = regexp.MustCompile(`^#\d+[\s,]`)

var sudoAliasRe = regexp.MustCompile(`^(User_Alias|Cmnd_Alias|Cmd_Alias|Host_Alias|Runas_Alias)\s+(.+)$`)

// parseSudoLines reads aliases and Defaults first, then the rules. sudo takes an
// alias defined below the rule that uses it, so a single pass in file order read
// such a rule as naming nobody.
func parseSudoLines(lines []sudoLine, managedDir string) []SudoRule {
	aliases := sudoAliases{users: map[string][]string{}, commands: map[string][]string{}, runas: map[string][]string{}}
	noAuth := []string{}
	rest := []sudoLine{}
	for _, line := range lines {
		text := stripSudoComment(line.text)
		if text == "" {
			continue
		}
		if m := sudoAliasRe.FindStringSubmatch(text); m != nil {
			aliases.add(m[1], m[2])
			continue
		}
		if m := sudoDefaultsRe.FindStringSubmatch(text); m != nil {
			if strings.Contains(" "+m[3]+" ", " !authenticate ") || strings.Contains(m[3], "!authenticate") {
				if m[1] == "" {
					noAuth = append(noAuth, "ALL")
				} else if m[1] == ":" {
					noAuth = append(noAuth, splitSudoList(m[2])...)
				}
			}
			continue
		}
		rest = append(rest, line)
	}
	noAuth = expandSudo(noAuth, aliases.users, 0)
	rules := []SudoRule{}
	for _, line := range rest {
		if rule, ok := parseSudoRule(stripSudoComment(line.text), aliases); ok {
			rule.File, rule.Line, rule.endLine = line.file, line.start, line.end
			rule.Text = line.text
			rule.ID = sudoRuleID(line.file, line.text)
			rule.Managed = filepath.Dir(line.file) == filepath.Clean(managedDir) &&
				strings.HasPrefix(filepath.Base(line.file), managedPrefix)
			rule.noAuth = noAuth
			rules = append(rules, rule)
		}
	}
	return rules
}

// sudoDefaultsRe reads `Defaults`, `Defaults:who`, and the host, command and runas
// forms it does not need to tell apart: only a user's or everyone's !authenticate
// can make a rule ask for no password.
var sudoDefaultsRe = regexp.MustCompile(`^Defaults([:@!>]?)(\S*)\s+(.*)$`)

func sudoRuleID(file, text string) string {
	sum := sha256.Sum256([]byte(file + "\x00" + text))
	return hex.EncodeToString(sum[:8])
}

func stripSudoComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		// "#1000" is a uid in a user list and "%#1000" a gid; a comment follows
		// whitespace and is not a number.
		if i > 0 && line[i-1] != ' ' && line[i-1] != '\t' {
			continue
		}
		if i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
			continue
		}
		return strings.TrimSpace(line[:i])
	}
	return strings.TrimSpace(line)
}

func (a sudoAliases) add(kind, body string) {
	var target map[string][]string
	switch kind {
	case "User_Alias":
		target = a.users
	case "Cmnd_Alias", "Cmd_Alias":
		target = a.commands
	case "Runas_Alias":
		target = a.runas
	default:
		return
	}
	for _, def := range strings.Split(body, " : ") {
		name, value, ok := strings.Cut(def, "=")
		if !ok {
			continue
		}
		target[strings.TrimSpace(name)] = splitSudoList(value)
	}
}

// splitSudoList splits on the commas that separate items. A comma inside
// `(root, www-data)` or escaped as `\,` in a command's arguments is part of the
// item it is in.
func splitSudoList(value string) []string {
	out := []string{}
	depth := 0
	current := strings.Builder{}
	flush := func() {
		if item := strings.TrimSpace(current.String()); item != "" {
			out = append(out, item)
		}
		current.Reset()
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '\\' && i+1 < len(value):
			current.WriteByte(c)
			i++
			current.WriteByte(value[i])
			continue
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case c == ',' && depth == 0:
			flush()
			continue
		}
		current.WriteByte(c)
	}
	flush()
	return out
}

func expandSudo(items []string, table map[string][]string, depth int) []string {
	out := []string{}
	for _, item := range items {
		bare := strings.TrimPrefix(item, "!")
		if expansion, ok := table[bare]; ok && depth < 8 {
			expanded := expandSudo(expansion, table, depth+1)
			if bare != item {
				for i := range expanded {
					expanded[i] = "!" + expanded[i]
				}
			}
			out = append(out, expanded...)
			continue
		}
		out = append(out, item)
	}
	return out
}

const sudoTagWords = `NOPASSWD|PASSWD|NOEXEC|EXEC|SETENV|NOSETENV|LOG_INPUT|NOLOG_INPUT|LOG_OUTPUT|NOLOG_OUTPUT|MAIL|NOMAIL|FOLLOW|NOFOLLOW|INTERCEPT|NOINTERCEPT`

var (
	sudoTagRe = regexp.MustCompile(`^(` + sudoTagWords + `):\s*`)
	// An option before a command: `CWD=* ALL` is ALL, not a command called CWD=*.
	sudoOptionRe = regexp.MustCompile(`^(CWD|CHROOT|ROLE|TYPE|PRIVS|LIMITPRIVS|NOTBEFORE|NOTAFTER|TIMEOUT|APPARMOR_PROFILE)=("[^"]*"|\S+)\s*`)
	sudoTagWord  = regexp.MustCompile(`^(?:` + sudoTagWords + `)$`)
	sudoListRe   = regexp.MustCompile(`\s*,\s*`)
)

// splitSudoSpecs cuts a rule's right side at the colons that start another host
// spec: `x ALL=/bin/ls : ALL=(ALL) ALL` is two grants, and reading only the first
// is how the second one hid. A colon inside (ALL : ALL), escaped, or ending a tag is
// not one of them.
func splitSudoSpecs(right string) []string {
	specs := []string{}
	depth, start := 0, 0
	for i := 0; i < len(right); i++ {
		switch right[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth > 0 {
				continue
			}
			word := i
			for word > start && (right[word-1] == '_' || (right[word-1] >= 'A' && right[word-1] <= 'Z')) {
				word--
			}
			if sudoTagWord.MatchString(right[word:i]) {
				continue
			}
			specs = append(specs, right[start:i])
			start = i + 1
		}
	}
	return append(specs, right[start:])
}

// parseSudoRule reads `who hosts = (runas) TAGS: commands`. It is not a sudoers
// compiler and does not try to be: a line it cannot make out is left out of the
// reading and left alone in the file, and `visudo` remains what judges a write.
//
// What it must get right is whether a line reaches root, because a rule read wrongly
// here is root access the page, the guard and the alert all cannot see. So the run-as
// and the tags are followed command by command, the way sudo does: each carries on to
// the commands after it until another replaces it, and a later `(nobody) /bin/true`
// says nothing about the `(ALL) NOPASSWD: ALL` before it.
func parseSudoRule(text string, aliases sudoAliases) (SudoRule, bool) {
	left, right, ok := strings.Cut(text, "=")
	if !ok {
		return SudoRule{}, false
	}
	// "a, b  ALL": the list has spaces in it and the host field does not.
	normal := sudoListRe.ReplaceAllString(strings.TrimSpace(left), ",")
	fields := strings.Fields(normal)
	if len(fields) < 2 {
		return SudoRule{}, false
	}
	who := strings.Split(fields[0], ",")
	for i := range who {
		// sudoers lets any name be quoted, and "x" is x.
		who[i] = strings.ReplaceAll(who[i], `"`, "")
	}
	rule := SudoRule{Who: expandSudo(who, aliases.users, 0)}
	full, fullNoPassword, anyNoPassword := false, false, false
	for index, spec := range splitSudoSpecs(right) {
		if index > 0 {
			var found bool
			if _, spec, found = strings.Cut(spec, "="); !found {
				continue
			}
		}
		runas, noPassword := "", false
		for _, part := range splitSudoList(spec) {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "(") {
				if end := strings.Index(part, ")"); end > 0 {
					runas = strings.TrimSpace(part[1:end])
					part = strings.TrimSpace(part[end+1:])
				}
			}
			for {
				if m := sudoOptionRe.FindString(part); m != "" {
					part = part[len(m):]
					continue
				}
				m := sudoTagRe.FindStringSubmatch(part)
				if m == nil {
					break
				}
				switch m[1] {
				case "NOPASSWD":
					noPassword = true
				case "PASSWD":
					noPassword = false
				}
				part = part[len(m[0]):]
			}
			if part == "" {
				continue
			}
			reaches := runasReachesRoot(runas, aliases)
			for _, command := range expandSudo([]string{part}, aliases.commands, 0) {
				rule.Commands = append(rule.Commands, command)
				if !reaches {
					continue
				}
				if !rule.AsRoot {
					rule.AsRoot, rule.RunAs = true, runas
				}
				anyNoPassword = anyNoPassword || noPassword
				if command == "ALL" {
					full = true
					fullNoPassword = fullNoPassword || noPassword
				}
			}
		}
	}
	if len(rule.Commands) == 0 {
		return SudoRule{}, false
	}
	// `ALL, !/bin/su` still counts as everything: sudo's own manual says a negated
	// command is no protection, and calling it limited would understate root.
	rule.Full = full
	// A tag on one command says nothing about the rest: `ALL, NOPASSWD: /bin/ls`
	// still asks for a password to be root.
	if full {
		rule.NoPassword = fullNoPassword
	} else {
		rule.NoPassword = anyNoPassword
	}
	return rule, true
}

// runasReachesRoot reads the part before the colon, the users a command may run
// as. No parentheses at all means root, which is sudo's own default.
func runasReachesRoot(runas string, aliases sudoAliases) bool {
	users, _, _ := strings.Cut(runas, ":")
	users = strings.TrimSpace(users)
	if users == "" {
		return true
	}
	for _, item := range expandSudo(splitSudoList(users), aliases.runas, 0) {
		if item == "ALL" || item == "root" || item == "#0" {
			return true
		}
	}
	return false
}

// ruleCarrier says whether a rule applies to an account, and through which group
// when it is a group's. A later "!name" in the list takes the account back out.
func ruleCarrier(rule SudoRule, u User) (applies bool, via string) {
	for _, who := range rule.Who {
		negated := strings.HasPrefix(who, "!")
		name := strings.TrimPrefix(who, "!")
		matched, group := false, ""
		switch {
		case name == "ALL", name == u.Username, name == "#"+strconv.Itoa(u.UID):
			matched = true
		case name == "%#"+strconv.Itoa(u.GID):
			matched, group = true, strconv.Itoa(u.GID)
		case strings.HasPrefix(name, "%"):
			if contains(u.Groups, strings.TrimPrefix(name, "%")) {
				matched, group = true, strings.TrimPrefix(name, "%")
			}
		}
		if !matched {
			continue
		}
		if negated {
			applies, via = false, ""
			continue
		}
		applies, via = true, group
	}
	return applies, via
}

func whoCovers(who []string, u User) bool {
	applies, _ := ruleCarrier(SudoRule{Who: who}, u)
	return applies
}

// rootRoutesFor is every way this account reaches root.
func rootRoutesFor(u User, rules []SudoRule) []RootRoute {
	routes := []RootRoute{}
	if u.UID == 0 {
		return append(routes, RootRoute{Kind: "uid0", Usable: true})
	}
	hasPassword := u.Password != PasswordNone && u.Password != PasswordLocked
	if rules == nil {
		for _, group := range u.Groups {
			if sudoGroupsWhenBlind[group] {
				routes = append(routes, RootRoute{Kind: "sudo", Via: group, Usable: hasPassword})
			}
		}
	}
	for _, rule := range rules {
		applies, via := ruleCarrier(rule, u)
		if !applies || !rule.AsRoot {
			continue
		}
		noPassword := rule.NoPassword || whoCovers(rule.noAuth, u)
		routes = append(routes, RootRoute{
			Kind: "sudo", Via: via, NoPassword: noPassword, Limited: !rule.Full,
			RuleID: rule.ID, File: rule.File, Usable: noPassword || hasPassword,
		})
	}
	for _, group := range u.Groups {
		if rootEquivalentGroups[group] {
			routes = append(routes, RootRoute{Kind: "group", Via: group, Usable: true})
		}
	}
	return routes
}

// --- what the dashboard writes ---------------------------------------------

// managedPrefix names the file this dashboard owns per account. Sudo reads a name
// with no dot in it, so "jd-ion" is live and the temporary files below are not.
const managedPrefix = "jd-"

// Sudo levels an operator can grant.
const (
	SudoNone       = "none"
	SudoPassword   = "password"
	SudoNoPassword = "nopassword"
)

// SudoGrant is what the dashboard's own file for an account says. It is one of
// the rules that may apply to the account, not the sum of them: a group's rule
// and cloud-init's file are read beside it and removed separately.
type SudoGrant struct {
	Level string `json:"level"`
	// Commands narrows the grant; empty is everything.
	Commands []string `json:"commands"`
}

// sudoCommandRe holds what visudo took when asked: an @ is refused everywhere,
// escaped or not, and an = only in an argument, escaped by the line below.
var sudoCommandRe = regexp.MustCompile(`^/[A-Za-z0-9_./+%-]+( [A-Za-z0-9_./+%=*-]+)*$`)

func (s *Service) managedSudoPath(username string) string {
	return s.path(filepath.Join("sudoers.d", managedPrefix+username))
}

func (g SudoGrant) line(username string) (string, error) {
	tag := ""
	if g.Level == SudoNoPassword {
		tag = "NOPASSWD: "
	} else if g.Level != SudoPassword {
		return "", fmt.Errorf("sudo level must be none, password or nopassword")
	}
	if len(g.Commands) == 0 {
		return fmt.Sprintf("%s ALL=(ALL:ALL) %sALL", username, tag), nil
	}
	for _, command := range g.Commands {
		if !sudoCommandRe.MatchString(command) {
			return "", fmt.Errorf("%q is not a command sudo can name: it needs a full path and plain arguments", command)
		}
	}
	escaped := make([]string, len(g.Commands))
	for i, command := range g.Commands {
		escaped[i] = sudoCommandEscaper.Replace(command)
	}
	return fmt.Sprintf("%s ALL=(root) %s%s", username, tag, strings.Join(escaped, ", ")), nil
}

// sudoCommandEscaper covers the one character sudoCommandRe lets through that
// means something to sudoers. `/usr/bin/env A=B` is what somebody types, and it is
// a syntax error as written.
var (
	sudoCommandEscaper   = strings.NewReplacer("=", `\=`)
	sudoCommandUnescaper = strings.NewReplacer(`\=`, "=")
)

// CheckGrant says whether a grant could be written, before there is an account
// to write it for.
func CheckGrant(username string, grant SudoGrant) error {
	if grant.Level == SudoNone {
		return nil
	}
	_, err := grant.line(username)
	return err
}

// ManagedGrant reads the dashboard's own rule for the account.
func (s *Service) ManagedGrant(username string) SudoGrant {
	grant := SudoGrant{Level: SudoNone, Commands: []string{}}
	for _, rule := range s.sudoRules() {
		if !rule.Managed || filepath.Base(rule.File) != managedPrefix+username {
			continue
		}
		grant.Level = SudoPassword
		if rule.NoPassword {
			grant.Level = SudoNoPassword
		}
		if !rule.Full {
			grant.Commands = make([]string, len(rule.Commands))
			for i, command := range rule.Commands {
				grant.Commands[i] = sudoCommandUnescaper.Replace(command)
			}
		}
	}
	return grant
}

// SetSudoGrant writes or removes the dashboard's own rule. A grant is checked by
// visudo before it can take effect, because one bad line in a sudoers file stops
// sudo for everyone on the machine.
func (s *Service) SetSudoGrant(ctx context.Context, username string, grant SudoGrant) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.managedSudoPath(username)
	change := Change{Username: username}
	for _, rule := range s.sudoRules() {
		if rule.Managed && rule.File == path {
			change.DropRule = rule.ID
		}
	}
	if grant.Level == SudoNone {
		if change.DropRule != "" {
			if err := s.guard(ctx, change); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	line, err := grant.line(username)
	if err != nil {
		return err
	}
	// Replacing a rule can close a way in as surely as removing one: asking for a
	// password on a keys-only administrator, or narrowing the last one to a command.
	if rule, ok := parseSudoRule(line, sudoAliases{}); ok {
		change.AddRule = &rule
		if err := s.guard(ctx, change); err != nil {
			return err
		}
	}
	content := "# Written by Just Dashboard for " + username + ". Change it there, or delete this file.\n" + line + "\n"
	return s.writeSudoers(ctx, path, []byte(content))
}

// writeSudoers puts a file in place only after visudo has passed both it and the
// whole configuration with it in place, and puts the old one back if the whole
// no longer parses. The temporary name has a dot in it, so sudo ignores it.
func (s *Service) writeSudoers(ctx context.Context, path string, content []byte) error {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	temp := filepath.Join(dir, ".jd-"+hex.EncodeToString(token[:]))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(temp, content, 0o440); err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := os.Chmod(temp, 0o440); err != nil {
		return err
	}
	if _, err := s.run(ctx, "", "visudo", "--check", "--file", temp); err != nil {
		if errors.Is(err, ErrToolMissing) {
			return fmt.Errorf("sudo is not installed on this host")
		}
		return fmt.Errorf("that rule was refused and nothing was changed: %w", err)
	}
	previous, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		// Without the old contents there is nothing to put back, so a failure below
		// could not be undone: this has to stop while nothing has changed.
		return fmt.Errorf("the current %s could not be read, so nothing was changed: %w", path, readErr)
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	if _, err := s.run(ctx, "", "visudo", "--check"); err != nil {
		var undo error
		if readErr == nil {
			// Renamed into place: the file on disk is already read-only, and the
			// whole configuration is broken until this lands.
			undo = writeAtomic(path, previous)
		} else {
			undo = os.Remove(path)
		}
		if undo != nil {
			return fmt.Errorf("sudo's configuration no longer parses and %s could not be put back, so sudo may be broken: %v (%w)", path, undo, err)
		}
		return fmt.Errorf("sudo's configuration no longer parsed with that rule, so it was taken back: %w", err)
	}
	return nil
}

// RulesFor is every rule that applies to an account, in the order sudo reads
// them. Reads that fail are an empty list: a host with no sudo has no rules.
func (s *Service) RulesFor(ctx context.Context, username string) ([]SudoRule, error) {
	user, err := s.Get(ctx, username)
	if err != nil {
		return nil, err
	}
	out := []SudoRule{}
	for _, rule := range s.sudoRules() {
		if applies, _ := ruleCarrier(rule, *user); applies {
			out = append(out, rule)
		}
	}
	return out, nil
}

// RemoveSudoRule takes one rule away from an account, in whatever file holds it.
// A rule granted to a group is the group's and is refused: leaving the group is
// what takes it away, and rewriting it here would take it from everyone else.
func (s *Service) RemoveSudoRule(ctx context.Context, username, id string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if username == "root" {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	// A rule can outlive the account it names, and taking that one out is still right.
	names := []string{username}
	if account, err := s.lookup(username); err == nil {
		names = s.ruleNames(*account)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var target *SudoRule
	rules := s.sudoRules()
	for i := range rules {
		if rules[i].ID == id {
			target = &rules[i]
		}
	}
	if target == nil {
		return fmt.Errorf("that rule is no longer in the sudoers files")
	}
	if !containsAny(target.Who, names) {
		for _, who := range target.Who {
			if strings.HasPrefix(who, "%") {
				return fmt.Errorf("this rule belongs to the group %s: remove %s from the group instead",
					strings.TrimPrefix(who, "%"), username)
			}
		}
		return fmt.Errorf("this rule does not name %s", username)
	}
	if err := s.guard(ctx, Change{Username: username, DropRule: id}); err != nil {
		return err
	}
	// Two identical lines are one ID, and reporting success while one stays would
	// be a rule the page says is gone and sudo still honours.
	for attempt := 0; attempt < 32; attempt++ {
		if err := s.dropRuleFor(ctx, *target, username, names); err != nil {
			return err
		}
		target = nil
		for _, rule := range s.sudoRules() {
			if rule.ID == id {
				rule := rule
				target = &rule
				break
			}
		}
		if target == nil {
			return nil
		}
	}
	return fmt.Errorf("the rule is still in the sudoers files")
}

// ruleNames is every spelling a sudoers rule can name this account by. The private
// group and the number are the ones a rule outlives the account through: the next
// account called the same, or given the freed number, holds whatever it grants. A
// number another account shares is that account's too, so it is left out.
func (s *Service) ruleNames(u User) []string {
	names := []string{u.Username}
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return names
	}
	sharedUID, sharedGID := false, false
	for _, other := range users {
		if other.Username == u.Username {
			continue
		}
		sharedUID = sharedUID || other.UID == u.UID
		sharedGID = sharedGID || other.GID == u.GID
	}
	if !sharedUID {
		names = append(names, "#"+strconv.Itoa(u.UID))
	}
	members, byGID := parseGroups(s.path("group"))
	private := !sharedGID && byGID[u.GID] == u.Username
	for user, groups := range members {
		if user != u.Username && contains(groups, u.Username) {
			private = false
		}
	}
	if private {
		names = append(names, "%"+u.Username, "%#"+strconv.Itoa(u.GID))
	}
	return names
}

func containsAny(list, wanted []string) bool {
	for _, item := range wanted {
		if contains(list, item) {
			return true
		}
	}
	return false
}

// dropRuleFor rewrites the file without this account's part of the rule. A rule
// that names others too keeps them; a rule that was all this account's goes,
// along with the file if that was the only thing it said and the dashboard wrote
// it. The line must still say what the rule read did: this is matched by position,
// and an edit made since would otherwise take the wrong one out.
func (s *Service) dropRuleFor(ctx context.Context, rule SudoRule, username string, names []string) error {
	if rule.Managed && filepath.Base(rule.File) == managedPrefix+username {
		return os.Remove(rule.File)
	}
	b, err := os.ReadFile(rule.File)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	if rule.Line < 1 || rule.endLine > len(lines) || joinSudoContinuation(lines[rule.Line-1:rule.endLine]) != rule.Text {
		return fmt.Errorf("%s changed while this was being read: try again", rule.File)
	}
	replacement := []string{}
	if others := otherPeople(rule.Who, names...); len(others) > 0 {
		rewritten, ok := rewriteRuleWho(strings.Join(lines[rule.Line-1:rule.endLine], "\n"), others)
		if !ok {
			return fmt.Errorf("the rule in %s names more than this account and could not be rewritten: edit the file", rule.File)
		}
		replacement = []string{rewritten}
	}
	out := append(append(append([]string{}, lines[:rule.Line-1]...), replacement...), lines[rule.endLine:]...)
	return s.writeSudoers(ctx, rule.File, []byte(strings.Join(out, "\n")))
}

// joinSudoContinuation is what readSudoLines made of these lines.
func joinSudoContinuation(lines []string) string {
	line := lines[0]
	for _, next := range lines[1:] {
		line = strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\") + " " + strings.TrimSpace(next)
	}
	return strings.TrimSpace(line)
}

func otherPeople(who []string, names ...string) []string {
	out := []string{}
	for _, name := range who {
		if !contains(names, name) {
			out = append(out, name)
		}
	}
	return out
}

// sudoWhoRe is the user list, whose names stop at a comma: `\S+` took the comma
// with it, so `alice, bob ALL` came back as `bob bob ALL`, which is a syntax error.
var sudoWhoRe = regexp.MustCompile(`^\s*[^\s,]+(\s*,\s*[^\s,]+)*\s+`)

func rewriteRuleWho(text string, who []string) (string, bool) {
	flat := strings.ReplaceAll(strings.ReplaceAll(text, "\\\n", " "), "\n", " ")
	loc := sudoWhoRe.FindStringIndex(flat)
	if loc == nil {
		return "", false
	}
	return strings.Join(who, ", ") + " " + flat[loc[1]:], true
}

// removeRulesNaming is deleting an account's rules with it: a rule left behind
// would hand root to whoever is next given that name.
func (s *Service) removeRulesNaming(ctx context.Context, u User) error {
	names := s.ruleNames(u)
	for attempt := 0; attempt < 32; attempt++ {
		var target *SudoRule
		rules := s.sudoRules()
		for i := range rules {
			if containsAny(rules[i].Who, names) {
				target = &rules[i]
				break
			}
		}
		if target == nil {
			return nil
		}
		if err := s.dropRuleFor(ctx, *target, u.Username, names); err != nil {
			return err
		}
	}
	return fmt.Errorf("the sudoers files still name %s", u.Username)
}
