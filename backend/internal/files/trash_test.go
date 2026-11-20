package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// trashFixture is a root of files and a data directory beside it, both on the
// test's temporary filesystem, so the data directory's Trash is the one used.
func trashFixture(t *testing.T, keepDays int) (*Service, *Trash, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	data := filepath.Join(base, "data")
	for _, dir := range []string{root, data} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := New([]string{root})
	return svc, NewTrash(svc, data, keepDays), root, data
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrashMovesAnEntryAsideAndRestoresItWhereItWas(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	file := filepath.Join(root, "site", "nginx.conf")
	writeFile(t, file, "server {}\n", 0o640)
	folder := filepath.Join(root, "site", "logs")
	writeFile(t, filepath.Join(folder, "access.log"), "GET /\n", 0o600)

	item, err := trash.Put(file, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the trashed file is still where it was: %v", err)
	}
	bin := filepath.Join(data, TrashFolderName)
	if item.Folder != bin {
		t.Fatalf("trashed into %s, want the data directory's %s", item.Folder, bin)
	}
	held := filepath.Join(bin, item.ID, "nginx.conf")
	if readBody(t, held) != "server {}\n" {
		t.Fatal("the Trash does not hold the file's contents")
	}
	for path, want := range map[string]os.FileMode{bin: 0o700, filepath.Join(bin, item.ID): 0o700, filepath.Join(bin, item.ID+".json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s is %o, want %o: the Trash is root's alone", path, info.Mode().Perm(), want)
		}
	}
	if item.OriginalPath != file || item.DeletedBy != "ada" || item.ModeOctal != "0640" || !item.SizeKnown || item.Size != 10 {
		t.Fatalf("the item does not say what it was: %+v", item)
	}
	if item.ExpiresAt == nil || item.ExpiresAt.Sub(item.DeletedAt) != 30*24*time.Hour {
		t.Fatalf("the item does not say when it goes: %+v", item.ExpiresAt)
	}

	dirItem, err := trash.Put(folder, "ada")
	if err != nil {
		t.Fatal(err)
	}
	listing, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 2 || listing.KeepDays != 30 {
		t.Fatalf("listing: %+v", listing)
	}
	if !listing.Items[0].IsDir && !listing.Items[1].IsDir {
		t.Fatal("the trashed folder is not listed as a folder")
	}

	restored, err := trash.Restore(item.ID, RestoreAsk, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Path != file || readBody(t, file) != "server {}\n" {
		t.Fatalf("restored to %s", restored.Path)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o640 {
		t.Fatalf("the restored file's mode is %o", info.Mode().Perm())
	}
	if _, err := os.Lstat(filepath.Join(bin, item.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a restored item left its holder behind")
	}
	if _, err := os.Lstat(filepath.Join(bin, item.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a restored item left its record behind")
	}
	if _, err := trash.Restore(dirItem.ID, RestoreAsk, "ada"); err != nil {
		t.Fatal(err)
	}
	if readBody(t, filepath.Join(folder, "access.log")) != "GET /\n" {
		t.Fatal("the folder came back without its contents")
	}
}

func TestTrashListingIsReadFromDiskAndSurvivesALostRecord(t *testing.T) {
	_, trash, root, data := trashFixture(t, 0)
	file := filepath.Join(root, "notes.txt")
	writeFile(t, file, "x", 0o644)
	item, err := trash.Put(file, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if item.ExpiresAt != nil {
		t.Fatal("an item kept until emptied has an expiry")
	}
	// A second Trash over the same folders, as after a restart: nothing is
	// remembered in memory.
	again := NewTrash(New([]string{root}), data, 0)
	listing, err := again.List()
	if err != nil || len(listing.Items) != 1 || listing.Items[0].OriginalPath != file {
		t.Fatalf("a fresh Trash does not see what is on disk: %+v %v", listing, err)
	}
	os.Remove(filepath.Join(data, TrashFolderName, item.ID+".json"))
	listing, _ = again.List()
	if len(listing.Items) != 1 || listing.Items[0].Name != "notes.txt" || listing.Items[0].OriginalPath != "" {
		t.Fatalf("an item whose record was lost is not listed by what it is: %+v", listing.Items)
	}
	if _, err := again.Restore(item.ID, RestoreAsk, ""); err == nil {
		t.Fatal("an item with nowhere recorded was restored somewhere")
	}
	if _, err := again.Remove(item.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTrashRestoreAsksWhenThePlaceIsTaken(t *testing.T) {
	_, trash, root, _ := trashFixture(t, 30)
	file := filepath.Join(root, "app.env")
	writeFile(t, file, "old", 0o600)
	item, err := trash.Put(file, "ada")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, "new", 0o644)

	if _, err := trash.Restore(item.ID, RestoreAsk, "ada"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("restoring over a new file: %v, want ErrExist", err)
	}
	if readBody(t, file) != "new" {
		t.Fatal("a refused restore changed the file that was there")
	}
	kept, err := trash.Restore(item.ID, RestoreKeep, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "app (2).env" || readBody(t, kept.Path) != "old" || readBody(t, file) != "new" {
		t.Fatalf("keep both: %+v", kept)
	}

	second, err := trash.Put(kept.Path, "ada")
	if err != nil {
		t.Fatal(err)
	}
	// Its record says it came from "app (2).env"; put something there and
	// replace it.
	writeFile(t, kept.Path, "newer", 0o644)
	replaced, err := trash.Restore(second.ID, RestoreReplace, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if readBody(t, kept.Path) != "old" || replaced.Replaced == nil || !replaced.Replaced.Replaced {
		t.Fatalf("replace: %+v", replaced)
	}
	listing, _ := trash.List()
	if len(listing.Items) != 1 || listing.Items[0].ID != replaced.Replaced.ID {
		t.Fatalf("what a restore replaced is not in the Trash: %+v", listing.Items)
	}
}

func TestTrashRestoreMakesAVanishedFolderAgain(t *testing.T) {
	_, trash, root, _ := trashFixture(t, 30)
	file := filepath.Join(root, "srv", "app", "config", "settings.yml")
	writeFile(t, file, "a: 1\n", 0o644)
	item, err := trash.Put(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "srv", "app")); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Restore(item.ID, RestoreAsk, ""); err != nil {
		t.Fatal(err)
	}
	if readBody(t, file) != "a: 1\n" {
		t.Fatal("not restored into the folder made again")
	}
	if info, _ := os.Stat(filepath.Dir(file)); !info.IsDir() || info.Mode().Perm() != 0o755 {
		t.Fatalf("the folder made again is %v", info.Mode())
	}
}

func TestTrashKeepsASymlinkAsTheLinkItIs(t *testing.T) {
	_, trash, root, _ := trashFixture(t, 30)
	target := filepath.Join(root, "releases", "v1")
	writeFile(t, filepath.Join(target, "index.html"), "v1", 0o644)
	link := filepath.Join(root, "current")
	if err := os.Symlink("releases/v1", link); err != nil {
		t.Fatal(err)
	}
	item, err := trash.Put(link, "")
	if err != nil {
		t.Fatal(err)
	}
	if readBody(t, filepath.Join(target, "index.html")) != "v1" {
		t.Fatal("trashing a link took what it points at")
	}
	if !item.IsSymlink || item.IsDir || item.LinkTarget != "releases/v1" {
		t.Fatalf("the Trash does not hold the link: %+v", item)
	}
	if _, err := trash.Restore(item.ID, RestoreAsk, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != "releases/v1" {
		t.Fatalf("restored link points at %q", got)
	}
}

func TestTrashKeepsOwnerAndMode(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own a file as another account")
	}
	_, trash, root, data := trashFixture(t, 30)
	if err := os.Chmod(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "home", "deploy", "key.pem")
	writeFile(t, file, "secret", 0o600)
	if err := os.Chown(file, 4242, 4343); err != nil {
		t.Fatal(err)
	}
	item, err := trash.Put(file, "root")
	if err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(item.Folder, item.ID, "key.pem")
	assertOwner := func(path string) {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != 4242 || st.Gid != 4343 || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s is %d:%d %o", path, st.Uid, st.Gid, info.Mode().Perm())
		}
	}
	assertOwner(held)
	// The vanished parent comes back owned as its folder's heir would be.
	if err := os.Chown(filepath.Join(root, "home"), 4242, 4343); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "home", "deploy")); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Restore(item.ID, RestoreAsk, "root"); err != nil {
		t.Fatal(err)
	}
	assertOwner(file)
	info, _ := os.Stat(filepath.Dir(file))
	if st := info.Sys().(*syscall.Stat_t); st.Uid != 4242 {
		t.Fatalf("the folder made again belongs to %d, not the folder above's owner", st.Uid)
	}
}

func TestTrashStaysInsideTheRoots(t *testing.T) {
	svc, trash, root, data := trashFixture(t, 30)
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	writeFile(t, outside, "x", 0o644)
	if _, err := trash.Put(outside, ""); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("trashing outside the roots: %v", err)
	}
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(filepath.Dir(root), escape); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Put(filepath.Join(escape, "outside.txt"), ""); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("trashing through a link out of the roots: %v", err)
	}
	for _, id := range []string{"../outside.txt", "..", "", "0123456789abcdef0123456789abcdeF", "0123456789abcdef0123456789abcdef"} {
		if _, err := trash.Restore(id, RestoreAsk, ""); !errors.Is(err, ErrNotInTrash) {
			t.Errorf("restoring %q: %v, want ErrNotInTrash", id, err)
		}
		if _, err := trash.Remove(id); !errors.Is(err, ErrNotInTrash) {
			t.Errorf("removing %q: %v, want ErrNotInTrash", id, err)
		}
	}
	// A record that says it came from outside the roots is not followed there.
	file := filepath.Join(root, "a.txt")
	writeFile(t, file, "x", 0o644)
	item, err := trash.Put(file, "")
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(data, TrashFolderName, item.ID+".json")
	var record trashRecord
	raw, _ := os.ReadFile(recordPath)
	json.Unmarshal(raw, &record)
	record.Path = outside + ".restored"
	bin, err := os.OpenRoot(filepath.Join(data, TrashFolderName))
	if err != nil {
		t.Fatal(err)
	}
	defer bin.Close()
	if err := writeTrashRecord(bin, record); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Restore(item.ID, RestoreAsk, ""); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("restoring to a recorded path outside the roots: %v", err)
	}
	if _, err := os.Lstat(outside + ".restored"); err == nil {
		t.Fatal("a restore wrote outside the roots")
	}
	_ = svc
}

func TestTrashRefusesWhatCannotGoInIt(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	if _, err := trash.Put(root, ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing a root: %v", err)
	}
	inBin := filepath.Join(root, TrashFolderName, "0123456789abcdef0123456789abcdef", "x")
	writeFile(t, inBin, "x", 0o644)
	if _, err := trash.Put(inBin, ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing something in a Trash folder: %v", err)
	}
	if _, err := trash.Put(filepath.Join(root, TrashFolderName), ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing a Trash folder: %v", err)
	}

	// A Trash whose data directory is inside the roots refuses the folders
	// that hold it.
	inner := New([]string{filepath.Dir(root)})
	own := NewTrash(inner, data, 30)
	if _, err := own.Put(data, ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing the data directory: %v", err)
	}

	file := filepath.Join(root, "mnt", "disk", "file")
	writeFile(t, file, "x", 0o644)
	fake := func(mounts ...mountPoint) {
		previous := readMountTable
		readMountTable = func() ([]mountPoint, error) { return mounts, nil }
		t.Cleanup(func() { readMountTable = previous })
	}
	fake(mountPoint{ID: 1, Path: "/", FSType: "ext4"},
		mountPoint{ID: 2, Path: filepath.Join(root, "mnt", "disk"), FSType: "ext4"})
	plan, err := trash.Check(filepath.Join(root, "mnt", "disk"))
	if err != nil || plan.Available || !strings.Contains(plan.Reason, "mounted") {
		t.Fatalf("a mount point: %+v %v", plan, err)
	}
	if _, err := trash.Put(filepath.Join(root, "mnt", "disk"), ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing a mount point: %v", err)
	}
	fake(mountPoint{ID: 1, Path: "/", FSType: "ext4"},
		mountPoint{ID: 2, Path: filepath.Join(root, "mnt"), FSType: "ext4", ReadOnly: true})
	if plan, _ := trash.Check(file); plan.Available || !strings.Contains(plan.Reason, "read-only") {
		t.Fatalf("a read-only filesystem: %+v", plan)
	}
	fake(mountPoint{ID: 1, Path: "/", FSType: "ext4"},
		mountPoint{ID: 2, Path: filepath.Join(root, "mnt"), FSType: "sysfs"})
	if plan, _ := trash.Check(file); plan.Available || !strings.Contains(plan.Reason, "sysfs") {
		t.Fatalf("a pseudo filesystem: %+v", plan)
	}
	if _, err := trash.Put(file, ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing on a pseudo filesystem: %v", err)
	}
	if readBody(t, file) != "x" {
		t.Fatal("a refused trash touched the file")
	}
	// A filesystem whose top already holds something of the Trash's name
	// that is not the dashboard's Trash folder.
	fake(mountPoint{ID: 1, Path: "/", FSType: "ext4"},
		mountPoint{ID: 2, Path: filepath.Join(root, "mnt"), FSType: "ext4"})
	writeFile(t, filepath.Join(root, "mnt", TrashFolderName), "not a folder", 0o644)
	if _, err := trash.Put(file, ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing into a file named like the Trash: %v", err)
	}
	if readBody(t, file) != "x" {
		t.Fatal("a refused trash touched the file")
	}
}

func TestTrashExpiresWhatIsKeptTooLong(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	clock := time.Now()
	trash.now = func() time.Time { return clock }
	old := filepath.Join(root, "old.txt")
	fresh := filepath.Join(root, "fresh.txt")
	writeFile(t, old, "x", 0o644)
	writeFile(t, fresh, "y", 0o644)
	oldItem, err := trash.Put(old, "")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(20 * 24 * time.Hour)
	freshItem, err := trash.Put(fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(11 * 24 * time.Hour)
	gone, err := trash.Expire()
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0].ID != oldItem.ID || gone[0].Path != old {
		t.Fatalf("expired: %+v", gone)
	}
	listing, _ := trash.List()
	if len(listing.Items) != 1 || listing.Items[0].ID != freshItem.ID {
		t.Fatalf("left after expiry: %+v", listing.Items)
	}

	forever := NewTrash(New([]string{root}), data, 0)
	forever.now = func() time.Time { return clock.Add(10 * 365 * 24 * time.Hour) }
	if gone, _ := forever.Expire(); len(gone) != 0 {
		t.Fatalf("a Trash kept until emptied expired %+v", gone)
	}
	removed, _, err := forever.Empty()
	if err != nil || removed != 1 {
		t.Fatalf("empty: %d %v", removed, err)
	}
}

func TestTrashKeepsAFileAReplacingCopyOverwrites(t *testing.T) {
	svc, trash, root, _ := trashFixture(t, 30)
	src := filepath.Join(root, "new.conf")
	dst := filepath.Join(root, "live.conf")
	writeFile(t, src, "new", 0o644)
	writeFile(t, dst, "old", 0o600)
	item, err := trash.PutReplaced(dst, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if readBody(t, dst) != "old" {
		t.Fatal("keeping a file for a copy moved it away first")
	}
	if err := svc.Copy(src, dst, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dst); readBody(t, dst) != "new" || info.Mode().Perm() != 0o600 {
		t.Fatalf("the copy did not replace the file in place: %o", info.Mode().Perm())
	}
	if readBody(t, filepath.Join(item.Folder, item.ID, "live.conf")) != "old" {
		t.Fatal("the Trash did not keep the replaced bytes")
	}
	if !item.Replaced {
		t.Fatal("the item does not say a replacement put it there")
	}
	// A failed operation discards it, leaving what it kept where it was.
	again, err := trash.PutReplaced(dst, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := trash.Discard(again.ID); err != nil {
		t.Fatal(err)
	}
	if readBody(t, dst) != "new" {
		t.Fatal("discarding the kept link touched the file")
	}
}

func TestTrashFoldersAreLeftOutOfFindSizesAndArchives(t *testing.T) {
	svc, _, root, _ := trashFixture(t, 30)
	writeFile(t, filepath.Join(root, "app", "needle.txt"), "visible", 0o644)
	writeFile(t, filepath.Join(root, TrashFolderName, "0123456789abcdef0123456789abcdef", "needle-trashed.txt"), "trashed bytes", 0o644)

	found, err := svc.Find(context.Background(), FindOptions{Root: root, Query: "needle", Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range found.Hits {
		if strings.Contains(hit.Path, TrashFolderName) {
			t.Fatalf("find returned the Trash: %s", hit.Path)
		}
	}
	if len(found.Hits) != 1 {
		t.Fatalf("find: %+v", found.Hits)
	}
	if hits, _ := svc.Search(context.Background(), SearchOptions{Root: root, Query: "needle", MaxDepth: 12, Limit: 50}); len(hits) != 1 {
		t.Fatalf("search reached the Trash: %+v", hits)
	}
	usage, err := svc.Usage(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bytes != int64(len("visible")) {
		t.Fatalf("the folder's size counts the Trash: %d", usage.Bytes)
	}
	for _, e := range usage.Largest {
		if e.Name == TrashFolderName {
			t.Fatal("the Trash is among the folder's largest entries")
		}
	}
	writeFile(t, filepath.Join(root, "app", TrashFolderName, "0123456789abcdef0123456789abcdef", "inner.txt"), "x", 0o644)
	base, members, err := svc.ResolveArchive(root, []string{filepath.Join(root, "app")})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	f, _ := os.Create(out)
	if err := svc.Compress(f, base, members, FormatTarGz); err != nil {
		t.Fatal(err)
	}
	f.Close()
	listing, err := exec.Command("tar", "-tzf", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listing), TrashFolderName) || !strings.Contains(string(listing), "needle.txt") {
		t.Fatalf("archive of the parent:\n%s", listing)
	}
}

// Trashing on a second filesystem uses that filesystem's own Trash folder at
// its top: a rename cannot leave its filesystem, and a copy is never made.
func TestTrashOnAnotherMountUsesThatMountsTrash(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to mount a tmpfs")
	}
	_, trash, root, data := trashFixture(t, 30)
	if err := os.Chmod(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(root, "scratch")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount("tmpfs", mnt, "tmpfs", 0, "size=8m"); err != nil {
		t.Skipf("cannot mount a tmpfs here: %v", err)
	}
	t.Cleanup(func() { syscall.Unmount(mnt, syscall.MNT_DETACH) })
	file := filepath.Join(mnt, "build", "out.bin")
	writeFile(t, file, "bytes", 0o644)

	if plan, err := trash.Check(mnt); err != nil || plan.Available {
		t.Fatalf("the mount point itself: %+v %v", plan, err)
	}
	item, err := trash.Put(file, "root")
	if err != nil {
		t.Fatal(err)
	}
	if item.Folder != filepath.Join(mnt, TrashFolderName) {
		t.Fatalf("trashed into %s, want the mount's own Trash", item.Folder)
	}
	listing, _ := trash.List()
	if len(listing.Items) != 1 || listing.Items[0].Folder != item.Folder {
		t.Fatalf("the mount's Trash is not listed: %+v", listing.Items)
	}
	if _, err := trash.Restore(item.ID, RestoreAsk, "root"); err != nil {
		t.Fatal(err)
	}
	if readBody(t, file) != "bytes" {
		t.Fatal("not restored")
	}
	// Read-only: the page is told why, and nothing moves.
	if err := syscall.Mount("", mnt, "", syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
		t.Skipf("cannot remount read-only: %v", err)
	}
	if _, err := trash.Put(file, "root"); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing on a read-only mount: %v", err)
	}
}

func TestMountInfoLinesAreRead(t *testing.T) {
	m, ok := parseMountInfoLine(`36 35 98:0 /mnt1 /srv/my\040disk ro,noatime master:1 - ext4 /dev/sdb1 rw,errors=continue`)
	if !ok || m.Path != "/srv/my disk" || m.FSType != "ext4" || !m.ReadOnly || m.ID != 36 {
		t.Fatalf("%+v", m)
	}
	mounts := []mountPoint{{ID: 1, Path: "/"}, {ID: 2, Path: "/srv"}, {ID: 3, Path: "/srv/data"}, {ID: 4, Path: "/srv"}}
	if got, _ := mountOf(mounts, "/srv/database/x"); got.ID != 4 {
		t.Fatalf("/srv/database is on %+v, want the mount over /srv", got)
	}
	if got, _ := mountOf(mounts, "/srv/data/x"); got.ID != 3 {
		t.Fatalf("/srv/data/x is on %+v", got)
	}
}

// plantedTrash is what an account that owns the top of a mount could put
// where the dashboard's Trash folder is expected: a holder with an item in it
// and a record saying the item came from target, deleted long ago.
func plantedTrash(t *testing.T, dir, id, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, id), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, id, "payload"), "planted", 0o644)
	record, _ := json.Marshal(trashRecord{
		ID: id, Name: "payload", Path: target, DeletedAt: time.Now().Add(-400 * 24 * time.Hour),
	})
	if err := os.WriteFile(filepath.Join(dir, id+".json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

// nothingFromPlanted runs every Trash operation and checks that none of them
// read, restored or removed anything of the planted folder.
func nothingFromPlanted(t *testing.T, trash *Trash, planted, id, target string) {
	t.Helper()
	listing, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listing.Items {
		if item.ID == id || item.OriginalPath == target {
			t.Fatalf("the listing shows the planted item: %+v", item)
		}
	}
	if _, err := trash.Restore(id, RestoreAsk, ""); !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("restoring the planted id: %v, want ErrNotInTrash", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Fatal("a planted record moved a file to the path it named")
	}
	if _, err := trash.Remove(id); !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("removing the planted id: %v, want ErrNotInTrash", err)
	}
	trash.now = func() time.Time { return time.Now().Add(1000 * 24 * time.Hour) }
	defer func() { trash.now = time.Now }()
	if _, err := trash.Expire(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := trash.Empty(); err != nil {
		t.Fatal(err)
	}
	if readBody(t, filepath.Join(planted, id, "payload")) != "planted" {
		t.Fatal("Expire or Empty removed what was in the planted folder")
	}
	if _, err := os.Lstat(filepath.Join(planted, id+".json")); err != nil {
		t.Fatal("Expire or Empty removed the planted record")
	}
}

// A Trash folder swapped, after it was made, for a link to a folder holding a
// crafted record and item is not a Trash folder: nothing in it is listed,
// restored or removed, and nothing is put in it.
func TestATrashFolderSwappedForALinkIsNotUsed(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	writeFile(t, filepath.Join(root, "first.txt"), "x", 0o644)
	if _, err := trash.Put(filepath.Join(root, "first.txt"), ""); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(data, TrashFolderName)
	planted := filepath.Join(t.TempDir(), "planted")
	const id = "0123456789abcdef0123456789abcdef"
	target := filepath.Join(root, "etc", "sudoers.d", "planted")
	plantedTrash(t, planted, id, target)
	if err := os.Rename(bin, bin+".aside"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, bin); err != nil {
		t.Fatal(err)
	}
	nothingFromPlanted(t, trash, planted, id, target)
	writeFile(t, filepath.Join(root, "second.txt"), "y", 0o644)
	if _, err := trash.Put(filepath.Join(root, "second.txt"), ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing into a linked Trash folder: %v", err)
	}
	if readBody(t, filepath.Join(root, "second.txt")) != "y" {
		t.Fatal("a refused trash moved the file")
	}
	entries, _ := os.ReadDir(planted)
	if len(entries) != 2 {
		t.Fatalf("something was put in the planted folder: %v", entries)
	}
}

// The same swap made between the moment the folder is checked and the moment
// it is used, by a hook rather than by timing: the operation goes on in the
// folder it checked, wherever that folder now is, and never in the one put in
// its place.
func TestATrashFolderSwappedAfterTheCheckIsNotUsed(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	bin := filepath.Join(data, TrashFolderName)
	original := filepath.Join(root, "site", "nginx.conf")
	writeFile(t, original, "server {}\n", 0o644)
	item, err := trash.Put(original, "")
	if err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(t.TempDir(), "planted")
	target := filepath.Join(root, "etc", "sudoers.d", "planted")
	// The planted folder holds a record under the real item's id, naming
	// another destination.
	plantedTrash(t, planted, item.ID, target)
	swaps := 0
	swap := func(folder string) {
		if folder != bin || swaps > 0 {
			return
		}
		swaps++
		if err := os.Rename(bin, bin+".aside"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(planted, bin); err != nil {
			t.Fatal(err)
		}
	}
	unswap := func() {
		os.Remove(bin)
		if err := os.Rename(bin+".aside", bin); err != nil {
			t.Fatal(err)
		}
		swaps = 0
	}

	trash.afterPin = swap
	restored, err := trash.Restore(item.ID, RestoreAsk, "")
	if err != nil {
		t.Fatal(err)
	}
	if swaps != 1 {
		t.Fatal("the hook did not swap the folder")
	}
	if restored.Path != original || readBody(t, original) != "server {}\n" {
		t.Fatalf("restored %+v from somewhere other than the folder it checked", restored)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Fatal("the planted record was followed")
	}
	if readBody(t, filepath.Join(planted, item.ID, "payload")) != "planted" {
		t.Fatal("the planted item was moved")
	}
	trash.afterPin = nil
	unswap()

	// A put swapped the same way lands in the folder that was checked.
	trash.afterPin = swap
	moved, err := trash.Put(original, "")
	if err != nil {
		t.Fatal(err)
	}
	if readBody(t, filepath.Join(bin+".aside", moved.ID, "nginx.conf")) != "server {}\n" {
		t.Fatal("the put did not land in the folder it checked")
	}
	if _, err := os.Lstat(filepath.Join(planted, moved.ID)); err == nil {
		t.Fatal("the put wrote into the planted folder")
	}
	trash.afterPin = nil
	unswap()

	// Empty, swapped the same way, empties the folder it checked and leaves
	// the planted one alone.
	trash.afterPin = swap
	if removed, _, err := trash.Empty(); err != nil || removed != 1 {
		t.Fatalf("empty: %d %v", removed, err)
	}
	if readBody(t, filepath.Join(planted, item.ID, "payload")) != "planted" {
		t.Fatal("Empty removed the planted item")
	}
	trash.afterPin = nil
}

// A Trash folder the dashboard owns but somebody opened to other accounts
// may hold what they put there while it was open: it is not used until it is
// closed again by hand.
func TestAnOpenedTrashFolderIsNotTrusted(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	writeFile(t, filepath.Join(root, "first.txt"), "x", 0o644)
	if _, err := trash.Put(filepath.Join(root, "first.txt"), ""); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(data, TrashFolderName)
	const id = "0123456789abcdef0123456789abcdef"
	target := filepath.Join(root, "etc", "sudoers.d", "planted")
	plantedTrash(t, bin, id, target)
	if err := os.Chmod(bin, 0o777); err != nil {
		t.Fatal(err)
	}
	nothingFromPlanted(t, trash, bin, id, target)
	writeFile(t, filepath.Join(root, "second.txt"), "y", 0o644)
	if _, err := trash.Put(filepath.Join(root, "second.txt"), ""); !errors.Is(err, ErrTrashUnavailable) {
		t.Fatalf("trashing into an opened Trash folder: %v", err)
	}
	plan, err := trash.Check(filepath.Join(root, "second.txt"))
	if err != nil || plan.Available || !strings.Contains(plan.Reason, "open to other accounts") {
		t.Fatalf("check: %+v %v", plan, err)
	}
}

// A folder of the Trash's name that another account owns is not the
// dashboard's: refused for a put, and never read.
func TestATrashFolderAnotherAccountOwnsIsNotUsed(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to make a folder another account owns")
	}
	_, trash, root, data := trashFixture(t, 30)
	if err := os.Chmod(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(data, TrashFolderName)
	const id = "0123456789abcdef0123456789abcdef"
	target := filepath.Join(root, "etc", "sudoers.d", "planted")
	plantedTrash(t, bin, id, target)
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{bin, filepath.Join(bin, id), filepath.Join(bin, id, "payload"), filepath.Join(bin, id+".json")} {
		if err := os.Lchown(p, 4242, 4242); err != nil {
			t.Fatal(err)
		}
	}
	nothingFromPlanted(t, trash, bin, id, target)
	writeFile(t, filepath.Join(root, "file.txt"), "y", 0o644)
	if _, err := trash.Put(filepath.Join(root, "file.txt"), ""); !errors.Is(err, ErrTrashUnavailable) ||
		!strings.Contains(err.Error(), "another account") {
		t.Fatalf("trashing into another account's folder: %v", err)
	}
}

func TestFreeNameStopsLookingAfterItsBound(t *testing.T) {
	dir := t.TempDir()
	if name, err := freeName(dir, "a.txt"); err != nil || name != "a (2).txt" {
		t.Fatalf("%q %v", name, err)
	}
	for n := 2; n <= freeNameLimit; n++ {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("a (%d).txt", n)))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if _, err := freeName(dir, "a.txt"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("every name taken: %v", err)
	}
}

// The same race without a seam of the Trash's own: the folder is swapped from
// inside the mount table read, which comes after a by-path check of the data
// directory's Trash folder and before its use. A record planted under the
// real item's id must never be followed.
func TestATrashFolderSwappedDuringALookupIsNotFollowed(t *testing.T) {
	_, trash, root, data := trashFixture(t, 30)
	bin := filepath.Join(data, TrashFolderName)
	original := filepath.Join(root, "site", "nginx.conf")
	writeFile(t, original, "server {}\n", 0o644)
	item, err := trash.Put(original, "")
	if err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(t.TempDir(), "planted")
	target := filepath.Join(root, "etc", "sudoers.d", "planted")
	plantedTrash(t, planted, item.ID, target)
	previous := readMountTable
	t.Cleanup(func() { readMountTable = previous })
	swapped := false
	readMountTable = func() ([]mountPoint, error) {
		if !swapped {
			swapped = true
			if err := os.Rename(bin, bin+".aside"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(planted, bin); err != nil {
				t.Fatal(err)
			}
		}
		return previous()
	}
	restored, err := trash.Restore(item.ID, RestoreAsk, "")
	if !swapped {
		t.Fatal("the swap did not happen")
	}
	if _, statErr := os.Lstat(target); statErr == nil {
		t.Fatalf("the planted record was followed: restored %+v", restored)
	}
	if readBody(t, filepath.Join(planted, item.ID, "payload")) != "planted" {
		t.Fatal("the planted item was moved")
	}
	if err != nil && !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("restore: %v", err)
	}
}
