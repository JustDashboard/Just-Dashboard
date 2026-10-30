package linuxusers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Deleting an account is the one thing here that destroys data, so it opens by
// reading what would go and what would be left behind. A home is the obvious
// part; the rest is what bites later: a process still running under a number
// nobody owns, files elsewhere that the next account given that number inherits,
// a sudo rule that hands root to whoever is next given that name.

// archiveRoot is where an account's home goes when it is kept as an archive.
var archiveRoot = "/var/backups/just-dashboard/accounts"

// Removal is what deleting an account would touch.
type Removal struct {
	Username string `json:"username"`
	Home     string `json:"home"`
	// HomeExists and HomeBytes describe the directory; HomeShared is a home
	// another account also has, or one too near the root to ever delete.
	HomeExists bool  `json:"homeExists"`
	HomeBytes  int64 `json:"homeBytes"`
	HomeUnsafe bool  `json:"homeUnsafe"`
	// Processes is how many are running under the account now.
	Processes int `json:"processes"`
	// CronJobs is whether it has a crontab, which is deleted with it.
	CronJobs  bool       `json:"cronJobs"`
	SSHKeys   int        `json:"sshKeys"`
	SudoRules []SudoRule `json:"sudoRules"`
	// Elsewhere is a sample of files outside the home the account owns.
	Elsewhere          []string `json:"elsewhere"`
	ElsewhereTruncated bool     `json:"elsewhereTruncated"`
	// SharesNumber names another account with this one's number, root included for
	// a second uid 0. Its processes and files are that account's too, so neither can
	// be touched from here: deleting it removes the name and nothing else.
	SharesNumber string `json:"sharesNumber,omitempty"`
	// Blocked says why the account cannot be deleted at all.
	Blocked string `json:"blocked,omitempty"`
}

// HomeChoice is what happens to the home directory.
const (
	HomeKeep    = "keep"
	HomeArchive = "archive"
	HomeDelete  = "delete"
)

// RemovalOptions is the operator's answer to the preview.
type RemovalOptions struct {
	Home        string
	EndSessions bool
	// TransferTo hands the account's files to another account rather than
	// leaving them under a number nobody owns.
	TransferTo string
}

// RemovalResult reports what was done that the operator will want to find.
type RemovalResult struct {
	Archive string `json:"archive,omitempty"`
}

// unsafeHomes are never deleted: `userdel --remove` takes whatever the passwd
// file says, and a daemon's home is often somewhere like /var or /nonexistent.
var unsafeHomes = map[string]bool{
	"/": true, "/root": true, "/home": true, "/etc": true, "/usr": true, "/var": true, "/bin": true,
	"/sbin": true, "/lib": true, "/lib64": true, "/opt": true, "/srv": true, "/tmp": true, "/boot": true,
	"/dev": true, "/proc": true, "/sys": true, "/run": true, "/mnt": true, "/media": true,
	"/var/lib": true, "/var/log": true, "/var/run": true, "/var/spool": true, "/usr/local": true,
	"/nonexistent": true,
}

// homeIsUnsafe is a home that is not this account's alone to remove: too near the
// root, shared, with another account's home inside it, or a link. Tar stores a link
// as a link, so an archive of one holds nothing, and `userdel --remove` then empties
// whatever it points at. Compared as paths and checked on disk, because the passwd
// file can say anything.
func homeIsUnsafe(home string, username string, users []User) bool {
	clean := filepath.Clean(home)
	if !filepath.IsAbs(clean) || unsafeHomes[clean] {
		return true
	}
	for _, u := range users {
		if u.Username == username {
			continue
		}
		other := filepath.Clean(u.Home)
		if other == clean || strings.HasPrefix(other+"/", clean+"/") {
			return true
		}
	}
	if info, err := os.Lstat(hostexec.HostPath(clean)); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return false
}

// PreviewRemoval reads everything the dialog shows.
func (s *Service) PreviewRemoval(ctx context.Context, username string) (*Removal, error) {
	st, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	var target *User
	for i := range st.users {
		if st.users[i].Username == username {
			target = &st.users[i]
		}
	}
	if target == nil {
		return nil, ErrNotFound
	}
	out := &Removal{
		Username:  username,
		Home:      target.Home,
		SSHKeys:   target.SSHKeys,
		SudoRules: []SudoRule{},
		Elsewhere: []string{},
	}
	out.HomeUnsafe = homeIsUnsafe(target.Home, username, st.users)
	if protected[username] {
		out.Blocked = "the system depends on this account"
		return out, nil
	}
	if err := judge(st, Change{Username: username, Delete: true}); err != nil {
		out.Blocked = err.Error()
	}
	out.SharesNumber = s.numberIsShared(*target)
	// A home need not be one of the paths this container shares.
	if info, statErr := os.Stat(hostexec.HostPath(target.Home)); statErr == nil && info.IsDir() {
		out.HomeExists = true
		if !out.HomeUnsafe {
			if bytes, duErr := s.pathBytes(ctx, target.Home); duErr == nil {
				out.HomeBytes = bytes
			}
		}
	}
	_, out.CronJobs = s.readCrontab(ctx, username)
	names := s.ruleNames(*target)
	for _, rule := range st.rules {
		if containsAny(rule.Who, names) {
			out.SudoRules = append(out.SudoRules, rule)
		}
	}
	// Under a shared number these two would be the other account's, and showing them
	// as this one's is how somebody decides to end them.
	if out.SharesNumber == "" {
		if count, _, procErr := s.Processes(ctx, username, 0); procErr == nil {
			out.Processes = count
		}
		out.Elsewhere, out.ElsewhereTruncated = s.ownedElsewhere(ctx, target.UID, target.Home)
	}
	return out, nil
}

// ownedElsewhereSample is how many paths the dialog names. It is an illustration
// of the fact, not an inventory.
const ownedElsewhereSample = 20

// ownedElsewhere searches the local filesystems for what the number owns. A
// large disk takes a while, so it runs against a clock and says when it stopped.
func (s *Service) ownedElsewhere(ctx context.Context, uid int, home string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := s.run(ctx, "", "find", "/", "-xdev",
		"(", "-path", "/proc", "-o", "-path", "/sys", "-o", "-path", "/dev", "-o", "-path", "/run",
		"-o", "-path", filepath.Clean(home), ")", "-prune",
		"-o", "-uid", strconv.Itoa(uid), "-print")
	truncated := err != nil && ctx.Err() != nil
	paths := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/") {
			continue
		}
		if len(paths) == ownedElsewhereSample {
			truncated = true
			break
		}
		paths = append(paths, line)
	}
	return paths, truncated
}

// ErrAccountBusy is an account with processes running that the operator did not
// ask to stop.
var ErrAccountBusy = errors.New("the account has processes running")

// closeAccount is the part of a delete that happens at once, under the lock: judge
// it, then shut the account so nothing it does can interfere with what follows. An
// archive takes minutes and a handover runs a root chown across its files, and an
// account still able to sign in, or with a cron job, could use that time to swap a
// path under it. It also means a second delete judged a moment later sees this one's
// account already gone, so the two cannot both pass as "somebody else can still sign
// in". The sudo rules go here too, ahead of everything irreversible, because a rule
// that cannot be rewritten should stop the delete before it has done anything.
func (s *Service) closeAccount(ctx context.Context, username string, opts RemovalOptions) (*User, bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.read(ctx)
	if err != nil {
		return nil, false, "", err
	}
	var target *User
	for i := range st.users {
		if st.users[i].Username == username {
			target = &st.users[i]
		}
	}
	if target == nil {
		return nil, false, "", ErrNotFound
	}
	if err := judge(st, Change{Username: username, Delete: true}); err != nil {
		return nil, false, "", err
	}
	unsafeHome := homeIsUnsafe(target.Home, username, st.users)
	if opts.Home != "" && opts.Home != HomeKeep && unsafeHome {
		return nil, false, "", fmt.Errorf("%s is not %s's alone to remove, so its home was left alone", target.Home, username)
	}
	shared := s.numberIsShared(*target)
	if shared != "" {
		// Its processes and files are the other account's as well, and a second
		// uid 0's are every root process and file on the machine. Only its name can
		// go.
		if opts.EndSessions || opts.TransferTo != "" || (opts.Home != "" && opts.Home != HomeKeep) {
			return nil, false, "", fmt.Errorf("%w: %s has the number of %s, so only its name can be removed", ErrSharedNumber, username, shared)
		}
	} else if !opts.EndSessions {
		if count, _, procErr := s.Processes(ctx, username, 0); procErr == nil && count > 0 {
			return nil, false, "", fmt.Errorf("%w: %d. End them with the account, or stop them first", ErrAccountBusy, count)
		}
	}
	if _, err := s.run(ctx, "", "chage", "--expiredate", "1", username); err != nil {
		return nil, false, "", err
	}
	_, _ = s.run(ctx, "", "crontab", "-r", "-u", username)
	if err := s.removeRulesNaming(ctx, *target); err != nil {
		return nil, false, "", notDeleted(username, err)
	}
	return target, unsafeHome, shared, nil
}

// notDeleted says what state a failure part-way left the account in: closed, and
// without its sudo rules, rather than as it was.
func notDeleted(username string, err error) error {
	return fmt.Errorf("%w. %s is disabled and has lost its sudo rules, but was not deleted", err, username)
}

// Delete removes an account as the preview described.
func (s *Service) Delete(ctx context.Context, username string, opts RemovalOptions) (*RemovalResult, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if protected[username] {
		return nil, fmt.Errorf("%w: %s", ErrProtected, username)
	}
	switch opts.Home {
	case "", HomeKeep, HomeArchive, HomeDelete:
	default:
		return nil, fmt.Errorf("home must be keep, archive or delete")
	}
	if opts.TransferTo != "" {
		if err := ValidateUsername(opts.TransferTo); err != nil {
			return nil, err
		}
		if opts.TransferTo == username {
			return nil, fmt.Errorf("an account cannot hand its files to itself")
		}
		if opts.Home == HomeDelete || opts.Home == HomeArchive {
			return nil, fmt.Errorf("files handed to %s must stay on the server: keep the home", opts.TransferTo)
		}
		if _, err := s.lookup(opts.TransferTo); err != nil {
			return nil, fmt.Errorf("there is no account named %s to hand the files to", opts.TransferTo)
		}
	}
	target, unsafeHome, shared, err := s.closeAccount(ctx, username, opts)
	if err != nil {
		return nil, err
	}
	if opts.EndSessions {
		if err := s.EndSessions(ctx, username); err != nil {
			return nil, notDeleted(username, err)
		}
	}
	// Nothing below may run with the account still doing things: it would be racing
	// the chown and the archive with its own files.
	if opts.Home == HomeArchive || opts.TransferTo != "" {
		if err := s.waitForNoProcesses(ctx, username); err != nil {
			return nil, notDeleted(username, err)
		}
		if fresh, err := s.lookup(username); err != nil || fresh.UID != target.UID {
			return nil, notDeleted(username, fmt.Errorf("the account changed while it was being deleted"))
		}
	}
	result := &RemovalResult{}
	if opts.Home == HomeArchive {
		archive, err := s.archiveHome(ctx, *target)
		if err != nil {
			return nil, notDeleted(username, err)
		}
		result.Archive = archive
	}
	if opts.TransferTo != "" {
		if err := s.handOver(ctx, target.UID, target.Home, unsafeHome, opts.TransferTo); err != nil {
			return nil, notDeleted(username, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.removeLimits(ctx, target.UID)
	args := []string{}
	if opts.Home == HomeArchive || opts.Home == HomeDelete {
		args = append(args, "--remove")
	}
	// userdel looks for processes by number and would find the other account's. The
	// home is kept in this case, which is the part of --force that would do harm.
	if shared != "" {
		args = append(args, "--force")
	}
	if _, err := s.run(ctx, "", "userdel", append(args, username)...); err != nil {
		return nil, notDeleted(username, err)
	}
	return result, nil
}

// waitForNoProcesses gives a killed process a moment to be gone. The kill is not
// the proof: what is still in the process table is.
func (s *Service) waitForNoProcesses(ctx context.Context, username string) error {
	for attempt := 0; attempt < 5; attempt++ {
		count, _, err := s.Processes(ctx, username, 0)
		if err != nil || count == 0 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%w: it would not stop", ErrAccountBusy)
}

// archiveHome writes the home to a root-only tarball and proves it is there before
// anything is removed. Every step goes through the tools rather than this process,
// because tar runs on the host. The directory is held to 0700 whatever it was made
// as, since tar's output is readable by anyone until the chmod below.
func (s *Service) archiveHome(ctx context.Context, u User) (string, error) {
	home := filepath.Clean(u.Home)
	if _, err := os.Lstat(hostexec.HostPath(home)); os.IsNotExist(err) {
		return "", nil
	}
	if _, err := s.run(ctx, "", "mkdir", "-p", "-m", "0700", archiveRoot); err != nil {
		return "", fmt.Errorf("the home could not be archived, so nothing was deleted: %w", err)
	}
	if _, err := s.run(ctx, "", "chmod", "0700", "--", archiveRoot); err != nil {
		return "", fmt.Errorf("the home could not be archived, so nothing was deleted: %w", err)
	}
	name := fmt.Sprintf("%s-%s.tar.gz", u.Username, s.now().UTC().Format("20060102-150405"))
	path := filepath.Join(archiveRoot, name)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	// The -- because a home's name is the passwd file's to say, and tar reads
	// `-Tlist` as an instruction.
	if _, err := s.run(ctx, "", "tar", "--create", "--gzip", "--file", path,
		"--directory", filepath.Dir(home), "--", filepath.Base(home)); err != nil {
		_, _ = s.run(context.WithoutCancel(ctx), "", "rm", "-f", "--", path)
		return "", fmt.Errorf("the home could not be archived, so nothing was deleted: %w", err)
	}
	size, err := s.run(ctx, "", "stat", "--format", "%s", "--", path)
	if err != nil {
		return "", fmt.Errorf("the archive of the home could not be read back, so nothing was deleted: %w", err)
	}
	if bytes, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64); err != nil || bytes == 0 {
		return "", fmt.Errorf("the archive of the home is empty, so nothing was deleted")
	}
	if _, err := s.run(ctx, "", "chmod", "0600", "--", path); err != nil {
		return "", fmt.Errorf("the archive of the home could not be made private, so nothing was deleted: %w", err)
	}
	return path, nil
}

// handOver gives the account's files to another: its home, then whatever else the
// number owns. A root chown through paths another account controlled is the risk, so
// each guard here closes one way of steering it. --from leaves anything the account
// does not own alone, so a hard link it made to somebody else's file is not handed
// over with the rest. -h stops at a symlink rather than following it. -execdir runs
// chown from inside the file's own directory, so a directory swapped for a link to
// /etc between find listing it and chown running changes nothing.
func (s *Service) handOver(ctx context.Context, uid int, home string, unsafeHome bool, to string) error {
	// "to:" is chown's own spelling of that account's login group, which need
	// not share its name.
	owner := to + ":"
	from := "--from=" + strconv.Itoa(uid)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if !unsafeHome {
		if _, err := os.Stat(hostexec.HostPath(home)); err == nil {
			if _, err := s.run(ctx, "", "chown", "-hR", from, owner, "--", home); err != nil {
				return err
			}
		}
	}
	_, err := s.run(ctx, "", "find", "/", "-xdev",
		"(", "-path", "/proc", "-o", "-path", "/sys", "-o", "-path", "/dev", "-o", "-path", "/run", ")", "-prune",
		"-o", "-uid", strconv.Itoa(uid), "-execdir", "chown", "-h", from, owner, "{}", "+")
	return err
}
