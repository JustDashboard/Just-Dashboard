package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func TestNetworkCapabilitiesReadAndProbePermissions(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	w := viewer.do(http.MethodGet, "/api/v1/network/capabilities", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("read = %d %s", w.Code, w.Body.String())
	}
	var view netx.HostSupport
	decodeNetworkBody(t, w.Body.Bytes(), &view)
	if len(view.Tools) == 0 || len(view.Notes) == 0 {
		t.Fatalf("missing support report: %+v", view)
	}
	for _, tool := range []string{"capabilities", "route", "mtu", "capture", "wol"} {
		w := viewer.do(http.MethodPost, "/api/v1/network/probe", `{"tool":"`+tool+`"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("viewer ran %s: %d", tool, w.Code)
		}
	}
	w = admin.do(http.MethodPost, "/api/v1/network/probe", `{"tool":"capabilities"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Boot persistence") {
		t.Fatalf("support probe = %d %s", w.Code, w.Body.String())
	}
}

func TestNetworkLANProbesRejectMalformedInputBeforeHostWork(t *testing.T) {
	admin, _, _ := networkClients(t)
	for _, body := range []string{
		`{"tool":"route","target":"example.com"}`,
		`{"tool":"mtu","target":"-help"}`,
		`{"tool":"wol","target":"ff:ff:ff:ff:ff:ff","option":"eno1"}`,
		`{"tool":"wol","target":"02:11:22:33:44:55","option":"a/b"}`,
		`{"tool":"capture","target":"eno1","option":"tcp or port 443"}`,
		`{"tool":"capture","target":"a/b","option":"tcp"}`,
	} {
		w := admin.do(http.MethodPost, "/api/v1/network/probe", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
}
