package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

// An environment owns the volumes its plan manages and, for a Compose stack,
// the ones Compose labelled with its live project; a name is claimed once.
func TestOwnedVolumesAreThePlansAndTheStacks(t *testing.T) {
	targets := []deploy.SizingTarget{
		{EnvironmentID: 1, Volumes: []string{"shop-uploads", "shared"}},
		{EnvironmentID: 2, Volumes: []string{"shared"}, ComposeProject: "jd-e2"},
	}
	docker := map[string]dockerx.Volume{
		"jd-e2_db":    {Name: "jd-e2_db", Labels: map[string]string{"com.docker.compose.project": "jd-e2"}},
		"jd-e9_db":    {Name: "jd-e9_db", Labels: map[string]string{"com.docker.compose.project": "jd-e9"}},
		"unlabelled":  {Name: "unlabelled"},
		"shop-upload": {Name: "shop-upload"},
	}
	owned := ownedVolumes(targets, docker)
	if got := owned[1]; len(got) != 2 || got[0] != "shop-uploads" || got[1] != "shared" {
		t.Errorf("environment 1 = %v", got)
	}
	if got := owned[2]; len(got) != 1 || got[0] != "jd-e2_db" {
		t.Errorf("environment 2 = %v", got)
	}
}

// Docker's size now wins over the newest sample; a volume a listing Docker
// answered does not have is not on this host; growth is the sum of the
// volumes' lines, and the fastest-growing app comes first.
func TestAppGrowthsJoinSizesAndLines(t *testing.T) {
	growth := func(v float64) *float64 { return &v }
	targets := []deploy.SizingTarget{
		{ProjectName: "blog", EnvironmentID: 1},
		{ProjectName: "shop", EnvironmentID: 2},
		{ProjectName: "empty", EnvironmentID: 3},
	}
	owned := map[int64][]string{1: {"blog-data"}, 2: {"shop-uploads", "shop-cache", "shop-gone"}, 3: {"never-made"}}
	docker := map[string]dockerx.Volume{
		"blog-data":    {Name: "blog-data", Size: 100, RefCount: 1},
		"shop-uploads": {Name: "shop-uploads", Size: 5 << 30, RefCount: 1},
		"shop-cache":   {Name: "shop-cache", Size: -1, RefCount: -1},
	}
	growths := map[string]metrics.VolumeGrowth{
		"blog-data":    {Status: metrics.ForecastSteady, GrowthBytesPerDay: growth(0), SizeBytes: 90, Measured: true},
		"shop-uploads": {Status: metrics.ForecastFilling, GrowthBytesPerDay: growth(2 << 30), SizeBytes: 4 << 30, Measured: true},
		"shop-cache":   {Status: metrics.ForecastShrinking, GrowthBytesPerDay: growth(-(1 << 29)), SizeBytes: 1 << 30, Measured: true},
		"shop-gone":    {Status: metrics.ForecastFilling, GrowthBytesPerDay: growth(1 << 40), SizeBytes: 1 << 40, Measured: true},
	}
	apps := appGrowths(targets, owned, docker, true, growths)
	if len(apps) != 2 || apps[0].ProjectName != "shop" || apps[1].ProjectName != "blog" {
		t.Fatalf("apps = %+v", apps)
	}
	shop := apps[0]
	if len(shop.Volumes) != 2 || shop.Volumes[0].Name != "shop-uploads" || shop.SizeBytes != 5<<30+1<<30 ||
		*shop.GrowthBytesPerDay != 3<<29 || shop.Status != metrics.ForecastFilling {
		t.Errorf("shop = %+v", shop)
	}
	if blog := apps[1]; blog.SizeBytes != 100 || blog.Status != metrics.ForecastSteady {
		t.Errorf("blog = %+v", blog)
	}
	// Without Docker's answer the samples are all there is, and a volume
	// with neither is still listed, unmeasured.
	offline := appGrowths(targets, owned, map[string]dockerx.Volume{}, false, growths)
	if len(offline) != 3 || offline[0].ProjectName != "shop" || offline[0].SizeBytes != 4<<30+1<<30+1<<40 {
		t.Errorf("offline = %+v", offline)
	}
	if last := offline[2]; last.ProjectName != "empty" || last.Status != metrics.ForecastInsufficient || last.Volumes[0].Measured {
		t.Errorf("never measured = %+v", last)
	}
}

// The route: the filesystem filling at 2 GiB a day is dated, and the
// deployment whose managed volume grows is ranked with its line.
func TestCapacityForecastReadsTheRecord(t *testing.T) {
	b := sizingBench(t)
	// No Docker to answer, so the volumes are the record's alone.
	b.s.modules.docker = dockerx.New("unix:///nonexistent/docker.sock")
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	seedKeyDeployment(t, b.s, "shop", image,
		`{"strategy":"stop_first","mounts":[{"source":"shop-uploads","target":"/data","ownership":"managed"}]}`, 0)
	seedKeyDeployment(t, b.s, "blog", image, `{"strategy":"stop_first"}`, 0)
	db := b.s.Store.DB
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-4 * 24 * time.Hour)
	for at := start; !at.After(now); at = at.Add(30 * time.Minute) {
		days := at.Sub(start).Hours() / 24
		if _, err := db.Exec(`INSERT INTO metric_mount_samples(ts, mountpoint, used_percent, used_bytes, total_bytes)
			VALUES(?, '/', ?, ?, ?)`, at.Unix(), 40+2*days, int64((40+2*days)*(1<<30)), int64(100<<30)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO metric_volume_samples(ts, name, size_bytes) VALUES(?, 'shop-uploads', ?)`,
			at.Unix(), int64((1+days)*(1<<30))); err != nil {
			t.Fatal(err)
		}
	}
	w := b.inSession(http.MethodGet, "/api/v1/system/metrics/capacity", nil)
	var forecast capacityForecast
	if err := json.Unmarshal(w.Body.Bytes(), &forecast); err != nil || w.Code != http.StatusOK {
		t.Fatalf("capacity = %d %s", w.Code, w.Body.String())
	}
	if forecast.Status != "available" || forecast.RetentionSeconds != 7*86400 || len(forecast.Mounts) != 1 {
		t.Fatalf("forecast = %+v", forecast)
	}
	root := forecast.Mounts[0]
	if root.Status != metrics.ForecastFilling || root.DaysToFull == nil || math.Abs(*root.DaysToFull-26) > 0.2 || root.Confidence != metrics.ConfidenceHigh {
		t.Errorf("/ = %+v", root)
	}
	if len(forecast.Apps) != 1 || forecast.Apps[0].ProjectName != "shop" || forecast.Apps[0].GrowthBytesPerDay == nil ||
		math.Abs(*forecast.Apps[0].GrowthBytesPerDay/(1<<30)-1) > 0.01 || forecast.Apps[0].SizeBytes != 5<<30 {
		t.Errorf("apps = %+v", forecast.Apps)
	}

	b.s.modules.metrics = metrics.New(b.s.Store, b.s.Log, time.Minute, 0)
	_ = json.Unmarshal(b.inSession(http.MethodGet, "/api/v1/system/metrics/capacity", nil).Body.Bytes(), &forecast)
	if forecast.Status != "unavailable" || forecast.Reason == "" {
		t.Errorf("history off = %+v", forecast)
	}
}
