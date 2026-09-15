package linuxusers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
	return k, nil
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
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
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

func (s *Service) ListKeys(username string) ([]SSHKey, string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, "", ErrNotFound
	}
	path := authorizedKeysPath(u.HomeDir)
	dir, _, _, err := openSSHDir(u, false)
	if os.IsNotExist(err) {
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

func (s *Service) AddKey(username, raw string) (*SSHKey, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	key, err := ValidatePublicKey(raw)
	if err != nil {
		return nil, err
	}
	u, err := user.Lookup(username)
	if err != nil {
		return nil, ErrNotFound
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
			return nil, fmt.Errorf("this key is already authorised for %s", username)
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
	u, err := user.Lookup(username)
	if err != nil {
		return ErrNotFound
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
