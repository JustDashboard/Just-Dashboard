package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

func TestMetricRetentionDefaultsToAWeekAndOffersFourWindows(t *testing.T) {
	c, s := newClient(t)
	var report databaseMetricsExport
	reply := c.do(http.MethodGet, "/api/v1/databases/metrics-export", "", nil)
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil || reply.Code != http.StatusOK {
		t.Fatalf("get = %d %s: %v", reply.Code, reply.Body.String(), err)
	}
	if report.RetentionDays != 7 || len(report.Options) != 4 || report.Endpoint != "/api/v1/metrics/databases" ||
		report.RawDays != 7 || report.CoarseMinutes != 5 || report.EverySeconds != 30 {
		t.Fatalf("report = %+v", report)
	}
	for i, days := range []int{7, 14, 30, 90} {
		// Nothing is recorded yet, so there is nothing to measure an estimate by.
		if report.Options[i].Days != days || report.Options[i].EstimatedBytes != nil {
			t.Fatalf("option %d = %+v", i, report.Options[i])
		}
	}
	if report.StoredBytes != nil || report.Samples != 0 {
		t.Fatalf("an empty history reported %+v", report)
	}

	id := saveOpsConnection(t, s, "estimated", dbx.DriverPostgres, "postgres://user:pw@127.0.0.1:1/db")
	saveSample(t, s, id, time.Minute, postgresSnapshot)
	saveSample(t, s, id, 2*time.Minute, postgresSnapshot)
	reply = c.do(http.MethodGet, "/api/v1/databases/metrics-export", "", nil)
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Samples != 2 || report.Connections != 1 || report.StoredBytes == nil || *report.StoredBytes <= 0 {
		t.Fatalf("measured report = %+v", report)
	}
	var last int64
	for _, option := range report.Options {
		if option.EstimatedBytes == nil || *option.EstimatedBytes <= last {
			t.Fatalf("estimates must grow with the window: %+v", report.Options)
		}
		last = *option.EstimatedBytes
	}
	// 90 days is a week of 30-second samples plus 83 days of 5-minute ones.
	want := int64(databaseMetricSamplesIn(90))
	if want != 7*2880+83*288 {
		t.Fatalf("samples in 90 days = %d", want)
	}
	if got := databaseMetricSamplesIn(7); got != 7*2880 {
		t.Fatalf("samples in 7 days = %d", got)
	}
}

func TestChoosingRetentionIsAnAdministratorsAndAudited(t *testing.T) {
	c, s := newClient(t)
	put := func(h *client, body string) int {
		return h.do(http.MethodPut, "/api/v1/databases/metrics-export", body, nil).Code
	}
	for _, bad := range []string{`{"retentionDays":0}`, `{"retentionDays":8}`, `{"retentionDays":365}`, `{}`, `{"retentionDays":"30"}`} {
		if code := put(c, bad); code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, code)
		}
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		h := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "retention-"+string(role), role)}
		if code := put(h, `{"retentionDays":30}`); code != http.StatusForbidden {
			t.Errorf("%s changed the retention: %d", role, code)
		}
		if reply := h.do(http.MethodGet, "/api/v1/databases/metrics-export", "", nil); reply.Code != http.StatusOK {
			t.Errorf("%s could not read the setting: %d", role, reply.Code)
		}
	}
	if days, err := s.databaseMetricRetentionDays(t.Context()); err != nil || days != 7 {
		t.Fatalf("refused requests changed the setting: %d, %v", days, err)
	}
	reply := c.do(http.MethodPut, "/api/v1/databases/metrics-export", `{"retentionDays":30}`, nil)
	var report databaseMetricsExport
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil || reply.Code != http.StatusOK || report.RetentionDays != 30 {
		t.Fatalf("put = %d %s", reply.Code, reply.Body.String())
	}
	var target, detail string
	if err := s.Store.DB.QueryRow(`SELECT target, detail FROM audit_log WHERE action='database.metrics.retention'`).Scan(&target, &detail); err != nil {
		t.Fatalf("no audit entry: %v", err)
	}
	if !strings.Contains(detail, `"days":30`) || !strings.Contains(detail, `"previousDays":7`) {
		t.Fatalf("audit detail = %q", detail)
	}
}

func TestMetricRetentionDecidesWhatIsPruned(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "kept", dbx.DriverPostgres, "postgres://user:pw@127.0.0.1:1/db")
	for _, age := range []time.Duration{100 * 24 * time.Hour, 60 * 24 * time.Hour, 20 * 24 * time.Hour, 10 * 24 * time.Hour, time.Hour} {
		saveSample(t, s, id, age, `{"counters":{"queries":1}}`)
	}
	count := func() (n int) {
		s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples`).Scan(&n)
		return
	}
	prune := func(setting string) {
		t.Helper()
		if setting != "" {
			if err := s.Store.SetSetting(t.Context(), databaseMetricRetentionKey, setting); err != nil {
				t.Fatal(err)
			}
		}
		s.pruneDatabaseMetrics(context.Background())
	}

	// A saved value that cannot be read must keep everything: pruning to the
	// default on a failed read would delete what the operator chose to keep.
	prune("forever")
	if count() != 5 {
		t.Fatalf("an unreadable retention pruned to %d samples", count())
	}
	for _, step := range []struct {
		setting string
		left    int
	}{{"90", 4}, {"30", 3}, {"14", 2}, {"7", 1}} {
		prune(step.setting)
		if count() != step.left {
			t.Fatalf("retention %s left %d samples, want %d", step.setting, count(), step.left)
		}
	}
}

func TestOlderSamplesAreThinnedToTheNewestOfEachFiveMinutes(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "thinned", dbx.DriverPostgres, "postgres://user:pw@127.0.0.1:1/db")
	if err := s.Store.SetSetting(t.Context(), databaseMetricRetentionKey, "30"); err != nil {
		t.Fatal(err)
	}
	// 20 minutes of raw samples ten days back, aligned to the step so the
	// buckets are exactly four, and an hour of them from yesterday.
	step := databaseMetricCoarseEvery.Milliseconds()
	old := (time.Now().Add(-10*24*time.Hour).UnixMilli() / step) * step
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(at int64, n int) {
		t.Helper()
		_, err := s.Store.DB.Exec(`INSERT INTO db_metric_samples VALUES(?,?,?,?)`, id, databaseMetricIdentity(rec), at,
			fmt.Sprintf(`{"counters":{"queries":%d}}`, n))
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		insert(old+int64(i)*30_000, i)
	}
	recent := time.Now().Add(-24 * time.Hour).UnixMilli()
	for i := range 120 {
		insert(recent+int64(i)*30_000, i)
	}
	// A second connection's samples at the same instants are its own.
	other := saveOpsConnection(t, s, "other", dbx.DriverPostgres, "postgres://user:pw@127.0.0.1:1/other")
	otherRec, _ := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, other).Scan)
	for i := range 20 {
		s.Store.DB.Exec(`INSERT INTO db_metric_samples VALUES(?,?,?,?)`, other, databaseMetricIdentity(otherRec), old+int64(i)*30_000, `{}`)
	}

	s.pruneDatabaseMetrics(context.Background())

	var thinned, kept, theirs int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=? AND at<?`, id, time.Now().Add(-8*24*time.Hour).UnixMilli()).Scan(&thinned)
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=? AND at>=?`, id, time.Now().Add(-8*24*time.Hour).UnixMilli()).Scan(&kept)
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=?`, other).Scan(&theirs)
	if thinned != 4 || kept != 120 || theirs != 2 {
		t.Fatalf("thinned=%d kept=%d other=%d, want 4, 120 and 2", thinned, kept, theirs)
	}
	// The sample that stands for a bucket is its newest, so a counter's
	// difference across buckets is still its growth.
	rows, err := s.Store.DB.Query(`SELECT snapshot FROM db_metric_samples WHERE connection_id=? AND at<? ORDER BY at`, id, time.Now().Add(-8*24*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var snapshot string
		rows.Scan(&snapshot)
		got = append(got, snapshot)
	}
	want := []int{9, 19, 29, 39}
	for i, n := range want {
		if got[i] != fmt.Sprintf(`{"counters":{"queries":%d}}`, n) {
			t.Fatalf("bucket %d kept %s, want the sample numbered %d", i, got[i], n)
		}
	}

	// Thinned again an hour on, with nothing left to thin, it is a no-op; and
	// within the hour it is not even attempted.
	s.dbMetricThinnedAt = time.Now()
	insert(old+7*30_000+1, 99)
	s.pruneDatabaseMetrics(context.Background())
	var after int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_metric_samples WHERE connection_id=? AND at<?`, id, time.Now().Add(-8*24*time.Hour).UnixMilli()).Scan(&after)
	if after != 5 {
		t.Fatalf("thinning ran again inside the hour: %d old samples", after)
	}
}

func TestLongHistoryIsReadableWithoutInventedOutages(t *testing.T) {
	s := testServer(t)
	id := saveOpsConnection(t, s, "long", dbx.DriverPostgres, "postgres://user:pw@127.0.0.1:1/db")
	s.Store.SetSetting(t.Context(), databaseMetricRetentionKey, "90")
	now := time.Now()
	// Three weeks of five-minute samples, then the raw week of 30-second ones.
	// The history is steady, so a gap flagged anywhere would be the reader
	// mistaking the thinning for downtime.
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	insert := func(at time.Time) {
		if _, err := tx.Exec(`INSERT INTO db_metric_samples VALUES(?,?,?,?)`,
			id, databaseMetricIdentity(rec), at.UnixMilli(), `{"counters":{"queries":1}}`); err != nil {
			t.Fatal(err)
		}
	}
	rawFrom := now.Add(-databaseMetricRawWindow)
	for at := now.Add(-30 * 24 * time.Hour); at.Before(rawFrom); at = at.Add(databaseMetricCoarseEvery) {
		insert(at)
	}
	for at := rawFrom; at.Before(now); at = at.Add(databaseMetricEvery) {
		insert(at)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "long-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/databases/%d/stats/history", id)
	var report struct {
		Samples        []databaseMetricSample `json:"samples"`
		RetentionHours int                    `json:"retentionHours"`
	}
	reply := viewer.do(http.MethodGet, path+"?hours=2160", "", nil)
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil || reply.Code != http.StatusOK {
		t.Fatalf("90 days = %d %s", reply.Code, reply.Body.String())
	}
	if report.RetentionHours != 2160 || len(report.Samples) < 100 || len(report.Samples) > 721 {
		t.Fatalf("retentionHours=%d, %d samples", report.RetentionHours, len(report.Samples))
	}
	for _, sample := range report.Samples {
		if sample.Gap {
			t.Fatalf("a steady history shows an outage at %s", time.UnixMilli(sample.At))
		}
	}
	if reply := viewer.do(http.MethodGet, path+"?hours=2161", "", nil); reply.Code != http.StatusBadRequest {
		t.Fatalf("beyond the retention = %d", reply.Code)
	}

	// A real outage inside the coarse stretch is still one.
	s.Store.DB.Exec(`DELETE FROM db_metric_samples WHERE at BETWEEN ? AND ?`,
		now.Add(-20*24*time.Hour).UnixMilli(), now.Add(-20*24*time.Hour+2*time.Hour).UnixMilli())
	reply = viewer.do(http.MethodGet, path+"?hours=720", "", nil)
	json.Unmarshal(reply.Body.Bytes(), &report)
	gaps := 0
	for _, sample := range report.Samples {
		if sample.Gap {
			gaps++
		}
	}
	if gaps == 0 {
		t.Fatal("a two-hour outage in thinned history was drawn as continuous")
	}

	// And a retention of a week refuses what is no longer kept.
	s.Store.SetSetting(t.Context(), databaseMetricRetentionKey, "7")
	if reply := viewer.do(http.MethodGet, path+"?hours=169", "", nil); reply.Code != http.StatusBadRequest {
		t.Fatalf("169 hours at a week's retention = %d", reply.Code)
	}
}
