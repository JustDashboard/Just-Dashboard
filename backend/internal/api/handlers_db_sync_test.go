package api

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What is said about a database that can be seen and cannot be used.
//
// This pins the defect the reporting exists to fix, which was a silence: a
// Postgres on a compose network with no published port was detected, its
// credentials were read, Connectable() came back false, and the loop moved on.
// The operator pressed a button that appeared to do nothing about a database
// sitting in plain sight on their own Docker page.
//
// The sync and the fleet both report from discovery's inventory now, and the
// routes are held to it in handlers_db_inventory_test.go. What is pinned here
// is the two things under them: that detection explains itself, and that a
// server detection did not explain is still not reported with a blank line —
// the reason string is half the feature, and an empty one is the same silence
// wearing a different shape.

func TestDetectionSaysWhyADatabaseCannotBeReached(t *testing.T) {
	cand, _ := dbx.Detect(
		"main-backend-postgres-1", "postgres:16-alpine",
		map[string]string{"POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "main"},
		nil, nil, // nothing published and no address of its own: nowhere to dial
	)
	if cand == nil {
		t.Fatal("postgres:16-alpine should be recognised whether or not it publishes a port")
	}
	if cand.Connectable() {
		t.Fatal("a container with no published port must be reported, not connected")
	}
	if cand.Container != "main-backend-postgres-1" {
		t.Errorf("container = %q, want the container's own name", cand.Container)
	}
	if cand.Driver != dbx.DriverPostgres {
		t.Errorf("driver = %q, want postgres", cand.Driver)
	}
	// The reason has to name the fix. An operator who is told only that
	// something failed is no better off than one who was told nothing.
	if reason := unreachableReason(cand.Reason); !strings.Contains(reason, "published port") {
		t.Errorf("reason = %q, want it to say the port is not published", reason)
	}
}

func TestDetectionSaysNothingAgainstADatabaseItCanAdopt(t *testing.T) {
	cand, password := dbx.Detect(
		"pg", "postgres:16",
		map[string]string{"POSTGRES_PASSWORD": "secret"},
		[]dbx.PublishedPort{{ContainerPort: 5432, HostIP: "127.0.0.1", HostPort: 5432}},
		nil,
	)
	if cand == nil || password != "secret" {
		t.Fatalf("detect returned %+v / %q", cand, password)
	}
	if !cand.Connectable() || cand.Reason != "" {
		t.Errorf("a connectable server must be adopted silently, not reported as a problem: %q", cand.Reason)
	}
}

// A server that is unconnectable for a reason detection did not name must
// still carry a sentence. This is the guard on the fallback, which exists so a
// future engine rule cannot reintroduce the blank-line version of the silence.
func TestAnUnreachableServerIsNeverReportedWithABlankReason(t *testing.T) {
	for _, said := range []string{"", "   ", "\n"} {
		if strings.TrimSpace(unreachableReason(said)) == "" {
			t.Errorf("unreachableReason(%q) is blank: the same silence in another shape", said)
		}
	}
	if got := unreachableReason("it has no published port"); got != "it has no published port" {
		t.Errorf("a reason detection gave was replaced: %q", got)
	}
}
