package gitx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A relay carries a workspace's credentialed network operations, so that no
// credential ever reaches a git process of the checkout's account.
//
// A git process that runs as the checkout's account, in the checkout, reads
// that checkout's .git/config and the account's ~/.gitconfig, and whoever can
// write either (the account, anything it runs, a Files role that can write the
// folder) chooses http.proxy, http.sslVerify, url.<x>.insteadOf and the rest:
// enough to send the Authorization header wherever they like. The same account
// can read /proc/<pid>/environ of a process of its own. So the credential is
// only ever given to git running as the dashboard, in a bare repository of the
// dashboard's own (the relay), with only the dashboard's own configuration.
// What moves between the relay and the checkout is objects and ref names, and
// no credential: a pack written by one side's pack-objects and read by the
// other side's index-pack through a pipe, each side running as itself in its
// own repository, so neither reads the other's configuration.
//
//   - a clone fetches into the relay, and the checkout's account makes the
//     checkout and takes the relay's objects, with origin the real remote;
//   - a fetch is the same into the checkout that is there;
//   - a push takes the checkout's HEAD into the relay, and the relay pushes
//     that commit, never forced, then the checkout's remote-tracking ref (and
//     its upstream, for a new branch) is set as a push of its own would set it.
//
// A push or fetch that needs no credential (the account's own credential
// helper or SSH keys) does not come here: it is the account's, and runs as it.

// Relay is one remote's relay repository.
type Relay struct {
	// Dir is the bare repository, Remote the HTTPS address it fetches from and
	// pushes to.
	Dir    string
	Remote string
	// home is the folder of the dashboard's own Git settings: HOME for the
	// relay's git, and config (its gitconfig) as git's global configuration.
	home   string
	config string
}

// relayEnvironmentDropped are variables of the dashboard's own environment that
// would point the relay's git somewhere else; the relay sets the ones it needs.
var relayEnvironmentDropped = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "XDG_CONFIG_HOME",
	"GIT_SSH", "GIT_SSH_COMMAND", "GIT_PROXY_COMMAND", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR",
}

// OpenRelay opens base's relay for remote, making both when they are missing.
// base is the dashboard's own folder for them (in its data folder), and holds
// gitconfig, the only Git configuration the relay's git reads: that is where an
// operator sets what a host needs, a company or self-signed CA for one.
func (s *Service) OpenRelay(ctx context.Context, base, remote string) (*Relay, error) {
	remote = strings.TrimSpace(remote)
	if err := ValidateRemoteURL(remote); err != nil {
		return nil, err
	}
	if parsed, err := url.Parse(remote); err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: a credential is carried only to an HTTPS address without one in it", ErrInvalidRef)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	if err := ownedDirectory(base); err != nil {
		return nil, err
	}
	config := filepath.Join(base, "gitconfig")
	file, err := os.OpenFile(config, os.O_RDONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the relay's Git settings %s: %w", config, err)
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("the relay's Git settings %s are not a file", config)
	}
	relays := filepath.Join(base, "relays")
	if err := os.Mkdir(relays, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := ownedDirectory(relays); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(remote))
	r := &Relay{Dir: filepath.Join(relays, hex.EncodeToString(sum[:12])+".git"), Remote: remote, home: base, config: config}
	unlock := s.Lock(r.Dir)
	defer func() { unlock() }()
	if err := os.Mkdir(r.Dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := ownedDirectory(r.Dir); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(r.Dir, "HEAD")); err != nil {
		if _, err := runGit(ctx, r.runner(nil), time.Minute, "", "init", "--bare", "-q"); err != nil {
			return nil, err
		}
	}
	// Used now: stamped under its lock, so the sweep never takes it.
	if err := os.WriteFile(filepath.Join(r.Dir, relayUsedStamp), []byte(strconv.FormatInt(s.clock().Unix(), 10)+"\n"), 0o600); err != nil {
		return nil, err
	}
	unlock()
	unlock = func() {}
	s.sweepRelays(base, r.Dir)
	return r, nil
}

// relayUsedStamp, in a relay, is when it was last opened, in Unix seconds.
const relayUsedStamp = "jd-last-used"

// relayUnusedFor is how long a relay is kept unused, and onceUnusedFor how long
// a shallow clone's own relay outlives a clone that could not remove it.
const (
	relayUnusedFor = 30 * 24 * time.Hour
	onceUnusedFor  = 24 * time.Hour
)

// sweepRelays removes the relays under base that nothing opened for
// relayUnusedFor, and shallow clones' relays left behind: they are the
// dashboard's own copies of a remote, which the next one that needs it fetches
// again. A relay in use is never removed: an operation holds its lock, and one
// opened since its stamp was read is stamped anew under that lock, so it is
// read again before anything goes. keep is the relay being opened.
func (s *Service) sweepRelays(base, keep string) {
	now := s.clock()
	sweep := func(dir string, unusedFor time.Duration, used func(string) (time.Time, bool)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if path == keep || !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".git") {
				continue
			}
			if at, ok := used(path); !ok || now.Sub(at) < unusedFor {
				continue
			}
			unlock, free := s.tryLock(path)
			if !free {
				continue
			}
			if at, ok := used(path); ok && now.Sub(at) >= unusedFor && ownedDirectory(path) == nil {
				_ = os.RemoveAll(path)
			}
			unlock()
		}
	}
	sweep(filepath.Join(base, "relays"), relayUnusedFor, relayLastUsed)
	sweep(filepath.Join(base, "once"), onceUnusedFor, func(path string) (time.Time, bool) {
		info, err := os.Lstat(path)
		if err != nil {
			return time.Time{}, false
		}
		return info.ModTime(), true
	})
}

// relayLastUsed reads a relay's stamp; a relay without one (made before
// stamps, or cut short) counts from its folder's time.
func relayLastUsed(path string) (time.Time, bool) {
	if raw, err := os.ReadFile(filepath.Join(path, relayUsedStamp)); err == nil {
		if at, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil {
			return time.Unix(at, 0), true
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// ownedDirectory refuses a folder that is a link or is not this process's own:
// the relay is the dashboard's, and its git never runs anywhere else.
func ownedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is not a folder of the dashboard's own", path)
	}
	return nil
}

// runner is how the relay runs git: as the dashboard, in the relay, with the
// dashboard's own settings and extra (a credential's environment) over them.
// It never drops to another account: the relay is this process's own.
func (r *Relay) runner(extra map[string]string) gitRunner {
	return func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = r.Dir
		env := []string{}
		for _, entry := range gitEnvironment(nil, args...) {
			key, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(key, "GIT_CONFIG") {
				continue
			}
			dropped := false
			for _, name := range relayEnvironmentDropped {
				dropped = dropped || key == name
			}
			if !dropped {
				env = append(env, entry)
			}
		}
		set := map[string]string{}
		for key, value := range extra {
			set[key] = value
		}
		for key, value := range map[string]string{
			"GIT_DIR": r.Dir, "GIT_CONFIG_GLOBAL": r.config, "GIT_CONFIG_NOSYSTEM": "1",
			"HOME": r.home, "GIT_ALLOW_PROTOCOL": "https",
		} {
			set[key] = value
		}
		cmd.Env = setEnvironment(env, set)
		return cmd
	}
}

// setEnvironment is env with each of values set, replacing what was there.
func setEnvironment(env []string, values map[string]string) []string {
	out := make([]string, 0, len(env)+len(values))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := values[key]; !ok {
			out = append(out, entry)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

// gitRunner makes one git command of one side of a transfer.
type gitRunner func(ctx context.Context, args ...string) *exec.Cmd

// accountRunner runs git in dir as the account that owns ownerDir now, the
// same account for every command it makes: a checkout swapped for a link
// between two commands cannot move them to another account. It carries no
// credential, and answers the uid it runs as.
//
// A root dashboard runs it as root only in a folder of root's that no other
// account can point elsewhere (rootsAllTheWay): a checkout in an account's home
// that the account swapped for a link to a folder of root's (/tmp) after it was
// resolved would otherwise be judged root's, and git as root would run what
// the account's repository there names (a hook, core.hooksPath).
func accountRunner(dir, ownerDir string) (gitRunner, uint32, error) {
	probe := &exec.Cmd{Dir: ownerDir, Env: []string{}}
	hostexec.AsOwner(probe)
	uid := uint32(os.Geteuid())
	var credential *syscall.Credential
	if probe.SysProcAttr != nil && probe.SysProcAttr.Credential != nil {
		held := *probe.SysProcAttr.Credential
		credential, uid = &held, held.Uid
	} else if uid == 0 && (!rootsAllTheWay(dir) || !rootsAllTheWay(ownerDir)) {
		return nil, 0, fmt.Errorf("%s is not a folder of one account's: git does not run in it as root", dir)
	}
	return func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitEnvironment(nil, args...), probe.Env...)
		if credential != nil {
			held := *credential
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &held}
		}
		return cmd
	}, uid, nil
}

// rootsAllTheWay is whether every folder from / to path is root's, none is a
// link, and none is one another account may rename entries in (writable by
// group or others without the sticky bit): a path no other account can point
// anywhere else.
func rootsAllTheWay(path string) bool {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return false
	}
	for at := path; ; at = filepath.Dir(at) {
		info, err := os.Lstat(at)
		if err != nil || !info.IsDir() {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || (info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return false
		}
		if at == filepath.Dir(at) {
			return true
		}
	}
}

// runGit runs one command and answers what it wrote to its output; a failure
// carries what git said.
func runGit(ctx context.Context, run gitRunner, timeout time.Duration, input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := run(ctx, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
		if said == "" {
			said = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", args[0], said)
	}
	return stdout.String(), nil
}

var objectIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// plainRef is a ref name that can go on a line of update-ref's input as it is.
func plainRef(name string) bool {
	return strings.HasPrefix(name, "refs/") && !strings.ContainsAny(name, " \t\r\n\x00\\") && !strings.Contains(name, "..")
}

// refsOf is one side's refs under patterns, name to object, symbolic ones left out.
func refsOf(ctx context.Context, run gitRunner, patterns ...string) (map[string]string, error) {
	out, err := runGit(ctx, run, time.Minute, "", append([]string{"for-each-ref", "--format=%(objectname) %(refname) %(symref)"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && objectIDPattern.MatchString(fields[0]) && plainRef(fields[1]) {
			refs[fields[1]] = fields[0]
		}
	}
	return refs, nil
}

// present is which of ids one side has.
func present(ctx context.Context, run gitRunner, ids []string) (map[string]bool, error) {
	have := map[string]bool{}
	if len(ids) == 0 {
		return have, nil
	}
	out, err := runGit(ctx, run, time.Minute, strings.Join(ids, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] != "missing" && objectIDPattern.MatchString(fields[0]) {
			have[fields[0]] = true
		}
	}
	return have, nil
}

// transfer gives to the objects of want that it lacks, from from: a pack
// written by from's pack-objects, read by to's index-pack, each running as
// itself in its own repository. What both sides have is left out of the pack.
func transfer(ctx context.Context, from, to gitRunner, want []string) error {
	have, err := present(ctx, to, want)
	if err != nil {
		return err
	}
	missing := []string{}
	for _, id := range want {
		if objectIDPattern.MatchString(id) && !have[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	tips := map[string]bool{}
	for _, run := range []gitRunner{from, to} {
		refs, err := refsOf(ctx, run, "refs")
		if err != nil {
			return err
		}
		for _, id := range refs {
			tips[id] = true
		}
	}
	candidates := make([]string, 0, len(tips))
	for id := range tips {
		candidates = append(candidates, id)
	}
	sort.Strings(candidates)
	inFrom, err := present(ctx, from, candidates)
	if err != nil {
		return err
	}
	inTo, err := present(ctx, to, candidates)
	if err != nil {
		return err
	}
	shared := []string{}
	for _, id := range candidates {
		if inFrom[id] && inTo[id] {
			shared = append(shared, id)
		}
	}
	err = pipePack(ctx, from, to, missing, shared)
	if err != nil && len(shared) > 0 {
		// A side that says it has a commit without all that it needs: send
		// the whole history instead.
		err = pipePack(ctx, from, to, missing, nil)
	}
	return err
}

// pipePack runs from's pack-objects into to's index-pack through a pipe.
func pipePack(ctx context.Context, from, to gitRunner, want, exclude []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	packArgs := []string{"pack-objects", "--stdout", "--revs", "-q"}
	indexArgs := []string{"index-pack", "--stdin"}
	if len(exclude) > 0 {
		packArgs = append(packArgs, "--thin")
		indexArgs = append(indexArgs, "--fix-thin")
	}
	var input strings.Builder
	for _, id := range want {
		input.WriteString(id + "\n")
	}
	for _, id := range exclude {
		input.WriteString("^" + id + "\n")
	}
	pack, index := from(ctx, packArgs...), to(ctx, indexArgs...)
	pack.Stdin = strings.NewReader(input.String())
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	var packSaid, indexSaid bytes.Buffer
	pack.Stdout, pack.Stderr = writer, &packSaid
	index.Stdin, index.Stdout, index.Stderr = reader, io.Discard, &indexSaid
	if err := pack.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return fmt.Errorf("git pack-objects could not be started: %w", err)
	}
	_ = writer.Close()
	if err := index.Start(); err != nil {
		_ = reader.Close()
		_ = pack.Wait()
		return fmt.Errorf("git index-pack could not be started: %w", err)
	}
	_ = reader.Close()
	indexErr := index.Wait()
	packErr := pack.Wait()
	switch {
	case packErr != nil:
		return fmt.Errorf("git pack-objects: %s", orErr(packSaid.String(), packErr))
	case indexErr != nil:
		return fmt.Errorf("git index-pack: %s", orErr(indexSaid.String(), indexErr))
	}
	return nil
}

func orErr(said string, err error) string {
	if said = strings.TrimSpace(said); said != "" {
		return said
	}
	return err.Error()
}

// Fetch brings the relay up to its remote: every branch and tag, as the remote
// has them now. It is the relay's own copy, so it follows the remote's forced
// updates; nothing here is ever pushed back but a commit a push names.
func (r *Relay) Fetch(ctx context.Context, extra map[string]string) error {
	return r.fetch(ctx, extra, 0, nil, nil)
}

// fetch is Fetch of refspecs (every branch and tag when none), as deep as depth
// when it is more than 0, with git's progress handed to line when there is one.
func (r *Relay) fetch(ctx context.Context, extra map[string]string, depth int, refspecs []string, line func(stream, text string)) error {
	args := []string{"fetch", "--prune", "--no-tags"}
	if depth > 0 {
		args = append(args, "--depth="+strconv.Itoa(depth))
	}
	if line != nil {
		args = append(args, "--progress")
	} else {
		args = append(args, "--quiet")
	}
	if len(refspecs) == 0 {
		refspecs = []string{"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}
	}
	args = append(append(args, r.Remote), refspecs...)
	if line == nil {
		_, err := runGit(ctx, r.runner(extra), 10*time.Minute, "", args...)
		return err
	}
	return runGitStreaming(ctx, r.runner(extra), 10*time.Minute, line, args...)
}

// runGitStreaming runs one command and hands its output to line as it comes,
// git's progress redraws collapsed as a clone's console shows them.
func runGitStreaming(ctx context.Context, run gitRunner, timeout time.Duration, line func(stream, text string), args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := run(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	var mu sync.Mutex
	last := ""
	emit := func(stream string) func(string) {
		return func(text string) {
			mu.Lock()
			defer mu.Unlock()
			if stream == "stderr" && strings.TrimSpace(text) != "" {
				last = strings.TrimSpace(text)
			}
			line(stream, text)
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git could not be started: %w", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); collapseProgress(stdout, time.Now, emit("stdout")) }()
	go func() { defer wg.Done(); collapseProgress(stderr, time.Now, emit("stderr")) }()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if last != "" {
			return fmt.Errorf("git %s: %s", args[0], last)
		}
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// defaultBranch is the branch the remote's HEAD names, or a guess from the
// branches the relay has when the remote does not say.
func (r *Relay) defaultBranch(ctx context.Context, extra map[string]string, refs map[string]string) string {
	if out, err := runGit(ctx, r.runner(extra), time.Minute, "", "ls-remote", "--symref", r.Remote, "HEAD"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if target, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
				if name, _, _ := strings.Cut(target, "\t"); ValidateRef(name) == nil {
					return name
				}
			}
		}
	}
	heads := []string{}
	for ref := range refs {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			heads = append(heads, name)
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, ok := refs["refs/heads/"+name]; ok {
			return name
		}
	}
	if len(heads) == 1 {
		return heads[0]
	}
	return ""
}

// relayRefs is the relay's branches and tags.
func (r *Relay) refs(ctx context.Context) (map[string]string, error) {
	return refsOf(ctx, r.runner(nil), "refs/heads", "refs/tags")
}

// intoCheckout gives the checkout the relay's objects and sets its
// remote-tracking refs as a fetch --prune of origin would: each of the relay's
// branches as origin/<branch>, none that the remote no longer has, and the
// tags it does not have yet (a tag it has is left as it is, as git's fetch
// leaves it).
func (r *Relay) intoCheckout(ctx context.Context, checkout gitRunner, refs map[string]string) error {
	ids := make([]string, 0, len(refs))
	for _, id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := transfer(ctx, r.runner(nil), checkout, ids); err != nil {
		return err
	}
	ours, err := refsOf(ctx, checkout, "refs/remotes/origin", "refs/tags")
	if err != nil {
		return err
	}
	var deletes, updates []string
	for ref := range ours {
		if rest, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok && rest != "HEAD" {
			if _, kept := refs["refs/heads/"+rest]; !kept {
				deletes = append(deletes, "delete "+ref+"\n")
			}
		}
	}
	for ref, id := range refs {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			if tracking := "refs/remotes/origin/" + name; ours[tracking] != id {
				updates = append(updates, "update "+tracking+" "+id+"\n")
			}
		} else if _, had := ours[ref]; !had && strings.HasPrefix(ref, "refs/tags/") {
			updates = append(updates, "create "+ref+" "+id+"\n")
		}
	}
	// Deleted first, on their own: a branch a/b the remote made where a was
	// cannot be written while a is still there.
	for _, lines := range [][]string{deletes, updates} {
		if len(lines) == 0 {
			continue
		}
		sort.Strings(lines)
		if _, err := runGit(ctx, checkout, time.Minute, strings.Join(lines, ""), "update-ref", "--stdin"); err != nil {
			return err
		}
	}
	return nil
}

// RelayCloneOptions are what a clone through the relay takes: the branch or
// tag to check out (the remote's default when empty), a depth for a shallow
// clone of that one branch, and line, which is handed git's progress for a
// console.
type RelayCloneOptions struct {
	Branch string
	Depth  int
	Line   func(stream, text string)
}

// CloneThrough makes a checkout of the relay's remote at parent/name, as the
// parent's owner, from what the relay fetched with extra: origin is the real
// remote, its branches are the checkout's remote-tracking refs, and the branch
// (a tag leaves HEAD on no branch, as git's clone does) is checked out. A
// shallow clone is fetched into a relay of its own for that one clone, removed
// after it, so the remote's shared relay stays whole. A clone that fails or is
// stopped part of the way leaves nothing of what it made.
func (s *Service) CloneThrough(ctx context.Context, r *Relay, parent, name string, options RelayCloneOptions, extra map[string]string) (string, error) {
	plan, err := s.PrepareClone(parent, r.Remote, name, CloneOptions{Branch: options.Branch, Depth: options.Depth})
	if err != nil {
		return "", err
	}
	unlock := s.Lock(plan.Target)
	defer unlock()
	branch := options.Branch
	if branch != "" {
		if err := ValidateRef(branch); err != nil {
			return "", err
		}
	}
	var refspecs []string
	if options.Depth == 0 {
		// The relay is in use until the clone ends: the sweep leaves it.
		unlockRelay := s.Lock(r.Dir)
		defer unlockRelay()
	} else {
		once, remove, err := s.onceRelay(ctx, r)
		if err != nil {
			return "", err
		}
		defer remove()
		r = once
		if branch == "" {
			branch = r.defaultBranch(ctx, extra, nil)
		}
		if branch == "" {
			return "", fmt.Errorf("%w: the remote names no default branch to clone shallow; name one", ErrInvalidRef)
		}
		// One branch or tag, as git's clone --depth fetches.
		refspecs = []string{"+refs/heads/" + branch + ":refs/heads/" + branch}
		if out, err := runGit(ctx, r.runner(extra), time.Minute, "", "ls-remote", "--heads", "--tags", r.Remote, "refs/heads/"+branch, "refs/tags/"+branch); err == nil &&
			!strings.Contains(out, "refs/heads/"+branch+"\n") && strings.Contains(out, "refs/tags/"+branch) {
			refspecs = []string{"+refs/tags/" + branch + ":refs/tags/" + branch}
		}
	}
	if err := r.fetch(ctx, extra, options.Depth, refspecs, options.Line); err != nil {
		return "", err
	}
	refs, err := r.refs(ctx)
	if err != nil {
		return "", err
	}
	defaultBranch := ""
	if options.Depth == 0 {
		defaultBranch = r.defaultBranch(ctx, extra, refs)
		if branch == "" {
			branch = defaultBranch
		}
	}
	kind := ""
	if branch != "" {
		switch {
		case refs["refs/heads/"+branch] != "":
			kind = "branch"
		case refs["refs/tags/"+branch] != "":
			kind = "tag"
		case len(refs) > 0 || options.Depth > 0:
			return "", fmt.Errorf("%w: the remote has no branch or tag %s", ErrInvalidRef, branch)
		}
	}
	if _, err := os.Lstat(plan.Target); err == nil {
		return "", fmt.Errorf("%w: %s already exists", ErrInvalidRef, plan.Target)
	}
	// The account makes the folder itself, in its own folder, as git clone
	// would: the dashboard never makes a folder of the account's.
	inParent, owner, err := accountRunner(plan.Dir, plan.Dir)
	if err != nil {
		return "", err
	}
	initArgs := []string{"init", "-q"}
	if kind == "" && branch != "" {
		initArgs = append(initArgs, "--initial-branch="+branch)
	}
	if _, err := runGit(ctx, inParent, time.Minute, "", append(initArgs, "--", plan.Name)...); err != nil {
		return "", err
	}
	made, err := os.Lstat(plan.Target)
	if err != nil || !made.IsDir() {
		return "", fmt.Errorf("the checkout %s was not made", plan.Target)
	}
	done := false
	defer func() {
		if !done {
			removeMade(plan.Dir, plan.Name, made, owner)
		}
	}()
	checkout, ran, err := accountRunner(plan.Target, plan.Dir)
	if err != nil {
		return "", err
	}
	if ran != owner {
		return "", fmt.Errorf("the checkout %s changed hands while it was made", plan.Target)
	}
	remoteArgs := []string{"remote", "add"}
	if options.Depth > 0 && kind == "branch" {
		remoteArgs = append(remoteArgs, "-t", branch)
	}
	if _, err := runGit(ctx, checkout, time.Minute, "", append(remoteArgs, "origin", r.Remote)...); err != nil {
		return "", err
	}
	if err := r.intoCheckout(ctx, checkout, refs); err != nil {
		return "", err
	}
	if options.Depth > 0 {
		if err := r.shallowInto(plan.Target, owner); err != nil {
			return "", err
		}
	}
	if defaultBranch != "" && refs["refs/heads/"+defaultBranch] != "" {
		_, _ = runGit(ctx, checkout, time.Minute, "", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+defaultBranch)
	}
	switch kind {
	case "branch":
		if _, err := runGit(ctx, checkout, 5*time.Minute, "", "checkout", "-q", "-b", branch, "refs/remotes/origin/"+branch, "--"); err != nil {
			return "", err
		}
		if err := setUpstream(ctx, checkout, branch); err != nil {
			return "", err
		}
	case "tag":
		if _, err := runGit(ctx, checkout, 5*time.Minute, "", "checkout", "-q", "--detach", "refs/tags/"+branch, "--"); err != nil {
			return "", err
		}
	}
	done = true
	return plan.Target, nil
}

// onceRelay is a relay of r's remote for one shallow clone, and what removes it.
func (s *Service) onceRelay(ctx context.Context, r *Relay) (*Relay, func(), error) {
	once := filepath.Join(r.home, "once")
	if err := os.Mkdir(once, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, nil, err
	}
	if err := ownedDirectory(once); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp(once, "clone-*.git")
	if err != nil {
		return nil, nil, err
	}
	remove := func() { _ = os.RemoveAll(dir) }
	relay := &Relay{Dir: dir, Remote: r.Remote, home: r.home, config: r.config}
	if _, err := runGit(ctx, relay.runner(nil), time.Minute, "", "init", "--bare", "-q"); err != nil {
		remove()
		return nil, nil, err
	}
	return relay, remove, nil
}

// shallowInto gives a checkout just made the relay's shallow boundary, the
// commits whose parents a shallow clone does not have. git writes it only in a
// fetch, which would read the relay as the account, so the dashboard writes it,
// as the marker is written: inside the checkout without following a link out
// of it, made new (never one that was there), and handed to the folder's owner
// through its descriptor.
func (r *Relay) shallowInto(target string, owner uint32) error {
	raw, err := os.ReadFile(filepath.Join(r.Dir, "shallow"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if objectIDPattern.MatchString(strings.TrimSpace(line)) {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, err := root.Lstat(".git"); err != nil || !info.IsDir() || !ownedBy(info, owner) {
		return fmt.Errorf("the checkout %s has no .git folder of its own", target)
	}
	if info, err := root.Stat("."); err != nil || !ownedBy(info, owner) {
		return fmt.Errorf("the checkout %s is not the one this clone made", target)
	}
	file, err := root.OpenFile(filepath.Join(".git", "shallow"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		return err
	}
	if os.Geteuid() == 0 && owner != 0 {
		if info, err := root.Stat("."); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				return file.Chown(int(owner), int(st.Gid))
			}
		}
	}
	return nil
}

// ownedBy is whether info is uid's.
func ownedBy(info os.FileInfo, uid uint32) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uid
}

// removeMade removes the checkout name in parent that a clone made and could
// not finish, and only that one: a folder at its place that is not the one
// made (made, when known), or not of owner's, the account the clone's git ran
// as, is left. parent is opened once and everything after is done through it,
// without following a link, so a parent swapped for a link (an account's own
// folder, in its home) cannot point a removal by root at a folder of root's or
// of another account's; one of root's is removed only where no other account
// could have pointed it (rootsAllTheWay).
func removeMade(parent, name string, made os.FileInfo, owner uint32) {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return
	}
	defer root.Close()
	now, err := root.Lstat(name)
	if err != nil || !now.IsDir() || (made != nil && !os.SameFile(made, now)) {
		return
	}
	if os.Geteuid() == 0 && (!ownedBy(now, owner) || (owner == 0 && !rootsAllTheWay(parent))) {
		return
	}
	_ = root.RemoveAll(name)
}

// setUpstream makes branch track origin's branch of its name, as push
// --set-upstream does.
func setUpstream(ctx context.Context, checkout gitRunner, branch string) error {
	if _, err := runGit(ctx, checkout, time.Minute, "", "config", "branch."+branch+".remote", "origin"); err != nil {
		return err
	}
	_, err := runGit(ctx, checkout, time.Minute, "", "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

// FetchThrough fetches the checkout at path from its remote through the relay:
// the relay fetches with extra, and the checkout's account takes its objects
// and refs from the relay.
func (s *Service) FetchThrough(ctx context.Context, r *Relay, path string, extra map[string]string) (*Result, error) {
	s = s.ForAccounts()
	unlock := s.Lock(r.Dir)
	defer unlock()
	if err := r.Fetch(ctx, extra); err != nil {
		return nil, err
	}
	refs, err := r.refs(ctx)
	if err != nil {
		return nil, err
	}
	checkout, _, err := accountRunner(path, path)
	if err != nil {
		return nil, err
	}
	if err := r.intoCheckout(ctx, checkout, refs); err != nil {
		return nil, err
	}
	return &Result{Command: "git fetch --prune origin", Output: "Done.", OK: true}, nil
}

// PushThrough pushes the checkout at path through the relay, as PushOrigin
// pushes it (the current branch as itself or as branch, HEAD from no branch to
// the branch named, the upstream set for a branch that has none): its HEAD is
// taken into the relay, and the relay pushes that commit with extra. It never
// forces: the refspec is the commit and a whole branch name, so a push the
// remote refuses because it moved is answered with git's own words.
func (s *Service) PushThrough(ctx context.Context, r *Relay, path, branch string, extra map[string]string) (*Result, error) {
	s = s.ForAccounts()
	unlock := s.Lock(r.Dir)
	defer unlock()
	checkout, _, err := accountRunner(path, path)
	if err != nil {
		return nil, err
	}
	current, err := s.CurrentBranch(ctx, path)
	if err != nil && branch == "" {
		return nil, err
	}
	target := current
	if branch != "" {
		if err := ValidateRef(branch); err != nil {
			return nil, err
		}
		target = branch
	}
	out, err := runGit(ctx, checkout, 30*time.Second, "", "rev-parse", "--verify", "HEAD^{commit}")
	head := strings.TrimSpace(out)
	if err != nil || !objectIDPattern.MatchString(head) {
		return nil, fmt.Errorf("%w: HEAD names no commit to push", ErrInvalidRef)
	}
	upstream := target == current && s.hasUpstream(ctx, path)
	// What the remote has now, so the pack holds only what is new and a refusal
	// says what git says when it knows the remote's branch.
	_ = r.Fetch(ctx, extra)
	if err := transfer(ctx, checkout, r.runner(nil), []string{head}); err != nil {
		return nil, err
	}
	refspec := head + ":refs/heads/" + target
	args := []string{"push", "--porcelain", r.Remote, refspec}
	res := &Result{Command: "git push --porcelain origin HEAD:refs/heads/" + target}
	output, err := runGit(ctx, r.runner(extra), 3*time.Minute, "", args...)
	res.Output = strings.TrimSpace(output)
	if err != nil {
		res.Output = strings.TrimPrefix(err.Error(), "git push: ")
		return res, err
	}
	res.OK = true
	if res.Output == "" {
		res.Output = "Done."
	}
	// The relay and the checkout record the remote's branch as it now is.
	_, _ = runGit(ctx, r.runner(nil), 30*time.Second, "", "update-ref", "refs/heads/"+target, head)
	if _, err := runGit(ctx, checkout, 30*time.Second, "", "update-ref", "refs/remotes/origin/"+target, head); err != nil {
		return res, nil
	}
	if target == current && !upstream {
		_ = setUpstream(ctx, checkout, current)
	}
	return res, nil
}
