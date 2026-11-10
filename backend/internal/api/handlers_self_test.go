package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/storehealth"
)

func dashboardHealthOf(t *testing.T, c *client) dashboardHealth {
	t.Helper()
	rec := c.do(http.MethodGet, "/api/v1/dashboard/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/health = %d: %s", rec.Code, rec.Body.String())
	}
	var out dashboardHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDashboardHealthReportsTheStoreToEveryRole(t *testing.T) {
	s := testServer(t)
	s.modules.storeHealth.Check(context.Background())
	h := s.Routes()
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleReadOnly} {
		c := &client{t: t, h: h, cookie: signInAs(t, s, "health-"+string(role), role)}
		got := dashboardHealthOf(t, c)
		if !got.OK || !got.Store.OK || got.Store.Path != s.Cfg.DataDir || got.Store.TotalBytes == 0 || got.Store.LastWriteAt == nil {
			t.Fatalf("%s: health = %+v, want a healthy store on the data directory's filesystem", role, got)
		}
	}
}

// The container's health check, and through it the updater's wait and its
// rollback, read /healthz. A store that cannot be written must not turn it
// red: the stack would restart-loop on a full disk and an update would be
// rolled back for a reason the old version shares.
func TestAStoreProblemIsSaidToTheOperatorAndNotToLiveness(t *testing.T) {
	c, s := newClient(t)
	full := &os.PathError{Op: "write", Path: s.Cfg.DataDir, Err: syscall.ENOSPC}
	s.modules.storeHealth = storehealth.New(storehealth.Options{
		Dir:   s.Cfg.DataDir,
		Space: func(string) (uint64, uint64, error) { return 0, 1 << 30, nil },
		Write: func(context.Context) error { return full },
		Read:  func(context.Context) error { return nil },
	})
	s.modules.storeHealth.Check(context.Background())

	got := dashboardHealthOf(t, c)
	if got.OK || got.Store.OK || got.Store.Code != storehealth.CodeDiskFull || got.Store.Since == nil {
		t.Fatalf("health = %+v, want the store's disk_full", got)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.5:9999"
	c.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("/healthz = %d %s with a full disk, want its fixed 200", rec.Code, rec.Body.String())
	}
}
