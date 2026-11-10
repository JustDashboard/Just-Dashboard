package httpx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// fullStoreError is SQLite's own answer to a write with no room left, from a
// database held to a few pages.
func fullStoreError(t *testing.T) error {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "small.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA max_page_count = 24; CREATE TABLE sessions (token BLOB)`); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if _, err := db.Exec(`INSERT INTO sessions VALUES (randomblob(4096))`); err != nil {
			return err
		}
	}
	t.Fatal("a database held to 24 pages never filled")
	return nil
}

func answer(t *testing.T, err error) (int, APIError) {
	t.Helper()
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil), err)
	var body struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body.Error
}

func TestAWriteRefusedForSpaceIsAnsweredAsAFullDisk(t *testing.T) {
	full := fullStoreError(t)
	// The sign-in returns the store's error bare; other handlers wrap theirs.
	for name, err := range map[string]error{
		"bare":     fmt.Errorf("start session: %w", full),
		"internal": Internal(full),
	} {
		status, body := answer(t, err)
		if status != http.StatusInsufficientStorage || body.Code != "disk_full" {
			t.Errorf("%s: answered %d %q, want 507 disk_full", name, status, body.Code)
		}
		if body.Message == "" || body.Message == "internal error" {
			t.Errorf("%s: message %q does not say the disk is full", name, body.Message)
		}
	}
}

func TestOtherFailuresKeepTheirAnswers(t *testing.T) {
	if status, body := answer(t, errors.New("boom")); status != http.StatusInternalServerError || body.Code != "internal" || body.Message != "internal error" {
		t.Errorf("an unknown error answered %d %q %q, want 500 internal", status, body.Code, body.Message)
	}
	// A handler's own answer stands even with a full disk underneath it.
	chosen := Wrap(http.StatusBadGateway, "docker_unavailable", fmt.Errorf("pull: %w", fullStoreError(t)))
	if status, body := answer(t, chosen); status != http.StatusBadGateway || body.Code != "docker_unavailable" {
		t.Errorf("a handler's own error answered %d %q, want it unchanged", status, body.Code)
	}
}
