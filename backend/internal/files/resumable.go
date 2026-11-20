package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

// An upload of any size arrives in pieces. Each piece is written at its
// offset into a staging file beside the destination (stagePrefix, never
// listed), and the whole is published by the same code the one-request
// upload uses (publishStaged). A dropped connection loses at most the piece
// in flight: the page asks where the upload stands and carries on. The
// session is kept on disk, root's alone, so a restart or a reloaded page that
// drops the same file again continues it; one left alone for a day is removed
// with its staged bytes.

const (
	// MaxPiece bounds one piece's request body.
	MaxPiece = 64 << 20
	// uploadMaxAge is how long an untouched upload is kept.
	uploadMaxAge = 24 * time.Hour
	// uploadsTotal and uploadsPerOwner bound the sessions held at once.
	uploadsTotal    = 512
	uploadsPerOwner = 64
)

var (
	// ErrUploadNotFound is an upload this owner does not have.
	ErrUploadNotFound = errors.New("no such upload: it finished, was cancelled or was removed after a day untouched")
	// ErrUploadIncomplete is a completion asked for before every byte arrived.
	ErrUploadIncomplete = errors.New("the upload is not complete yet")
	// ErrUploadLimit refuses a new session past the bounds.
	ErrUploadLimit = errors.New("too many uploads are in progress; finish or cancel some first")
	// ErrPiece is a piece that does not fit the upload.
	ErrPiece = errors.New("this piece does not fit the upload")
)

// UploadSession is one upload in pieces, as the page sees it and as it is
// kept on disk.
type UploadSession struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Dir       string `json:"dir"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Key       string `json:"key"`
	Overwrite bool   `json:"overwrite"`
	// Received is how many bytes are in; Next is where the first gap starts,
	// which is where a page carrying on sends from.
	Received int64      `json:"received"`
	Next     int64      `json:"next"`
	Ranges   [][2]int64 `json:"ranges"`
	Created  time.Time  `json:"created"`
	Updated  time.Time  `json:"updated"`

	Owner    string  `json:"owner,omitempty"`
	Expected *string `json:"expectedRevision,omitempty"`
	// What the staging file and its folder were when the upload began: a
	// piece goes nowhere else.
	Full      string `json:"full,omitempty"`
	Stage     string `json:"stage,omitempty"`
	StageDev  uint64 `json:"stageDev,omitempty"`
	StageIno  uint64 `json:"stageIno,omitempty"`
	ParentDev uint64 `json:"parentDev,omitempty"`
	ParentIno uint64 `json:"parentIno,omitempty"`
}

// public is the session without what only the server needs.
func (u UploadSession) public() UploadSession {
	u.Owner, u.Expected, u.Full, u.Stage = "", nil, "", ""
	u.StageDev, u.StageIno, u.ParentDev, u.ParentIno = 0, 0, 0, 0
	u.Ranges = append([][2]int64(nil), u.Ranges...)
	return u
}

// BeginUpload is what a page asks for to start, or carry on, an upload.
type BeginUpload struct {
	Dir       string  `json:"path"`
	Name      string  `json:"name"`
	Size      int64   `json:"size"`
	Key       string  `json:"key"`
	Overwrite bool    `json:"overwrite"`
	Expected  *string `json:"expectedRevision,omitempty"`
}

// Uploads holds the sessions. Its records live in stateDir (0700, one 0600
// file each); the bytes live beside their destinations.
type Uploads struct {
	svc      *Service
	stateDir string
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]*uploadEntry
}

type uploadEntry struct {
	mu sync.Mutex
	s  UploadSession
	// gone is set, under mu, once the session is finished or removed, for a
	// request that found it a moment before.
	gone bool
	// held is the staging file's folder as it was opened when the upload
	// began, kept so that removing the staged bytes follows the folder if it
	// is moved; a piece never does. Nil for a session read back after a
	// restart, which finds its folder by path again.
	held *os.Root
}

// NewUploads reads back the sessions a previous run left in stateDir, making
// the folder if it is not there.
func NewUploads(svc *Service, stateDir string) (*Uploads, error) {
	u := &Uploads{svc: svc, stateDir: stateDir, now: time.Now, sessions: map[string]*uploadEntry{}}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return u, err
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return u, err
	}
	names, err := os.ReadDir(stateDir)
	if err != nil {
		return u, err
	}
	for _, d := range names {
		if filepath.Ext(d.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(stateDir, d.Name()))
		if err != nil {
			continue
		}
		var s UploadSession
		if json.Unmarshal(b, &s) != nil || s.ID+".json" != d.Name() {
			continue
		}
		u.sessions[s.ID] = &uploadEntry{s: s}
	}
	return u, nil
}

func (u *Uploads) save(s *UploadSession) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(u.stateDir, ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(u.stateDir, s.ID+".json"))
}

// forget drops a session whose lock the caller holds.
func (u *Uploads) forget(e *uploadEntry) {
	e.gone = true
	if e.held != nil {
		e.held.Close()
		e.held = nil
	}
	u.mu.Lock()
	delete(u.sessions, e.s.ID)
	u.mu.Unlock()
	_ = os.Remove(filepath.Join(u.stateDir, e.s.ID+".json"))
}

// entry is the session with its lock held; the caller unlocks it. The lock
// order is u.mu before e.mu, and neither is taken while the other is held in
// the reverse order.
func (u *Uploads) entry(owner, id string) (*uploadEntry, error) {
	u.mu.Lock()
	e := u.sessions[id]
	u.mu.Unlock()
	if e == nil {
		return nil, ErrUploadNotFound
	}
	e.mu.Lock()
	if e.gone || e.s.Owner != owner {
		e.mu.Unlock()
		return nil, ErrUploadNotFound
	}
	return e, nil
}

// Begin starts an upload, or answers with the one this owner already has for
// the same file (same folder, name, size and key): a page reloaded and the
// same file dropped again carries on from where it stood.
func (u *Uploads) Begin(owner string, req BeginUpload) (UploadSession, error) {
	name, err := SafeName(req.Name)
	if err != nil {
		return UploadSession{}, err
	}
	if req.Dir == "" {
		return UploadSession{}, fmt.Errorf("%w: a folder is required", fs.ErrInvalid)
	}
	if req.Size < 0 {
		return UploadSession{}, fmt.Errorf("%w: a size cannot be negative", fs.ErrInvalid)
	}
	path := filepath.Join(req.Dir, name)
	full, err := u.svc.Resolve(path)
	if err != nil {
		return UploadSession{}, err
	}
	u.mu.Lock()
	count := 0
	var same *uploadEntry
	for _, e := range u.sessions {
		// Owner, Full, Size and Key never change after a session begins.
		if e.s.Owner != owner {
			continue
		}
		count++
		if req.Key != "" && e.s.Full == full && e.s.Size == req.Size && e.s.Key == req.Key {
			same = e
		}
	}
	crowded := count >= uploadsPerOwner || len(u.sessions) >= uploadsTotal
	u.mu.Unlock()
	if same != nil {
		if found, err := u.Get(owner, same.s.ID); err == nil {
			return found, nil
		}
	}
	if crowded {
		return UploadSession{}, ErrUploadLimit
	}

	s := UploadSession{
		Path: path, Dir: filepath.Dir(full), Name: filepath.Base(full), Size: req.Size, Key: req.Key,
		Overwrite: req.Overwrite, Owner: owner, Expected: req.Expected, Full: full,
		Ranges: [][2]int64{}, Created: u.now().UTC(),
	}
	s.Updated = s.Created
	held, err := u.svc.beginStage(&s)
	if err != nil {
		return UploadSession{}, err
	}
	if err := u.save(&s); err != nil {
		_ = u.svc.dropStage(&s, held)
		held.Close()
		return UploadSession{}, err
	}
	u.mu.Lock()
	u.sessions[s.ID] = &uploadEntry{s: s, held: held}
	u.mu.Unlock()
	return s.public(), nil
}

// Get is where an upload stands.
func (u *Uploads) Get(owner, id string) (UploadSession, error) {
	e, err := u.entry(owner, id)
	if err != nil {
		return UploadSession{}, err
	}
	defer e.mu.Unlock()
	return e.s.public(), nil
}

// List is this owner's uploads, oldest first.
func (u *Uploads) List(owner string) []UploadSession {
	u.mu.Lock()
	entries := make([]*uploadEntry, 0, len(u.sessions))
	for _, e := range u.sessions {
		entries = append(entries, e)
	}
	u.mu.Unlock()
	out := []UploadSession{}
	for _, e := range entries {
		e.mu.Lock()
		if !e.gone && e.s.Owner == owner {
			out = append(out, e.s.public())
		}
		e.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Put writes one piece at offset. A piece already received is written again
// with the same bytes, which is harmless; one past the end or larger than
// MaxPiece is refused. A body cut short keeps what arrived, so the next try
// starts at the first byte missing.
func (u *Uploads) Put(owner, id string, offset int64, body io.Reader) (UploadSession, error) {
	e, err := u.entry(owner, id)
	if err != nil {
		return UploadSession{}, err
	}
	defer e.mu.Unlock()
	if offset < 0 || offset > e.s.Size {
		return e.s.public(), fmt.Errorf("%w: offset %d is outside a file of %d bytes", ErrPiece, offset, e.s.Size)
	}
	limit := min(e.s.Size-offset, MaxPiece)
	n, writeErr := u.svc.writePiece(&e.s, offset, body, limit)
	if n > 0 {
		e.s.Ranges = addRange(e.s.Ranges, offset, offset+n)
	}
	e.s.Updated = u.now().UTC()
	e.s.Received, e.s.Next = covered(e.s.Ranges)
	if err := u.save(&e.s); err != nil && writeErr == nil {
		writeErr = err
	}
	return e.s.public(), writeErr
}

// Complete publishes a whole upload under its name. A refusal (the name was
// taken meanwhile, the folder moved, the revision changed) keeps the staged
// bytes, so the page can say why and the person can cancel or try again.
func (u *Uploads) Complete(owner, id string) (UploadSession, error) {
	e, err := u.entry(owner, id)
	if err != nil {
		return UploadSession{}, err
	}
	defer e.mu.Unlock()
	if received, _ := covered(e.s.Ranges); received != e.s.Size {
		return e.s.public(), fmt.Errorf("%w: %s of %s received", ErrUploadIncomplete, HumanBytes(received), HumanBytes(e.s.Size))
	}
	if err := u.svc.publishUpload(&e.s); err != nil {
		return e.s.public(), err
	}
	done := e.s.public()
	u.forget(e)
	return done, nil
}

// Cancel removes an upload and its staged bytes.
func (u *Uploads) Cancel(owner, id string) error {
	e, err := u.entry(owner, id)
	if err != nil {
		return err
	}
	defer e.mu.Unlock()
	err = u.svc.dropStage(&e.s, e.held)
	u.forget(e)
	return err
}

// Sweep removes uploads untouched for a day, with their staged bytes. It
// returns how many it removed.
func (u *Uploads) Sweep() int {
	cutoff := u.now().Add(-uploadMaxAge)
	u.mu.Lock()
	var stale []*uploadEntry
	for _, e := range u.sessions {
		stale = append(stale, e)
	}
	u.mu.Unlock()
	removed := 0
	for _, e := range stale {
		e.mu.Lock()
		if !e.gone && e.s.Updated.Before(cutoff) {
			_ = u.svc.dropStage(&e.s, e.held)
			u.forget(e)
			removed++
		}
		e.mu.Unlock()
	}
	return removed
}

// addRange folds [start, end) into sorted, merged ranges.
func addRange(ranges [][2]int64, start, end int64) [][2]int64 {
	out := make([][2]int64, 0, len(ranges)+1)
	placed := false
	for _, r := range ranges {
		switch {
		case r[1] < start:
			out = append(out, r)
		case end < r[0]:
			if !placed {
				out = append(out, [2]int64{start, end})
				placed = true
			}
			out = append(out, r)
		default:
			start, end = min(start, r[0]), max(end, r[1])
		}
	}
	if !placed {
		out = append(out, [2]int64{start, end})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// covered is the bytes the ranges hold, and where the first gap starts.
func covered(ranges [][2]int64) (received, next int64) {
	for _, r := range ranges {
		received += r[1] - r[0]
	}
	if len(ranges) > 0 && ranges[0][0] == 0 {
		next = ranges[0][1]
	}
	return received, next
}

// beginStage checks the destination as a one-request upload would, checks
// the disk has room for the whole file, and creates the staging file beside
// it.
func (s *Service) beginStage(u *UploadSession) (*os.Root, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (*os.Root, error) { return s.beginStage(u) })
	}
	t, err := s.openUploadTarget(u.Path, u.Overwrite, u.Expected)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	if t.full != u.Full {
		return nil, contentguard.ErrConflict
	}
	if err := checkRoom(t.directory, u.Size); err != nil {
		return nil, err
	}
	id, stage, err := newStageName()
	if err != nil {
		return nil, err
	}
	held, err := t.parent.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	f, err := held.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		held.Close()
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		_ = held.Remove(stage)
		held.Close()
		return nil, err
	}
	u.ID, u.Stage = id, stage
	u.StageDev, u.StageIno = devIno(st)
	u.ParentDev, u.ParentIno = devIno(t.parentInfo)
	return held, nil
}

func devIno(info os.FileInfo) (uint64, uint64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), st.Ino
	}
	return 0, 0
}

// openStage reaches an upload's staging file through its folder as it was
// when the upload began: the folder found again through the roots and
// checked to be the same one, and the file checked to be the one created,
// with no other name. A folder moved or swapped, or a staging name replaced
// by a link to something else, is a conflict, never a write somewhere else.
func (s *Service) openStage(u *UploadSession, flag int) (*os.Root, *os.File, error) {
	dir, err := s.Resolve(u.Dir)
	if err != nil {
		return nil, nil, err
	}
	if dir != u.Dir {
		return nil, nil, contentguard.ErrConflict
	}
	allowed, relative, err := s.openRootEntry(dir)
	if err != nil {
		return nil, nil, err
	}
	defer allowed.Close()
	parent, err := allowed.OpenRoot(relative)
	if err != nil {
		return nil, nil, err
	}
	info, err := parent.Stat(".")
	if err != nil {
		parent.Close()
		return nil, nil, err
	}
	if dev, ino := devIno(info); dev != u.ParentDev || ino != u.ParentIno {
		parent.Close()
		return nil, nil, contentguard.ErrConflict
	}
	f, err := parent.OpenFile(u.Stage, flag|syscall.O_NOFOLLOW, 0)
	if err != nil {
		parent.Close()
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		parent.Close()
		return nil, nil, err
	}
	sys, _ := st.Sys().(*syscall.Stat_t)
	if dev, ino := devIno(st); dev != u.StageDev || ino != u.StageIno || !st.Mode().IsRegular() || sys == nil || sys.Nlink != 1 {
		f.Close()
		parent.Close()
		return nil, nil, contentguard.ErrConflict
	}
	return parent, f, nil
}

// writePiece copies at most limit bytes from body to the staging file at
// offset, checking the disk's room for them first.
func (s *Service) writePiece(u *UploadSession, offset int64, body io.Reader, limit int64) (int64, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (int64, error) { return s.writePiece(u, offset, body, limit) })
	}
	parent, f, err := s.openStage(u, os.O_WRONLY)
	if err != nil {
		return 0, err
	}
	defer parent.Close()
	defer f.Close()
	if err := checkRoom(f, pieceGrowth(f, offset, limit)); err != nil {
		return 0, err
	}
	n, err := io.Copy(io.NewOffsetWriter(f, offset), io.LimitReader(body, limit))
	if err != nil {
		return n, err
	}
	// More than the piece may hold, or more than the file has left: refused,
	// with what fitted kept.
	var probe [1]byte
	if extra, _ := body.Read(probe[:]); extra > 0 {
		return n, fmt.Errorf("%w: a piece is at most %s and may not run past the file's end", ErrPiece, HumanBytes(MaxPiece))
	}
	return n, nil
}

// pieceGrowth is how much writing limit bytes at offset adds to the file.
func pieceGrowth(f *os.File, offset, limit int64) int64 {
	st, err := f.Stat()
	if err != nil {
		return limit
	}
	return max(offset+limit-st.Size(), 0)
}

// publishUpload gives a whole staging file its name.
func (s *Service) publishUpload(u *UploadSession) error {
	if s.run != nil {
		return hosted1(s, func(s *Service) error { return s.publishUpload(u) })
	}
	t, err := s.openUploadTarget(u.Path, u.Overwrite, u.Expected)
	if err != nil {
		return err
	}
	defer t.Close()
	if t.full != u.Full {
		return contentguard.ErrConflict
	}
	if dev, ino := devIno(t.parentInfo); dev != u.ParentDev || ino != u.ParentIno {
		return contentguard.ErrConflict
	}
	stage, err := t.parent.OpenFile(u.Stage, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	st, err := stage.Stat()
	if err != nil {
		stage.Close()
		return err
	}
	sys, _ := st.Sys().(*syscall.Stat_t)
	if dev, ino := devIno(st); dev != u.StageDev || ino != u.StageIno || sys == nil || sys.Nlink != 1 {
		stage.Close()
		return contentguard.ErrConflict
	}
	if st.Size() != u.Size {
		if err := stage.Truncate(u.Size); err != nil {
			stage.Close()
			return err
		}
	}
	if err := t.seal(stage); err != nil {
		return err
	}
	return s.publishStaged(t, u.Stage, u.Overwrite, u.Expected, nil)
}

// dropStage removes an upload's staging file, if it is still the one the
// upload created. It follows the folder held since the upload began, so the
// bytes go even when that folder was moved; read back after a restart, it
// finds the folder by path and leaves alone one that has moved.
func (s *Service) dropStage(u *UploadSession, held *os.Root) error {
	if s.run != nil {
		return hosted1(s, func(s *Service) error { return s.dropStage(u, held) })
	}
	if u.Stage == "" {
		return nil
	}
	if held != nil {
		f, err := held.OpenFile(u.Stage, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		st, err := f.Stat()
		f.Close()
		if err != nil {
			return err
		}
		if dev, ino := devIno(st); dev != u.StageDev || ino != u.StageIno {
			return nil
		}
		return held.Remove(u.Stage)
	}
	parent, f, err := s.openStage(u, os.O_RDONLY)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	f.Close()
	defer parent.Close()
	return parent.Remove(u.Stage)
}
