package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// This is a point-in-time guard, not proof that an old file is unneeded. A
// process can open it afterwards, and a mapped file may no longer have an fd.
// The operator still reviews and confirms each selected path.
func storageNotOpen(ctx context.Context, info os.FileInfo, procRoot string) error {
	processes, err := os.ReadDir(procRoot)
	if err != nil {
		return errors.New("cannot check open files on this host; review in Files")
	}
	checked := 0
	for _, process := range processes {
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		fdsPath := filepath.Join(procRoot, process.Name(), "fd")
		fds, err := os.ReadDir(fdsPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot check open files for PID %s; review in Files", process.Name())
		}
		for _, fd := range fds {
			checked++
			if checked > 100_000 {
				return errors.New("open-file check reached its limit; review in Files")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			openInfo, err := os.Stat(filepath.Join(fdsPath, fd.Name()))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("cannot inspect a file descriptor for PID %s; review in Files", process.Name())
			}
			if os.SameFile(info, openInfo) {
				return fmt.Errorf("file is open by PID %s; inspect or stop its owner first", process.Name())
			}
		}
	}
	return nil
}
