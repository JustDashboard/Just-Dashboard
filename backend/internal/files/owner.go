package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The backend runs as root, so every entry the Files page made used to be
// root's: a folder created in /home/ubuntu/app that ubuntu could not then
// write into, and that git — which the Git page runs as the checkout's owner —
// could neither clean nor pull into. A new entry belongs instead to whoever
// owns the folder it is made in, which is what the same verb typed in that
// account's shell would have left. A folder root owns goes on getting root's
// entries, a replaced file keeps its own owner as before, and a symbolic link
// stays root's: who owns a link decides nothing about what goes through it,
// and the folder's owner can remove it.

// heir is the account a new entry in a folder with this info belongs to, or
// -1s when that is this process itself.
func heir(folder os.FileInfo) (uid, gid int) {
	st, ok := folder.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 || os.Geteuid() != 0 {
		return -1, -1
	}
	return int(st.Uid), int(st.Gid)
}

// unmade is path and each folder above it that does not exist yet, deepest
// first: what creating path is about to make.
func unmade(path string) []string {
	var out []string
	for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		out = append(out, p)
	}
	return out
}

// handDown gives the folders and files made under base to base's owner.
//
// base is a folder its owner can write, so a name in it could be swapped for
// a link between the creation and the chown; hostexec.GiveBack therefore
// changes each entry through a descriptor reached without following one.
// Deepest first, so a folder stays root's, and closed to its owner, until
// what is in it has been handed over. A failed extraction removes the file it
// was writing, so an entry already gone is skipped.
func handDown(base string, made []string) error {
	owner, ok := hostexec.OwnerOf(base)
	if !ok || len(made) == 0 {
		return nil
	}
	var rels []string
	for _, path := range made {
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
	}
	slices.SortStableFunc(rels, func(a, b string) int {
		return strings.Count(b, string(filepath.Separator)) - strings.Count(a, string(filepath.Separator))
	})
	return hostexec.GiveBack(base, owner, rels)
}

// madeBase is the existing folder that entries made for path land under.
func madeBase(path string, made []string) string {
	if len(made) == 0 {
		return path
	}
	return filepath.Dir(made[len(made)-1])
}
