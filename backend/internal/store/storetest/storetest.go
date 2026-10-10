// Package storetest opens the dashboard's store for a test without building
// its schema again.
package storetest

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// The database a fresh store.Open leaves behind, built once per test binary.
var image = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "jd-store-image-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, store.DatabaseFile))
})

// Open is store.Open for a directory that holds no database yet. The schema is
// a couple of hundred statements, each its own transaction, and SQLite here is
// Go the race detector instruments: building it was nearly two seconds of
// every test that wanted a store, and most of what the race gate spent. A copy
// of a database that already has it opens as an existing install does, finding
// nothing to add.
func Open(dataDir string) (*store.Store, error) {
	built, err := image()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataDir, store.DatabaseFile), built, 0o600); err != nil {
		return nil, err
	}
	return store.Open(dataDir)
}
