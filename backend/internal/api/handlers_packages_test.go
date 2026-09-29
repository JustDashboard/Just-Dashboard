package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
)

// The package surface, driven through the whole chain.
//
// Two things unit tests cannot answer and this can: whether each route is
// mounted inside the group it was meant to be in — installing software runs
// maintainer scripts as root, so it must be `system.admin` and not merely
// authenticated — and whether the read routes survive a host whose package
// manager is absent, which is every container this suite runs in.

func TestPackageReadRoutesAnswerOnAnyHost(t *testing.T) {
	c, _ := newClient(t)
	for _, path := range []string{
		"/api/v1/packages/",
		"/api/v1/packages/updates",
		"/api/v1/packages/search?q=nginx",
		"/api/v1/packages/coreutils",
		"/api/v1/packages/coreutils/usage",
	} {
		w := c.do(http.MethodGet, path, "", nil)
		// 503 is the honest answer where there is no package manager, and 404
		// where a name is not known — neither is a fault. A 500 is.
		if w.Code >= 500 {
			t.Errorf("GET %s: %d: %s", path, w.Code, w.Body.String())
		}
	}
}

// A search is a read, and deliberately so: finding out what a repository
// offers is not a privilege, and a limited operator has to be able to tell an
// admin what to install.
func TestPackageSearchIsReadableByEveryRole(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "looker", auth.RoleReadOnly)}
	if w := c.do(http.MethodGet, "/api/v1/packages/search?q=nginx", "", nil); w.Code == http.StatusForbidden {
		t.Error("a readonly session cannot search for a package")
	}
}

// Installing a package runs the maintainer's scripts as root, which is
// arbitrary code execution on the host. It sits at the same tier as a
// privileged container spec, and a role that may restart a service must not
// reach it.
func TestInstallingAndRemovingNeedSystemAdmin(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	for _, path := range []string{"/api/v1/packages/install", "/api/v1/packages/remove"} {
		w := c.do(http.MethodPost, path, `{"packages":["htop"]}`, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("POST %s as limited: %d, want 403: %s", path, w.Code, w.Body.String())
		}
	}
}

// Both forms of removal reach the protected-package guard without a phrase.
func TestPackageRemovalAndPurgeUseOrdinaryConfirmation(t *testing.T) {
	c, s := newClient(t)
	if !s.modules.updates.Available() {
		t.Skip("no package manager on this host, so nothing can be removed to find out")
	}

	w := c.do(http.MethodPost, "/api/v1/packages/remove", `{"packages":["systemd"],"purge":true}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "does not boot") {
		t.Fatalf("purging protected package: %d %s", w.Code, w.Body.String())
	}

	// Ordinary removal reaches the same guard.
	w = c.do(http.MethodPost, "/api/v1/packages/remove", `{"packages":["systemd"]}`, nil)
	if w.Code == http.StatusPreconditionRequired {
		t.Fatal("an ordinary removal asked for a typed phrase")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("removing systemd: %d, want 400: %s", w.Code, w.Body.String())
	}
	// The narrow guard, through the route rather than in the service: the
	// answer has to be a refusal an operator can read, not a job that takes
	// the machine down.
	if !strings.Contains(w.Body.String(), "does not boot") {
		t.Errorf("the refusal does not say why: %s", w.Body.String())
	}
}

// A name that is not a package name is refused before it can become an
// argument. These never go through a shell, so the risk is not injection — it
// is that every one of these tools reads a leading dash as a flag and several
// accept a path to a package file in the same position.
func TestInstallRefusesWhatIsNotAPackageName(t *testing.T) {
	c, s := newClient(t)
	if !s.modules.updates.Available() {
		t.Skip("no package manager on this host")
	}
	for _, body := range []string{
		`{"packages":["--reinstall"]}`,
		`{"packages":["/tmp/evil.deb"]}`,
		`{"packages":[]}`,
	} {
		w := c.do(http.MethodPost, "/api/v1/packages/install", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("install %s: %d, want 400: %s", body, w.Code, w.Body.String())
		}
	}
}

// The upgrade report's own honesty check, kept from when this lived at
// /updates: a host with no manager says so rather than reporting nothing to do.
func TestPackageUpdatesReportNamesTheManager(t *testing.T) {
	c, s := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/packages/updates", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var rep updates.Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Available != s.modules.updates.Available() {
		t.Errorf("available = %v, want %v", rep.Available, s.modules.updates.Available())
	}
	if rep.Available && rep.Manager == "" {
		t.Error("a host with a package manager reported none by name")
	}
}
