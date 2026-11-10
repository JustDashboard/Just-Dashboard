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
	st, err := store.Open(dataDir)
	if err == nil && os.Getenv(oneConnectionEnv) == "1" {
		OneConnection(st)
	}
	return st, err
}

// oneConnectionEnv runs every store a test opens on a single connection. It
// is how to hunt through a package's whole suite for the mistake OneConnection
// describes: `JD_STORE_ONE_CONNECTION=1 go test -timeout 5m ./internal/deploy`
// hangs in the test that makes it, and the timeout's stack names the line.
const oneConnectionEnv = "JD_STORE_ONE_CONNECTION"

// OneConnection leaves the store a single connection, so that a call which
// needs a second one while it still holds the first can never get it.
//
// That is the mistake it exists to catch: a query made on the pool, directly
// or through another method, while a cursor or a transaction from the same
// pool is still open. On the real pool the call works, until as many of them
// run at once as the pool has connections; then each holds one and waits for
// another, and every database call in the process waits behind them. A test
// that calls a read with rows present, under a deadline, on a store this was
// applied to turns that into a deadline error on the first call.
func OneConnection(st *store.Store) {
	st.DB.SetMaxOpenConns(1)
}
