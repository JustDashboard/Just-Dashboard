package linuxusers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

// SSHKey is one entry of an authorized_keys file, with the fingerprint
// computed the same way `ssh-keygen -lf` reports it so an operator can compare
// against what they have locally.
type SSHKey struct {
	Line        int    `json:"line"`
	Type        string `json:"type"`
	Comment     string `json:"comment"`
	Fingerprint string `json:"fingerprint"`
	Bits        int    `json:"bits,omitempty"`
	Options     string `json:"options,omitempty"`
	Raw         string `json:"raw"`
	// Weak says why a key should be replaced: DSA is gone from OpenSSH and an
	// RSA key under 2048 bits is breakable.
	Weak string `json:"weak,omitempty"`
	// File is set on a key in a file other than the one this page writes: sshd
	// reads it, and it can be seen here but not changed.
	File string `json:"file,omitempty"`
	// LastUsed is the newest sign-in the host's auth log credits to this
	// fingerprint. Absent means the log does not reach it, not that it never
	// happened.
	LastUsed     *time.Time `json:"lastUsed,omitempty"`
	LastUsedFrom string     `json:"lastUsedFrom,omitempty"`
}

func authorizedKeysPath(home string) string {
	return filepath.Join(home, ".ssh", "authorized_keys")
}

func parseAuthorizedKey(line string) (*SSHKey, error) {
	pub, comment, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, err
	}
	k := &SSHKey{
		Type:        pub.Type(),
		Comment:     comment,
		Fingerprint: ssh.FingerprintSHA256(pub),
		Raw:         line,
	}
	if len(options) > 0 {
		k.Options = strings.Join(options, ",")
	}
	k.Bits, k.Weak = keyStrength(pub)
	return k, nil
}

// keyStrength reads what the key type does not say on its own. Only RSA has a
// size worth reporting; every other type's is fixed by its name.
func keyStrength(pub ssh.PublicKey) (int, string) {
	if pub.Type() == ssh.KeyAlgoDSA {
		return 1024, "DSA keys are no longer accepted by current OpenSSH"
	}
	crypto, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return 0, ""
	}
	rsaKey, ok := crypto.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return 0, ""
	}
	bits := rsaKey.N.BitLen()
	if bits < 2048 {
		return bits, fmt.Sprintf("a %d-bit RSA key is too short to be safe", bits)
	}
	return bits, ""
}

// ValidatePublicKey rejects anything that is not a well-formed public key
// before it is written. An authorized_keys file with a malformed line makes
// sshd ignore it, which would silently lock the operator out.
func ValidatePublicKey(raw string) (*SSHKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("key is empty")
	}
	if strings.Contains(raw, "\n") {
		return nil, fmt.Errorf("paste a single public key, not a file with several lines")
	}
	if strings.Contains(raw, "PRIVATE KEY") {
		return nil, fmt.Errorf("that is a private key — paste the matching .pub file instead")
	}
	return parseAuthorizedKey(raw)
}

// A directory descriptor anchors every operation even if the account renames
// its home or .ssh while a privileged request is in flight.
var sshKeysMu sync.Mutex

func openSSHDir(u *user.User, create bool) (*os.File, int, int, error) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, 0, 0, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, 0, 0, err
	}
	if !filepath.IsAbs(u.HomeDir) || filepath.Clean(u.HomeDir) == "/" {
		return nil, 0, 0, fmt.Errorf("invalid account home")
	}
	// The host's root rather than this container's: a daemon's home under
	// /var/lib or an account's under /data is not one of the mounts this image
	// shares, and its keys were being counted as none.
	fd, err := unix.Open(hostexec.HostPath("/"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(u.HomeDir), "/"), "/") {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, 0, 0, fmt.Errorf("open account home: %w", openErr)
		}
		fd = next
	}
	defer unix.Close(fd)
	created := false
	if create {
		err = unix.Mkdirat(fd, ".ssh", 0700)
		if err == nil {
			created = true
		} else if err != unix.EEXIST {
			return nil, 0, 0, err
		}
	}
	sshFD, err := unix.Openat(fd, ".ssh", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, 0, err
	}
	dir := os.NewFile(uintptr(sshFD), ".ssh")
	if created {
		if err := dir.Chown(uid, gid); err != nil {
			dir.Close()
			return nil, 0, 0, err
		}
	}
	if create {
		var info unix.Stat_t
		if err := unix.Fstat(sshFD, &info); err != nil {
			dir.Close()
			return nil, 0, 0, err
		}
		if int(info.Uid) != uid {
			dir.Close()
			return nil, 0, 0, fmt.Errorf(".ssh must already belong to the account")
		}
		if err := dir.Chmod(0700); err != nil {
			dir.Close()
			return nil, 0, 0, err
		}
	}
	return dir, uid, gid, nil
}

// keySourceCap bounds a key file read for counting, as readKeysAt does.
const keySourceCap = 2 << 20

// otherKeyFiles is every file sshd reads for this account besides the one this page
// writes. OpenSSH's own default includes ~/.ssh/authorized_keys2.
func otherKeyFiles(policy SSHPolicy, u User) []string {
	sources := policy.AuthorizedKeysFiles
	if !policy.Known {
		sources = []string{".ssh/authorized_keys", ".ssh/authorized_keys2"}
	}
	primary := filepath.Clean(authorizedKeysPath(u.Home))
	expand := strings.NewReplacer("%%", "%", "%h", u.Home, "%U", strconv.Itoa(u.UID), "%u", u.Username)
	out := []string{}
	for _, source := range sources {
		if source == "none" {
			continue
		}
		path := expand.Replace(source)
		if !filepath.IsAbs(path) {
			path = filepath.Join(u.Home, path)
		}
		if path = filepath.Clean(path); path != primary && !contains(out, path) {
			out = append(out, path)
		}
	}
	return out
}

// otherKeys reads those files. Nothing it finds is written back: they are the
// account's, or somebody's configuration management's. It does not follow a link at
// the end of the path, so what comes back as unreadable is a link sshd may well
// follow, and the caller treats that as keys it cannot count.
func otherKeys(policy SSHPolicy, u User) (keys []SSHKey, unreadable bool) {
	if !policy.Installed || !isLoginShell(u.Shell) {
		return nil, false
	}
	for _, path := range otherKeyFiles(policy, u) {
		f, err := os.OpenFile(hostexec.HostPath(path), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			if !os.IsNotExist(err) && !errors.Is(err, unix.ENOTDIR) {
				unreadable = true
			}
			continue
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			f.Close()
			unreadable = true
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(f, keySourceCap))
		f.Close()
		if readErr != nil {
			unreadable = true
			continue
		}
		for _, key := range keysFromBytes(content) {
			key.File = path
			keys = append(keys, key)
		}
	}
	return keys, unreadable
}

func readKeysAt(dir *os.File) ([]byte, error) {
	fd, err := unix.Openat(int(dir.Fd()), "authorized_keys", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "authorized_keys")
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("authorized_keys must be a regular file")
	}
	// A second name means this may be somebody else's file. An account can link
	// anything it can read in here, and a privileged rewrite would then copy that
	// file's contents into one the account owns.
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Nlink > 1 {
		return nil, fmt.Errorf("authorized_keys has more than one name on disk: it needs fixing by hand")
	}
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("authorized_keys exceeds 2 MiB")
	}
	return b, err
}

func writeKeysAt(dir *os.File, content []byte, uid, gid int) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	name := ".authorized_keys-" + hex.EncodeToString(token[:])
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err := f.Chown(uid, gid); err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Rename replaces the entry itself: a concurrent symlink or hard link can
	// never redirect a write or chown into an unrelated file.
	return unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), "authorized_keys")
}

func keysFromBytes(content []byte) []SSHKey {
	keys := []SSHKey{}
	for n, raw := range strings.Split(string(content), "\n") {
		key, err := parseAuthorizedKey(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		key.Line = n + 1
		keys = append(keys, *key)
	}
	return keys
}

// account finds a name in the same passwd file the rest of the service reads.
func (s *Service) account(username string) (*user.User, error) {
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Username == username {
			return &user.User{
				Username: u.Username, Uid: strconv.Itoa(u.UID), Gid: strconv.Itoa(u.GID), HomeDir: u.Home,
			}, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) ListKeys(username string) ([]SSHKey, string, error) {
	u, err := s.account(username)
	if err != nil {
		return nil, "", err
	}
	path := authorizedKeysPath(u.HomeDir)
	dir, _, _, err := openSSHDir(u, false)
	// Wrapped, so os.IsNotExist cannot see it: an account whose home is gone has
	// no keys, which is an answer rather than a failure.
	if errors.Is(err, os.ErrNotExist) {
		return []SSHKey{}, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	defer dir.Close()
	content, err := readKeysAt(dir)
	if os.IsNotExist(err) {
		return []SSHKey{}, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	return keysFromBytes(content), path, nil
}

// KeyEdit is what an operator may say about a key beyond the key itself. A nil
// Comment or Options keeps what the line already has.
type KeyEdit struct {
	Comment *string  `json:"comment,omitempty"`
	Options []string `json:"options,omitempty"`
	// ClearOptions lets an edit take every restriction off, which a nil Options
	// could not say.
	ClearOptions bool `json:"clearOptions,omitempty"`
}

// keyOptionRe is the closed list of what authorized_keys understands. A word
// outside it makes sshd ignore the whole line, which would look like the key
// stopped working for no reason.
var keyOptionRe = regexp.MustCompile(`^(restrict|cert-authority|no-agent-forwarding|no-port-forwarding|no-pty|no-user-rc|no-X11-forwarding|agent-forwarding|port-forwarding|pty|user-rc|X11-forwarding|(command|environment|expiry-time|from|permitlisten|permitopen|principals|tunnel)="[^"\n]*")$`)

// composeKeyLine builds one authorized_keys line and proves it still parses to
// the same fingerprint, so an option can never be used to smuggle in a second
// key or a broken line.
func composeKeyLine(raw string, comment string, options []string) (*SSHKey, string, error) {
	current, err := ValidatePublicKey(raw)
	if err != nil {
		return nil, "", err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
	if err != nil {
		return nil, "", err
	}
	for _, option := range options {
		if !keyOptionRe.MatchString(option) {
			return nil, "", fmt.Errorf("%q is not a restriction authorized_keys understands", option)
		}
	}
	if strings.ContainsAny(comment, "\n\r") {
		return nil, "", fmt.Errorf("a key's label cannot contain a line break")
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	if len(options) > 0 {
		line = strings.Join(options, ",") + " " + line
	}
	parsed, err := parseAuthorizedKey(line)
	if err != nil {
		return nil, "", fmt.Errorf("that combination would not parse: %w", err)
	}
	if parsed.Fingerprint != current.Fingerprint {
		return nil, "", fmt.Errorf("that combination changes which key this is")
	}
	return parsed, line, nil
}

// AddKeyWith is AddKey with a label and restrictions applied to the pasted line.
func (s *Service) AddKeyWith(username, raw string, edit KeyEdit) (*SSHKey, error) {
	current, err := ValidatePublicKey(raw)
	if err != nil {
		return nil, err
	}
	comment := current.Comment
	if edit.Comment != nil {
		comment = *edit.Comment
	}
	options := splitKeyOptions(current.Options)
	if edit.Options != nil || edit.ClearOptions {
		options = edit.Options
	}
	_, line, err := composeKeyLine(raw, comment, options)
	if err != nil {
		return nil, err
	}
	return s.AddKey(username, line)
}

// splitKeyOptions undoes the comma join parseAuthorizedKey does, respecting
// quotes: `from="a,b",no-pty` is two options.
func splitKeyOptions(joined string) []string {
	out := []string{}
	current := strings.Builder{}
	quoted := false
	for _, r := range joined {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// UpdateKey rewrites one key's label and restrictions in place.
func (s *Service) UpdateKey(username, fingerprint string, edit KeyEdit) (*SSHKey, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	u, err := s.account(username)
	if err != nil {
		return nil, err
	}
	sshKeysMu.Lock()
	defer sshKeysMu.Unlock()
	dir, uid, gid, err := openSSHDir(u, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	content, err := readKeysAt(dir)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	var updated *SSHKey
	for i, raw := range lines {
		key, parseErr := parseAuthorizedKey(strings.TrimSpace(raw))
		if parseErr != nil || key.Fingerprint != fingerprint {
			continue
		}
		comment := key.Comment
		if edit.Comment != nil {
			comment = *edit.Comment
		}
		options := splitKeyOptions(key.Options)
		if edit.Options != nil || edit.ClearOptions {
			options = edit.Options
		}
		parsed, line, composeErr := composeKeyLine(strings.TrimSpace(raw), comment, options)
		if composeErr != nil {
			return nil, composeErr
		}
		parsed.Line = i + 1
		lines[i] = line
		updated = parsed
	}
	if updated == nil {
		return nil, fmt.Errorf("no key with fingerprint %s is authorised for %s", fingerprint, username)
	}
	if err := writeKeysAt(dir, []byte(strings.Join(lines, "\n")), uid, gid); err != nil {
		return nil, err
	}
	return updated, nil
}

// GeneratedKey is a new pair. The private half exists here for one response and
// is never written anywhere on the server.
type GeneratedKey struct {
	Key        *SSHKey `json:"key"`
	PrivateKey string  `json:"privateKey"`
}

// GenerateKey makes an Ed25519 pair, authorises the public half and hands the
// private half back once, protected by a passphrase if one is given.
func (s *Service) GenerateKey(username, comment, passphrase string) (*GeneratedKey, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if strings.ContainsAny(comment, "\n\r") {
		return nil, fmt.Errorf("a key's label cannot contain a line break")
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var block *pem.Block
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(private, comment, []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(private, comment)
	}
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	key, err := s.AddKey(username, line)
	if err != nil {
		return nil, err
	}
	return &GeneratedKey{Key: key, PrivateKey: string(pem.EncodeToMemory(block))}, nil
}

// CopyKey authorises a key one account already has for others. Each target is
// its own outcome: an account that already has it is not a failure.
func (s *Service) CopyKey(from, fingerprint string, to []string) (map[string]string, error) {
	keys, _, err := s.ListKeys(from)
	if err != nil {
		return nil, err
	}
	raw := ""
	for _, key := range keys {
		if key.Fingerprint == fingerprint {
			raw = key.Raw
		}
	}
	if raw == "" {
		return nil, fmt.Errorf("no key with fingerprint %s is authorised for %s", fingerprint, from)
	}
	results := map[string]string{}
	for _, target := range to {
		if _, err := s.AddKey(target, raw); err != nil {
			results[target] = err.Error()
			continue
		}
		results[target] = ""
	}
	return results, nil
}

// githubLoginRe is GitHub's own rule for a login, so nothing else can be spliced
// into the one URL this fetches.
var githubLoginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// githubKeysURL is a variable so a test can point it at a local server.
var githubKeysURL = "https://github.com/%s.keys"

// githubClient follows a redirect only within the host it asked. Keys that open a
// server are not something to take from wherever a response points.
var githubClient = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("GitHub redirected to %s", req.URL.Host)
	}
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects")
	}
	return nil
}}

// keyImportCap bounds the response: an account has a handful of keys, and
// anything larger is not an answer to this question.
const keyImportCap = 256 << 10

// FetchGitHubKeys reads the public keys GitHub publishes for a login. Nothing
// here is secret, and the address is fixed, so a request cannot be aimed
// anywhere else from the dashboard's network.
func FetchGitHubKeys(ctx context.Context, login string) ([]string, error) {
	if !githubLoginRe.MatchString(login) {
		return nil, fmt.Errorf("%q is not a GitHub username", login)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(githubKeysURL, login), nil)
	if err != nil {
		return nil, err
	}
	res, err := githubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub could not be reached: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("GitHub has no user named %s", login)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, keyImportCap))
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// ImportResult is what an import did, key by key.
type ImportResult struct {
	Added   []SSHKey `json:"added"`
	Skipped int      `json:"skipped"`
}

// ImportGitHubKeys authorises what GitHub publishes for a login, labelled so the
// page can draw GitHub's mark and a person can tell later where it came from.
func (s *Service) ImportGitHubKeys(ctx context.Context, username, login string) (*ImportResult, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	lines, err := FetchGitHubKeys(ctx, login)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s has no public keys on GitHub", login)
	}
	result := &ImportResult{Added: []SSHKey{}}
	label := "github:" + login
	for _, line := range lines {
		key, addErr := s.AddKeyWith(username, line, KeyEdit{Comment: &label})
		if IsAlreadyAuthorised(addErr) {
			// The one expected refusal, and a run of them is an import done twice
			// rather than a failure.
			result.Skipped++
			continue
		}
		if addErr != nil {
			return result, addErr
		}
		result.Added = append(result.Added, *key)
	}
	return result, nil
}

var errAlreadyAuthorised = errors.New("this key is already authorised")

// IsAlreadyAuthorised is the one refusal an import or a template expects.
func IsAlreadyAuthorised(err error) bool { return errors.Is(err, errAlreadyAuthorised) }

func (s *Service) AddKey(username, raw string) (*SSHKey, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	key, err := ValidatePublicKey(raw)
	if err != nil {
		return nil, err
	}
	u, err := s.account(username)
	if err != nil {
		return nil, err
	}
	sshKeysMu.Lock()
	defer sshKeysMu.Unlock()
	dir, uid, gid, err := openSSHDir(u, true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	content, err := readKeysAt(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, existing := range keysFromBytes(content) {
		if existing.Fingerprint == key.Fingerprint {
			return nil, fmt.Errorf("%w for %s", errAlreadyAuthorised, username)
		}
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	key.Line = strings.Count(string(content), "\n") + 1
	content = append(content, []byte(strings.TrimSpace(raw)+"\n")...)
	if err := writeKeysAt(dir, content, uid, gid); err != nil {
		return nil, err
	}
	return key, nil
}

// RemoveKey retains comments and unrecognised lines; another tool may depend
// on them even though the dashboard only displays recognised public keys.
func (s *Service) RemoveKey(username, fingerprint string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	u, err := s.account(username)
	if err != nil {
		return err
	}
	sshKeysMu.Lock()
	defer sshKeysMu.Unlock()
	dir, uid, gid, err := openSSHDir(u, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	content, err := readKeysAt(dir)
	if err != nil {
		return err
	}
	kept := []string{}
	found := false
	for _, raw := range strings.SplitAfter(string(content), "\n") {
		key, err := parseAuthorizedKey(strings.TrimSpace(raw))
		if err == nil && key.Fingerprint == fingerprint {
			found = true
			continue
		}
		kept = append(kept, raw)
	}
	if !found {
		return fmt.Errorf("no key with fingerprint %s is authorised for %s", fingerprint, username)
	}
	return writeKeysAt(dir, []byte(strings.Join(kept, "")), uid, gid)
}

// RevokeKey takes a key away after judging it, under the one lock. The same key on
// two lines is one revocation of two keys: the guard must count what RemoveKey will
// actually remove.
func (s *Service) RevokeKey(ctx context.Context, username, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, _, err := s.ListKeys(username)
	if err != nil {
		return err
	}
	lines := 0
	for _, key := range keys {
		if key.Fingerprint == fingerprint {
			lines++
		}
	}
	if lines > 0 {
		if err := s.guard(ctx, Change{Username: username, KeysRemoved: lines}); err != nil {
			return err
		}
	}
	return s.RemoveKey(username, fingerprint)
}

// KeysWithUse is ListKeys with what the auth log says about each key, and the keys
// in files this page does not write, which sshd honours all the same.
func (s *Service) KeysWithUse(ctx context.Context, username string) ([]SSHKey, string, error) {
	keys, path, err := s.ListKeys(username)
	if err != nil {
		return keys, path, err
	}
	if account, lookupErr := s.lookup(username); lookupErr == nil {
		other, _ := otherKeys(s.sshPolicy(ctx, username), *account)
		keys = append(keys, other...)
	}
	uses := keyUses(s.authLines(ctx), username, s.now())
	for i := range keys {
		if use, ok := uses[keys[i].Fingerprint]; ok {
			at := use.at
			keys[i].LastUsed = &at
			keys[i].LastUsedFrom = use.from
		}
	}
	return keys, path, nil
}
