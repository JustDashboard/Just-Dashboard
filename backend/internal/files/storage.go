package files

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	storageMaxEntries   = 200_000
	storageMaxDepth     = 64
	storageMaxFiles     = 2048
	storageHashBudget   = 256 << 20
	storageMinDuplicate = 1 << 20
	storageMaxDuplicate = 128 << 20
	storageTempAge      = 7 * 24 * time.Hour
)

// StorageFile keeps a fingerprint of the measured inode. Cleanup can refuse a
// stale selection instead of deleting whatever later occupies the same name.
type StorageFile struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Allocated int64     `json:"allocated"`
	Modified  time.Time `json:"modified"`
	Identity  string    `json:"identity"`
	Links     uint64    `json:"links"`
}

type StorageDirectory struct {
	Path      string `json:"path"`
	Allocated int64  `json:"allocated"`
	Entries   int    `json:"entries"`
}

type StorageDuplicate struct {
	SHA256      string        `json:"sha256"`
	Files       []StorageFile `json:"files"`
	Reclaimable int64         `json:"reclaimable"`
}

type StorageSilence struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type StorageFilesystem struct {
	Total       uint64 `json:"total"`
	Available   uint64 `json:"available"`
	FreeInodes  uint64 `json:"freeInodes"`
	TotalInodes uint64 `json:"totalInodes"`
}

type StorageReport struct {
	RequestedPath          string             `json:"requestedPath,omitempty"`
	HostFilesystem         bool               `json:"hostFilesystem,omitempty"`
	Path                   string             `json:"path"`
	CheckedAt              time.Time          `json:"checkedAt"`
	Complete               bool               `json:"complete"`
	Entries                int                `json:"entries"`
	Allocated              int64              `json:"allocated"`
	Directories            []StorageDirectory `json:"directories"`
	InodeDirectories       []StorageDirectory `json:"inodeDirectories"`
	LargeFiles             []StorageFile      `json:"largeFiles"`
	TemporaryFiles         []StorageFile      `json:"temporaryFiles"`
	Duplicates             []StorageDuplicate `json:"duplicates"`
	Silences               []StorageSilence   `json:"silences"`
	SkippedMounts          int                `json:"skippedMounts"`
	HashedBytes            int64              `json:"hashedBytes"`
	DuplicateScanComplete  bool               `json:"duplicateScanComplete"`
	DuplicateScanRequested bool               `json:"duplicateScanRequested"`
	DuplicateScope         string             `json:"duplicateScope"`
	Filesystem             *StorageFilesystem `json:"filesystem,omitempty"`
}

type storageScan struct {
	hostRoot    string
	root        *os.Root
	report      *StorageReport
	dev         uint64
	mounts      map[string]bool
	seen        map[[2]uint64]bool
	files       storageFileHeap
	directories []StorageDirectory
	maxEntries  int
}

// Anchor the directory under a configured root before following its children.
// Resolving a pathname alone cannot prevent a parent symlink swap afterwards.
func (s *Service) openStorageDirectory(path string) (*os.Root, error) {
	allowed, rel, err := s.openRootEntry(path)
	if err != nil {
		return nil, err
	}
	defer allowed.Close()
	return allowed.OpenRoot(rel)
}

// ScanStorage measures allocated blocks, not apparent lengths. Sparse files,
// hard links and other mounts must not look like reclaimable space on this disk.
// Traversal uses os.Root so a directory swapped for a symlink cannot escape the
// validated root; links and special files are never read or followed.
func (s *Service) ScanStorage(ctx context.Context, path string, duplicates bool) (*StorageReport, error) {
	select {
	case s.storageSlots <- struct{}{}:
		defer func() { <-s.storageSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resolved, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	if s.storageHostErr != nil {
		return nil, s.storageHostErr
	}
	requested := resolved
	resolved, err = s.storagePath(resolved)
	if err != nil {
		return nil, err
	}
	root, err := s.openStorageDirectory(resolved)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var fs syscall.Statfs_t
	if err := syscall.Fstatfs(int(dir.Fd()), &fs); err != nil {
		return nil, err
	}
	if virtualStorageFS(int64(fs.Type)) {
		return nil, errors.New("scan a data filesystem; virtual kernel filesystems cannot be cleaned")
	}
	mounts, mountErr := storageMounts()
	report, err := s.scanStorage(ctx, root, resolved, duplicates, storageMaxEntries, mounts)
	if report != nil && mountErr != nil {
		report.Complete = false
		report.Silences = append(report.Silences, StorageSilence{Path: resolved, Reason: "mount boundaries are unavailable; same-device bind mounts may overlap"})
	}
	if report != nil {
		report.RequestedPath = requested
		report.HostFilesystem = s.storageHostRoot != ""
		if statErr := syscall.Fstatfs(int(dir.Fd()), &fs); statErr == nil {
			report.Filesystem = &StorageFilesystem{Total: fs.Blocks * uint64(fs.Bsize), Available: fs.Bavail * uint64(fs.Bsize), FreeInodes: fs.Ffree, TotalInodes: fs.Files}
		}
	}
	return report, err
}

func virtualStorageFS(kind int64) bool {
	// Kernel pseudo-files can report as regular entries but do not represent
	// disk allocation, and reading them for duplicate detection has side effects.
	switch kind {
	case 0x9fa0, 0x62656572, 0x64626720, 0x1cd1, 0x27e0eb, 0x63677270, 0x73636673, 0x74726163, 0xcafe4a11, 0x65735543:
		return true
	}
	return false
}

func (s *Service) scanStorage(ctx context.Context, root *os.Root, path string, duplicates bool, maxEntries int, mounts map[string]bool) (*StorageReport, error) {
	info, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("filesystem allocation information is unavailable")
	}
	report := &StorageReport{Path: path, CheckedAt: time.Now().UTC(), Complete: true,
		Directories: []StorageDirectory{}, InodeDirectories: []StorageDirectory{}, LargeFiles: []StorageFile{}, TemporaryFiles: []StorageFile{},
		Duplicates: []StorageDuplicate{}, Silences: []StorageSilence{}, DuplicateScanComplete: true,
		DuplicateScanRequested: duplicates, DuplicateScope: StorageDuplicateLimits()}
	scan := &storageScan{root: root, hostRoot: s.storageHostRoot, report: report, dev: uint64(st.Dev),
		mounts: mounts, seen: map[[2]uint64]bool{}, maxEntries: maxEntries}
	_, _, err = scan.walk(ctx, ".", 0)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		scan.silence(path, err.Error())
	}
	sort.Slice(scan.directories, func(i, j int) bool { return scan.directories[i].Entries > scan.directories[j].Entries })
	report.InodeDirectories = append(report.InodeDirectories, scan.directories[:min(len(scan.directories), 20)]...)
	sort.Slice(scan.directories, func(i, j int) bool { return scan.directories[i].Allocated > scan.directories[j].Allocated })
	if len(scan.directories) > 30 {
		scan.directories = scan.directories[:30]
	}
	report.Directories = scan.directories
	sort.Slice(scan.files, func(i, j int) bool {
		if scan.files[i].Allocated != scan.files[j].Allocated {
			return scan.files[i].Allocated > scan.files[j].Allocated
		}
		return scan.files[i].Path < scan.files[j].Path
	})
	report.LargeFiles = append(report.LargeFiles, scan.files[:min(len(scan.files), 30)]...)
	sort.Slice(report.TemporaryFiles, func(i, j int) bool { return report.TemporaryFiles[i].Allocated > report.TemporaryFiles[j].Allocated })
	if duplicates && ctx.Err() == nil {
		scan.findDuplicates(ctx)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	if ctx.Err() != nil {
		report.DuplicateScanComplete = false
	}
	if !report.Complete {
		report.DuplicateScanComplete = false
	}
	return report, nil
}

func (s *storageScan) silence(path, reason string) {
	s.report.Complete = false
	if len(s.report.Silences) < 20 {
		s.report.Silences = append(s.report.Silences, StorageSilence{Path: path, Reason: reason})
	}
}

func (s *storageScan) walk(ctx context.Context, rel string, depth int) (total int64, count int, scanErr error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if s.report.Entries >= s.maxEntries {
		return 0, 0, errors.New("entry limit reached; scan a smaller directory")
	}
	full := filepath.Join(s.report.Path, rel)
	if depth > storageMaxDepth {
		s.silence(full, "directory depth limit reached")
		return 0, 0, nil
	}
	info, err := s.root.Lstat(rel)
	if err != nil {
		s.silence(full, "could not read this entry")
		return 0, 0, nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		s.silence(full, "allocation information unavailable")
		return 0, 0, nil
	}
	if rel != "." && (uint64(st.Dev) != s.dev || s.mounts[full]) {
		s.report.SkippedMounts++
		return 0, 0, nil
	}
	s.report.Entries++
	if !info.Mode().IsRegular() && !info.IsDir() {
		return 0, 1, nil
	}
	key := [2]uint64{uint64(st.Dev), st.Ino}
	allocated := st.Blocks * 512
	if s.seen[key] {
		return 0, 1, nil
	}
	s.seen[key] = true
	s.report.Allocated += allocated
	if info.Mode().IsRegular() {
		file := storageFile(full, info)
		if len(s.files) < storageMaxFiles {
			heap.Push(&s.files, file)
		} else {
			// Bounded evidence keeps a root scan from allocating one object per
			// file. A later pass reports that duplicate coverage is incomplete.
			s.report.DuplicateScanComplete = false
			if file.Allocated > s.files[0].Allocated {
				s.files[0] = file
				heap.Fix(&s.files, 0)
			}
		}
		if file.Links == 1 && storageTemporaryPath(full, s.hostRoot) && time.Since(info.ModTime()) >= storageTempAge && len(s.report.TemporaryFiles) < 100 {
			s.report.TemporaryFiles = append(s.report.TemporaryFiles, file)
		}
		return allocated, 1, nil
	}
	dir, err := s.root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		s.silence(full, "could not open this directory")
		return allocated, 1, nil
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(info, opened) {
		s.silence(full, "directory changed during the scan")
		return allocated, 1, nil
	}
	total, count = allocated, 1
	defer func() {
		if rel == "." {
			return
		}
		s.directories = append(s.directories, StorageDirectory{Path: full, Allocated: total, Entries: count})
		if len(s.directories) > 1000 {
			byEntries := append([]StorageDirectory{}, s.directories...)
			sort.Slice(byEntries, func(i, j int) bool { return byEntries[i].Entries > byEntries[j].Entries })
			sort.Slice(s.directories, func(i, j int) bool { return s.directories[i].Allocated > s.directories[j].Allocated })
			keep := map[string]bool{}
			best := append([]StorageDirectory{}, s.directories[:100]...)
			for _, dir := range best {
				keep[dir.Path] = true
			}
			for _, dir := range byEntries[:100] {
				if !keep[dir.Path] {
					best = append(best, dir)
				}
			}
			s.directories = best
		}
	}()
	for {
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			size, n, err := s.walk(ctx, filepath.Join(rel, entry.Name()), depth+1)
			total += size
			count += n
			if err != nil {
				return total, count, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			s.silence(full, "could not read all directory entries")
			break
		}
	}
	return total, count, nil
}

type storageFileHeap []StorageFile

func (h storageFileHeap) Len() int           { return len(h) }
func (h storageFileHeap) Less(i, j int) bool { return h[i].Allocated < h[j].Allocated }
func (h storageFileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *storageFileHeap) Push(v any)        { *h = append(*h, v.(StorageFile)) }
func (h *storageFileHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func storageFile(path string, info os.FileInfo) StorageFile {
	st := info.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d", st.Dev, st.Ino, info.Size(), info.ModTime().UnixNano(), st.Ctim.Sec, st.Ctim.Nsec, info.Mode())
	sum := sha256.Sum256([]byte(identity))
	return StorageFile{Path: path, Size: info.Size(), Allocated: st.Blocks * 512, Modified: info.ModTime().UTC(), Identity: hex.EncodeToString(sum[:]), Links: uint64(st.Nlink)}
}

func temporaryPath(path string) bool {
	return strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/var/tmp/")
}

func storageMounts() (map[string]bool, error) {
	out := map[string]bool{}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		path := fields[4]
		for _, pair := range [][2]string{{"\\040", " "}, {"\\011", "\t"}, {"\\012", "\n"}, {"\\134", "\\"}} {
			path = strings.ReplaceAll(path, pair[0], pair[1])
		}
		out[filepath.Clean(path)] = true
	}
	return out, nil
}

func (s *storageScan) findDuplicates(ctx context.Context) {
	bySize := map[int64][]StorageFile{}
	for _, file := range s.files {
		if file.Links == 1 && file.Size >= storageMinDuplicate && file.Size <= storageMaxDuplicate {
			bySize[file.Size] = append(bySize[file.Size], file)
		}
	}
	sizes := make([]int64, 0, len(bySize))
	for size, files := range bySize {
		if len(files) > 1 {
			sizes = append(sizes, size)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })
	for _, size := range sizes {
		byHash := map[string][]StorageFile{}
		for _, file := range bySize[size] {
			if s.report.HashedBytes+file.Size > storageHashBudget {
				s.report.DuplicateScanComplete = false
				continue
			}
			rel, err := filepath.Rel(s.report.Path, file.Path)
			if err != nil {
				continue
			}
			s.report.HashedBytes += file.Size
			hash, err := hashStorageFile(ctx, s.root, rel, file)
			if err != nil {
				s.report.DuplicateScanComplete = false
				continue
			}
			byHash[hash] = append(byHash[hash], file)
		}
		for hash, files := range byHash {
			if len(files) < 2 {
				continue
			}
			sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
			group := StorageDuplicate{SHA256: hash, Files: files}
			for _, file := range files[1:] {
				group.Reclaimable += file.Allocated
			}
			s.report.Duplicates = append(s.report.Duplicates, group)
		}
	}
	sort.Slice(s.report.Duplicates, func(i, j int) bool {
		if s.report.Duplicates[i].Reclaimable != s.report.Duplicates[j].Reclaimable {
			return s.report.Duplicates[i].Reclaimable > s.report.Duplicates[j].Reclaimable
		}
		return s.report.Duplicates[i].SHA256 < s.report.Duplicates[j].SHA256
	})
}

func hashStorageFile(ctx context.Context, root *os.Root, rel string, expected StorageFile) (string, error) {
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || storageFile(expected.Path, info).Identity != expected.Identity {
		return "", errors.New("file changed")
	}
	hash := sha256.New()
	buf := make([]byte, 64<<10)
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		read += int64(n)
		if read > expected.Size {
			return "", errors.New("file grew while hashing")
		}
		_, _ = hash.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if read != expected.Size || storageFile(expected.Path, after).Identity != expected.Identity {
		return "", errors.New("file changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Keep the bounds visible to callers without accepting client-selected scan
// budgets: a read route must not become an unbounded disk workload.
func StorageDuplicateLimits() string {
	return "Exact copies from up to " + strconv.Itoa(storageMaxFiles) + " measured files, 1–128 MiB each; up to 256 MiB read per scan."
}
