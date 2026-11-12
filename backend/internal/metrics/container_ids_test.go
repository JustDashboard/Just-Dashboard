package metrics

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// writeSpelledOut stores samples as every release up to 0.8.5 did, in that
// release's own words: the container's id in the row itself and no column
// for a number. It is what an upgraded install holds, and what the binary a
// rollback returns to writes.
func writeSpelledOut(t *testing.T, r *Recorder, stats []dockerx.ContainerStats) {
	t.Helper()
	for _, st := range stats {
		var periods, throttled *int64
		if st.CPUPeriods > 0 {
			p, th := int64(st.CPUPeriods), int64(st.CPUThrottledPeriods)
			periods, throttled = &p, &th
		}
		if _, err := r.st.DB.Exec(`
			INSERT INTO metric_container_samples
			  (ts, name, cpu_percent, mem_bytes, mem_limit, mem_percent, net_rx, net_tx, pids,
			   block_read, block_write, container_id, size_rw, network_available, block_available,
			   mem_limited, cpu_total, sample_time, cpu_periods, cpu_throttled)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			st.TS.Unix(), st.Name, st.CPUPercent, int64(st.MemUsage), int64(st.MemLimit), st.MemPercent,
			int64(st.NetRx), int64(st.NetTx), int64(st.PIDs), int64(st.BlockRead), int64(st.BlockWrite),
			st.ID, st.SizeRw, st.NetworkAvailable, st.BlockAvailable, st.MemLimited, int64(st.CPUTotal),
			float64(st.TS.UnixNano())/1e9, periods, throttled); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	chartWebFirst  = strings.Repeat("a", 64)
	chartWebSecond = strings.Repeat("b", 64)
	chartDatabase  = strings.Repeat("c", 64)
)

// chartHistory is forty minutes of two containers, a batch an interval: web
// is replaced half way (a new id under the same name, its counters starting
// again), the database runs throughout under a CPU limit, and one container
// reports no id at all.
func chartHistory(base time.Time) [][]dockerx.ContainerStats {
	batches := [][]dockerx.ContainerStats{}
	for i := 0; i < 160; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		web, n := chartWebFirst, uint64(i)
		if i >= 80 {
			web, n = chartWebSecond, uint64(i-80)
		}
		batches = append(batches, []dockerx.ContainerStats{
			{ID: web, Name: "web", TS: at, CPUPercent: float64(10 + i%7*5), MemUsage: mibs(float64(100 + i%11)),
				MemLimit: mibs(512), MemLimited: true, MemPercent: float64(100+i%11) / 5.12, PIDs: 4 + uint64(i%3),
				NetworkAvailable: true, BlockAvailable: true, NetRx: n * 1500, NetTx: n * 700,
				BlockRead: n * 4096, BlockWrite: n * 8192, CPUTotal: (n + 1) * 1_000_000, SizeRw: int64(i) * 1024},
			{ID: chartDatabase, Name: "db", TS: at, CPUPercent: float64(40 + i%5*9), MemUsage: mibs(float64(300 + i%13*4)),
				MemLimit: mibs(1024), MemLimited: true, MemPercent: float64(300+i%13*4) / 10.24, PIDs: 20,
				NetworkAvailable: true, NetRx: uint64(i) * 90, NetTx: uint64(i) * 4000,
				CPUTotal: uint64(i+1) * 3_000_000, CPUPeriods: uint64(i+1) * 100, CPUThrottledPeriods: uint64(i) * 7},
			{Name: "anonymous", TS: at, CPUPercent: 3, MemUsage: mibs(8)},
		})
	}
	return batches
}

// everyChart reads what every page that draws container history reads, as
// the JSON the pages are sent, plus the sizing figures, whose fields are not
// all exported.
func everyChart(t *testing.T, r *Recorder, base time.Time) (string, *SizingUsage) {
	t.Helper()
	ctx := context.Background()
	from, to := base, base.Add(40*time.Minute)
	ids := []string{chartWebFirst, chartWebSecond, chartDatabase}
	read := map[string]any{}
	for _, name := range []string{"web", "db", "anonymous"} {
		series, err := r.ContainerRange(ctx, name, from, to, 60)
		if err != nil {
			t.Fatal(err)
		}
		read["range "+name] = series
		// A window that begins after the replacement, whose first rate needs
		// the sample before it.
		late, err := r.ContainerRange(ctx, name, base.Add(25*time.Minute), to, 600)
		if err != nil {
			t.Fatal(err)
		}
		read["late range "+name] = late
	}
	identity, err := r.ContainerIdentityRange(ctx, ids, from, to, 60)
	if err != nil {
		t.Fatal(err)
	}
	read["identity"] = identity
	sparklines, err := r.Sparklines(ctx, from, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	read["sparklines"] = sparklines
	recorded, err := r.RecordedContainers(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	read["recorded"] = recorded
	sizing, err := r.SizingUsage(ctx, ids, from, to)
	if err != nil {
		t.Fatal(err)
	}
	read["throttled share"] = sizing.ThrottledShare(ids)
	encoded, err := json.MarshalIndent(read, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded), sizing
}

// The charts read the same numbers whichever way a sample spells its
// container's id: from history as 0.8.5 recorded it, as this release records
// it, as the short form will, and from an install that changed in the middle
// of a container's life, where one series is made of both.
func TestTheChartsReadTheSameNumbersFromEitherSpellingOfAnId(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	history := chartHistory(base)
	stores := map[string]func(*Recorder){
		"recorded before": func(r *Recorder) {
			for _, batch := range history {
				writeSpelledOut(t, r, batch)
			}
		},
		"recorded by this release": func(r *Recorder) {
			for _, batch := range history {
				if err := r.writeContainers(context.Background(), batch[0].TS, batch); err != nil {
					t.Fatal(err)
				}
			}
		},
		"recorded in the short form": func(r *Recorder) {
			r.numberIDs = true
			for _, batch := range history {
				if err := r.writeContainers(context.Background(), batch[0].TS, batch); err != nil {
					t.Fatal(err)
				}
			}
		},
		// The short form began while web's second container and the database
		// were running: each of them has samples in both spellings.
		"changed half way": func(r *Recorder) {
			r.numberIDs = true
			for i, batch := range history {
				if i < 110 {
					writeSpelledOut(t, r, batch)
				} else if err := r.writeContainers(context.Background(), batch[0].TS, batch); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	charts, sizings := map[string]string{}, map[string]*SizingUsage{}
	for name, fill := range stores {
		r := testRecorder(t, 15*time.Second, 24*time.Hour)
		fill(r)
		charts[name], sizings[name] = everyChart(t, r, base)
	}
	want := charts["recorded before"]
	// The comparison means nothing if the charts are empty.
	for _, must := range []string{`"name": "web"`, chartWebSecond, `"samples": `, `"netRx": `} {
		if !strings.Contains(want, must) {
			t.Fatalf("the charts read from spelled-out history have no %s:\n%.2000s", must, want)
		}
	}
	if sizings["recorded before"].Samples == 0 {
		t.Fatal("the sizing figures read from spelled-out history are empty")
	}
	for _, name := range []string{"recorded by this release", "recorded in the short form", "changed half way"} {
		if charts[name] != want {
			t.Errorf("history %s draws different charts than the same history spelled out:\n%s", name, firstDifference(want, charts[name]))
		}
		if !reflect.DeepEqual(sizings[name], sizings["recorded before"]) {
			t.Errorf("history %s gives different sizing figures than the same history spelled out:\n%+v\n%+v",
				name, sizings[name], sizings["recorded before"])
		}
	}
}

func firstDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			from := max(i-6, 0)
			return "want:\n" + strings.Join(wantLines[from:min(i+3, len(wantLines))], "\n") +
				"\ngot:\n" + strings.Join(gotLines[from:min(i+3, len(gotLines))], "\n")
		}
	}
	return "one is a prefix of the other"
}

// This release still spells a container's id out in every sample, as 0.8.5
// does, and writes no number. manage.sh rollback puts the previous binary
// back over the database as it is, and that binary finds a release's samples
// with the condition below and no other: a sample that held a number in
// place of its id would be missing from its release charts and from
// right-sizing. Before numberIDs is turned on for the server, the binary a
// rollback returns to has to be one that reads the short form.
func TestSamplesSpellTheirIdOutForTheBinaryARollbackReturnsTo(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	base := time.Unix(1_800_000_000, 0).UTC()
	for _, batch := range chartHistory(base)[:50] {
		if err := r.writeContainers(context.Background(), batch[0].TS, batch); err != nil {
			t.Fatal(err)
		}
	}
	var found, numbered, ids int
	for query, into := range map[string]*int{
		// identityFilter as 0.8.5 has it.
		`SELECT COUNT(*) FROM metric_container_samples WHERE container_id IN ('` + chartWebFirst + `','` + chartDatabase + `')`: &found,
		`SELECT COUNT(*) FROM metric_container_samples WHERE container_ref IS NOT NULL`:                                         &numbered,
		`SELECT COUNT(*) FROM metric_container_ids`:                                                                             &ids,
	} {
		if err := r.st.DB.QueryRow(query).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	if found != 100 || numbered != 0 || ids != 0 {
		t.Fatalf("of 100 samples of two containers, 0.8.5 finds %d by the id they spell, %d hold a number and %d ids are written down; want 100, 0, 0: "+
			"a dashboard rolled back would not find the rest", found, numbered, ids)
	}
}

// In the short form an id is written once however many samples its container
// gives, and a sample holds its number in place of the sixty-four characters.
func TestAContainersIdIsWrittenOnce(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	r.numberIDs = true
	base := time.Unix(1_800_000_000, 0).UTC()
	for _, batch := range chartHistory(base)[:50] {
		if err := r.writeContainers(context.Background(), batch[0].TS, batch); err != nil {
			t.Fatal(err)
		}
	}
	var ids, spelled, numbered, anonymous int
	for query, into := range map[string]*int{
		`SELECT COUNT(*) FROM metric_container_ids`:                                     &ids,
		`SELECT COUNT(*) FROM metric_container_samples WHERE container_id <> ''`:        &spelled,
		`SELECT COUNT(*) FROM metric_container_samples WHERE container_ref IS NOT NULL`: &numbered,
		`SELECT COUNT(*) FROM metric_container_samples WHERE container_ref IS NULL`:     &anonymous,
	} {
		if err := r.st.DB.QueryRow(query).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	if ids != 2 || spelled != 0 || numbered != 100 {
		t.Fatalf("150 samples of two containers with ids and one without: %d ids written down, %d samples spell theirs out, %d hold a number; want 2, 0, 100",
			ids, spelled, numbered)
	}
	// A container that reports no id has none: it is not given the number of
	// an empty one, which would make every such container the same one.
	if anonymous != 50 {
		t.Fatalf("%d samples without an id hold no number, want all 50", anonymous)
	}
	var id string
	if err := r.st.DB.QueryRow(`
		SELECT i.container_id FROM metric_container_samples s JOIN metric_container_ids i ON i.id = s.container_ref
		 WHERE s.name = 'db' LIMIT 1`).Scan(&id); err != nil || id != chartDatabase {
		t.Fatalf("the database's samples lead to the id %q, %v", id, err)
	}
}

// Pruning forgets an id when the last sample that refers to it has gone, and
// with it the number the recorder remembered: a container that comes back is
// written down again and found.
func TestPruningForgetsTheIdsNoSampleRefersTo(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	r.numberIDs = true
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	gone, kept := strings.Repeat("d", 64), strings.Repeat("e", 64)
	if err := r.writeContainers(ctx, now, []dockerx.ContainerStats{
		{ID: gone, Name: "old", TS: now.Add(-2 * time.Hour), CPUPercent: 5},
		{ID: kept, Name: "new", TS: now.Add(-time.Minute), CPUPercent: 5},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.prune(ctx); err != nil {
		t.Fatal(err)
	}
	var left string
	if err := r.st.DB.QueryRow(`SELECT group_concat(container_id) FROM metric_container_ids`).Scan(&left); err != nil || left != kept {
		t.Fatalf("after pruning the ids written down are %q, %v; want only the one a sample still refers to", left, err)
	}
	// The same container again, an hour's retention later.
	if err := r.writeContainers(ctx, now, []dockerx.ContainerStats{
		{ID: gone, Name: "old", TS: now.Add(-30 * time.Second), CPUPercent: 7, MemUsage: mibs(1)},
	}); err != nil {
		t.Fatal(err)
	}
	history, err := r.ContainerIdentityRange(ctx, []string{gone}, now.Add(-time.Hour), now, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Series) != 1 || len(history.Series[0].Points) != 1 {
		t.Fatalf("a container that came back after its id was forgotten has %+v: its sample was stored under a number that leads nowhere", history.Series)
	}
}

// A release's samples are found through an index in both spellings. A
// condition the planner cannot serve from them reads every sample in the
// store for every right-sizing and every release chart.
func TestIdentityReadsUseAnIndexForBothSpellings(t *testing.T) {
	r := testRecorder(t, 15*time.Second, time.Hour)
	where, args := identityFilter([]string{chartWebFirst, chartDatabase}, 0, 1)
	rows, err := r.st.DB.Query(`EXPLAIN QUERY PLAN `+sizingUsageQuery(where), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	whole := strings.Join(plan, "\n")
	for _, index := range []string{"idx_container_samples_identity_ts", "idx_container_samples_ref_ts"} {
		if !strings.Contains(whole, "SEARCH s USING INDEX "+index) {
			t.Errorf("the read does not use %s:\n%s", index, whole)
		}
	}
	if strings.Contains(whole, "SCAN s") {
		t.Errorf("the read scans every sample:\n%s", whole)
	}
}
