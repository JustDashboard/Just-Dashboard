package api

import (
	"net/http"
	"strings"
	"testing"
)

// The extension routes over the whole router, on an engine that has none: who
// reaches each route is decided before the engine is asked, and an engine
// with no extensions answers with a sentence rather than a failure.
func TestExtensionRoutesNeedTheirCapability(t *testing.T) {
	h := newOpsHarness(t)

	for _, path := range []string{"/server/extensions", "/server/extensions/pg_trgm"} {
		rec := h.viewer.do(http.MethodGet, h.base+path, "", nil)
		if rec.Code == http.StatusForbidden || rec.Code >= http.StatusInternalServerError {
			t.Errorf("a viewer's GET %s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := h.viewer.do(http.MethodGet, h.base+"/server/extensions/pg_trgm", "", nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "sqlite has no extensions") {
		t.Errorf("an extension's detail on SQLite = %d %s", rec.Code, rec.Body.String())
	}

	for _, c := range []struct{ method, path, body, refusal string }{
		{http.MethodPost, "/server/extensions", `{"name":"pg_trgm","schema":"public","cascade":true}`, "sqlite has no extensions"},
		{http.MethodPost, "/server/extensions?preview=1", `{"name":"pg_trgm"}`, "sqlite has no extensions"},
		{http.MethodPatch, "/server/extensions/pg_trgm", `{"version":"1.6"}`, "sqlite has no extensions"},
		{http.MethodPost, "/server/preload", `{"library":"pg_cron"}`, "sqlite loads no libraries at start"},
		{http.MethodPost, "/server/preload?preview=1", `{"library":"pg_cron"}`, "sqlite loads no libraries at start"},
		{http.MethodDelete, "/server/extensions/pg_trgm?cascade=1", ``, "sqlite has no extensions"},
	} {
		if rec := h.viewer.do(c.method, h.base+c.path, c.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("a viewer's %s %s = %d %s, want 403", c.method, c.path, rec.Code, rec.Body.String())
		}
		// service.control starts a server; it does not change what the server is.
		if rec := h.worker.do(c.method, h.base+c.path, c.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("a limited account's %s %s = %d %s, want 403", c.method, c.path, rec.Code, rec.Body.String())
		}
		rec := h.admin.do(c.method, h.base+c.path, c.body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.refusal) {
			t.Errorf("an administrator's %s %s on SQLite = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	// A preview runs nothing, so a refused one is not on the trail; the same
	// request sent to run is.
	var previews, runs int
	if err := h.s.Store.DB.QueryRow(`SELECT
	    COUNT(CASE WHEN path LIKE '%preview%' THEN 1 END), COUNT(CASE WHEN path NOT LIKE '%preview%' THEN 1 END)
	  FROM audit_log WHERE path LIKE '%/server/extensions%' OR path LIKE '%/server/preload%'`).Scan(&previews, &runs); err != nil {
		t.Fatal(err)
	}
	if previews != 0 {
		t.Errorf("%d previews were audited", previews)
	}
	if runs == 0 {
		t.Error("no refused change was audited")
	}
}

// Neither a create, an update nor a drop of an extension takes a typed
// phrase: each is undone by its opposite, and a phrase on a routine act
// empties the phrase of meaning.
func TestExtensionRoutesDoNotAskForAPhrase(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/server/extensions", `{"name":"pg_trgm"}`},
		{http.MethodPatch, "/server/extensions/pg_trgm", `{}`},
		{http.MethodDelete, "/server/extensions/pg_trgm?cascade=1", ``},
		{http.MethodPost, "/server/preload", `{"library":"pg_cron"}`},
	} {
		rec := h.admin.do(c.method, h.base+c.path, c.body, nil)
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("%s %s asked for a phrase: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}
