package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// Where a database is reachable from is read off its port binding, and the
// binding is the one thing the four spellings of "every interface" agree on.
func TestExposureReadsTheBinding(t *testing.T) {
	for _, c := range []struct {
		ip   string
		want dbExposure
	}{
		{"127.0.0.1", exposureLocal},
		{"::1", exposureLocal},
		{"0.0.0.0", exposurePublic},
		{"", exposurePublic},
		{"::", exposurePublic},
		{"10.0.0.5", exposurePrivate},
		{"100.64.0.7", exposurePrivate},
	} {
		got := exposureOf(&dbServer{binding: &dockerx.Port{IP: c.ip, PrivatePort: 5432, PublicPort: 5432}})
		if got != c.want {
			t.Errorf("binding on %q read as %s, want %s", c.ip, got, c.want)
		}
	}
	if got := exposureOf(&dbServer{}); got != exposureLocal {
		t.Errorf("a container reached at its own address read as %s, want local", got)
	}
}

// The string handed to a laptop is the saved one with the server's address in
// place of loopback and nothing else changed — the password, the database and
// the options are what make it work.
func TestPublicConnectionURLKeepsEverythingButTheHost(t *testing.T) {
	dsn := "postgres://jd:s3cret@127.0.0.1:5433/app?sslmode=disable"
	info, err := dbx.ParseDSN(dbx.DriverPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	got, err := publicConnectionURL(dbx.DriverPostgres, dsn, info, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://jd:s3cret@203.0.113.9:5433/app?sslmode=disable"; got != want {
		t.Errorf("public URL = %q, want %q", got, want)
	}

	// An IPv6 address has to be bracketed before a port can follow it.
	got, err = publicConnectionURL(dbx.DriverPostgres, dsn, info, "2001:db8::9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "@[2001:db8::9]:5433/") {
		t.Errorf("IPv6 public URL = %q, want the address bracketed", got)
	}

	// MySQL's driver form is not something another program can paste, so it
	// goes out as a URL.
	mysql := "jd:s3cret@tcp(127.0.0.1:3306)/app"
	info, err = dbx.ParseDSN(dbx.DriverMySQL, mysql)
	if err != nil {
		t.Fatal(err)
	}
	got, err = publicConnectionURL(dbx.DriverMySQL, mysql, info, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if want := "mysql://jd:s3cret@203.0.113.9:3306/app"; got != want {
		t.Errorf("MySQL public URL = %q, want %q", got, want)
	}

	if _, err := publicConnectionURL(dbx.DriverPostgres, dsn, info, ""); err == nil {
		t.Error("a host with no public address must refuse rather than hand back loopback")
	}
	if _, err := publicConnectionURL(dbx.DriverSQLite, "/data/app.db", &dbx.ConnInfo{Database: "/data/app.db"}, "203.0.113.9"); err == nil {
		t.Error("a SQLite file has no public form")
	}
}

func TestPreferredPublicAddressIsIPv4WhereThereIsOne(t *testing.T) {
	if got := preferredPublicAddress([]string{"2001:db8::9", "203.0.113.9"}); got != "203.0.113.9" {
		t.Errorf("got %q, want the IPv4 address", got)
	}
	if got := preferredPublicAddress([]string{"2001:db8::9"}); got != "2001:db8::9" {
		t.Errorf("got %q, want the only address there is", got)
	}
	if got := preferredPublicAddress(nil); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// A connection to a server somewhere else is nobody's to publish from here,
// and the sync would never re-add it — so forgetting it is a real act, and
// the page keeps the button for it.
func TestAccessOfARemoteServerIsRemoteAndNotDetected(t *testing.T) {
	s := testServer(t)
	conn := &dbConnection{ID: 1, Name: "prod", Driver: dbx.DriverPostgres}
	got := s.describeDBAccess(context.Background(), conn, "postgres://app:pw@db.example.com:5432/shop?sslmode=require")
	if got.Exposure != exposureRemote || got.Detected || got.Managed || got.Container != "" {
		t.Errorf("remote server described as %+v", got)
	}
	if got.PublicAddresses == nil {
		t.Error("publicAddresses must be a list, never null, so the page can read its length")
	}
}

// The access change is refused, with a reason, where no container on this
// host is behind the connection — before any confirmation, since a binding is
// changed back the same way it was changed.
func TestAccessChangeNeedsAContainer(t *testing.T) {
	_, router := dbTestRouter(t)
	req := httptest.NewRequest(http.MethodPut, "/databases/1/access", strings.NewReader(`{"exposure":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no container") {
		t.Fatalf("access change with no container = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	req = httptest.NewRequest(http.MethodPut, "/databases/1/access", strings.NewReader(`{"exposure":"everyone"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown exposure = %d, want 400", rec.Code)
	}
}

// Deleting with the container asks for the same phrase as dropping, and is
// then refused where no container is behind the connection rather than
// falling through to a DROP the operator did not ask for.
func TestRemovingTheContainerIsConfirmedThenNeedsAContainer(t *testing.T) {
	_, router := dbTestRouter(t)
	rec := driveWithoutConfirmation(t, router, confirmCase{
		http.MethodDelete, "/databases/1/database", `{"removeContainer":true}`, "",
	})
	if !strings.Contains(rec.Body.String(), "confirmation") {
		t.Fatalf("container removal ran without a phrase: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	req := httptest.NewRequest(http.MethodDelete, "/databases/1/database", strings.NewReader(`{"removeContainer":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Confirm", "never-opened.db")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no container") {
		t.Fatalf("container removal with no container = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// --- a list the gateway keeps --------------------------------------------------
//
// The gateway's own tests (netx) load what it renders against a recorder and
// a throwaway namespace. Here it is a stand-in that keeps what it is told, so
// the order of a change and what it is audited with can be read without a
// table being loaded anywhere.

type fakeKeeper struct {
	mu      sync.Mutex
	entries []netx.PortAllowlistSpec
	calls   []string
	// refuse fails a set with this error, as a host without nft would.
	refuse error
}

func (k *fakeKeeper) did() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls...)
}

func (k *fakeKeeper) KeptPorts() ([]netx.PortAllowlistSpec, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]netx.PortAllowlistSpec(nil), k.entries...), nil
}

func (k *fakeKeeper) PortAllowlists(context.Context) (netx.PortAllowlistReading, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := netx.PortAllowlistReading{Available: true, Loaded: true, AlsoAdmitted: []string{"100.64.0.0/10"}, Entries: []netx.PortAllowlistView{}}
	for _, e := range k.entries {
		out.Entries = append(out.Entries, netx.PortAllowlistView{PortAllowlistSpec: e, Enforced: true, Refused: 12})
	}
	return out, nil
}

func (k *fakeKeeper) PortAllowlistRules(req netx.PortAllowlistRequest) ([]string, error) {
	return []string{"drop from all but " + strings.Join(req.Sources, ", ")}, nil
}

func (k *fakeKeeper) SetPortAllowlist(_ context.Context, req netx.PortAllowlistRequest, actor string) (netx.PortAllowlistSpec, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, "set "+req.Owner+" "+strconv.Itoa(req.Port)+" "+strings.Join(req.Sources, ","))
	if k.refuse != nil {
		return netx.PortAllowlistSpec{}, k.refuse
	}
	sources, err := netx.NormalizePortSources(req.Sources)
	if err != nil {
		return netx.PortAllowlistSpec{}, err
	}
	entry := netx.PortAllowlistSpec{ID: 7, Name: req.Name, Owner: req.Owner, Protocol: req.Protocol, Port: req.Port, Sources: sources}
	entry.CreatedBy = actor
	k.entries = append(slices.DeleteFunc(k.entries, func(e netx.PortAllowlistSpec) bool { return e.Owner == req.Owner }), entry)
	return entry, nil
}

func (k *fakeKeeper) RemovePortAllowlist(_ context.Context, owner string) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, "remove "+owner)
	before := len(k.entries)
	k.entries = slices.DeleteFunc(k.entries, func(e netx.PortAllowlistSpec) bool { return e.Owner == owner })
	return len(k.entries) != before, nil
}

// reachHarness is a Postgres container this dashboard started, published on
// hostIP, behind the routes with the audit middleware, the gateway a
// stand-in and no host firewall at all.
func reachHarness(t *testing.T, hostIP string) (*connHarness, *fakeKeeper, http.Handler, int64) {
	t.Helper()
	h := newConnHarness(t)
	origNet, origSec := h.s.modules.network, h.s.modules.netsec
	t.Cleanup(func() { h.s.modules.network, h.s.modules.netsec = origNet, origSec })
	h.s.modules.network, h.s.modules.netsec = nil, nil
	keeper := &fakeKeeper{}
	h.s.dbConns.keeper = keeper
	h.engine.containers = []*fakeDBContainer{{
		id: "c0ffee", name: "shop-db", image: "postgres:17", state: "running",
		hostIP: hostIP, hostPort: 5432, port: 5432, env: []string{"POSTGRES_PASSWORD=pw"},
	}}
	id := h.add("shop", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:5432/shop?sslmode=disable")
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(h.s.Audit))
	h.s.mountDatabaseRoutes(r)
	return h, keeper, r, id
}

// engineChanged is whether the test's Docker was asked to do anything to a
// container: it records every verb and removal, and no read.
func engineChanged(h *connHarness) bool { return len(h.engine.did()) > 0 }

func auditDetail(t *testing.T, s *Server) map[string]any {
	t.Helper()
	entry := lastAudit(t, s, "database.access.change")
	// A refused request's entry carries the error after the detail.
	var detail map[string]any
	if err := json.NewDecoder(strings.NewReader(entry.Detail)).Decode(&detail); err != nil {
		t.Fatalf("detail %q: %v", entry.Detail, err)
	}
	return detail
}

// A port already published on every interface is kept to a list without a
// recreate: the gateway starts refusing the rest, nothing restarts, and the
// change is audited with the list.
func TestAListOnAPortOnEveryInterfaceIsKeptWithoutARecreate(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "0.0.0.0")
	rec := do(t, router, http.MethodPut, pathf("/databases/%d/access", id),
		`{"exposure":"allowlist","allow":["203.0.113.7","198.51.100.9/24"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("keeping a list = %d %s", rec.Code, rec.Body.String())
	}
	if got := keeper.did(); !slices.Equal(got, []string{"set database:shop-db 5432 198.51.100.0/24,203.0.113.7"}) {
		t.Fatalf("the gateway was told %v", got)
	}
	if engineChanged(h) {
		t.Fatalf("a list on an open port recreated the container: %v", h.engine.did())
	}
	var got struct {
		Access   dbAccess `json:"access"`
		Gateway  string   `json:"gateway"`
		Firewall string   `json:"firewall"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Gateway != "kept" || got.Firewall != "none" || got.Access.Exposure != exposureAllowlist ||
		!slices.Equal(got.Access.Allow, []string{"198.51.100.0/24", "203.0.113.7"}) || !got.Access.Gateway.Enforced {
		t.Fatalf("answer = %+v", got)
	}
	detail := auditDetail(t, h.s)
	if detail["from"] != "public" || detail["to"] != "allowlist" || detail["gateway"] != "kept" || fmt.Sprint(detail["allow"]) != "[198.51.100.0/24 203.0.113.7]" {
		t.Fatalf("audited as %v", detail)
	}

	// Back to anywhere is the list taken away, still with no recreate.
	rec = do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"public"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"gateway":"released"`) {
		t.Fatalf("back to anywhere = %d %s", rec.Code, rec.Body.String())
	}
	if got := keeper.did(); len(got) != 2 || got[1] != "remove database:shop-db" || engineChanged(h) {
		t.Fatalf("the gateway was told %v, Docker %v", got, h.engine.did())
	}
	if detail := auditDetail(t, h.s); detail["from"] != "allowlist" || fmt.Sprint(detail["allowBefore"]) != "[198.51.100.0/24 203.0.113.7]" {
		t.Fatalf("audited as %v", detail)
	}
}

// The reading names the list, who else gets in first, and the address the
// request came from for the form to offer.
func TestTheReadingNamesTheListAndWhoElseGetsIn(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "0.0.0.0")
	keeper.entries = []netx.PortAllowlistSpec{{ID: 7, Owner: "database:shop-db", Name: "shop-db", Protocol: "tcp", Port: 5432, Sources: []string{"203.0.113.7"}}}
	got := readJSON[dbAccess](t, do(t, router, http.MethodGet, pathf("/databases/%d/access", id), ""))
	if got.Exposure != exposureAllowlist || !slices.Equal(got.Allow, []string{"203.0.113.7"}) || !got.Gateway.Available ||
		got.Gateway.Refused != 12 || !slices.Equal(got.Gateway.AlsoAdmitted, []string{"100.64.0.0/10"}) || got.Addresses == nil || got.Client == "" {
		t.Fatalf("reading = %+v", got)
	}
	// The fleet and the summary say the same of it.
	conn, dsn, err := h.s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if place := h.s.newDBHostView(t.Context()).place(t.Context(), conn, dsn); place.Exposure != exposureAllowlist {
		t.Fatalf("placed as %s", place.Exposure)
	}
	// Without the network module there is nothing to keep a list with, and
	// the reading says so rather than offering one.
	h.s.dbConns.keeper = nil
	got = readJSON[dbAccess](t, do(t, router, http.MethodGet, pathf("/databases/%d/access", id), ""))
	if got.Gateway.Available || got.Gateway.Reason == "" || got.Exposure != exposurePublic {
		t.Fatalf("no gateway = %+v", got.Gateway)
	}
}

// Everything that can be refused is refused before the gateway or Docker is
// asked anything.
func TestAReachThatCannotBeKeptIsRefusedBeforeAnythingChanges(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "127.0.0.1")
	for _, c := range []struct {
		name, body, says string
		code             int
	}{
		{"an unknown answer", `{"exposure":"everyone"}`, "exposure must be", http.StatusBadRequest},
		{"an empty list", `{"exposure":"allowlist","allow":[]}`, "at least one", http.StatusBadRequest},
		{"every address as a list", `{"exposure":"allowlist","allow":["0.0.0.0/0"]}`, "choose Anywhere", http.StatusBadRequest},
		{"words for an address", `{"exposure":"allowlist","allow":["the office"]}`, "not an IP address", http.StatusBadRequest},
		{"an address this machine does not have", `{"exposure":"private","address":"192.0.2.55"}`, "not one of this machine's", http.StatusBadRequest},
	} {
		rec := do(t, router, http.MethodPut, pathf("/databases/%d/access", id), c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.says) {
			t.Errorf("%s = %d %s", c.name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		rec = do(t, router, http.MethodPost, pathf("/databases/%d/access/preview", id), c.body)
		if rec.Code != c.code {
			t.Errorf("%s previewed as %d", c.name, rec.Code)
		}
	}
	h.s.dbConns.keeper = nil
	rec := do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"allowlist","allow":["203.0.113.7"]}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "gateway_unavailable") {
		t.Errorf("a list with no gateway = %d %s", rec.Code, rec.Body.String())
	}
	if len(keeper.did()) != 0 || engineChanged(h) {
		t.Fatalf("a refusal changed something: gateway %v, Docker %v", keeper.did(), h.engine.did())
	}
}

// A list that cannot be kept stops the change before the binding is touched;
// a recreate that fails takes the list it had just kept away again.
func TestAFailedStepLeavesThePortAsItWas(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "127.0.0.1")
	keeper.refuse = &netx.UnavailableError{Tool: "nft", Package: "nftables"}
	rec := do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"allowlist","allow":["203.0.113.7"]}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "nftables") {
		t.Fatalf("a gateway that cannot load = %d %s", rec.Code, rec.Body.String())
	}
	if engineChanged(h) {
		t.Fatalf("the container was recreated behind a list that was never kept: %v", h.engine.did())
	}

	// The test's Docker creates nothing, so the recreate fails after the list
	// went in: the list comes out again and the failure is the answer.
	keeper.refuse = nil
	rec = do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"allowlist","allow":["203.0.113.7"]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a failed recreate answered %d %s", rec.Code, rec.Body.String())
	}
	if got := keeper.did(); !slices.Equal(got[len(got)-2:], []string{"set database:shop-db 5432 203.0.113.7", "remove database:shop-db"}) {
		t.Fatalf("the gateway was told %v", got)
	}
	if kept, _ := keeper.KeptPorts(); len(kept) != 0 {
		t.Fatalf("a list outlived the change it was for: %+v", kept)
	}
	if detail := auditDetail(t, h.s); detail["gateway"] != "restored" || detail["error"] == nil {
		t.Fatalf("audited as %v", detail)
	}
}

// The preview lists what the change would do and does none of it.
func TestThePreviewSaysWhatWouldRunAndRunsNothing(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "127.0.0.1")
	got := readJSON[dbAccessPreview](t, do(t, router, http.MethodPost, pathf("/databases/%d/access/preview", id),
		`{"exposure":"allowlist","allow":["203.0.113.7","2001:db8::1"]}`))
	kinds := []string{}
	for _, step := range got.Steps {
		kinds = append(kinds, step.Kind)
	}
	if !slices.Equal(kinds, []string{"gateway", "recreate", "firewall"}) || !got.Recreate || got.To.HostIP != "" ||
		!slices.Equal(got.Steps[0].Lines, []string{"drop from all but 203.0.113.7, 2001:db8::1"}) ||
		!strings.Contains(got.Steps[1].Description, "every interface, IPv4 and IPv6") || got.Firewall != "none" {
		t.Fatalf("preview = %+v", got)
	}
	if len(keeper.did()) != 0 || engineChanged(h) {
		t.Fatalf("a preview changed something: gateway %v, Docker %v", keeper.did(), h.engine.did())
	}
	if entries, _, _ := h.s.Audit.List(t.Context(), audit.Filter{Action: "database.access.change", Limit: 1}); len(entries) != 0 {
		t.Fatalf("a preview was audited as a change: %+v", entries)
	}
}

// Removing the container takes its list with it: left, it would refuse
// whatever published that port next.
func TestRemovingTheContainerReleasesItsList(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "0.0.0.0")
	keeper.entries = []netx.PortAllowlistSpec{{ID: 7, Owner: "database:shop-db", Name: "shop-db", Protocol: "tcp", Port: 5432, Sources: []string{"203.0.113.7"}}}
	req := httptest.NewRequest(http.MethodDelete, pathf("/databases/%d/database", id), strings.NewReader(`{"removeContainer":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Confirm", "shop")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("removing the container = %d %s", rec.Code, rec.Body.String())
	}
	if got := keeper.did(); !slices.Equal(got, []string{"remove database:shop-db"}) {
		t.Fatalf("the gateway was told %v", got)
	}
	if got := h.engine.did(); !slices.Equal(got, []string{"remove shop-db"}) {
		t.Fatalf("Docker was asked %v", got)
	}
}

// Who may read and change a server's reach: the reading and the preview list
// containers and the firewall, so they are an administrator's, and the change
// recreates a container, so it also takes the destructive capability.
func TestTheReachRoutesAreAnAdministrators(t *testing.T) {
	h, keeper, _, id := reachHarness(t, "0.0.0.0")
	limited := h.as(auth.RoleLimited)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/databases/%d/access", ""},
		{http.MethodPost, "/databases/%d/access/preview", `{"exposure":"local"}`},
		{http.MethodPut, "/databases/%d/access", `{"exposure":"allowlist","allow":["203.0.113.7"]}`},
	} {
		if rec := do(t, limited, c.method, pathf(c.path, id), c.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a limited role = %d", c.method, c.path, rec.Code)
		}
	}
	if len(keeper.did()) != 0 || engineChanged(h) {
		t.Fatalf("a refused role changed something: gateway %v, Docker %v", keeper.did(), h.engine.did())
	}
}

// The string for another machine names the address the port is published on
// when that is one of the host's own: the public address would lead nowhere.
func TestTheStringForAnotherMachineNamesTheBoundAddress(t *testing.T) {
	h, _, router, id := reachHarness(t, "100.101.12.4")
	rec := do(t, router, http.MethodGet, pathf("/databases/%d/url?target=public", id), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "@100.101.12.4:5432/") {
		t.Fatalf("the string for another machine = %d %s", rec.Code, rec.Body.String())
	}
	got := readJSON[dbAccess](t, do(t, h.as(auth.RoleAdmin), http.MethodGet, pathf("/databases/%d/access", id), ""))
	if got.Exposure != exposurePrivate || got.Address != "100.101.12.4" {
		t.Fatalf("reading = %s %q", got.Exposure, got.Address)
	}
}

// A list a container left behind when it was removed from the Docker page
// would hold its port against every list after it. The next database to keep
// that port to a list takes it away; one whose container is still there is
// left to the gateway to refuse.
func TestAListLeftByARemovedContainerGivesWayToTheNext(t *testing.T) {
	h, keeper, router, id := reachHarness(t, "0.0.0.0")
	keeper.entries = []netx.PortAllowlistSpec{{ID: 3, Owner: "database:gone-db", Name: "gone-db", Protocol: "tcp", Port: 5432, Sources: []string{"192.0.2.1"}}}
	rec := do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"allowlist","allow":["203.0.113.7"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("keeping a list = %d %s", rec.Code, rec.Body.String())
	}
	if got := keeper.did(); !slices.Equal(got, []string{"remove database:gone-db", "set database:shop-db 5432 203.0.113.7"}) {
		t.Fatalf("the gateway was told %v", got)
	}

	h.engine.containers = append(h.engine.containers, &fakeDBContainer{id: "beef", name: "other-db", image: "postgres:17", state: "exited"})
	keeper.entries = []netx.PortAllowlistSpec{{ID: 4, Owner: "database:other-db", Name: "other-db", Protocol: "tcp", Port: 5432, Sources: []string{"192.0.2.1"}}}
	keeper.calls = nil
	do(t, router, http.MethodPut, pathf("/databases/%d/access", id), `{"exposure":"allowlist","allow":["203.0.113.8"]}`)
	if got := keeper.did(); slices.Contains(got, "remove database:other-db") {
		t.Fatalf("the list of a container that is still there was taken away: %v", got)
	}
}
