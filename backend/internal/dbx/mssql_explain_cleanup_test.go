package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var errPlanProbe = errors.New("plan query failed")
var errResetProbe = errors.New("reset failed")
var errEnableProbe = errors.New("enable outcome uncertain")

// This connector exercises database/sql pooling without contacting a database.
type explainProbeConnector struct {
	mu                    sync.Mutex
	connections           []*explainProbeConn
	commands              []string
	cancel                context.CancelFunc
	failReset, failEnable bool
	cleanupError          error
	cleanupBounded        bool
}

func (c *explainProbeConnector) Connect(context.Context) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn := &explainProbeConn{connector: c}
	c.connections = append(c.connections, conn)
	return conn, nil
}
func (c *explainProbeConnector) Driver() driver.Driver { return explainProbeDriver{} }

type explainProbeDriver struct{}

func (explainProbeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type explainProbeConn struct {
	connector *explainProbeConnector
	closed    bool
}

func (c *explainProbeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *explainProbeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *explainProbeConn) Close() error {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	c.closed = true
	return nil
}
func (c *explainProbeConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	p := c.connector
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, q)
	switch q {
	case "SET SHOWPLAN_ALL ON":
		if p.failEnable {
			return nil, errEnableProbe
		}
	case "SET SHOWPLAN_ALL OFF":
		p.cleanupError = ctx.Err()
		d, ok := ctx.Deadline()
		p.cleanupBounded = ok && time.Until(d) > 0 && time.Until(d) <= 6*time.Second
		if p.failReset {
			return nil, errResetProbe
		}
	default:
		return nil, fmt.Errorf("unexpected exec: %s", q)
	}
	return driver.RowsAffected(0), nil
}
func (c *explainProbeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	p := c.connector
	p.mu.Lock()
	p.commands = append(p.commands, q)
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil, errPlanProbe
}

func TestMSSQLExplainResetsAfterQueryFailureAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &explainProbeConnector{}
			if canceled {
				p.cancel = cancel
			}
			db := sql.OpenDB(p)
			defer db.Close()
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			_, err := (mssqlDialect{}).ExplainPlan(ctx, db, "SELECT 1")
			if !errors.Is(err, errPlanProbe) {
				t.Fatalf("query error lost: %v", err)
			}
			p.mu.Lock()
			if len(p.commands) != 3 || p.commands[2] != "SET SHOWPLAN_ALL OFF" {
				t.Errorf("cleanup sequence: %v", p.commands)
			}
			if p.cleanupError != nil || !p.cleanupBounded {
				t.Errorf("cleanup context: %v bounded=%v", p.cleanupError, p.cleanupBounded)
			}
			p.mu.Unlock()
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.connections) != 1 || p.connections[0].closed {
				t.Error("reset connection was not reused")
			}
		})
	}
}

func TestMSSQLExplainDiscardsConnectionWithUncertainMode(t *testing.T) {
	for _, enabling := range []bool{false, true} {
		t.Run(fmt.Sprint(enabling), func(t *testing.T) {
			p := &explainProbeConnector{failEnable: enabling, failReset: !enabling}
			db := sql.OpenDB(p)
			defer db.Close()
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			_, err := (mssqlDialect{}).ExplainPlan(context.Background(), db, "SELECT 1")
			want := errResetProbe
			if enabling {
				want = errEnableProbe
			}
			if !errors.Is(err, want) {
				t.Fatalf("mode error lost: %v", err)
			}
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.connections) != 2 || !p.connections[0].closed {
				t.Errorf("uncertain connection returned to pool: connections=%d", len(p.connections))
			}
		})
	}
}
