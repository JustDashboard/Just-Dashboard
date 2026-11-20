package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// A folder on the Files page follows the disk: the kernel says which names in
// it changed (inotify), and the page is sent those rows and nothing else.
// Where the kernel cannot say — a network or FUSE filesystem, /proc, the
// watch limit reached — the page is told to fall back on Fingerprint, a cheap
// check it polls, and to fetch the listing only when that changes.

var (
	// ErrWatchLimit refuses a watch past the per-session or total bound. The
	// page falls back on Fingerprint rather than failing.
	ErrWatchLimit = errors.New("too many folders are being followed already")
	// ErrWatchUnavailable is a folder the kernel cannot report changes in.
	ErrWatchUnavailable = errors.New("this folder's filesystem cannot report changes")
)

// Change is one coalesced batch for one followed folder: the entries whose
// names changed, as a listing would show them now, and the names that are no
// longer there. Resync asks for the whole listing once (too many names, or the
// kernel's queue overflowed); Gone says the folder itself was removed or moved.
type Change struct {
	Entries []Entry  `json:"entries"`
	Removed []string `json:"removed"`
	Resync  bool     `json:"resync,omitempty"`
	Gone    bool     `json:"gone,omitempty"`
}

const (
	watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
		unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF |
		unix.IN_MOVE_SELF | unix.IN_ONLYDIR | unix.IN_EXCL_UNLINK
	// DefaultWatchTotal and DefaultWatchPerOwner bound the folders followed at
	// once. A tab follows one folder, and an open editor one more.
	DefaultWatchTotal    = 256
	DefaultWatchPerOwner = 16
)

// Filesystems inotify says nothing useful about: changes made on another
// machine never reach this kernel, and pseudo-filesystems do not report.
var unwatchable = map[int64]string{
	0x6969:     "NFS",
	0xFF534D42: "CIFS",
	0xFE534D42: "SMB",
	0x517B:     "SMB",
	0x65735546: "FUSE",
	0x01021997: "9P",
	0x9fa0:     "proc",
	0x62656572: "sysfs",
	0x64626720: "debugfs",
	0x27e0eb:   "cgroup",
	0x63677270: "cgroup2",
}

// WatchHub holds one inotify instance for every followed folder, so the
// per-user instance limit (128 by default, shared with the rest of the
// server) is spent once. Two sessions following one folder share its watch.
type WatchHub struct {
	svc         *Service
	maxTotal    int
	maxPerOwner int
	// settle is how long a first change waits for its burst; window is the
	// least time between two batches to one page.
	settle   time.Duration
	window   time.Duration
	maxBatch int

	mu       sync.Mutex
	file     *os.File
	fd       int
	byWD     map[int32]*watchDir
	total    int
	perOwner map[string]int
}

type watchDir struct {
	wd   int32
	subs map[*Watch]struct{}
}

// NewWatchHub follows folders through svc, which for the Files page is the
// host's view: the watch is added inside the host's mount namespace and its
// descriptor is valid outside it.
func NewWatchHub(svc *Service, maxTotal, maxPerOwner int) *WatchHub {
	return &WatchHub{
		svc: svc, maxTotal: maxTotal, maxPerOwner: maxPerOwner,
		settle: 150 * time.Millisecond, window: 300 * time.Millisecond, maxBatch: 200,
		fd: -1, byWD: map[int32]*watchDir{}, perOwner: map[string]int{},
	}
}

// Active is how many watches are open, for tests and the shutdown log.
func (h *WatchHub) Active() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

// Watch is one page following one folder. C carries its batches; Close
// releases it.
type Watch struct {
	C      <-chan Change
	Path   string
	hub    *WatchHub
	dir    *watchDir
	owner  string
	hidden bool
	only   string

	out  chan Change
	kick chan struct{}
	done chan struct{}
	once sync.Once

	mu      sync.Mutex
	pending map[string]struct{}
	resync  bool
	gone    bool
}

// Watch follows the folder at path for owner (a session). Hidden says whether
// dotfiles are the page's business; only, when set, narrows the watch to one
// name in the folder (the file an editor has open).
func (h *WatchHub) Watch(path, owner string, hidden bool, only string) (*Watch, error) {
	if only != "" && (only != filepath.Base(only) || only == "." || only == "..") {
		return nil, fmt.Errorf("%w: a name, not a path", fs.ErrInvalid)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= h.maxTotal || h.perOwner[owner] >= h.maxPerOwner {
		return nil, ErrWatchLimit
	}
	if h.file == nil {
		fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrWatchUnavailable, err)
		}
		h.fd = fd
		h.file = os.NewFile(uintptr(fd), "inotify")
		go h.read(h.file)
	}
	resolved, wd, err := h.svc.addWatch(h.fd, path)
	if err != nil {
		h.closeIfIdle()
		return nil, err
	}
	dir := h.byWD[int32(wd)]
	if dir == nil {
		dir = &watchDir{wd: int32(wd), subs: map[*Watch]struct{}{}}
		h.byWD[dir.wd] = dir
	}
	out := make(chan Change, 1)
	w := &Watch{
		C: out, Path: resolved, hub: h, dir: dir, owner: owner, hidden: hidden, only: only,
		out: out, kick: make(chan struct{}, 1), done: make(chan struct{}),
		pending: map[string]struct{}{},
	}
	dir.subs[w] = struct{}{}
	h.total++
	h.perOwner[owner]++
	go w.loop()
	return w, nil
}

// Close releases the watch; the last one on a folder removes the kernel's
// watch, and the last one of all closes the inotify instance.
func (w *Watch) Close() {
	w.once.Do(func() {
		close(w.done)
		h := w.hub
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(w.dir.subs, w)
		if len(w.dir.subs) == 0 {
			if h.byWD[w.dir.wd] == w.dir {
				delete(h.byWD, w.dir.wd)
				if h.fd >= 0 {
					_, _ = unix.InotifyRmWatch(h.fd, uint32(w.dir.wd))
				}
			}
		}
		h.total--
		if h.perOwner[w.owner]--; h.perOwner[w.owner] <= 0 {
			delete(h.perOwner, w.owner)
		}
		h.closeIfIdle()
	})
}

// closeIfIdle runs with h.mu held.
func (h *WatchHub) closeIfIdle() {
	if h.total > 0 || h.file == nil {
		return
	}
	h.file.Close()
	h.file, h.fd = nil, -1
	h.byWD = map[int32]*watchDir{}
}

func (h *WatchHub) read(file *os.File) {
	buf := make([]byte, 64*1024)
	for {
		n, err := file.Read(buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			return
		}
		h.dispatch(file, buf[:n])
	}
}

func (h *WatchHub) dispatch(file *os.File, buf []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file != file {
		// An instance closed while this buffer was read; its watch numbers
		// mean nothing to the one that replaced it.
		return
	}
	for len(buf) >= unix.SizeofInotifyEvent {
		var ev unix.InotifyEvent
		ev.Wd = int32(binary.NativeEndian.Uint32(buf[0:4]))
		ev.Mask = binary.NativeEndian.Uint32(buf[4:8])
		ev.Len = binary.NativeEndian.Uint32(buf[12:16])
		end := unix.SizeofInotifyEvent + int(ev.Len)
		if end > len(buf) {
			return
		}
		name := string(bytes.TrimRight(buf[unix.SizeofInotifyEvent:end], "\x00"))
		buf = buf[end:]
		if ev.Mask&unix.IN_Q_OVERFLOW != 0 {
			for _, dir := range h.byWD {
				for w := range dir.subs {
					w.note("", true, false)
				}
			}
			continue
		}
		dir := h.byWD[ev.Wd]
		if dir == nil {
			continue
		}
		gone := ev.Mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_IGNORED|unix.IN_UNMOUNT) != 0
		if ev.Mask&unix.IN_IGNORED != 0 {
			// The kernel has already dropped this watch.
			delete(h.byWD, ev.Wd)
		}
		for w := range dir.subs {
			w.note(name, false, gone)
		}
	}
}

func (w *Watch) note(name string, resync, gone bool) {
	if !resync && !gone {
		if name == "" || IsStagingName(name) || (w.only != "" && name != w.only) ||
			(!w.hidden && strings.HasPrefix(name, ".")) {
			return
		}
	}
	w.mu.Lock()
	switch {
	case gone:
		w.gone = true
	case resync:
		w.resync = true
	default:
		w.pending[name] = struct{}{}
	}
	w.mu.Unlock()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// loop is the one goroutine that turns this watch's notes into batches, so
// batches leave in order and at most one every window.
func (w *Watch) loop() {
	defer close(w.out)
	var last time.Time
	for {
		select {
		case <-w.done:
			return
		case <-w.kick:
		}
		wait := w.hub.settle
		if since := time.Since(last); since+wait < w.hub.window {
			wait = w.hub.window - since
		}
		select {
		case <-w.done:
			return
		case <-time.After(wait):
		}
		w.mu.Lock()
		names := make([]string, 0, len(w.pending))
		for name := range w.pending {
			names = append(names, name)
		}
		w.pending = map[string]struct{}{}
		resync, gone := w.resync, w.gone
		w.resync = false
		w.mu.Unlock()
		var change Change
		switch {
		case gone:
			change = Change{Gone: true}
		case resync || len(names) > w.hub.maxBatch:
			change = Change{Resync: true}
		case len(names) == 0:
			continue
		default:
			sort.Strings(names)
			var err error
			change, err = w.hub.svc.entriesIn(w.Path, names)
			if err != nil {
				change = Change{Gone: true}
			}
		}
		select {
		case <-w.done:
			return
		case w.out <- change:
		}
		last = time.Now()
		if change.Gone {
			return
		}
	}
}

// addWatch resolves path through the roots and asks the kernel to watch it.
func (s *Service) addWatch(fd int, path string) (string, int, error) {
	if s.run != nil {
		var resolved string
		var wd int
		var err error
		s.run(func() { resolved, wd, err = s.inPlace().addWatch(fd, path) })
		return resolved, wd, err
	}
	dir, err := s.Resolve(path)
	if err != nil {
		return "", 0, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return "", 0, err
	}
	if !st.IsDir() {
		return "", 0, ErrNotDir
	}
	var sfs unix.Statfs_t
	if err := unix.Statfs(dir, &sfs); err == nil {
		if kind, ok := unwatchable[int64(sfs.Type)]; ok {
			return "", 0, fmt.Errorf("%w (%s)", ErrWatchUnavailable, kind)
		}
	}
	wd, err := unix.InotifyAddWatch(fd, dir, watchMask)
	if err != nil {
		if errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
			return "", 0, fmt.Errorf("%w: %v", ErrWatchUnavailable, err)
		}
		return "", 0, err
	}
	return dir, wd, nil
}

// entriesIn describes the named entries of dir as List would, and names the
// ones that are gone. dir must still resolve to itself: a folder swapped for
// a link since it was watched is reported as gone, not read through.
func (s *Service) entriesIn(dir string, names []string) (Change, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (Change, error) { return s.entriesIn(dir, names) })
	}
	resolved, err := s.Resolve(dir)
	if err != nil {
		return Change{}, err
	}
	if resolved != dir {
		return Change{}, fs.ErrNotExist
	}
	change := Change{Entries: []Entry{}, Removed: []string{}}
	owners := newEntryNames()
	for _, name := range names {
		e, err := s.entryWithNames(filepath.Join(dir, name), name, owners)
		if err != nil {
			change.Removed = append(change.Removed, name)
			continue
		}
		change.Entries = append(change.Entries, *e)
	}
	return change, nil
}

// Fingerprint is the fallback for a folder the kernel cannot watch: a digest
// of every name with its size, time, mode and owner, read without the owner
// name lookups or the JSON of a listing. The page polls it and reads the
// listing only when it changes.
func (s *Service) Fingerprint(path string, hidden bool) (string, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (string, error) { return s.Fingerprint(path, hidden) })
	}
	dir, err := s.Resolve(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	var scratch [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(scratch[:], v)
		sum.Write(scratch[:])
	}
	for _, d := range entries {
		name := d.Name()
		if IsStagingName(name) || (!hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		info, err := d.Info()
		if err != nil {
			continue
		}
		put(uint64(info.Size()))
		put(uint64(info.ModTime().UnixNano()))
		put(uint64(info.Mode()))
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			put(uint64(st.Uid)<<32 | uint64(st.Gid))
			put(uint64(st.Ctim.Sec)<<30 ^ uint64(st.Ctim.Nsec))
		}
	}
	return hex.EncodeToString(sum.Sum(nil)[:16]), nil
}

// IsStagingName is a name the dashboard stages a transfer under: never shown
// in a listing, a find or a search, never followed, and never published.
func IsStagingName(name string) bool {
	return strings.HasPrefix(name, ".jd-upload-") || strings.HasPrefix(name, stagePrefix)
}
