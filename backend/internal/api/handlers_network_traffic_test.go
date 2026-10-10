package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// Budgets and annotations follow the capability split of the rest of the
// section: a budget is a reading anyone may see, setting one is an
// administrator's, clearing one is destructive, and the annotations name who
// changed what and which incidents were saved.
func TestTrafficBudgetAndAnnotationRoutesAreGatedAndAudited(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/network/traffic/quotas/lo", `{"period":"day","limitBytes":1073741824}`},
		{http.MethodDelete, "/api/v1/network/traffic/quotas/lo", ``},
		{http.MethodGet, "/api/v1/network/traffic/annotations?window=1h", ``},
		{http.MethodGet, "/api/v1/network/traffic/processes", ``},
		{http.MethodGet, "/api/v1/connections/198.51.100.23", ``},
	} {
		if w := viewer.do(c.method, c.path, c.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("readonly %s %s = %d", c.method, c.path, w.Code)
		}
	}
	if w := viewer.do(http.MethodGet, "/api/v1/network/traffic/quotas", "", nil); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("readonly budgets = %d %s", w.Code, w.Body)
	}

	if w := admin.do(http.MethodPut, "/api/v1/network/traffic/quotas/veth1234", `{"period":"day","limitBytes":1073741824}`, nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not recorded") {
		t.Fatalf("a veth budget = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPut, "/api/v1/network/traffic/quotas/lo", `{"period":"day","limitBytes":12}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a tiny budget = %d %s", w.Code, w.Body)
	}
	w := admin.do(http.MethodPut, "/api/v1/network/traffic/quotas/lo", `{"period":"day","limitBytes":1073741824}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	var q netx.InterfaceQuota
	decodeNetworkBody(t, w.Body.Bytes(), &q)
	if q.Iface != "lo" || q.Period != "day" || q.Direction != "both" || q.Enforced || q.CreatedBy != "admin" {
		t.Fatalf("budget = %+v", q)
	}
	var list []netx.InterfaceQuota
	decodeNetworkBody(t, viewer.do(http.MethodGet, "/api/v1/network/traffic/quotas", "", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].Iface != "lo" {
		t.Fatalf("list = %+v", list)
	}
	if w := admin.do(http.MethodDelete, "/api/v1/network/traffic/quotas/lo", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("clear = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodDelete, "/api/v1/network/traffic/quotas/lo", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("clear twice = %d %s", w.Code, w.Body)
	}

	var annotations []netx.TrafficAnnotation
	w = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/network/traffic/annotations?from=%d&to=%d", time.Now().Add(-time.Hour).Unix(), time.Now().Unix()+5), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("annotations = %d %s", w.Code, w.Body)
	}
	decodeNetworkBody(t, w.Body.Bytes(), &annotations)
	seen := map[string]bool{}
	for _, a := range annotations {
		seen[a.Title] = true
	}
	// The budget's own audit entries are among the changes it marks.
	if !seen["network.traffic.quota.set lo"] || !seen["network.traffic.quota.clear lo"] {
		t.Fatalf("annotations = %+v", annotations)
	}
	if w := admin.do(http.MethodGet, "/api/v1/network/traffic/annotations?from=200&to=100", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a backwards window = %d", w.Code)
	}
}

// An explicit window is honoured and carries the percentiles; a window the
// recorder cannot answer is refused before SQL runs.
func TestTrafficHistoryTakesAnExplicitWindow(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	now := time.Now().Unix()
	path := fmt.Sprintf("/api/v1/network/traffic/history?from=%d&to=%d&points=60", now-7200, now-3600)
	w := viewer.do(http.MethodGet, path, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("history = %d %s", w.Code, w.Body)
	}
	var h netx.History
	decodeNetworkBody(t, w.Body.Bytes(), &h)
	if h.From != now-7200 || h.To != now-3600 || h.Interfaces == nil {
		t.Fatalf("history = %+v", h)
	}
	for _, bad := range []string{
		fmt.Sprintf("?from=%d&to=%d", now, now-60),
		fmt.Sprintf("?from=%d&to=%d", now-40*86400, now),
		"?from=yesterday",
		"?window=forever",
		"?points=1",
	} {
		if w := admin.do(http.MethodGet, "/api/v1/network/traffic/history"+bad, "", nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, w.Code)
		}
	}
	// The live answer says how old its newest reading is.
	var live netx.LiveTraffic
	decodeNetworkBody(t, viewer.do(http.MethodGet, "/api/v1/network/traffic/live", "", nil).Body.Bytes(), &live)
	if live.StepSeconds != 2 || live.Now == 0 || live.Series == nil || live.TCP == nil || live.Latency != nil {
		t.Fatalf("live = %+v", live)
	}
}

func TestContainerTrafficDetailAnswersForRecordedNamesOnly(t *testing.T) {
	admin, _, _ := networkClients(t)
	if w := admin.do(http.MethodGet, "/api/v1/network/traffic/containers/nothing-here", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("an unrecorded container = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodGet, "/api/v1/network/traffic/containers/web?window=forever", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad window = %d", w.Code)
	}
}
