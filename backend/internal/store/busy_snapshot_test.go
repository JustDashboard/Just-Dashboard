package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A deployment worker reads a row and then writes it inside one transaction.
// Under a deferred BEGIN, another connection committing between those two
// statements makes the write fail immediately with SQLITE_BUSY_SNAPSHOT — the
// busy timeout never applies — and the worker used to give up the run. With
// immediate transactions the second writer waits for the first instead.
func TestConcurrentWritersWaitInsteadOfFailingWithBusySnapshot(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, `CREATE TABLE busy_probe (id INTEGER PRIMARY KEY, counter INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO busy_probe(id, counter) VALUES (1, 0), (2, 0)`); err != nil {
		t.Fatal(err)
	}

	readThenWriteStarted := make(chan struct{})
	var wg sync.WaitGroup
	var readThenWriteErr, otherWriterErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		tx, err := st.DB.BeginTx(ctx, nil)
		if err != nil {
			readThenWriteErr = err
			close(readThenWriteStarted)
			return
		}
		defer tx.Rollback()
		var counter int
		if err := tx.QueryRowContext(ctx, `SELECT counter FROM busy_probe WHERE id = 1`).Scan(&counter); err != nil {
			readThenWriteErr = err
			close(readThenWriteStarted)
			return
		}
		close(readThenWriteStarted)
		// Hold the read open long enough for the other writer to try.
		time.Sleep(300 * time.Millisecond)
		if _, err := tx.ExecContext(ctx, `UPDATE busy_probe SET counter = ? WHERE id = 1`, counter+1); err != nil {
			readThenWriteErr = err
			return
		}
		readThenWriteErr = tx.Commit()
	}()
	go func() {
		defer wg.Done()
		<-readThenWriteStarted
		time.Sleep(50 * time.Millisecond)
		_, otherWriterErr = st.DB.ExecContext(ctx, `UPDATE busy_probe SET counter = counter + 1 WHERE id = 2`)
	}()
	wg.Wait()
	if readThenWriteErr != nil {
		t.Fatalf("read-then-write transaction failed: %v", readThenWriteErr)
	}
	if otherWriterErr != nil {
		t.Fatalf("concurrent writer failed: %v", otherWriterErr)
	}
	var first, second int
	if err := st.DB.QueryRowContext(ctx, `SELECT (SELECT counter FROM busy_probe WHERE id = 1), (SELECT counter FROM busy_probe WHERE id = 2)`).Scan(&first, &second); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 1 {
		t.Fatalf("counters = %d/%d, want both writes applied", first, second)
	}
}
