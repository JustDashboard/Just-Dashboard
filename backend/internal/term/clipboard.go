package term

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// ClipboardRoot is mounted into the backend container at the same path it
	// has on the host. A path returned to the browser therefore names the same
	// file from inside the host-side shell.
	ClipboardRoot = "/tmp/just-dashboard"

	// MaxClipboardImageBytes is deliberately much smaller than the file
	// manager's upload limit. A clipboard screenshot should fit comfortably;
	// larger transfers belong in the file manager, scp or rsync.
	MaxClipboardImageBytes int64 = 20 << 20

	// ClipboardTTL leaves an uploaded screenshot available to a long-running
	// interactive tool without letting abandoned session directories collect
	// forever. A directory is also removed immediately when its PTY truly ends.
	ClipboardTTL = 7 * 24 * time.Hour
)

var (
	ErrClipboardType      = errors.New("clipboard image type is not supported")
	ErrClipboardTooLarge  = errors.New("clipboard image exceeds the upload limit")
	ErrClipboardSessionID = errors.New("invalid terminal session id")
	ErrSessionOwner       = errors.New("terminal session belongs to another dashboard user")

	clipboardName = regexp.MustCompile(`^clipboard-[0-9a-f]{32}\.(png|jpg|webp)$`)
)

var clipboardTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// ClipboardFile is the server-generated temporary file returned to the
// upload handler. Path is always absolute and chosen here, never by a client.
type ClipboardFile struct {
	Path string `json:"path"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

type clipboardStore struct {
	root string
}

func newClipboardStore(root string) *clipboardStore {
	return &clipboardStore{root: filepath.Clean(root)}
}

// SetClipboardRootForTest isolates handler tests from a running dashboard's
// host-mounted clipboard directory. Production never calls this.
func (m *Manager) SetClipboardRootForTest(root string) {
	m.clipboard = newClipboardStore(root)
}

// SaveClipboard stores one image for a live session owned by owner.
//
// The ownership check and the second existence check belong beside the
// manager lookup: a session can end while a multipart body is still arriving.
// In that race the completed file is removed and the caller learns that there
// is no terminal left to receive its path.
func (m *Manager) SaveClipboard(sessionID, owner, declaredMIME string, src io.Reader) (ClipboardFile, error) {
	sess, err := m.Get(sessionID)
	if err != nil {
		return ClipboardFile{}, err
	}
	if sess.Owner != owner {
		return ClipboardFile{}, ErrSessionOwner
	}
	if m.accountErr != nil {
		return ClipboardFile{}, m.accountErr
	}
	if m.clipboard == nil {
		m.clipboard = newClipboardStore(ClipboardRoot)
	}

	file, err := m.clipboard.save(sessionID, declaredMIME, src, m.account.UID, m.account.GID)
	if err != nil {
		return ClipboardFile{}, err
	}
	current, err := m.Get(sessionID)
	if err != nil || current != sess {
		m.clipboard.removeFile(file.Path)
		return ClipboardFile{}, ErrNotFound
	}
	return file, nil
}

func (s *clipboardStore) save(sessionID, declaredMIME string, src io.Reader, uid, gid int) (ClipboardFile, error) {
	ext, ok := clipboardTypes[declaredMIME]
	if !ok {
		return ClipboardFile{}, ErrClipboardType
	}
	if !validSessionID(sessionID) {
		return ClipboardFile{}, ErrClipboardSessionID
	}
	if err := s.ensureRoot(); err != nil {
		return ClipboardFile{}, err
	}

	dir := filepath.Join(s.root, sessionID)
	if filepath.Dir(dir) != s.root {
		return ClipboardFile{}, ErrClipboardSessionID
	}
	if err := ensureClipboardDir(dir, uid, gid); err != nil {
		return ClipboardFile{}, err
	}

	// The declared multipart type is not trusted. Detect the bytes before a
	// destination exists, and require the two to agree so a renamed executable
	// or archive cannot enter this image-only pipeline.
	head := make([]byte, 512)
	n, readErr := io.ReadFull(src, head)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return ClipboardFile{}, readErr
	}
	head = head[:n]
	if http.DetectContentType(head) != declaredMIME {
		return ClipboardFile{}, ErrClipboardType
	}

	random := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return ClipboardFile{}, fmt.Errorf("generate clipboard filename: %w", err)
	}
	name := "clipboard-" + hex.EncodeToString(random) + ext
	path := filepath.Join(dir, name)
	if filepath.Dir(path) != dir || !clipboardName.MatchString(name) {
		return ClipboardFile{}, fmt.Errorf("generated an invalid clipboard filename")
	}

	// O_EXCL is important even with a random name: it refuses an existing
	// symlink instead of following it. The terminal account owns the containing
	// directory, so the random name is also what makes a collision impractical.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ClipboardFile{}, err
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(path)
		}
	}()

	limited := io.LimitReader(io.MultiReader(bytes.NewReader(head), src), MaxClipboardImageBytes+1)
	size, err := io.Copy(f, limited)
	if err != nil {
		return ClipboardFile{}, err
	}
	if size > MaxClipboardImageBytes {
		return ClipboardFile{}, ErrClipboardTooLarge
	}
	// Apply ownership through the already-open descriptor. The containing
	// directory belongs to the terminal account, so path-based Chmod/Chown
	// here would introduce a symlink-swap race after the upload completed.
	if err := f.Chmod(0o600); err != nil {
		return ClipboardFile{}, err
	}
	if err := f.Chown(uid, gid); err != nil {
		return ClipboardFile{}, err
	}
	if err := f.Close(); err != nil {
		return ClipboardFile{}, err
	}
	keep = true
	return ClipboardFile{Path: path, MIME: declaredMIME, Size: size}, nil
}

func (s *clipboardStore) ensureRoot() error {
	if !filepath.IsAbs(s.root) {
		return fmt.Errorf("clipboard root must be absolute")
	}
	if err := os.Mkdir(s.root, 0o711); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(s.root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("clipboard root is not a directory")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		// /tmp is shared with every local account. Refuse a directory another
		// account planted there rather than letting a root backend follow or
		// take ownership of an attacker's filesystem object.
		return fmt.Errorf("clipboard root is not owned by the dashboard process")
	}
	// The base is searchable so the terminal account can reach its own 0700
	// session directory, but not listable or writable by that account.
	return os.Chmod(s.root, 0o711)
}

func ensureClipboardDir(dir string, uid, gid int) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("clipboard session path is not a directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return os.Chown(dir, uid, gid)
}

func validSessionID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *clipboardStore) removeFile(path string) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	os.Remove(path)
}

func (s *clipboardStore) removeSession(sessionID string) {
	if !validSessionID(sessionID) {
		return
	}
	os.RemoveAll(filepath.Join(s.root, sessionID))
}

// cleanupExpired removes only files this pipeline could have created. The
// terminal account owns its session directory and may have put something else
// there; a TTL pass must not turn that into an arbitrary recursive delete.
func (s *clipboardStore) cleanupExpired(cutoff time.Time, live map[string]bool) {
	dirs, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, dirEntry := range dirs {
		if !dirEntry.IsDir() || !validSessionID(dirEntry.Name()) || live[dirEntry.Name()] {
			continue
		}
		dir := filepath.Join(s.root, dirEntry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fileEntry := range files {
			if !clipboardName.MatchString(fileEntry.Name()) {
				continue
			}
			path := filepath.Join(dir, fileEntry.Name())
			info, err := os.Lstat(path)
			if err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
				os.Remove(path)
			}
		}
		if left, err := os.ReadDir(dir); err == nil && len(left) == 0 {
			os.Remove(dir)
		}
	}
}
