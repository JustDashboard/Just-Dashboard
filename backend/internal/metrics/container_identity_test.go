package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestContainerIdentityHistorySeparatesReplacementsAndSurvivesRename(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	at := time.Unix(1800000000, 0).UTC()
	for i, sample := range []dockerx.ContainerStats{
		{Name: "web", CPUPercent: 99},
		{ID: "prior", Name: "web", CPUPercent: 10, MemUsage: 100},
		{ID: "candidate", Name: "web", CPUPercent: 20, MemUsage: 200},
		{ID: "candidate", Name: "renamed-web", CPUPercent: 40, MemUsage: 400},
	} {
		if err := r.writeContainers(t.Context(), at.Add(time.Duration(i)*5*time.Second), []dockerx.ContainerStats{sample}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.ContainerIdentityRange(t.Context(), []string{"candidate", "prior", "missing", "' OR 1=1 --"}, at, at.Add(20*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 4 || len(result.Series[0].Points) != 2 || len(result.Series[1].Points) != 1 ||
		len(result.Series[2].Points) != 0 || len(result.Series[3].Points) != 0 {
		t.Fatalf("identity contamination: %+v", result.Series)
	}
	if result.Series[0].Points[0].CPU != 20 || result.Series[0].Points[1].MemBytes != 400 || result.Series[1].Points[0].CPU != 10 {
		t.Fatalf("incorrect identity samples: %+v", result.Series)
	}
	before, err := r.ContainerIdentityRange(t.Context(), []string{"candidate"}, at, at.Add(10*time.Second), 10)
	if err != nil || len(before.Series[0].Points) != 0 {
		t.Fatalf("activation boundary included in before window: %+v %v", before, err)
	}
	fractional, err := r.ContainerIdentityRange(t.Context(), []string{"candidate"}, at.Add(9500*time.Millisecond), at.Add(10500*time.Millisecond), 10)
	if err != nil || len(fractional.Series[0].Points) != 1 {
		t.Fatalf("fractional bounds dropped an enclosed sample: %+v %v", fractional, err)
	}
	fractional, err = r.ContainerIdentityRange(t.Context(), []string{"candidate"}, at.Add(10500*time.Millisecond), at.Add(14500*time.Millisecond), 10)
	if err != nil || len(fractional.Series[0].Points) != 0 {
		t.Fatalf("fractional bounds admitted an earlier sample: %+v %v", fractional, err)
	}
	name, err := r.ContainerRange(t.Context(), "web", at, at.Add(20*time.Second), 10)
	if err != nil || len(name.Points) != 3 {
		t.Fatalf("name-continuous history regressed: %+v %v", name, err)
	}
	r.retention = 0
	disabled, err := r.ContainerIdentityRange(t.Context(), []string{"candidate"}, at, at.Add(20*time.Second), 10)
	if err != nil || disabled.RetentionSeconds != 0 || len(disabled.Series[0].Points) != 0 {
		t.Fatalf("disabled history returned samples: %+v %v", disabled, err)
	}
}

func TestContainerIdentityHistoryBoundsAndAggregation(t *testing.T) {
	r := testRecorder(t, 5*time.Second, time.Hour)
	at := time.Unix(1800000000, 0).UTC()
	for _, ids := range [][]string{nil, {""}, {"same", "same"}, make([]string, 65)} {
		if _, err := r.ContainerIdentityRange(t.Context(), ids, at, at.Add(time.Minute), 10); !errors.Is(err, ErrContainerIdentityWindow) {
			t.Fatalf("accepted identities %v: %v", ids, err)
		}
	}
	for _, until := range []time.Time{at, at.Add(-time.Minute), at.Add(25 * time.Hour)} {
		if _, err := r.ContainerIdentityRange(t.Context(), []string{"id"}, at, until, 10); !errors.Is(err, ErrContainerIdentityWindow) {
			t.Fatalf("accepted window: %v", err)
		}
	}
	for i, cpu := range []float64{10, 30} {
		if err := r.writeContainers(t.Context(), at.Add(time.Duration(i)*5*time.Second), []dockerx.ContainerStats{{ID: "id", Name: "web", CPUPercent: cpu}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.ContainerIdentityRange(t.Context(), []string{"id"}, at, at.Add(10*time.Second), 1)
	if err != nil {
		t.Fatal(err)
	}
	points := result.Series[0].Points
	if len(points) != 1 || points[0].Samples != 2 || points[0].CPU != 20 || points[0].CPUPeak != 30 {
		t.Fatalf("aggregation lost sample count or peak: %+v", points)
	}
}
