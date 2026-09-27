package dbx

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func stalledPool(t *testing.T, m *Manager, id int64) <-chan error {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { _ = conn.Close() })
		close(accepted)
	}()
	result := make(chan error, 1)
	go func() {
		_, err := m.Pool(context.Background(), id, DriverMySQL, "root@tcp("+listener.Addr().String()+")/test")
		result <- err
	}()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("pool never began its handshake")
	}
	return result
}

func TestPoolInitializationDoesNotBlockOtherConnections(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Shutdown)
	warm, err := m.Pool(context.Background(), 1, DriverSQLite, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	stalled := stalledPool(t, m, 2)
	result := make(chan *sql.DB, 1)
	go func() {
		db, _ := m.Pool(context.Background(), 1, DriverSQLite, ":memory:")
		result <- db
	}()
	select {
	case db := <-result:
		if db != warm {
			t.Fatal("cached pool changed")
		}
	case <-time.After(time.Second):
		t.Fatal("healthy pool blocked behind another handshake")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Pool(ctx, 2, DriverMySQL, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	m.Close(2)
	if err := <-stalled; !errors.Is(err, context.Canceled) {
		t.Fatalf("closed initializer: %v", err)
	}
	if m.Stats(2) != nil {
		t.Fatal("closed initialization published a pool")
	}
	if _, err := m.Pool(context.Background(), 2, DriverSQLite, ":memory:"); err != nil {
		t.Fatalf("replacement credentials: %v", err)
	}
}

func TestConcurrentPoolRequestsShareOnePool(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	var wg sync.WaitGroup
	results := make(chan *sql.DB, 20)
	for range 20 {
		wg.Go(func() {
			db, err := m.Pool(context.Background(), 1, DriverSQLite, ":memory:")
			if err != nil {
				t.Error(err)
			}
			results <- db
		})
	}
	wg.Wait()
	close(results)
	var first *sql.DB
	for db := range results {
		if first == nil {
			first = db
		}
		if db != first {
			t.Fatal("duplicate pools opened for the same connection")
		}
	}
}
