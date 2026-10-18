package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
)

func TestNativeLogExportDisclosesItsSelectedLineCap(t *testing.T) {
	s := testServer(t)
	lines := make([]string, nativeLogExportLimit+1)
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for i := range lines {
		lines[i] = stamped(base.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("record-%05d", i))
	}
	serveFakeLogEngine(t, s, fakeLogContainer{id: "aaaaaaaaaaaa1111", name: "export-fixture", state: "running", lines: lines})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	rec := c.do(http.MethodGet, "/api/v1/logs/download?source=docker:export-fixture", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, evidence := range []string{"# Selected-line limit: 20000", "matching records: 20001", "# Export coverage: PARTIAL", "later matching lines are omitted", "record-00000", "record-19999"} {
		if !strings.Contains(body, evidence) {
			t.Errorf("downloaded artifact omits %q", evidence)
		}
	}
	if strings.Contains(body, "record-20000") {
		t.Fatal("native export did not honor its disclosed bound")
	}
}

func TestNativeLogExportPreservesScanAndSourceGapEvidence(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeNativeLogExportHeader(rec, "stack:app", logsx.KindStack, logsx.SearchOptions{}, false, &logsx.SearchResult{
		Complete: false, Files: []logsx.SearchedFile{{Name: "app-worker", Error: "logs unavailable"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{"native scan did not finish", "source app-worker could not be read: logs unavailable", "Export coverage: PARTIAL"} {
		if !strings.Contains(rec.Body.String(), evidence) {
			t.Errorf("artifact omits gap evidence %q", evidence)
		}
	}
}

func TestNativeLogExportRefusesUnreadableContainersBeforeDownloadHeaders(t *testing.T) {
	s := testServer(t)
	serveFakeLogEngine(t, s)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	rec := c.do(http.MethodGet, "/api/v1/logs/download?source=docker:gone", "", nil)
	if rec.Code == http.StatusOK || rec.Body.Len() == 0 || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("native source failure became a download: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestJournalExportDisclosesItsImplicitWindowAndRefusesCommandFailure(t *testing.T) {
	fakeJournalctl(t, sshJournal(time.Now().Add(-time.Minute))...)
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	rec := c.do(http.MethodGet, "/api/v1/logs/download?source=journal:", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "last 48 hours") || !strings.Contains(rec.Body.String(), "Export coverage: PARTIAL") {
		t.Fatalf("implicit journal bound missing: %d %s", rec.Code, rec.Body.String())
	}
	for _, window := range []string{"&boot=true", "&since=2026-01-01T00:00:00Z"} {
		rec := c.do(http.MethodGet, "/api/v1/logs/download?source=journal:"+window, "", nil)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "last 48 hours") {
			t.Fatalf("explicit journal bound was mislabeled: %d %s", rec.Code, rec.Body.String())
		}
	}
	binary, err := exec.LookPath("journalctl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'journal fixture read failed\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"journal:", "journal-id:sshd", "kernel:"} {
		rec := c.do(http.MethodGet, "/api/v1/logs/download?source="+source, "", nil)
		if rec.Code == http.StatusOK || rec.Body.Len() == 0 || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("journal command failure became a download: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
		}
	}
}

func TestFileLogExportRemainsUncappedAndRefusesAnUnreadableFile(t *testing.T) {
	c, root := logClient(t)
	path := filepath.Join(root, "export.log")
	content := strings.Repeat("retained file line\n", nativeLogExportLimit+1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := c.do(http.MethodGet, "/api/v1/logs/download?source="+path, "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != content {
		t.Fatalf("file export was capped or changed: status %d, bytes %d/%d", rec.Code, rec.Body.Len(), len(content))
	}
	rec = c.do(http.MethodGet, "/api/v1/logs/download?source="+filepath.Join(root, "gone.log"), "", nil)
	if rec.Code == http.StatusOK || rec.Body.Len() == 0 || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("missing file became an empty successful export: %d %s", rec.Code, rec.Body.String())
	}
	for _, window := range []string{"&since=10000-01-01T00:00:00Z", "&since=2026-10-14T12:00:00Z&until=2026-10-14T11:00:00Z"} {
		rec := c.do(http.MethodGet, "/api/v1/logs/download?source="+path+window, "", nil)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("invalid export window was not refused: %d %s", rec.Code, rec.Body.String())
		}
	}
}
