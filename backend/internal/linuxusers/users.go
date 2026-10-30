// Package linuxusers manages the host's own accounts and their SSH keys.
//
// These are operating-system accounts, entirely separate from the dashboard's
// own logins. Creating one here grants somebody access to the machine, so the
// whole package sits behind the system-admin capability.
package linuxusers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrInvalidUser = errors.New("invalid username")
	ErrNotFound    = errors.New("user not found")
	ErrProtected   = errors.New("refusing to modify a protected system account")
	// ErrNothingToUnlock is what `usermod --unlock` should have said. On an
	// account with no password behind its "!" it prints a warning, changes
	// nothing and exits 0, which read as success here for as long as the Unlock
	// button existed.
	ErrNothingToUnlock = errors.New("there is no password to unlock")
	// ErrLastWayIn refuses a change that would leave nobody able to sign in and
	// become root over SSH.
	ErrLastWayIn = errors.New("this is the last way in")
)

// usernameRe matches what useradd itself accepts, which is stricter than what
// /etc/passwd can technically hold.
var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// protected accounts underpin the system; a dashboard button must not be able
// to delete or lock the account the machine boots with.
var protected = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"nobody": true, "systemd-network": true, "systemd-resolve": true,
	"sshd": true, "messagebus": true,
}

func ValidateUsername(name string) error {
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidUser, name)
	}
	return nil
}

type User struct {
	Username   string     `json:"username"`
	UID        int        `json:"uid"`
	GID        int        `json:"gid"`
	Comment    string     `json:"comment"`
	Home       string     `json:"home"`
	Shell      string     `json:"shell"`
	Groups     []string   `json:"groups"`
	System     bool       `json:"system"`
	Locked     bool       `json:"locked"`
	NoPassword bool       `json:"noPassword"`
	LastLogin  *time.Time `json:"lastLogin,omitempty"`
	LastFrom   string     `json:"lastLoginFrom,omitempty"`
	SSHKeys    int        `json:"sshKeyCount"`
	// KeysUnknown is a key source this could not read: a symlinked file, or an
	// AuthorizedKeysCommand. SSHKeys is then a floor, not the answer.
	KeysUnknown bool `json:"keysUnknown,omitempty"`
	CanLogin    bool `json:"canLogin"`

	// Password says what is behind the shadow field, which Locked and
	// NoPassword cannot: "!" and "!$y$…" are both Locked, and only the second
	// has a password to come back to. See the Password* constants.
	Password string `json:"password"`
	// Disabled is an account whose expiry date has passed: nothing admits it,
	// keys included, which a password lock does not do.
	Disabled bool `json:"disabled"`
	// PasswordDead is the same outcome reached by the password's age rules, which
	// clearing the expiry date does not undo.
	PasswordDead bool          `json:"passwordDead,omitempty"`
	ExpiresOn    string        `json:"expiresOn,omitempty"`
	Aging        PasswordAging `json:"aging"`
	// SignIn is whether SSH would let this account in, and what stands in the
	// way, taken from the account and sshd's own effective configuration.
	SignIn SignIn `json:"signIn"`
	// RootRoutes is every way this account can become root, read from the
	// sudoers files rather than guessed from group names.
	RootRoutes []RootRoute `json:"rootRoutes"`
	Protected  bool        `json:"protected"`
	// PrimaryGroup is named separately because Groups holds it too.
	PrimaryGroup string `json:"primaryGroup,omitempty"`
}

// Service reads and changes the host's accounts. A single mutex serialises the
// writes: the shadow utilities take their own locks, but the sudoers, limit and
// defaults files written here have nothing else protecting a read-modify-write.
type Service struct {
	etc string
	run runner
	now func() time.Time
	mu  sync.Mutex
}

func New() *Service {
	return &Service{etc: "/etc", run: runOnHost, now: time.Now}
}

// accountState is one reading of everything an account's standing depends on,
// so a change can be judged against it before it is made (guard.go).
type accountState struct {
	users  []User
	rules  []SudoRule
	policy SSHPolicy
	shadow map[string]shadowEntry
}

func (s *Service) path(name string) string { return filepath.Join(s.etc, name) }

// read assembles the state. It reads the account databases directly: going
// through getent would be slower and no more accurate, since /etc/passwd is the
// source of truth for local accounts, which is all this manages.
func (s *Service) read(ctx context.Context) (*accountState, error) {
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return nil, err
	}
	groups, primary := parseGroups(s.path("group"))
	// Shadow is root-only. If it is unreadable the password state is reported
	// as unknown rather than wrongly.
	shadow, readable := parseShadow(s.path("shadow"))
	last := s.lastLogins(ctx)
	rules := s.sudoRules()
	policy := s.sshPolicy(ctx, "")
	today := s.today()

	for i := range users {
		u := &users[i]
		u.System = u.UID < 1000 || u.UID == 65534
		u.Protected = protected[u.Username]
		u.CanLogin = isLoginShell(u.Shell)
		u.Password = PasswordUnknown
		if entry, ok := shadow[u.Username]; ok {
			u.Password = entry.passwordState()
			u.Locked = entry.locked()
			u.NoPassword = entry.Hash == ""
			u.Disabled = entry.expired(today)
			u.PasswordDead = entry.passwordDead(today)
			u.ExpiresOn = dayString(entry.Expire)
			u.Aging = entry.aging()
		} else if readable {
			u.Password = PasswordNone
		}
		u.Groups = append([]string{}, groups[u.Username]...)
		if g, ok := primary[u.GID]; ok {
			u.PrimaryGroup = g
			if !contains(u.Groups, g) {
				u.Groups = append([]string{g}, u.Groups...)
			}
		}
		if ll, ok := last[u.Username]; ok {
			u.LastLogin = &ll.at
			u.LastFrom = ll.from
		}
		// A key file that cannot be read is not a file with no keys in it. sshd may
		// well read it, and "no keys" would let the guard close the last way in.
		keys, _, keyErr := s.ListKeys(u.Username)
		if keyErr == nil {
			u.SSHKeys = len(keys)
		} else {
			u.KeysUnknown = true
		}
		other, unreadable := otherKeys(policy, *u)
		u.SSHKeys += len(other)
		u.KeysUnknown = u.KeysUnknown || unreadable || policy.KeysCommand
		u.RootRoutes = rootRoutesFor(*u, rules)
		u.SignIn = evaluateSignIn(*u, policy)
	}
	sort.Slice(users, func(i, j int) bool {
		// Real accounts first: system accounts are noise on this page.
		if users[i].System != users[j].System {
			return !users[i].System
		}
		return users[i].Username < users[j].Username
	})
	return &accountState{users: users, rules: rules, policy: policy, shadow: shadow}, nil
}

// List is every account on the host.
func (s *Service) List(ctx context.Context) ([]User, error) {
	st, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	return st.users, nil
}

// isLoginShell answers the one thing a shell says about signing in.
func isLoginShell(shell string) bool {
	base := filepath.Base(shell)
	return base != "nologin" && base != "false"
}

// IsLoginShell is isLoginShell for a handler judging a request before the account
// exists.
func IsLoginShell(shell string) bool { return isLoginShell(shell) }

func (s *Service) today() int { return int(s.now().Unix() / 86400) }

// Get is one account. Unlike the list it asks sshd about this user by name, so
// a Match block that treats them differently is in the answer.
func (s *Service) Get(ctx context.Context, username string) (*User, error) {
	users, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == username {
			u := users[i]
			u.SignIn = evaluateSignIn(u, s.sshPolicy(ctx, username))
			return &u, nil
		}
	}
	return nil, ErrNotFound
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func parsePasswd(path string) ([]User, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []User{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 7 {
			continue
		}
		uid, err1 := strconv.Atoi(fields[2])
		gid, err2 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, User{
			Username: fields[0], UID: uid, GID: gid,
			Comment: strings.TrimRight(fields[4], ","),
			Home:    fields[5], Shell: fields[6],
			Groups: []string{},
		})
	}
	return out, sc.Err()
}

// parseGroups returns supplementary memberships per user and the name of each
// GID so a user's primary group can be shown too.
func parseGroups(path string) (map[string][]string, map[int]string) {
	members := map[string][]string{}
	byGID := map[int]string{}
	f, err := os.Open(path)
	if err != nil {
		return members, byGID
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 4 {
			continue
		}
		name := fields[0]
		if gid, err := strconv.Atoi(fields[2]); err == nil {
			byGID[gid] = name
		}
		for _, m := range strings.Split(fields[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				members[m] = append(members[m], name)
			}
		}
	}
	return members, byGID
}

type loginRecord struct {
	at   time.Time
	from string
}

// lastLogins reads `lastlog` on the host, or `lastlog2` where a distribution
// has replaced it. Both are text formats that vary between distributions, so
// parse failures degrade to "unknown" rather than failing the whole listing.
func (s *Service) lastLogins(ctx context.Context) map[string]loginRecord {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	buf, err := s.run(ctx, "", "lastlog")
	if err != nil {
		if buf, err = s.run(ctx, "", "lastlog2"); err != nil {
			return map[string]loginRecord{}
		}
	}
	return parseLastlog(buf)
}

func parseLastlog(buf string) map[string]loginRecord {
	out := map[string]loginRecord{}
	sc := bufio.NewScanner(strings.NewReader(buf))
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		user := fields[0]
		if strings.Contains(line, "**Never logged in**") {
			continue
		}
		// The timestamp is the trailing portion; formats differ, so several
		// layouts are tried before giving up on the line.
		rest := strings.TrimSpace(strings.TrimPrefix(line, user))
		if ts, from, ok := parseLastlogTail(rest); ok {
			out[user] = loginRecord{at: ts, from: from}
		}
	}
	return out
}

var lastlogLayouts = []string{
	"Mon Jan 2 15:04:05 -0700 2006",
	"Mon Jan  2 15:04:05 -0700 2006",
	"Mon Jan 2 15:04:05 2006",
	"Mon Jan  2 15:04:05 2006",
}

func parseLastlogTail(rest string) (time.Time, string, bool) {
	fields := strings.Fields(rest)
	if len(fields) < 5 {
		return time.Time{}, "", false
	}
	// Walk backwards over progressively longer suffixes: the date is at the
	// end and the columns before it are port and host, which vary in count.
	for start := 0; start < len(fields)-3; start++ {
		candidate := strings.Join(fields[start:], " ")
		for _, layout := range lastlogLayouts {
			if ts, err := time.Parse(layout, candidate); err == nil {
				return ts.UTC(), lastlogOrigin(fields[:start]), true
			}
		}
	}
	return time.Time{}, "", false
}

// lastlogOrigin picks the address out of lastlog's Port and From columns. Both
// are optional, and a console login has a tty and nothing else, so a lone field
// that names a terminal says the sign-in came from nowhere.
func lastlogOrigin(columns []string) string {
	switch len(columns) {
	case 0:
		return ""
	case 1:
		if isTTY(columns[0]) {
			return ""
		}
		return columns[0]
	}
	return columns[len(columns)-1]
}

func isTTY(name string) bool {
	for _, prefix := range []string{"pts/", "tty", "ssh", "console", ":"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

type CreateOptions struct {
	Username string
	Comment  string
	Shell    string
	Groups   []string
	System   bool
	NoHome   bool
}

// Create adds an account with no password set; handlers then give it the ways
// in the operator chose, so nothing about its state depends on distro defaults.
func (s *Service) Create(ctx context.Context, opts CreateOptions) error {
	if err := ValidateUsername(opts.Username); err != nil {
		return err
	}
	args := []string{}
	if opts.NoHome {
		args = append(args, "--no-create-home")
	} else {
		args = append(args, "--create-home")
	}
	if opts.System {
		args = append(args, "--system")
	}
	if opts.Shell != "" {
		if err := s.validateShell(opts.Shell); err != nil {
			return err
		}
		args = append(args, "--shell", opts.Shell)
	}
	if opts.Comment != "" {
		if err := validateComment(opts.Comment); err != nil {
			return err
		}
		args = append(args, "--comment", opts.Comment)
	}
	if len(opts.Groups) > 0 {
		for _, g := range opts.Groups {
			if err := ValidateUsername(g); err != nil {
				return fmt.Errorf("invalid group %q", g)
			}
		}
		args = append(args, "--groups", strings.Join(opts.Groups, ","))
	}
	args = append(args, opts.Username)
	if _, err := s.run(ctx, "", "useradd", args...); err != nil {
		return err
	}
	// useradd leaves the password field as "!" already, but making it
	// explicit means the account state does not depend on distro defaults.
	_, err := s.run(ctx, "", "usermod", "--lock", opts.Username)
	return err
}

func validateComment(comment string) error {
	if strings.ContainsAny(comment, ":\n") {
		return fmt.Errorf("comment may not contain colons or newlines")
	}
	return nil
}

// validateShell takes the host's own list of login shells, plus the two that
// mean "no login". An absolute path was all this used to ask, and a typo in one
// is an account nobody can sign in to, root's included.
func (s *Service) validateShell(shell string) error {
	if !filepath.IsAbs(shell) {
		return fmt.Errorf("shell must be an absolute path")
	}
	// A colon ends the field in /etc/passwd and a line break the line in
	// /etc/default/useradd, whose format this value is also written into.
	if strings.ContainsAny(shell, ":") || strings.IndexFunc(shell, unicode.IsControl) >= 0 {
		return fmt.Errorf("that is not a shell's path")
	}
	if !isLoginShell(shell) {
		return nil
	}
	known := s.Shells()
	// A host with no list at all has nothing to judge against.
	if len(known) == 0 || contains(known, shell) {
		return nil
	}
	return fmt.Errorf("%s is not listed in /etc/shells", shell)
}

// Shells is the host's list of login shells.
func (s *Service) Shells() []string {
	b, err := os.ReadFile(s.path("shells"))
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// SetLocked locks or unlocks the password. Unlocking asks the shadow file first
// rather than usermod, because usermod's own answer cannot be trusted here: on a
// bare "!" it warns on stderr, leaves the file alone and exits 0.
func (s *Service) SetLocked(ctx context.Context, username string, locked bool) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if protected[username] && locked {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, known := s.shadowEntry(username)
	if !locked && known && entry.passwordState() != PasswordLocked {
		return fmt.Errorf("%w on %s: set one instead", ErrNothingToUnlock, username)
	}
	if locked {
		if err := s.guard(ctx, Change{Username: username, Password: stringPtr(PasswordLocked)}); err != nil {
			return err
		}
	}
	flag := "--unlock"
	if locked {
		flag = "--lock"
	}
	if _, err := s.run(ctx, "", "usermod", flag, username); err != nil {
		return err
	}
	if after, ok := s.shadowEntry(username); ok && known && after.locked() != locked {
		return fmt.Errorf("usermod reported success but %s's password did not change", username)
	}
	return nil
}

// SetShell changes the login shell. It belongs to whoever may use the account,
// so a protected account keeps a shell it can log in with, and the last way in
// is not closed by this route either.
func (s *Service) SetShell(ctx context.Context, username, shell string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if err := s.validateShell(shell); err != nil {
		return err
	}
	if protected[username] && !isLoginShell(shell) {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Username: username, Shell: &shell}); err != nil {
		return err
	}
	_, err := s.run(ctx, "", "usermod", "--shell", shell, username)
	return err
}

func (s *Service) SetGroups(ctx context.Context, username string, groups []string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	for _, g := range groups {
		if err := ValidateUsername(g); err != nil {
			return fmt.Errorf("invalid group %q", g)
		}
	}
	// The account's main group is not one usermod lists, and naming it would add a
	// row to /etc/group that means nothing. It still counts toward what the account
	// can do, so the guard is told about it.
	primary := s.primaryGroupOf(username)
	supplementary := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != primary {
			supplementary = append(supplementary, g)
		}
	}
	held := append([]string{}, supplementary...)
	if primary != "" {
		held = append(held, primary)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(ctx, Change{Username: username, Groups: &held}); err != nil {
		return err
	}
	_, err := s.run(ctx, "", "usermod", "--groups", strings.Join(supplementary, ","), username)
	return err
}

func (s *Service) primaryGroupOf(username string) string {
	u, err := s.lookup(username)
	if err != nil {
		return ""
	}
	_, byGID := parseGroups(s.path("group"))
	return byGID[u.GID]
}
