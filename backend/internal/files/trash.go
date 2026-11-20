package files

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The Trash is where a delete on the Files page puts things, so a slip of the
// Delete key costs an Undo rather than a backup restore.
//
// Trashing is a rename and never a copy: a fifty-gigabyte folder goes to the
// Trash at once, and a Trash that copied would be a Trash that failed when the
// disk was nearly full, which is when people delete things. A rename cannot
// leave its filesystem, so there is one Trash folder per filesystem — under
// the dashboard's data directory for whatever shares its filesystem, and
// `.jd-trash` at the top of every other mount — each root's alone (0700), made
// the first time something is put in it. Where none can be (a read-only
// filesystem, the kernel's own, a mount point itself) the item is refused with
// the reason, and the page asks for a permanent delete instead: a silent copy
// across devices is the one thing this must never do.
//
// Each item sits alone in a folder named by its id, under its own name, with
// its owner, mode and contents exactly as they were, and beside that folder a
// small record of where it came from, when, and who put it there. The listing
// is read from those folders every time, so it cannot drift from what is on
// disk and survives a lost database.

// TrashFolderName is the Trash folder's name in every place it is made. Find,
// search, folder sizes and archives skip a folder of this name.
const TrashFolderName = ".jd-trash"

// IsTrashFolderName reports whether a directory entry's name is the Trash's.
func IsTrashFolderName(name string) bool { return name == TrashFolderName }

var (
	// ErrTrashUnavailable is the refusal of an item that cannot be trashed;
	// errors.As finds the *TrashUnavailableError carrying the reason.
	ErrTrashUnavailable = errors.New("this cannot go to the Trash")
	// ErrNotInTrash is an id that names nothing in any Trash folder.
	ErrNotInTrash = errors.New("nothing in the Trash has that id")
)

// TrashUnavailableError says why an item cannot go to the Trash, in words the
// page shows beside its offer to delete permanently instead.
type TrashUnavailableError struct{ Reason string }

func (e *TrashUnavailableError) Error() string        { return e.Reason }
func (e *TrashUnavailableError) Is(target error) bool { return target == ErrTrashUnavailable }

func unavailable(format string, args ...any) error {
	return &TrashUnavailableError{Reason: fmt.Sprintf(format, args...)}
}

var trashIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// trashRecord is what is kept beside an item, in `<id>.json`.
type trashRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	DeletedAt   time.Time `json:"deletedAt"`
	DeletedBy   string    `json:"deletedBy,omitempty"`
	Replaced    bool      `json:"replaced,omitempty"`
	Size        int64     `json:"size"`
	SizeKnown   bool      `json:"sizeKnown"`
	SizePartial bool      `json:"sizePartial,omitempty"`
}

// TrashItem is one thing in the Trash as the page lists it.
type TrashItem struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OriginalPath string     `json:"originalPath"`
	DeletedAt    time.Time  `json:"deletedAt"`
	DeletedBy    string     `json:"deletedBy,omitempty"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	// Replaced is an item that went in because a move, a copy or a restore
	// put something else at its name.
	Replaced    bool   `json:"replaced,omitempty"`
	IsDir       bool   `json:"isDir"`
	IsSymlink   bool   `json:"isSymlink"`
	LinkTarget  string `json:"linkTarget,omitempty"`
	Size        int64  `json:"size"`
	SizeKnown   bool   `json:"sizeKnown"`
	SizePartial bool   `json:"sizePartial,omitempty"`
	Owner       string `json:"owner"`
	Group       string `json:"group"`
	Mode        string `json:"mode"`
	ModeOctal   string `json:"modeOctal"`
	// Folder is the Trash folder holding it, which says which filesystem.
	Folder string `json:"folder"`
}

// TrashListing is everything in every Trash folder, newest first.
type TrashListing struct {
	Items []TrashItem `json:"items"`
	// Bytes is what the items hold together; Partial says at least one size
	// is still being measured or could only be measured in part, so Bytes is
	// a floor.
	Bytes   int64 `json:"bytes"`
	Partial bool  `json:"partial"`
	// KeepDays is how long an item is kept; 0 keeps everything until it is
	// removed by hand.
	KeepDays int `json:"keepDays"`
}

// TrashPlan answers "would this go to the Trash?" before anybody asks to
// delete it, so the confirmation can say what will really happen.
type TrashPlan struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

// Trash is the Files page's Trash, over the same roots and in the same mount
// namespace as the Service it is made from.
type Trash struct {
	svc     *Service
	dataDir string
	keep    time.Duration
	now     func() time.Time

	mu        sync.Mutex
	measuring map[string]bool
}

// NewTrash keeps items for keepDays days; 0 keeps them until emptied.
func NewTrash(svc *Service, dataDir string, keepDays int) *Trash {
	if keepDays < 0 {
		keepDays = 0
	}
	if dataDir != "" {
		if abs, err := filepath.Abs(dataDir); err == nil {
			dataDir = filepath.Clean(abs)
		}
	}
	return &Trash{
		svc: svc, dataDir: dataDir, keep: time.Duration(keepDays) * 24 * time.Hour,
		now: time.Now, measuring: map[string]bool{},
	}
}

// KeepDays is how long an item stays before it is removed in the background.
func (t *Trash) KeepDays() int { return int(t.keep / (24 * time.Hour)) }

// on runs fn where the Service runs its operations: in the host's mount
// namespace for the Files page.
func (t *Trash) on(fn func(s *Service)) {
	if t.svc.run != nil {
		t.svc.run(func() { fn(t.svc.inPlace()) })
		return
	}
	fn(t.svc)
}

// Check says whether path would go to the Trash, and why not where it would
// not. It changes nothing.
func (t *Trash) Check(path string) (plan *TrashPlan, err error) {
	t.on(func(s *Service) {
		var full string
		full, err = s.ResolveEntry(path)
		if err != nil {
			return
		}
		if _, err = os.Lstat(full); err != nil {
			return
		}
		plan = &TrashPlan{Path: full}
		folder, why := t.folderFor(s, full)
		if why != nil {
			var refusal *TrashUnavailableError
			if errors.As(why, &refusal) {
				plan.Reason = refusal.Reason
				return
			}
			err = why
			plan = nil
			return
		}
		if why := checkFolder(folder, false); why != nil {
			plan.Reason = why.Error()
			return
		}
		plan.Available, plan.Folder = true, folder
	})
	return plan, err
}

// Put moves path into its filesystem's Trash. actor is recorded as who deleted
// it.
func (t *Trash) Put(path, actor string) (item *TrashItem, err error) {
	t.on(func(s *Service) { item, err = t.put(s, path, actor, false, false) })
	if err == nil && item.IsDir {
		t.measureLater(item.Folder, item.ID)
	}
	return item, err
}

// PutReplaced is Put for an entry a move or a copy is about to replace.
// A regular file is kept by a hard link rather than renamed away: the copy that
// replaces it reads the file's owner, mode and access list from where it is,
// and the link keeps the old bytes once the copy's rename has taken the name.
// Discard undoes it when the operation that asked for it fails.
func (t *Trash) PutReplaced(path, actor string, link bool) (item *TrashItem, err error) {
	t.on(func(s *Service) { item, err = t.put(s, path, actor, true, link) })
	if err == nil && item.IsDir {
		t.measureLater(item.Folder, item.ID)
	}
	return item, err
}

func (t *Trash) put(s *Service, path, actor string, replaced, link bool) (*TrashItem, error) {
	full, err := s.ResolveEntry(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	folder, err := t.folderFor(s, full)
	if err != nil {
		return nil, err
	}
	if err := checkFolder(folder, true); err != nil {
		return nil, err
	}
	id, err := newTrashID()
	if err != nil {
		return nil, err
	}
	holder := filepath.Join(folder, id)
	if err := os.Mkdir(holder, 0o700); err != nil {
		return nil, err
	}
	name := filepath.Base(full)
	record := trashRecord{
		ID: id, Name: name, Path: full, DeletedAt: t.now().UTC(), DeletedBy: actor, Replaced: replaced,
	}
	if !info.IsDir() {
		record.Size, record.SizeKnown = info.Size(), true
	}
	// The record first: an item without one would be in the Trash with no
	// way back to where it came from, and a record without an item is
	// skipped by the listing and swept by the next expiry.
	if err := writeTrashRecord(folder, record); err != nil {
		os.Remove(holder)
		return nil, err
	}
	discard := func() {
		os.Remove(filepath.Join(folder, id+".json"))
		os.Remove(holder)
	}
	if err := s.moveIntoTrash(full, holder, name, link); err != nil {
		discard()
		return nil, err
	}
	item, err := t.item(folder, record)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// moveIntoTrash renames (or, with link, hard-links) the entry at full into
// holder. The source's folder is opened through the pinned root, so a link
// swapped in above it after the resolve cannot redirect the rename.
func (s *Service) moveIntoTrash(full, holder, name string, link bool) error {
	root, rel, err := s.openRootEntry(full)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, err := root.OpenFile(filepath.Dir(rel), os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer parent.Close()
	into, err := os.OpenFile(holder, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer into.Close()
	from, to := int(parent.Fd()), int(into.Fd())
	if link {
		err = unix.Linkat(from, name, to, name, 0)
	} else {
		err = unix.Renameat2(from, name, to, name, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
			// No no-replace rename on this filesystem. The holder is new and
			// empty, so a plain rename cannot clobber anything.
			err = unix.Renameat(from, name, to, name)
		}
	}
	switch {
	case errors.Is(err, unix.EXDEV):
		return unavailable("it is on a different filesystem from its Trash folder")
	case errors.Is(err, unix.EROFS):
		return unavailable("its filesystem is read-only")
	case errors.Is(err, unix.EBUSY):
		return unavailable("it is in use as a mount point")
	case err != nil:
		return &os.LinkError{Op: "trash", Old: full, New: holder, Err: err}
	}
	return nil
}

// folderFor is the Trash folder an entry would go to, or why there is none.
func (t *Trash) folderFor(s *Service, full string) (string, error) {
	for _, root := range s.roots {
		if full == root {
			return "", unavailable("it is %s, one of the folders the dashboard manages", root)
		}
	}
	for _, part := range strings.Split(full, string(filepath.Separator)) {
		if IsTrashFolderName(part) {
			return "", unavailable("it is in a Trash folder already")
		}
	}
	if t.dataDir != "" && pathWithin(t.dataDir, full) {
		return "", unavailable("it holds the dashboard's own data, and the Trash with it")
	}
	var st unix.Statfs_t
	if unix.Statfs(filepath.Dir(full), &st) == nil && st.Flags&unix.ST_RDONLY != 0 {
		return "", unavailable("its filesystem is read-only")
	}
	dataDir := t.dataDir
	if dataDir != "" {
		if resolved, err := resolveFromExistingAncestor(dataDir); err == nil {
			dataDir = resolved
		}
	}
	mounts, err := readMountTable()
	if err != nil || len(mounts) == 0 {
		return t.folderByDevice(full, dataDir)
	}
	if isMountPoint(mounts, full) {
		return "", unavailable("a filesystem is mounted at it; unmount it rather than deleting it")
	}
	mount, ok := mountOf(mounts, full)
	if !ok {
		return t.folderByDevice(full, dataDir)
	}
	if pseudoFS[mount.FSType] {
		return "", unavailable("it is on %s, a filesystem the kernel makes up rather than one that holds files", mount.FSType)
	}
	if mount.ReadOnly {
		return "", unavailable("its filesystem is read-only")
	}
	folder := filepath.Join(mount.Path, TrashFolderName)
	if dataDir != "" {
		if home, ok := mountOf(mounts, dataDir); ok && home.ID == mount.ID {
			folder = filepath.Join(dataDir, TrashFolderName)
		}
	}
	if pathWithin(folder, full) {
		return "", unavailable("it holds its filesystem's Trash folder")
	}
	return folder, nil
}

// folderByDevice is folderFor without a mount table: the data directory when
// it shares the entry's device, else the highest folder above the entry that
// still does.
func (t *Trash) folderByDevice(full, dataDir string) (string, error) {
	dev, ok := deviceOf(filepath.Dir(full))
	if !ok {
		return "", unavailable("its filesystem could not be read")
	}
	if own, ok := deviceOf(full); ok && own != dev {
		return "", unavailable("a filesystem is mounted at it; unmount it rather than deleting it")
	}
	if dataDir != "" {
		if home, ok := deviceOf(dataDir); ok && home == dev {
			return filepath.Join(dataDir, TrashFolderName), nil
		}
	}
	top := filepath.Dir(full)
	for top != "/" {
		above, ok := deviceOf(filepath.Dir(top))
		if !ok || above != dev {
			break
		}
		top = filepath.Dir(top)
	}
	folder := filepath.Join(top, TrashFolderName)
	if pathWithin(folder, full) {
		return "", unavailable("it holds its filesystem's Trash folder")
	}
	return folder, nil
}

func deviceOf(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// checkFolder makes sure folder is the dashboard's own Trash folder: a real
// directory, this process's, closed to everyone else. With create it is made
// when missing, and its mode closed again if somebody opened it.
func checkFolder(folder string, create bool) error {
	info, err := os.Lstat(folder)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			// Not there yet is fine: the first item makes it. Whether the
			// place can hold it is tested by making it, so a check only asks
			// that the folder above is a directory.
			if parent, err := os.Stat(filepath.Dir(folder)); err != nil || !parent.IsDir() {
				return unavailable("the top of its filesystem cannot hold a Trash folder")
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(folder), 0o700); err != nil {
			return unavailable("the top of its filesystem cannot hold a Trash folder")
		}
		if err := os.Mkdir(folder, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			if errors.Is(err, unix.EROFS) {
				return unavailable("its filesystem is read-only")
			}
			return unavailable("the top of its filesystem cannot hold a Trash folder")
		}
		info, err = os.Lstat(folder)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return unavailable("%s is not a folder the dashboard made", folder)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return unavailable("%s belongs to another account, not the dashboard", folder)
	}
	if info.Mode().Perm() != 0o700 && create {
		if err := os.Chmod(folder, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func newTrashID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeTrashRecord(folder string, record trashRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(folder, ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(folder, record.ID+".json"))
}

// readTrashRecord reads an item's record, or makes one up from the folder for
// an item whose record was lost: it is still listed, and can still be removed,
// but there is nowhere recorded to restore it to.
func readTrashRecord(folder, id string) (trashRecord, error) {
	raw, err := os.ReadFile(filepath.Join(folder, id+".json"))
	if err == nil {
		var record trashRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return trashRecord{}, err
		}
		if record.ID != id || !validTrashName(record.Name) {
			return trashRecord{}, fmt.Errorf("the Trash record %s is not one the dashboard wrote", id)
		}
		return record, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return trashRecord{}, err
	}
	holder := filepath.Join(folder, id)
	entries, readErr := os.ReadDir(holder)
	if readErr != nil || len(entries) != 1 {
		return trashRecord{}, err
	}
	info, statErr := os.Lstat(holder)
	if statErr != nil {
		return trashRecord{}, err
	}
	return trashRecord{ID: id, Name: entries[0].Name(), DeletedAt: info.ModTime().UTC()}, nil
}

func validTrashName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, filepath.Separator)
}

// item reads what is in the Trash under record, as the page lists it.
func (t *Trash) item(folder string, record trashRecord) (*TrashItem, error) {
	full := filepath.Join(folder, record.ID, record.Name)
	entry, err := t.svc.inPlace().entry(full, record.Name)
	if err != nil {
		return nil, err
	}
	item := &TrashItem{
		ID: record.ID, Name: record.Name, OriginalPath: record.Path, DeletedAt: record.DeletedAt,
		DeletedBy: record.DeletedBy, Replaced: record.Replaced,
		IsDir: entry.IsDir && !entry.IsSymlink, IsSymlink: entry.IsSymlink, LinkTarget: entry.LinkTarget,
		Size: record.Size, SizeKnown: record.SizeKnown, SizePartial: record.SizePartial,
		Owner: entry.Owner, Group: entry.Group, Mode: entry.Mode, ModeOctal: entry.ModeOctal,
		Folder: folder,
	}
	if entry.IsSymlink {
		// A link's own size, not what it points at: the Trash holds the link.
		if li, err := os.Lstat(full); err == nil {
			item.Size, item.SizeKnown = li.Size(), true
		}
	}
	if t.keep > 0 {
		expires := record.DeletedAt.Add(t.keep)
		item.ExpiresAt = &expires
	}
	return item, nil
}

// folders are the Trash folders that exist: the data directory's, and one at
// the top of each mount. Anything of the name that is not a directory this
// process owns is not a Trash folder and is left alone.
func (t *Trash) folders(s *Service) []string {
	seen := map[string]bool{}
	var out []string
	consider := func(folder string) {
		if seen[folder] {
			return
		}
		seen[folder] = true
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
			return
		}
		out = append(out, folder)
	}
	if t.dataDir != "" {
		dataDir := t.dataDir
		if resolved, err := resolveFromExistingAncestor(dataDir); err == nil {
			dataDir = resolved
		}
		consider(filepath.Join(dataDir, TrashFolderName))
	}
	mounts, _ := readMountTable()
	for _, m := range mounts {
		if pseudoFS[m.FSType] {
			continue
		}
		consider(filepath.Join(m.Path, TrashFolderName))
	}
	for _, root := range s.roots {
		consider(filepath.Join(root, TrashFolderName))
	}
	sort.Strings(out)
	return out
}

// ids lists the items in one Trash folder by the folders that hold them.
func trashIDs(folder string) []string {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && trashIDPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// List is everything in every Trash folder, newest first.
func (t *Trash) List() (listing *TrashListing, err error) {
	var unmeasured [][2]string
	t.on(func(s *Service) {
		listing = &TrashListing{Items: []TrashItem{}, KeepDays: t.KeepDays()}
		for _, folder := range t.folders(s) {
			for _, id := range trashIDs(folder) {
				record, err := readTrashRecord(folder, id)
				if err != nil {
					continue
				}
				item, err := t.item(folder, record)
				if err != nil {
					continue
				}
				listing.Items = append(listing.Items, *item)
				listing.Bytes += item.Size
				if !item.SizeKnown || item.SizePartial {
					listing.Partial = true
				}
				if !item.SizeKnown {
					unmeasured = append(unmeasured, [2]string{folder, id})
				}
			}
		}
	})
	sort.SliceStable(listing.Items, func(i, j int) bool {
		return listing.Items[i].DeletedAt.After(listing.Items[j].DeletedAt)
	})
	for _, u := range unmeasured {
		t.measureLater(u[0], u[1])
	}
	return listing, nil
}

// find locates an item by id in whichever Trash folder holds it. An id is
// checked against its pattern before it reaches a path, so it can only ever
// name a folder directly inside a Trash folder.
func (t *Trash) find(s *Service, id string) (string, trashRecord, error) {
	if !trashIDPattern.MatchString(id) {
		return "", trashRecord{}, fmt.Errorf("%w: %q is not a Trash id", ErrNotInTrash, id)
	}
	for _, folder := range t.folders(s) {
		info, err := os.Lstat(filepath.Join(folder, id))
		if err != nil || !info.IsDir() {
			continue
		}
		record, err := readTrashRecord(folder, id)
		if err != nil {
			return "", trashRecord{}, err
		}
		return folder, record, nil
	}
	return "", trashRecord{}, ErrNotInTrash
}

// Restore conflict choices, the ones the page offers for every other name
// that is taken.
const (
	RestoreAsk     = ""
	RestoreReplace = "replace"
	RestoreKeep    = "keep"
)

// Restored is where an item went back to, and what it displaced into the
// Trash when the choice was to replace.
type Restored struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Replaced *TrashItem `json:"replaced,omitempty"`
}

// Restore puts an item back where it was deleted from, with the owner and
// mode it has kept all along. A taken place is an fs.ErrExist unless conflict
// says to replace what is there (which then goes to the Trash) or keep both
// (the item comes back as "name (2)"). A folder it came from that is gone is
// made again, owned as a new folder there would be.
func (t *Trash) Restore(id, conflict, actor string) (out *Restored, err error) {
	var displaced *TrashItem
	t.on(func(s *Service) { out, displaced, err = t.restore(s, id, conflict, actor) })
	if displaced != nil && displaced.IsDir {
		t.measureLater(displaced.Folder, displaced.ID)
	}
	return out, err
}

func (t *Trash) restore(s *Service, id, conflict, actor string) (*Restored, *TrashItem, error) {
	switch conflict {
	case RestoreAsk, RestoreReplace, RestoreKeep:
	default:
		return nil, nil, fmt.Errorf("conflict must be empty, %q or %q", RestoreReplace, RestoreKeep)
	}
	folder, record, err := t.find(s, id)
	if err != nil {
		return nil, nil, err
	}
	if record.Path == "" {
		return nil, nil, errors.New("where this came from was not recorded; move it out of the Trash folder by hand")
	}
	target, err := s.ResolveEntry(record.Path)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(target)
	if info, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		made := unmade(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
		if err := handDown(madeBase(dir, made), made); err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	} else if !info.IsDir() {
		return nil, nil, fmt.Errorf("%w: %s, the folder it came from, is now something else", fs.ErrExist, dir)
	}
	// Resolved again now that the folder is certainly there, so a link put
	// in its place while it was missing still has to land inside the roots.
	target, err = s.ResolveEntry(record.Path)
	if err != nil {
		return nil, nil, err
	}
	dir, name := filepath.Dir(target), filepath.Base(target)
	var displaced *TrashItem
	if _, err := os.Lstat(target); err == nil {
		switch conflict {
		case RestoreReplace:
			displaced, err = t.put(s, target, actor, true, false)
			if err != nil {
				return nil, nil, err
			}
		case RestoreKeep:
			name = freeName(dir, name)
		default:
			return nil, nil, fmt.Errorf("%w: %s", fs.ErrExist, target)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	holder := filepath.Join(folder, id)
	if err := s.moveOutOfTrash(holder, record.Name, filepath.Join(dir, name)); err != nil {
		if displaced != nil {
			// Put back what was displaced: a restore that failed must not
			// also have emptied the place it was going to.
			if _, _, undoErr := t.restore(s, displaced.ID, RestoreAsk, actor); undoErr != nil {
				err = errors.Join(err, undoErr)
			}
		}
		return nil, nil, err
	}
	os.Remove(filepath.Join(folder, id+".json"))
	os.Remove(holder)
	return &Restored{Path: filepath.Join(dir, name), Name: name, Replaced: displaced}, displaced, nil
}

// moveOutOfTrash renames the item in holder to target, refusing to replace
// anything that claimed target in the meantime.
func (s *Service) moveOutOfTrash(holder, name, target string) error {
	root, rel, err := s.openRootEntry(target)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, err := root.OpenFile(filepath.Dir(rel), os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer parent.Close()
	from, err := os.OpenFile(holder, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer from.Close()
	err = unix.Renameat2(int(from.Fd()), name, int(parent.Fd()), filepath.Base(rel), unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		// No no-replace rename here: a hard link claims the name only if it
		// is free, for a file; a folder is checked and renamed.
		if _, statErr := os.Lstat(target); statErr == nil {
			return fmt.Errorf("%w: %s", fs.ErrExist, target)
		}
		err = unix.Renameat(int(from.Fd()), name, int(parent.Fd()), filepath.Base(rel))
	}
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("%w: %s", fs.ErrExist, target)
	case errors.Is(err, unix.EXDEV):
		return fmt.Errorf("the folder it came from is now on another filesystem, so it cannot be moved back in one step; copy it out of %s by hand", holder)
	case err != nil:
		return &os.LinkError{Op: "restore", Old: filepath.Join(holder, name), New: target, Err: err}
	}
	return nil
}

// freeName is name, or "name (2).ext" and up, whichever is not taken in dir:
// the page's own "keep both" for a transfer.
func freeName(dir, name string) string {
	stem, ext := name, ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		stem, ext = name[:dot], name[dot:]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		if _, err := os.Lstat(filepath.Join(dir, candidate)); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
}

// Remove deletes one item for good, and returns what it was.
func (t *Trash) Remove(id string) (item *TrashItem, err error) {
	t.on(func(s *Service) {
		var folder string
		var record trashRecord
		folder, record, err = t.find(s, id)
		if err != nil {
			return
		}
		item, _ = t.item(folder, record)
		if item == nil {
			item = &TrashItem{ID: id, Name: record.Name, OriginalPath: record.Path, Folder: folder}
		}
		err = removeTrashItem(folder, id)
	})
	return item, err
}

func removeTrashItem(folder, id string) error {
	if err := os.RemoveAll(filepath.Join(folder, id)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(folder, id+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Discard removes an item PutReplaced made for an operation that then failed:
// what it kept is still where it was.
func (t *Trash) Discard(id string) error {
	_, err := t.Remove(id)
	return err
}

// Empty deletes everything in every Trash folder for good.
func (t *Trash) Empty() (removed int, bytes int64, err error) {
	t.on(func(s *Service) {
		var errs []error
		for _, folder := range t.folders(s) {
			for _, id := range trashIDs(folder) {
				record, _ := readTrashRecord(folder, id)
				if err := removeTrashItem(folder, id); err != nil {
					errs = append(errs, err)
					continue
				}
				removed++
				bytes += record.Size
			}
		}
		err = errors.Join(errs...)
	})
	return removed, bytes, err
}

// Expired is one item the background sweep removed.
type Expired struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	DeletedAt time.Time `json:"deletedAt"`
}

// Expire removes what has been in the Trash longer than it is kept, and the
// leftovers of a put that never finished (a record whose item never arrived,
// an empty holder) once they are an hour old. Nothing expires when items are
// kept until emptied.
func (t *Trash) Expire() (gone []Expired, err error) {
	t.on(func(s *Service) {
		now := t.now()
		var errs []error
		for _, folder := range t.folders(s) {
			entries, _ := os.ReadDir(folder)
			for _, e := range entries {
				name := e.Name()
				if strings.HasSuffix(name, ".json") && trashIDPattern.MatchString(strings.TrimSuffix(name, ".json")) {
					id := strings.TrimSuffix(name, ".json")
					if _, err := os.Lstat(filepath.Join(folder, id)); errors.Is(err, fs.ErrNotExist) {
						if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
							os.Remove(filepath.Join(folder, name))
						}
					}
					continue
				}
				if !e.IsDir() || !trashIDPattern.MatchString(name) {
					continue
				}
				holder := filepath.Join(folder, name)
				if inside, _ := os.ReadDir(holder); len(inside) == 0 {
					if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
						removeTrashItem(folder, name)
					}
					continue
				}
				if t.keep <= 0 {
					continue
				}
				record, err := readTrashRecord(folder, name)
				if err != nil || now.Sub(record.DeletedAt) < t.keep {
					continue
				}
				if err := removeTrashItem(folder, name); err != nil {
					errs = append(errs, err)
					continue
				}
				gone = append(gone, Expired{ID: name, Name: record.Name, Path: record.Path, DeletedAt: record.DeletedAt})
			}
		}
		err = errors.Join(errs...)
	})
	return gone, err
}

// trashMeasureVisits bounds the walk that sizes one trashed folder.
const trashMeasureVisits = 2_000_000

// measureLater sizes a trashed folder in the background and writes the size
// into its record, so the listing can say how much the Trash holds without
// walking every folder in it on every visit.
func (t *Trash) measureLater(folder, id string) {
	key := folder + "/" + id
	t.mu.Lock()
	if t.measuring[key] {
		t.mu.Unlock()
		return
	}
	t.measuring[key] = true
	t.mu.Unlock()
	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.measuring, key)
			t.mu.Unlock()
		}()
		t.on(func(s *Service) { t.measure(folder, id) })
	}()
}

func (t *Trash) measure(folder, id string) {
	record, err := readTrashRecord(folder, id)
	if err != nil || record.SizeKnown {
		return
	}
	var total int64
	visits := 0
	partial := false
	deadline := time.Now().Add(2 * time.Minute)
	_ = filepath.WalkDir(filepath.Join(folder, id, record.Name), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			partial = true
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visits++
		if visits > trashMeasureVisits || (visits%4096 == 0 && time.Now().After(deadline)) {
			partial = true
			return filepath.SkipAll
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	// Read again: a restore or a removal while the walk ran means there is no
	// record left to write the size into.
	current, err := readTrashRecord(folder, id)
	if err != nil || current.Path == "" && record.Path != "" {
		return
	}
	if _, err := os.Stat(filepath.Join(folder, id+".json")); err != nil {
		return
	}
	current.Size, current.SizeKnown, current.SizePartial = total, true, partial
	_ = writeTrashRecord(folder, current)
}

// CopyEnds is where a copy from one path to another lands: onto `to` itself,
// or inside it when `to` is an existing directory, as Copy decides. The API
// reads it to put what a replacing copy is about to overwrite in the Trash.
func (s *Service) CopyEnds(from, to string) (src, dst string, err error) {
	if s.run != nil {
		return hosted3(s, func(s *Service) (string, string, error) { return s.CopyEnds(from, to) })
	}
	src, err = s.ResolveEntry(from)
	if err != nil {
		return "", "", err
	}
	dst, err = s.Resolve(to)
	if err != nil {
		return "", "", err
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	return src, dst, nil
}
