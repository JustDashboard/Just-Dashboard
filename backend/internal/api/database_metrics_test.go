package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// A protocol fixture answers INFO without an external service. The test still
// exercises connection opening, the real engine reader and SQLite persistence.
func metricRedis(t *testing.T, delay ...time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					count, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
					args := make([]string, count)
					for i := range args {
						line, _ := reader.ReadString('\n')
						length, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
						value := make([]byte, length+2)
						if _, err := io.ReadFull(reader, value); err != nil {
							return
						}
						args[i] = string(value[:length])
					}
					if count == 0 {
						return
					}
					if len(delay) > 0 {
						time.Sleep(delay[0])
					}
					switch strings.ToLower(args[0]) {
					case "hello":
						fmt.Fprint(conn, "-ERR unknown command 'hello'\r\n")
					case "ping":
						fmt.Fprint(conn, "+PONG\r\n")
					case "info":
						info := "# Server\r\nredis_version:7.4.0\r\nuptime_in_seconds:600\r\n# Stats\r\ntotal_commands_processed:1000\r\nkeyspace_hits:300\r\nkeyspace_misses:20\r\n# Clients\r\nconnected_clients:3\r\n"
						fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(info), info)
					default:
						fmt.Fprint(conn, "+OK\r\n")
					}
				}
			}()
		}
	}()
	return "redis://" + listener.Addr().String() + "/0"
}

func TestDatabaseMetricsRecordWithoutAViewerAndSurviveRestart(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "history", dbx.DriverRedis, metricRedis(t))
	s.startDatabaseMetrics(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=?`, id).Scan(&count)
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background recorder saved no samples without a page request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.stopDatabaseMetrics()
	var file string
	if err := s.Store.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	s.Store.Close()
	reopened, err := store.Open(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	s.Store.DB = reopened.DB
	t.Cleanup(func() { reopened.Close() })
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "history-viewer", auth.RoleReadOnly)}
	reply := viewer.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/%d/stats/history", id), "", nil)
	if reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), "total_commands_processed") {
		t.Fatalf("retained history after restart: %d %s", reply.Code, reply.Body.String())
	}
}

func TestDatabaseMetricsScopeRetentionBucketsAndGaps(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "history", dbx.DriverPostgres, "postgres://user:private-password@127.0.0.1:1/db")
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, age := range []time.Duration{8 * 24 * time.Hour, 10 * time.Minute, 9*time.Minute + 30*time.Second, 2 * time.Minute} {
		_, err := s.Store.DB.Exec(`INSERT INTO db_metric_samples VALUES(?,?,?,?)`, id, databaseMetricIdentity(rec), now.Add(-age).UnixMilli(), `{"counters":{"queries":10}}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	// No server answers at this address. Its existing samples must remain
	// readable, and pruning still runs even though collection fails.
	s.recordDatabaseMetrics(context.Background())
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "history-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/databases/%d/stats/history", id)
	reply := viewer.do(http.MethodGet, path, "", nil)
	var report struct {
		Samples []databaseMetricSample `json:"samples"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if reply.Code != 200 || len(report.Samples) != 3 || !report.Samples[2].Gap || report.Samples[1].Gap {
		t.Fatalf("offline history or gap lost: %s", reply.Body.String())
	}
	if strings.Contains(reply.Body.String(), "private-password") {
		t.Fatal("history disclosed the connection credential")
	}
	var count int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=?`, id).Scan(&count)
	if count != 3 {
		t.Fatalf("retention kept %d samples", count)
	}
	for _, hours := range []string{"0", "169", "oops"} {
		if reply := viewer.do(http.MethodGet, path+"?hours="+hours, "", nil); reply.Code != 400 {
			t.Fatalf("invalid hours %q accepted: %d", hours, reply.Code)
		}
	}
	// A wider window buckets raw samples and still preserves an outage.
	reply = viewer.do(http.MethodGet, path+"?hours=168", "", nil)
	json.Unmarshal(reply.Body.Bytes(), &report)
	if len(report.Samples) > 2 || len(report.Samples) == 0 || !report.Samples[len(report.Samples)-1].Gap {
		t.Fatalf("bucketing lost the outage: %s", reply.Body.String())
	}
	s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc='different target' WHERE id=?`, id)
	reply = viewer.do(http.MethodGet, path, "", nil)
	if !strings.Contains(reply.Body.String(), `"samples":[]`) {
		t.Fatalf("new target inherited old history: %s", reply.Body.String())
	}
	s.Store.DB.Exec(`DELETE FROM db_connections WHERE id=?`, id)
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=?`, id).Scan(&count)
	if count != 0 {
		t.Fatal("forgetting a connection left its history behind")
	}
	if reply := viewer.do(http.MethodGet, path, "", nil); reply.Code != 404 {
		t.Fatalf("forgotten connection history returned %d", reply.Code)
	}
}

func TestDatabaseMetricsRedisRespectsCollectionDeadline(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "slow", dbx.DriverRedis, metricRedis(t, time.Second))
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	s.recordDatabaseMetric(ctx, rec)
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("a slow Redis reply outlived the collection deadline: %s", elapsed)
	}
	var count int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=?`, id).Scan(&count)
	if count != 0 {
		t.Fatal("a failed read was recorded")
	}
}
