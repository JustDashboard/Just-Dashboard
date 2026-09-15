// Package linuxusers manages the host's own accounts and their SSH keys.
//
// These are operating-system accounts, entirely separate from the dashboard's
// own logins. Creating one here grants somebody access to the machine, so the
// whole package sits behind the system-admin capability.
package linuxusers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidUser = errors.New("invalid username")
	ErrNotFound    = errors.New("user not found")
	ErrProtected   = errors.New("refusing to modify a protected system account")
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
	CanLogin   bool       `json:"canLogin"`
}

type Service struct{}

func New() *Service { return &Service{} }

// List reads the account databases directly. Going through getent would be
// slower and no more accurate; /etc/passwd is the source of truth for local
// accounts, which is all this manages.
func (s *Service) List(ctx context.Context) ([]User, error) {
	users, err := parsePasswd("/etc/passwd")
	if err != nil {
		return nil, err
	}
	groups, primary := parseGroups("/etc/group")
	// Shadow is root-only. If it is unreadable the lock state is simply
	// unknown rather than reported wrongly.
	locked, noPassword := parseShadow("/etc/shadow")
	lastLogins := lastLogins(ctx)

	for i := range users {
		u := &users[i]
		u.System = u.UID < 1000 || u.UID == 65534
		u.CanLogin = !strings.HasSuffix(u.Shell, "nologin") && !strings.HasSuffix(u.Shell, "false")
		u.Locked = locked[u.Username]
		u.NoPassword = noPassword[u.Username]
		u.Groups = append([]string{}, groups[u.Username]...)
		if g, ok := primary[u.GID]; ok && !contains(u.Groups, g) {
			u.Groups = append([]string{g}, u.Groups...)
		}
		if ll, ok := lastLogins[u.Username]; ok {
			u.LastLogin = &ll.at
			u.LastFrom = ll.from
		}
		keys, _, keyErr := s.ListKeys(u.Username)
		if keyErr == nil {
			u.SSHKeys = len(keys)
		}
	}
	sort.Slice(users, func(i, j int) bool {
		// Real accounts first: system accounts are noise on this page.
		if users[i].System != users[j].System {
			return !users[i].System
		}
		return users[i].Username < users[j].Username
	})
	return users, nil
}

func (s *Service) Get(ctx context.Context, username string) (*User, error) {
	users, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == username {
			return &users[i], nil
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

// parseShadow reads the password field only. A field starting with ! or * means
// the account cannot authenticate with a password; an empty field means no
// password is required at all, which is worth flagging loudly.
func parseShadow(path string) (locked, noPassword map[string]bool) {
	locked, noPassword = map[string]bool{}, map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 2 {
			continue
		}
		name, hash := fields[0], fields[1]
		switch {
		case hash == "":
			noPassword[name] = true
		case strings.HasPrefix(hash, "!"), strings.HasPrefix(hash, "*"):
			locked[name] = true
		}
	}
	return
}

type loginRecord struct {
	at   time.Time
	from string
}

// lastLogins reads `lastlog`, falling back to `last`. Both are text formats
// that vary between distributions, so parse failures degrade to "unknown"
// rather than failing the whole listing.
func lastLogins(ctx context.Context) map[string]loginRecord {
	out := map[string]loginRecord{}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	buf, err := exec.CommandContext(ctx, "lastlog").Output()
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(buf))
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
				from := strings.Join(fields[:start], " ")
				return ts.UTC(), strings.TrimSpace(from), true
			}
		}
	}
	return time.Time{}, "", false
}

type CreateOptions struct {
	Username string
	Comment  string
	Shell    string
	Groups   []string
	System   bool
	NoHome   bool
}

// Create adds an account with no password set, so the only way in is an SSH
// key the operator adds deliberately afterwards.
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
		if !filepath.IsAbs(opts.Shell) {
			return fmt.Errorf("shell must be an absolute path")
		}
		args = append(args, "--shell", opts.Shell)
	}
	if opts.Comment != "" {
		if strings.ContainsAny(opts.Comment, ":\n") {
			return fmt.Errorf("comment may not contain colons or newlines")
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
	if _, err := run(ctx, "useradd", args...); err != nil {
		return err
	}
	// useradd leaves the password field as "!" already, but making it
	// explicit means the account state does not depend on distro defaults.
	_, err := run(ctx, "usermod", "--lock", opts.Username)
	return err
}

func (s *Service) Delete(ctx context.Context, username string, removeHome bool) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	args := []string{}
	if removeHome {
		args = append(args, "--remove")
	}
	args = append(args, username)
	_, err := run(ctx, "userdel", args...)
	return err
}

func (s *Service) SetLocked(ctx context.Context, username string, locked bool) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if protected[username] && locked {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	flag := "--unlock"
	if locked {
		flag = "--lock"
	}
	_, err := run(ctx, "usermod", flag, username)
	return err
}

func (s *Service) SetShell(ctx context.Context, username, shell string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if !filepath.IsAbs(shell) {
		return fmt.Errorf("shell must be an absolute path")
	}
	_, err := run(ctx, "usermod", "--shell", shell, username)
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
	_, err := run(ctx, "usermod", "--groups", strings.Join(groups, ","), username)
	return err
}

// ListGroups is used by the frontend to offer a picker rather than a free-text
// field, which is how typos in group names get avoided.
func (s *Service) ListGroups(ctx context.Context) ([]string, error) {
	_, byGID := parseGroups("/etc/group")
	out := make([]string, 0, len(byGID))
	for _, name := range byGID {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("%s is not installed on this host", name)
		}
		return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}
