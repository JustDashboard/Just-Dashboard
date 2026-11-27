package api

import (
	"errors"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/imagescan"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
)

// os.updates is the Packages page's check: a host with no package manager has
// none, a failed listing is not known, and a manager that cannot tell a
// security update sends no security count.
func TestHubOSUpdatesReadsThePackagesCheck(t *testing.T) {
	checked := time.Unix(1761350400, 0)
	if u, err := hubOSUpdates(&updates.Report{}, updates.ErrNotSupported); u != nil || err != nil {
		t.Fatalf("no package manager: %+v %v", u, err)
	}
	if _, err := hubOSUpdates(&updates.Report{Available: true, Error: "apt list failed"}, nil); err == nil {
		t.Fatal("a failed listing was read as nothing pending")
	}
	if _, err := hubOSUpdates(nil, errors.New("boom")); err == nil {
		t.Fatal("an error was dropped")
	}
	u, err := hubOSUpdates(&updates.Report{
		Available: true, Packages: make([]updates.Package, 5), SecurityCount: 2, SecurityFiltering: true,
		RebootRequired: true, LastChecked: checked,
	}, nil)
	if err != nil || u.Pending != 5 || u.Security == nil || *u.Security != 2 || !u.RebootRequired || u.CheckedAt != checked.Unix() {
		t.Fatalf("apt: %+v %v", u, err)
	}
	u, err = hubOSUpdates(&updates.Report{Available: true, Packages: make([]updates.Package, 3), LastChecked: checked}, nil)
	if err != nil || u.Security != nil || u.Pending != 3 {
		t.Fatalf("a manager without security filtering: %+v %v", u, err)
	}
}

// imageScans counts what the owner can act on, from finished scans only.
func TestHubImageScansCountsActionableFindingsOfFinishedScans(t *testing.T) {
	finished := time.Unix(1761346800, 0)
	got, err := hubImageScans([]deploy.LiveImageScan{
		{ProjectName: "shop", EnvironmentName: "production", Scan: &deploy.ImageScan{
			State: deploy.ImageScanScanned, FinishedAt: &finished,
			Summary:    imagescan.Summary{Counts: imagescan.Counts{Critical: 40, High: 90}},
			Actionable: imagescan.Counts{Critical: 1, High: 3},
		}},
		{ProjectName: "blog", EnvironmentName: "production", Scan: &deploy.ImageScan{State: "scanning"}},
		{ProjectName: "docs", EnvironmentName: "production"},
		{ProjectName: "api", EnvironmentName: "staging", Scan: &deploy.ImageScan{
			State: deploy.ImageScanScanned, FinishedAt: &finished, Actionable: imagescan.Counts{Critical: 2},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Deployment != "api" || got[0].Critical != 2 ||
		got[1].Deployment != "shop" || got[1].Critical != 1 || got[1].High != 3 || got[1].ScannedAt != finished.Unix() {
		t.Fatalf("got %+v", got)
	}
}
