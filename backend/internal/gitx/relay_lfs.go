package gitx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Git LFS through the relay. A repository's LFS files are fetched and uploaded
// by git-lfs's own HTTP client, which takes the same credential git does
// (http.<remote>.extraHeader), so like git it runs only as the dashboard, in
// the relay, with the dashboard's own settings and the credential in that one
// process's environment. The LFS endpoint is pinned to the remote's both ways
// (lfs.url and lfs.pushurl, at command scope, which win over a repository's
// .lfsconfig), so what a repository's files say cannot send the header or an
// upload anywhere else.
//
// The objects cross to and from the checkout's account as content alone, each
// side running git-lfs as itself in its own repository: the account's
// `git lfs clean` reads an object's content from a pipe and stores it in its
// own .git/lfs/objects, and its `git lfs smudge` writes one it holds to a pipe,
// which the dashboard checks against the object's id before the relay keeps it.
// Without git-lfs on the server, a repository's LFS files stay pointers, as
// they did when the account's own git cloned without it.

// lfsObject is one LFS file: its object id and where it is in a tree.
type lfsObject struct {
	OID  string
	Path string
}

var lfsOIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// lfsInstalled is whether git-lfs is on this server; a test stands in for one
// without it.
var lfsInstalled = func() bool {
	_, err := exec.LookPath("git-lfs")
	return err == nil
}

// lfsNotInstalled is what a clone or fetch of a repository with LFS files says
// when git-lfs is not on the server.
const lfsNotInstalled = "This repository keeps files in Git LFS, and git-lfs is not installed on this server, so those files are left as LFS pointers."

// withConfig is extra with keys set at git's command scope after its own.
func withConfig(extra map[string]string, pairs ...string) map[string]string {
	out := map[string]string{}
	for key, value := range extra {
		out[key] = value
	}
	count, _ := strconv.Atoi(out["GIT_CONFIG_COUNT"])
	for i := 0; i+1 < len(pairs); i += 2 {
		out["GIT_CONFIG_KEY_"+strconv.Itoa(count)] = pairs[i]
		out["GIT_CONFIG_VALUE_"+strconv.Itoa(count)] = pairs[i+1]
		count++
	}
	out["GIT_CONFIG_COUNT"] = strconv.Itoa(count)
	return out
}

// lfsRunner is the relay's git for git-lfs: origin is the remote, the LFS
// endpoint is the remote's alone both ways (lfs.url and lfs.pushurl: a
// repository's .lfsconfig, which git-lfs reads from the relay's HEAD, may set
// either, and lfs.pushurl wins over lfs.url for an upload), a push of a commit
// whose objects the relay never held (older ones, a tag's) goes when the
// remote has them, which git-lfs asks it, and is refused when it lacks one
// that neither side holds, as git-lfs's own hook refuses it
// (lfs.allowincompletepush off), and no lock is checked.
func (r *Relay) lfsRunner(extra map[string]string) gitRunner {
	endpoint := lfsEndpoint(r.Remote)
	return r.runner(withConfig(extra,
		"remote.origin.url", r.Remote,
		"lfs.url", endpoint,
		"lfs.pushurl", endpoint,
		"lfs.allowincompletepush", "false",
		"lfs.locksverify", "false",
	))
}

// lfsEndpoint is remote's Git LFS API as git-lfs finds it on its own: beside
// <repository>.git, the only place GitHub and GitLab answer it.
func lfsEndpoint(remote string) string {
	base := strings.TrimSuffix(remote, "/")
	if !strings.HasSuffix(base, ".git") {
		base += ".git"
	}
	return base + "/info/lfs"
}

// lfsAt is the LFS files of rev in the relay.
func (r *Relay) lfsAt(ctx context.Context, rev string) ([]lfsObject, error) {
	out, err := runGit(ctx, r.lfsRunner(nil), time.Minute, "", "lfs", "ls-files", "--long", rev)
	if err != nil {
		return nil, err
	}
	var objects []lfsObject
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(fields) == 3 && lfsOIDPattern.MatchString(fields[0]) {
			objects = append(objects, lfsObject{OID: fields[0], Path: fields[2]})
		}
	}
	return objects, nil
}

// usesLFS is whether rev's .gitattributes sends a file through Git LFS.
func (r *Relay) usesLFS(ctx context.Context, rev string) bool {
	out, err := runGit(ctx, r.runner(nil), 30*time.Second, "", "show", rev+":.gitattributes")
	return err == nil && strings.Contains(out, "filter=lfs")
}

// lfsPath is where a git-lfs object store keeps oid.
func lfsPath(store, oid string) string {
	return filepath.Join(store, oid[0:2], oid[2:4], oid)
}

// lfsIntoCheckout fetches the LFS files of revs into the relay with extra and
// gives the checkout's account those it lacks, and answers how many it gave.
// A fetch that brought only some (an object the server no longer has) still
// gives those it brought, and answers what git-lfs said as unfetched.
func (r *Relay) lfsIntoCheckout(ctx context.Context, checkout gitRunner, path string, extra map[string]string, revs []string) (given int, unfetched, err error) {
	seen := map[string]bool{}
	var objects []lfsObject
	for _, rev := range revs {
		at, err := r.lfsAt(ctx, rev)
		if err != nil {
			return 0, nil, err
		}
		for _, object := range at {
			if !seen[object.OID] {
				seen[object.OID] = true
				objects = append(objects, object)
			}
		}
	}
	// What the checkout has already is not sent again. Only read: a link here
	// changes nothing but what is sent.
	missing := objects[:0]
	for _, object := range objects {
		if info, err := os.Lstat(lfsPath(filepath.Join(path, ".git", "lfs", "objects"), object.OID)); err != nil || !info.Mode().IsRegular() {
			missing = append(missing, object)
		}
	}
	if len(missing) == 0 {
		return 0, nil, nil
	}
	_, unfetched = runGit(ctx, r.lfsRunner(extra), 30*time.Minute, "", append([]string{"lfs", "fetch", "origin"}, revs...)...)
	if unfetched != nil && ctx.Err() != nil {
		return 0, nil, unfetched
	}
	store := filepath.Join(r.Dir, "lfs", "objects")
	for _, object := range missing {
		file, err := os.Open(lfsPath(store, object.OID))
		if err != nil {
			continue
		}
		err = runGitWithInput(ctx, checkout, 10*time.Minute, file, io.Discard, "lfs", "clean", "--", object.Path)
		_ = file.Close()
		if err != nil {
			return given, unfetched, err
		}
		given++
	}
	return given, unfetched, nil
}

// runGitWithInput runs one command with input as its standard input and its
// output written to output.
func runGitWithInput(ctx context.Context, run gitRunner, timeout time.Duration, input io.Reader, output io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := stoppedWhole(run(ctx, args...))
	var said bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, &said
	if err := stillOK(cmd.Run()); err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args[:min(2, len(args))], " "), orErr(said.String(), err))
	}
	return nil
}

// lfsCheckout makes the checkout's LFS files real from what it holds, as its
// account, with no credential: git-lfs set up for that one checkout (so git
// reads them back as the pointers it committed), then its files checked out.
// A download its smudge cannot make leaves the pointer
// (lfs.skipdownloaderrors): the account's git-lfs has no credential, so a
// checkout of a branch whose LFS files it was never given (one the fetch did
// not move, one the account switches to itself) is made with those as
// pointers instead of failing; those it holds are real.
func lfsCheckout(ctx context.Context, checkout gitRunner) error {
	if _, err := runGit(ctx, checkout, time.Minute, "", "lfs", "install", "--local", "--skip-repo"); err != nil {
		if _, err := runGit(ctx, checkout, time.Minute, "", "lfs", "install", "--local"); err != nil {
			return err
		}
	}
	if _, err := runGit(ctx, checkout, time.Minute, "", "config", "--local", "lfs.skipdownloaderrors", "true"); err != nil {
		return err
	}
	_, err := runGit(ctx, checkout, 10*time.Minute, "", "lfs", "checkout")
	return err
}

// lfsUploaded uploads, through the relay, the LFS objects a push of head to
// target carries that the remote may not have: the relay asks git-lfs which,
// the checkout's account hands over those the relay lacks, and git-lfs uploads
// them with extra before the commit is pushed, as its pre-push hook does. It
// answers how many the checkout handed over: those are the ones made there,
// which the remote cannot have had.
func (r *Relay) lfsUploaded(ctx context.Context, checkout gitRunner, path, head, target string, extra map[string]string) (int, error) {
	remoteSHA := strings.Repeat("0", len(head))
	if out, err := runGit(ctx, r.runner(nil), 30*time.Second, "", "rev-parse", "--verify", "-q", "refs/remotes/origin/"+target); err == nil && objectIDPattern.MatchString(strings.TrimSpace(out)) {
		remoteSHA = strings.TrimSpace(out)
	}
	line := "refs/heads/" + target + " " + head + " refs/heads/" + target + " " + remoteSHA + "\n"
	out, err := runGit(ctx, r.lfsRunner(nil), 5*time.Minute, line, "lfs", "pre-push", "--dry-run", "origin", r.Remote)
	if err != nil {
		return 0, err
	}
	store := filepath.Join(r.Dir, "lfs", "objects")
	var oids []string
	for _, l := range strings.Split(out, "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 2 && fields[0] == "push" && lfsOIDPattern.MatchString(fields[1]) {
			oids = append(oids, fields[1])
		}
	}
	sort.Strings(oids)
	if len(oids) == 0 {
		return 0, nil
	}
	taken := 0
	for _, oid := range oids {
		if info, err := os.Lstat(lfsPath(store, oid)); err == nil && info.Mode().IsRegular() {
			continue
		}
		if err := r.lfsFromCheckout(ctx, checkout, path, store, oid); err != nil {
			return 0, err
		}
		if info, err := os.Lstat(lfsPath(store, oid)); err == nil && info.Mode().IsRegular() {
			taken++
		}
	}
	if _, err := runGit(ctx, r.lfsRunner(extra), 30*time.Minute, line, "lfs", "pre-push", "origin", r.Remote); err != nil {
		return 0, err
	}
	return taken, nil
}

// lfsFromCheckout takes one LFS object from the checkout's account into the
// relay's store: the account's git-lfs writes it from its own store to a pipe,
// and the relay keeps it only when its content is that object. One the account
// does not hold is left out (the remote has it, or the push says so).
func (r *Relay) lfsFromCheckout(ctx context.Context, checkout gitRunner, path, store, oid string) error {
	held, err := os.Lstat(lfsPath(filepath.Join(path, ".git", "lfs", "objects"), oid))
	if err != nil || !held.Mode().IsRegular() {
		return nil
	}
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + "\nsize " + strconv.FormatInt(held.Size(), 10) + "\n"
	tmp := filepath.Join(r.Dir, "lfs", "incoming")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(tmp, oid+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	hash := sha256.New()
	// No more than the object's size is kept: what the account's git-lfs
	// writes is the account's, and the relay's store is the dashboard's disk.
	bounded := &sizeBound{w: io.MultiWriter(file, hash), left: held.Size()}
	smudged := runGitWithInput(ctx, checkout, 10*time.Minute, strings.NewReader(pointer), bounded, "lfs", "smudge", "--", "object")
	if err := file.Close(); err != nil {
		return err
	}
	if bounded.over {
		return fmt.Errorf("the checkout's Git LFS object %s is larger than its size", oid)
	}
	if smudged != nil {
		return smudged
	}
	if hex.EncodeToString(hash.Sum(nil)) != oid {
		return fmt.Errorf("the checkout's Git LFS object %s does not hold what its id says", oid)
	}
	into := lfsPath(store, oid)
	if err := os.MkdirAll(filepath.Dir(into), 0o700); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), into); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// sizeBound writes to w no more than left bytes, and fails a write past them.
type sizeBound struct {
	w    io.Writer
	left int64
	over bool
}

func (b *sizeBound) Write(p []byte) (int, error) {
	if int64(len(p)) > b.left {
		b.over = true
		return 0, errors.New("larger than its size")
	}
	b.left -= int64(len(p))
	return b.w.Write(p)
}

// lfsConfigured is whether the checkout's git sends files through git-lfs, so
// that files made real are read back as the pointers committed.
func lfsConfigured(ctx context.Context, checkout gitRunner) bool {
	out, err := runGit(ctx, checkout, 30*time.Second, "", "config", "--get", "filter.lfs.process")
	if err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	out, err = runGit(ctx, checkout, 30*time.Second, "", "config", "--get", "filter.lfs.smudge")
	return err == nil && strings.TrimSpace(out) != ""
}

// withEnv is run with values added to every command's environment.
func withEnv(run gitRunner, values ...string) gitRunner {
	return func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := run(ctx, args...)
		cmd.Env = append(cmd.Env, values...)
		return cmd
	}
}
