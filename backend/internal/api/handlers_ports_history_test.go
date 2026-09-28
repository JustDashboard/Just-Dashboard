package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Before the recorder's first sample the history is empty, not an error, and
// says it has not begun.
func TestPortHistoryAnswersBeforeItsFirstSample(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/ports/history", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["recordingSince"] != nil || got["lastSample"] != nil {
		t.Errorf("recording since %v, last sample %v, want both null", got["recordingSince"], got["lastSample"])
	}
	if events, ok := got["events"].([]any); !ok || len(events) != 0 {
		t.Errorf("events %v, want an empty list", got["events"])
	}
	if got["intervalSeconds"] != float64(60) || got["retentionDays"] != float64(30) {
		t.Errorf("interval %v and retention %v", got["intervalSeconds"], got["retentionDays"])
	}
}

// Every event is placed and graded as the ports list places a socket
// listening now, and the window is the caller's.
func TestPortHistoryPlacesEachSocket(t *testing.T) {
	c, s := newClient(t)
	now := time.Now().Unix()
	db := s.Store.DB
	for _, statement := range []string{
		`INSERT INTO listener_history (id, started_at, last_sample) VALUES (1, ?1 - 90000, ?1)`,
		// Redis opened on every interface an hour ago and still listens.
		`INSERT INTO listener_observations (protocol, family, address, port, process, username, pid, cmdline, opened_after, first_seen)
			VALUES ('tcp', 'ipv4', '0.0.0.0', 6379, 'redis-server', 'redis', 2000, 'redis-server *:6379', ?1 - 3660, ?1 - 3600)`,
		// A dev server on loopback, listening when recording began, went two
		// days ago: outside a day's window, inside a week's.
		`INSERT INTO listener_observations (protocol, family, address, port, process, username, pid, first_seen, gone_after, gone_at)
			VALUES ('tcp', 'ipv4', '127.0.0.1', 3000, 'node', 'app', 1400, ?1 - 90000, ?1 - 172860, ?1 - 172800)`,
	} {
		if _, err := db.Exec(statement, now); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) map[string]any {
		t.Helper()
		w := c.do(http.MethodGet, "/api/v1/ports/history"+query, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", query, w.Code, strings.TrimSpace(w.Body.String()))
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	day := read("")
	events := day["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("a day's events: %v", events)
	}
	redis := events[0].(map[string]any)
	for key, want := range map[string]any{
		"kind": "opened", "address": "0.0.0.0", "port": float64(6379), "process": "redis-server",
		"scope": "all", "exposed": true, "reach": "all", "network": "all",
	} {
		if redis[key] != want {
			t.Errorf("redis %s = %v, want %v", key, redis[key], want)
		}
	}
	// The posture levels a database on every interface; the firewall this
	// test cannot read decides only how far.
	if redis["level"] != "critical" && redis["level"] != "warning" {
		t.Errorf("redis on every interface was not levelled: %v", redis["level"])
	}

	week := read("?hours=168")
	events = week["events"].([]any)
	if len(events) != 2 || events[1].(map[string]any)["kind"] != "closed" {
		t.Fatalf("a week's events: %v", events)
	}
	node := events[1].(map[string]any)
	if node["reach"] != "loopback" || node["exposed"] != false || node["baseline"] != true {
		t.Errorf("the dev server's closing: %v", node)
	}

	limited := read("?hours=168&limit=1")
	if len(limited["events"].([]any)) != 1 || limited["truncated"] != true {
		t.Errorf("one event of two: %v", limited)
	}
}

func TestPortHistoryRefusesAWindowItDoesNotKeep(t *testing.T) {
	c, _ := newClient(t)
	for _, query := range []string{"?hours=0", "?hours=721", "?hours=day", "?hours=1.5", "?limit=0", "?limit=5001"} {
		w := c.do(http.MethodGet, "/api/v1/ports/history"+query, "", nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET /ports/history%s = %d, want 400", query, w.Code)
		}
	}
}

// The history is the ports list at other times, read as the list is by any
// signed-in account.
func TestPortHistoryIsReadByEveryAccount(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := c.do(http.MethodGet, "/api/v1/ports/history", "", nil); w.Code != http.StatusOK {
		t.Fatalf("a read-only account got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
}

// GET /ports dates a socket the history saw open, and only while the program
// it saw holds it.
func TestPortListDatesWhatTheHistorySawOpen(t *testing.T) {
	c, s := newClient(t)
	mine, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mine.Close()
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	minePort := mine.Addr().(*net.TCPAddr).Port
	takenPort := taken.Addr().(*net.TCPAddr).Port
	seen := time.Now().Add(-10 * time.Minute).Unix()
	// An owner not read when the stretch began matches whoever holds it; a
	// stretch nginx began is not this test's socket.
	for _, row := range []struct {
		port    int
		process string
	}{{minePort, ""}, {takenPort, "nginx"}} {
		if _, err := s.Store.DB.Exec(`INSERT INTO listener_observations (protocol, family, address, port, process, opened_after, first_seen)
			VALUES ('tcp', 'ipv4', '127.0.0.1', ?, ?, ?, ?)`, row.port, row.process, seen-60, seen); err != nil {
			t.Fatal(err)
		}
	}
	w := c.do(http.MethodGet, "/api/v1/ports", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ports = %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	var listeners []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &listeners); err != nil {
		t.Fatal(err)
	}
	found := map[int]map[string]any{}
	for _, l := range listeners {
		if l["address"] == "127.0.0.1" && l["protocol"] == "tcp" {
			found[int(l["port"].(float64))] = l
		}
	}
	if found[minePort] == nil || found[takenPort] == nil {
		t.Fatalf("the test's sockets are not listed: %d and %d", minePort, takenPort)
	}
	first, _ := found[minePort]["firstSeen"].(string)
	if at, err := time.Parse(time.RFC3339, first); err != nil || at.Unix() != seen {
		t.Errorf("firstSeen %q, want %s", first, time.Unix(seen, 0).Format(time.RFC3339))
	}
	if _, dated := found[takenPort]["firstSeen"]; dated {
		t.Errorf("a socket another program's stretch names was dated: %v", found[takenPort])
	}
	if found[minePort]["process"] == "" || found[minePort]["reach"] != "loopback" {
		t.Errorf("the dated socket lost what the list says of it: %v", found[minePort])
	}
}
