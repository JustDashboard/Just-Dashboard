package dockerx

import (
	"errors"
	"math"
	"os"
	"syscall"
)

const (
	adoptionImageArchiveLimit = int64(2 << 30)
	adoptionImageDiskCushion  = uint64(256 << 20)
)

var errAdoptionImageSpace = errors.New("private image recovery storage is too low; free space before retrying (256 MiB is kept available)")

func adoptionImageArchiveBudget(available uint64) (int64, error) {
	if available <= adoptionImageDiskCushion {
		return 0, errAdoptionImageSpace
	}
	// Retain room for a second copy if Docker imports onto this filesystem.
	budget := (available - adoptionImageDiskCushion) / 2
	if budget > uint64(adoptionImageArchiveLimit) {
		budget = uint64(adoptionImageArchiveLimit)
	}
	if budget == 0 {
		return 0, errAdoptionImageSpace
	}
	return int64(budget), nil
}

func adoptionFileAvailable(file *os.File) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(file.Fd()), &stat); err != nil || stat.Bsize <= 0 {
		return 0, errors.New("private image recovery storage capacity could not be verified")
	}
	blockSize := uint64(stat.Bsize)
	if stat.Bavail > math.MaxUint64/blockSize {
		return math.MaxUint64, nil
	}
	return stat.Bavail * blockSize, nil
}

type adoptionArchiveWriter struct {
	file      *os.File
	budget    int64
	written   int64
	available func(*os.File) (uint64, error)
}

func newAdoptionArchiveWriter(file *os.File) (*adoptionArchiveWriter, error) {
	available, err := adoptionFileAvailable(file)
	if err != nil {
		return nil, err
	}
	budget, err := adoptionImageArchiveBudget(available)
	if err != nil {
		return nil, err
	}
	return &adoptionArchiveWriter{file: file, budget: budget, available: adoptionFileAvailable}, nil
}

func (w *adoptionArchiveWriter) Write(content []byte) (int, error) {
	if int64(len(content)) > w.budget-w.written {
		if w.budget < adoptionImageArchiveLimit {
			return 0, errAdoptionImageSpace
		}
		return 0, errors.New("original filesystem exceeds the image recovery storage budget")
	}
	available, err := w.available(w.file)
	if err != nil {
		return 0, err
	}
	if available < adoptionImageDiskCushion+uint64(w.written)+2*uint64(len(content)) {
		return 0, errAdoptionImageSpace
	}
	written, err := w.file.Write(content)
	w.written += int64(written)
	return written, err
}

func checkAdoptionImageImportSpace(file *os.File, size int64) error {
	if size < 0 || size > adoptionImageArchiveLimit {
		return errors.New("original filesystem exceeds the safe image recovery bounds")
	}
	available, err := adoptionFileAvailable(file)
	if err != nil {
		return err
	}
	if available < adoptionImageDiskCushion+uint64(size) {
		return errAdoptionImageSpace
	}
	return nil
}
