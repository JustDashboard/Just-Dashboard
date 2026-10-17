package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

const promPrefix = "jd_database_"

// A reading older than this is not a reading of now. It is the recorder's own
// rule for an outage (three missed intervals), so the endpoint and the charts
// agree on when a server went quiet.
const databaseMetricStale = 3 * databaseMetricEvery

// mountDatabaseMetricsExport registers the scrape endpoint on its own, because
// it is the one route a scoped API key may reach: every other group refuses
// such a key before its capability is asked. A session is refused here too,
// since a browser has no use for it and its cookie must never be the way in.
func (s *Server) mountDatabaseMetricsExport(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.Authn.AuthenticateScoped)
		r.Use(s.apiLim.ByPrincipal)
		r.Use(httpx.AuditMutations(s.Audit))
		r.Use(httpx.RequireToken)
		r.Use(httpx.RequireCapability(auth.CapMetricsRead))
		r.Method(http.MethodGet, databaseMetricsPath, s.handle(s.handleDatabaseMetricsExport))
	})
}

// handleDatabaseMetricsExport serves the recorder's newest reading of every
// server in the Prometheus text format. It reads the dashboard's own store and
// dials nothing, so a scrape costs the databases nothing and works while one of
// them is down. The route is for API keys: see mountDatabaseMetricsExport.
func (s *Server) handleDatabaseMetricsExport(w http.ResponseWriter, r *http.Request) error {
	records, err := s.recordedDatabaseConns(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	out := newPromFamilies()
	now := time.Now()
	for _, rec := range records {
		var at int64
		var snapshot string
		err := s.Store.DB.QueryRowContext(r.Context(), `SELECT at, snapshot FROM db_metric_samples
			WHERE connection_id=? AND identity=? ORDER BY at DESC LIMIT 1`,
			rec.conn.ID, databaseMetricIdentity(rec)).Scan(&at, &snapshot)
		if err != nil {
			// A server never recorded has no series, rather than a zero that
			// reads as an outage.
			continue
		}
		base := []promLabel{
			{"driver", string(rec.conn.Driver)},
			{"name", rec.conn.Name},
			{"id", strconv.FormatInt(rec.conn.ID, 10)},
		}
		sampled := time.UnixMilli(at)
		fresh := now.Sub(sampled) <= databaseMetricStale
		out.add("last_sample_timestamp_seconds", promGauge, "When the recorder last saved a reading of this server, in Unix seconds.",
			base, float64(sampled.Unix()))
		out.add("up", promGauge, "1 when the recorder read this server within the last 90 seconds, otherwise 0.",
			base, boolValue(fresh))
		// What the last reading said is only worth exporting while it is news:
		// a scraper would otherwise chart the figures of an hour ago as now.
		if fresh {
			addDatabaseSnapshot(out, base, rec.conn.Driver, []byte(snapshot))
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err = out.WriteTo(w)
	return err
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// addDatabaseSnapshot turns one saved snapshot into series. A snapshot that
// does not decode adds nothing: the server's up and timestamp series stand.
func addDatabaseSnapshot(out *promFamilies, base []promLabel, driver dbx.Driver, data []byte) {
	switch driver {
	case dbx.DriverRedis:
		var snap struct {
			Server map[string]any `json:"server"`
		}
		if json.Unmarshal(data, &snap) == nil {
			addRedisSnapshot(out, base, snap.Server)
		}
	case dbx.DriverMongo:
		var snap struct {
			Server map[string]any `json:"server"`
		}
		if json.Unmarshal(data, &snap) == nil {
			addMongoSnapshot(out, base, snap.Server)
		}
	default:
		var snap dbStatsResponse
		if json.Unmarshal(data, &snap) == nil && snap.ServerStats != nil {
			addSQLSnapshot(out, base, &snap)
		}
	}
}

func addSQLSnapshot(out *promFamilies, base []promLabel, snap *dbStatsResponse) {
	info := append(append([]promLabel{}, base...),
		promLabel{"version", snap.Version}, promLabel{"role", snap.Role}, promLabel{"database", snap.Database})
	out.add("info", promGauge, "The server's version and role, and its database where it has one, as labels.", info, 1)
	if snap.UptimeSeconds > 0 {
		out.add("uptime_seconds", promGauge, "Seconds since the engine started.", base, snap.UptimeSeconds)
	}
	if snap.DatabaseBytes > 0 {
		out.add("size_bytes", promGauge, "Size of the connected database.", base, float64(snap.DatabaseBytes))
	}
	if c := snap.Connections; c != nil {
		out.add("connections", promGauge, "Sessions the server holds.", base, float64(c.Total))
		out.add("connections_active", promGauge, "Sessions running a statement.", base, float64(c.Active))
		out.add("connections_idle", promGauge, "Sessions waiting for work.", base, float64(c.Idle))
		out.add("connections_idle_in_transaction", promGauge, "Sessions idle inside an open transaction.", base, float64(c.IdleInTransaction))
		out.add("connections_waiting", promGauge, "Sessions waiting on a lock.", base, float64(c.Waiting))
		if c.Max > 0 {
			out.add("connections_max", promGauge, "Sessions the server is configured to accept.", base, float64(c.Max))
		}
	}
	for _, key := range sortedReadingKeys(snap.Counters) {
		out.add(strings.TrimSuffix(promName(key), "_total")+"_total", promCounter, "Engine counter "+key+", as read by the recorder.", base, snap.Counters[key])
	}
	for _, key := range sortedReadingKeys(snap.Gauges) {
		out.add(promName(key), promGauge, "Engine reading "+key+", as read by the recorder.", base, snap.Gauges[key])
	}
	if p := snap.Pool; p != nil {
		out.add("pool_connections_open", promGauge, "Connections the dashboard holds open to this server.", base, float64(p.Open))
		out.add("pool_connections_in_use", promGauge, "Dashboard connections to this server running something.", base, float64(p.InUse))
		out.add("pool_connections_idle", promGauge, "Dashboard connections to this server waiting for work.", base, float64(p.Idle))
	}
}

// promRead names one number in a Redis or MongoDB snapshot and the series it
// becomes. Both engines' snapshots are loose maps the pages read by key, so the
// exported set is stated here rather than inferred from whatever the map holds.
type promRead struct {
	path  []string
	name  string
	kind  promKind
	help  string
	scale float64
	label promLabel
}

var redisPromReads = []promRead{
	{path: []string{"counters", "uptime_in_seconds"}, name: "uptime_seconds", kind: promGauge, help: "Seconds since the engine started."},
	{path: []string{"counters", "connected_clients"}, name: "connections", kind: promGauge, help: "Sessions the server holds."},
	{path: []string{"counters", "blocked_clients"}, name: "connections_waiting", kind: promGauge, help: "Sessions waiting on a blocking call."},
	{path: []string{"counters", "used_memory"}, name: "memory_used_bytes", kind: promGauge, help: "Memory the engine has allocated."},
	{path: []string{"counters", "used_memory_rss"}, name: "memory_resident_bytes", kind: promGauge, help: "Memory the operating system has given the engine."},
	{path: []string{"counters", "maxmemory"}, name: "memory_max_bytes", kind: promGauge, help: "The configured memory limit, 0 for none."},
	{path: []string{"counters", "keys"}, name: "keys", kind: promGauge, help: "Keys across every database."},
	{path: []string{"counters", "instantaneous_ops_per_sec"}, name: "operations_per_second", kind: promGauge, help: "Commands per second, as the engine reports it now."},
	{path: []string{"counters", "total_connections_received"}, name: "connections_received_total", kind: promCounter, help: "Connections accepted."},
	{path: []string{"counters", "rejected_connections"}, name: "connections_rejected_total", kind: promCounter, help: "Connections refused for want of room."},
	{path: []string{"counters", "total_commands_processed"}, name: "commands_total", kind: promCounter, help: "Commands run."},
	{path: []string{"counters", "total_net_input_bytes"}, name: "network_received_bytes_total", kind: promCounter, help: "Bytes received from clients."},
	{path: []string{"counters", "total_net_output_bytes"}, name: "network_sent_bytes_total", kind: promCounter, help: "Bytes sent to clients."},
	{path: []string{"counters", "keyspace_hits"}, name: "keyspace_hits_total", kind: promCounter, help: "Lookups that found their key."},
	{path: []string{"counters", "keyspace_misses"}, name: "keyspace_misses_total", kind: promCounter, help: "Lookups that did not."},
	{path: []string{"counters", "expired_keys"}, name: "keys_expired_total", kind: promCounter, help: "Keys removed after their time to live."},
	{path: []string{"counters", "evicted_keys"}, name: "keys_evicted_total", kind: promCounter, help: "Keys removed to stay under the memory limit."},
}

const bytesPerMebibyte = 1 << 20

var mongoPromReads = []promRead{
	{path: []string{"uptime"}, name: "uptime_seconds", kind: promGauge, help: "Seconds since the engine started."},
	{path: []string{"connections", "current"}, name: "connections", kind: promGauge, help: "Sessions the server holds."},
	{path: []string{"connections", "available"}, name: "connections_available", kind: promGauge, help: "Sessions the server can still accept."},
	{path: []string{"mem", "resident"}, name: "memory_resident_bytes", kind: promGauge, scale: bytesPerMebibyte, help: "Memory the operating system has given the engine."},
	{path: []string{"network", "bytesIn"}, name: "network_received_bytes_total", kind: promCounter, help: "Bytes received from clients."},
	{path: []string{"network", "bytesOut"}, name: "network_sent_bytes_total", kind: promCounter, help: "Bytes sent to clients."},
	{path: []string{"network", "numRequests"}, name: "requests_total", kind: promCounter, help: "Requests received."},
	{path: []string{"opcounters", "insert"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "insert"}, help: "Operations run, by kind."},
	{path: []string{"opcounters", "query"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "query"}, help: "Operations run, by kind."},
	{path: []string{"opcounters", "update"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "update"}, help: "Operations run, by kind."},
	{path: []string{"opcounters", "delete"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "delete"}, help: "Operations run, by kind."},
	{path: []string{"opcounters", "getmore"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "getmore"}, help: "Operations run, by kind."},
	{path: []string{"opcounters", "command"}, name: "operations_total", kind: promCounter, label: promLabel{"operation", "command"}, help: "Operations run, by kind."},
	{path: []string{"documents", "inserted"}, name: "documents_total", kind: promCounter, label: promLabel{"operation", "inserted"}, help: "Documents touched, by what was done to them."},
	{path: []string{"documents", "returned"}, name: "documents_total", kind: promCounter, label: promLabel{"operation", "returned"}, help: "Documents touched, by what was done to them."},
	{path: []string{"documents", "updated"}, name: "documents_total", kind: promCounter, label: promLabel{"operation", "updated"}, help: "Documents touched, by what was done to them."},
	{path: []string{"documents", "deleted"}, name: "documents_total", kind: promCounter, label: promLabel{"operation", "deleted"}, help: "Documents touched, by what was done to them."},
}

func addRedisSnapshot(out *promFamilies, base []promLabel, server map[string]any) {
	info := append(append([]promLabel{}, base...),
		promLabel{"version", mapString(server, "version")}, promLabel{"role", mapString(server, "role")},
		promLabel{"flavor", mapString(server, "flavor")}, promLabel{"mode", mapString(server, "mode")})
	out.add("info", promGauge, "The server's version and role, and its database where it has one, as labels.", info, 1)
	addReads(out, base, server, redisPromReads)
}

func addMongoSnapshot(out *promFamilies, base []promLabel, server map[string]any) {
	info := append(append([]promLabel{}, base...),
		promLabel{"version", mapString(server, "version")}, promLabel{"role", mapString(server, "role")})
	out.add("info", promGauge, "The server's version and role, and its database where it has one, as labels.", info, 1)
	addReads(out, base, server, mongoPromReads)
}

func addReads(out *promFamilies, base []promLabel, server map[string]any, reads []promRead) {
	for _, read := range reads {
		value, ok := mapNumber(server, read.path...)
		if !ok {
			continue
		}
		if read.scale != 0 {
			value *= read.scale
		}
		labels := base
		if read.label.name != "" {
			labels = append(append([]promLabel{}, base...), read.label)
		}
		out.add(read.name, read.kind, read.help, labels, value)
	}
}

func mapString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// mapNumber follows a path of keys to a JSON number. Redis counters are
// numbers in the snapshot already, so a text value is not read as one.
func mapNumber(m map[string]any, path ...string) (float64, bool) {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = next[key]
	}
	n, ok := cur.(float64)
	return n, ok
}

func sortedReadingKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// promName turns an engine's own key ("rowsInserted", "Innodb_buffer_pool_reads")
// into a metric name: lower case, underscores between words, nothing outside
// [a-z0-9_].
func promName(key string) string {
	var b strings.Builder
	var prev byte
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'A' && c <= 'Z':
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
				b.WriteByte('_')
			}
			b.WriteByte(c + 'a' - 'A')
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
		default:
			if prev != '_' && b.Len() > 0 {
				b.WriteByte('_')
			}
			c = '_'
		}
		prev = c
	}
	return strings.Trim(b.String(), "_")
}

type promKind string

const (
	promGauge   promKind = "gauge"
	promCounter promKind = "counter"
)

type promLabel struct{ name, value string }

type promSeries struct {
	labels string
	value  float64
}

type promFamily struct {
	help   string
	kind   promKind
	series []promSeries
	seen   map[string]bool
}

// promFamilies collects series by metric name, because the text format wants
// each name's lines together under one HELP and TYPE.
type promFamilies struct{ byName map[string]*promFamily }

func newPromFamilies() *promFamilies { return &promFamilies{byName: map[string]*promFamily{}} }

// add records one series. A name already taken by the other kind, or a series
// with the labels it already holds, is dropped: two engines' own keys can land
// on one name, and an invalid exposition fails the whole scrape.
func (f *promFamilies) add(name string, kind promKind, help string, labels []promLabel, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) || name == "" {
		return
	}
	name = promPrefix + name
	family := f.byName[name]
	if family == nil {
		family = &promFamily{help: help, kind: kind, seen: map[string]bool{}}
		f.byName[name] = family
	}
	if family.kind != kind {
		return
	}
	rendered := renderPromLabels(labels)
	if family.seen[rendered] {
		return
	}
	family.seen[rendered] = true
	family.series = append(family.series, promSeries{rendered, value})
}

func renderPromLabels(labels []promLabel) string {
	sorted := append([]promLabel(nil), labels...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	var b strings.Builder
	b.WriteByte('{')
	for i, label := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(label.name)
		b.WriteString(`="`)
		b.WriteString(promEscape(label.value, true))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// promEscape is the text format's escaping: a backslash and a newline
// everywhere, and a double quote inside a label value.
func promEscape(s string, quotes bool) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	if quotes {
		s = strings.ReplaceAll(s, `"`, `\"`)
	}
	return s
}

func (f *promFamilies) WriteTo(w io.Writer) (int64, error) {
	names := make([]string, 0, len(f.byName))
	for name := range f.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		family := f.byName[name]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, promEscape(family.help, false), name, family.kind)
		for _, series := range family.series {
			fmt.Fprintf(&b, "%s%s %s\n", name, series.labels, strconv.FormatFloat(series.value, 'f', -1, 64))
		}
	}
	n, err := w.Write([]byte(b.String()))
	return int64(n), err
}
