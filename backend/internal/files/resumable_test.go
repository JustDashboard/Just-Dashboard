package files

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func newTestUploads(t *testing.T, root string) *Uploads {
	t.Helper()
	u, err := NewUploads(New([]string{root}), filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func stages(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, stagePrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// cutReader hands over n bytes of data and then fails, as a dropped
// connection does halfway through a piece.
type cutReader struct {
	data []byte
	n    int
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, errors.New("connection reset")
	}
	k := copy(p, c.data[:min(c.n, len(c.data))])
	c.data, c.n = c.data[k:], c.n-k
	return k, nil
}

func TestUploadInPiecesResumesAtTheFirstMissingByte(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	content := bytes.Repeat([]byte("0123456789"), 1000)
	s, err := u.Begin("alice", BeginUpload{Dir: root, Name: "big.bin", Size: int64(len(content)), Key: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages(t, root)) != 1 {
		t.Fatal("no staging file beside the destination")
	}
	if _, err := os.Stat(filepath.Join(root, "big.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("an upload in progress is visible under its final name")
	}
	listing, _ := New([]string{root}).List(root, true)
	if len(listing.Entries) != 0 {
		t.Fatalf("the staging file is listed: %+v", listing.Entries)
	}

	// The connection drops 3,500 bytes into a 6,000-byte piece.
	after, err := u.Put("alice", s.ID, 0, &cutReader{data: content[:6000], n: 3500})
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("a cut piece returned %v", err)
	}
	if after.Next != 3500 || after.Received != 3500 {
		t.Fatalf("after the cut the upload stands at %d (%d received)", after.Next, after.Received)
	}
	// A reloaded page drops the same file again: the same upload answers.
	again, err := u.Begin("alice", BeginUpload{Dir: root, Name: "big.bin", Size: int64(len(content)), Key: "k1"})
	if err != nil || again.ID != s.ID || again.Next != 3500 {
		t.Fatalf("dropping the same file again began %+v, %v", again, err)
	}
	if _, err := u.Put("alice", s.ID, again.Next, bytes.NewReader(content[again.Next:])); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Begin("bob", BeginUpload{Dir: root, Name: "big.bin", Size: int64(len(content)), Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Complete("bob", s.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("another account completed alice's upload: %v", err)
	}
	done, err := u.Complete("alice", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Name != "big.bin" {
		t.Fatalf("completed %+v", done)
	}
	got, err := os.ReadFile(filepath.Join(root, "big.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("published %d bytes, %v", len(got), err)
	}
	if left := stages(t, root); len(left) != 1 {
		// bob's upload is still staged; alice's is gone.
		t.Fatalf("staging files left: %v", left)
	}
	if _, err := u.Get("alice", s.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatal("a finished upload is still held")
	}
}

func TestUploadPiecesOutOfOrderAndRepeated(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	content := []byte("abcdefghijklmnopqrst")
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "letters.txt", Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := u.Put("a", s.ID, 10, bytes.NewReader(content[10:])); st.Next != 0 || st.Received != 10 {
		t.Fatalf("a later piece first: %+v", st)
	}
	if _, err := u.Complete("a", s.ID); !errors.Is(err, ErrUploadIncomplete) {
		t.Fatalf("completing with a gap returned %v", err)
	}
	if st, _ := u.Put("a", s.ID, 0, bytes.NewReader(content[:10])); st.Next != 20 || st.Received != 20 {
		t.Fatalf("the gap filled: %+v", st)
	}
	// The same piece again, as a retry after a lost answer would send it.
	if st, err := u.Put("a", s.ID, 0, bytes.NewReader(content[:10])); err != nil || st.Received != 20 || len(st.Ranges) != 1 {
		t.Fatalf("a repeated piece: %+v %v", st, err)
	}
	if _, err := u.Put("a", s.ID, 15, bytes.NewReader([]byte("too long for the end"))); !errors.Is(err, ErrPiece) {
		t.Fatalf("a piece past the end returned %v", err)
	}
	if _, err := u.Put("a", s.ID, 15, bytes.NewReader(content[15:])); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Put("a", s.ID, 21, strings.NewReader("x")); !errors.Is(err, ErrPiece) {
		t.Fatalf("an offset past the end returned %v", err)
	}
	if _, err := u.Complete("a", s.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "letters.txt")); string(got) != string(content) {
		t.Fatalf("published %q", got)
	}
}

func TestUploadInPiecesClobbersOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	path := filepath.Join(root, "site.conf")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Begin("a", BeginUpload{Dir: root, Name: "site.conf", Size: 3}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("an occupied name without overwrite began: %v", err)
	}
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "site.conf", Size: 3, Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	u.Put("a", s.ID, 0, strings.NewReader("new"))
	if _, err := u.Complete("a", s.ID); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if got, _ := os.ReadFile(path); string(got) != "new" || info.Mode().Perm() != 0o640 {
		t.Fatalf("replacement %q mode %v", got, info.Mode().Perm())
	}

	// A name claimed while the upload was arriving is not clobbered: the
	// completion is refused and the staged bytes kept for a decision.
	s, err = u.Begin("a", BeginUpload{Dir: root, Name: "fresh.txt", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	u.Put("a", s.ID, 0, strings.NewReader("mine"))
	os.WriteFile(filepath.Join(root, "fresh.txt"), []byte("theirs"), 0o644)
	if _, err := u.Complete("a", s.ID); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a name taken meanwhile returned %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "fresh.txt")); string(got) != "theirs" {
		t.Fatal("the other writer's file was replaced")
	}
	if len(stages(t, root)) != 1 {
		t.Fatal("the refused upload's bytes were not kept")
	}
	if err := u.Cancel("a", s.ID); err != nil {
		t.Fatal(err)
	}
	if len(stages(t, root)) != 0 {
		t.Fatal("cancel left the staged bytes")
	}
}

func TestUploadInPiecesRefusesAStaleRevision(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	path := filepath.Join(root, "photo.png")
	os.WriteFile(path, []byte("opened"), 0o644)
	stale := contentguard.Revision("something else")
	if _, err := u.Begin("a", BeginUpload{Dir: root, Name: "photo.png", Size: 5, Overwrite: true, Expected: &stale}); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("a stale revision began: %v", err)
	}
	opened := contentguard.Revision("opened")
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "photo.png", Size: 5, Overwrite: true, Expected: &opened})
	if err != nil {
		t.Fatal(err)
	}
	u.Put("a", s.ID, 0, strings.NewReader("saved"))
	os.WriteFile(path, []byte("native edit"), 0o644)
	if _, err := u.Complete("a", s.ID); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("a native edit during the upload returned %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "native edit" {
		t.Fatal("the native edit was replaced")
	}
}

func TestUploadInPiecesRefusesWhatTheDiskCannotHold(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	free := uint64(1 << 30)
	restore := diskRoom
	diskRoom = func(*os.File) (uint64, uint64, error) { return free, 10 << 30, nil }
	t.Cleanup(func() { diskRoom = restore })
	// 10 GiB disk: the reserve is 2 per cent, 204.8 MiB.
	if _, err := u.Begin("a", BeginUpload{Dir: root, Name: "huge.img", Size: 900 << 20}); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("an upload past the room left began: %v", err)
	} else if !strings.Contains(err.Error(), "can be used") {
		t.Fatalf("the refusal does not say how much room there is: %v", err)
	}
	if len(stages(t, root)) != 0 {
		t.Fatal("a refused upload left a staging file")
	}
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "fits.img", Size: 16})
	if err != nil {
		t.Fatal(err)
	}
	// Something else fills the disk while the upload is under way.
	free = 100 << 20
	if _, err := u.Put("a", s.ID, 0, strings.NewReader("0123456789abcdef")); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("a piece into a full disk returned %v", err)
	}
	free = 1 << 30
	if _, err := u.Put("a", s.ID, 0, strings.NewReader("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
}

func TestAnAbandonedUploadIsRemovedAfterADayAndSurvivesARestart(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "uploads")
	svc := New([]string{root})
	u, err := NewUploads(svc, state)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return clock }
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "left.bin", Size: 8, Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	u.Put("a", s.ID, 0, strings.NewReader("half"))
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state folder %v %v", info, err)
	}
	record, err := os.Stat(filepath.Join(state, s.ID+".json"))
	if err != nil || record.Mode().Perm() != 0o600 {
		t.Fatalf("record %v %v", record, err)
	}

	// The dashboard restarts: the upload is read back and carries on.
	again, err := NewUploads(svc, state)
	if err != nil {
		t.Fatal(err)
	}
	again.now = func() time.Time { return clock }
	resumed, err := again.Begin("a", BeginUpload{Dir: root, Name: "left.bin", Size: 8, Key: "k"})
	if err != nil || resumed.ID != s.ID || resumed.Next != 4 {
		t.Fatalf("after a restart: %+v %v", resumed, err)
	}
	clock = clock.Add(23 * time.Hour)
	if again.Sweep() != 0 || len(stages(t, root)) != 1 {
		t.Fatal("an upload under a day old was removed")
	}
	clock = clock.Add(2 * time.Hour)
	if again.Sweep() != 1 {
		t.Fatal("an upload a day untouched was kept")
	}
	if len(stages(t, root)) != 0 {
		t.Fatal("the abandoned staging file was left")
	}
	if _, err := os.Stat(filepath.Join(state, s.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the abandoned record was left")
	}
}

// The cases upload_parent_test.go holds the one-request upload to, for an
// upload in pieces: the folder moved and its name taken by a link out of the
// roots, a link to another folder inside them, or a new folder, between two
// pieces. No piece and no publication follows the name; nothing native is
// touched; and cancelling removes the staged bytes from the folder that moved.
func TestUploadInPiecesStaysWithItsFolder(t *testing.T) {
	for _, change := range []string{"outside-symlink", "inside-symlink", "replacement-directory"} {
		for _, step := range []string{"piece", "complete"} {
			t.Run(change+"/"+step, func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				parent := filepath.Join(root, "images")
				moved := filepath.Join(root, "moved-images")
				os.Mkdir(parent, 0o700)
				path := filepath.Join(parent, "image.png")
				os.WriteFile(path, []byte("original"), 0o600)
				u := newTestUploads(t, root)
				s, err := u.Begin("a", BeginUpload{Dir: parent, Name: "image.png", Size: 8, Overwrite: true})
				if err != nil {
					t.Fatal(err)
				}
				first := "new"
				if step == "complete" {
					first = "new imag"
				}
				if _, err := u.Put("a", s.ID, 0, strings.NewReader(first)); err != nil {
					t.Fatal(err)
				}
				stage := stages(t, parent)[0]

				if err := os.Rename(parent, moved); err != nil {
					t.Fatal(err)
				}
				replacement := outside
				switch change {
				case "inside-symlink":
					replacement = filepath.Join(root, "replacement")
					os.Mkdir(replacement, 0o700)
				case "replacement-directory":
					replacement = parent
					os.Mkdir(parent, 0o700)
				}
				if change != "replacement-directory" {
					os.Symlink(replacement, parent)
				}
				os.WriteFile(filepath.Join(replacement, "image.png"), []byte("original"), 0o644)
				shadow := filepath.Join(replacement, filepath.Base(stage))
				os.WriteFile(shadow, []byte("unrelated native file"), 0o644)

				if step == "piece" {
					_, err = u.Put("a", s.ID, 3, strings.NewReader(" ima"))
				} else {
					_, err = u.Complete("a", s.ID)
				}
				if change == "outside-symlink" {
					if !errors.Is(err, ErrOutsideRoot) {
						t.Fatalf("outward retarget returned %v", err)
					}
				} else if !errors.Is(err, contentguard.ErrConflict) {
					t.Fatalf("native parent replacement returned %v", err)
				}
				if got, _ := os.ReadFile(shadow); string(got) != "unrelated native file" {
					t.Fatalf("the shadow file was written: %q", got)
				}
				for _, image := range []string{filepath.Join(moved, "image.png"), filepath.Join(replacement, "image.png")} {
					if got, _ := os.ReadFile(image); string(got) != "original" {
						t.Fatalf("an image was replaced: %s %q", image, got)
					}
				}
				if err := u.Cancel("a", s.ID); err != nil {
					t.Fatal(err)
				}
				if left := stages(t, moved); len(left) != 0 {
					t.Fatalf("staging left in the moved folder: %v", left)
				}
				if _, err := os.Stat(shadow); err != nil {
					t.Fatal("cancel removed the unrelated shadow file")
				}
			})
		}
	}
}

func TestAStagingNameSwappedForALinkIsNotWrittenThrough(t *testing.T) {
	root := t.TempDir()
	u := newTestUploads(t, root)
	victim := filepath.Join(root, "victim")
	os.WriteFile(victim, []byte("keep me"), 0o644)
	s, err := u.Begin("a", BeginUpload{Dir: root, Name: "x.bin", Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	stage := stages(t, root)[0]
	os.Remove(stage)
	if err := os.Link(victim, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Put("a", s.ID, 0, strings.NewReader("evil")); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("a hard-linked stage returned %v", err)
	}
	os.Remove(stage)
	os.Symlink(victim, stage)
	if _, err := u.Put("a", s.ID, 0, strings.NewReader("evil")); err == nil {
		t.Fatal("a symlinked stage was written through")
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep me" {
		t.Fatalf("the victim was written: %q", got)
	}
}

func TestSafeNameDropsFoldersAndRefusesTheUnsafe(t *testing.T) {
	for raw, want := range map[string]string{
		"report.pdf": "report.pdf", "a/b/c.txt": "c.txt", `C:\Users\x\d.zip`: "d.zip", "../../etc/passwd": "passwd",
	} {
		if got, err := SafeName(raw); err != nil || got != want {
			t.Errorf("SafeName(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", ".", "..", "dir/", "a\x00b", "bad\nname", ".jd-part-123", strings.Repeat("x", 300)} {
		if got, err := SafeName(raw); err == nil {
			t.Errorf("SafeName(%q) accepted %q", raw, got)
		}
	}
	_ = io.EOF
}
