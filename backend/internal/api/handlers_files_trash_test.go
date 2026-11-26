package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

// A delete puts the entry in the Trash and says so; the Trash lists it, a
// restore puts it back, and only the destructive capability deletes for good.
func TestFileDeleteGoesToTheTrashAndComesBack(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	conf := filepath.Join(root, "etc", "nginx", "nginx.conf")

	w := c.do(http.MethodDelete, query("/api/v1/files/delete", map[string]string{"path": conf}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	var deleted deleteFileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if !deleted.Trashed || deleted.Item == nil || deleted.Item.OriginalPath != conf || deleted.Item.DeletedBy != "tester" {
		t.Fatalf("the delete does not say it trashed: %s", w.Body.String())
	}
	if _, err := os.Lstat(conf); !os.IsNotExist(err) {
		t.Fatal("the trashed file is still there")
	}

	w = c.do(http.MethodGet, "/api/v1/files/trash", "", nil)
	var listing files.TrashListing
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || len(listing.Items) != 1 {
		t.Fatalf("trash listing: %d %s", w.Code, w.Body.String())
	}

	// The place is taken again: a restore asks, then keeps both.
	os.WriteFile(conf, []byte("new"), 0o644)
	body := `{"id":"` + deleted.Item.ID + `"}`
	if w := c.do(http.MethodPost, "/api/v1/files/trash/restore", body, nil); w.Code != http.StatusConflict {
		t.Fatalf("restore over a taken name: %d %s", w.Code, w.Body.String())
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := reader.do(http.MethodPost, "/api/v1/files/trash/restore", body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a reader restored: %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/api/v1/files/trash/restore", `{"id":"`+deleted.Item.ID+`","conflict":"keep"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("restore keeping both: %d %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc", "nginx", "nginx (2).conf")); string(b) != "server {\n  listen 80;\n}\n" {
		t.Fatalf("restored as %q", b)
	}
	if w := c.do(http.MethodPost, "/api/v1/files/trash/restore", `{"id":"../../etc"}`, nil); w.Code != http.StatusNotFound {
		t.Fatalf("a restore of something not in the Trash: %d", w.Code)
	}

	// Permanent is the old behaviour, and a limited account cannot do it.
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	html := filepath.Join(root, "index.html")
	if w := limited.do(http.MethodDelete, query("/api/v1/files/delete", map[string]string{"path": html, "permanent": "true"}), "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account deleted for good: %d", w.Code)
	}
	w = c.do(http.MethodDelete, query("/api/v1/files/delete", map[string]string{"path": html, "permanent": "true"}), "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &deleted) != nil || deleted.Trashed {
		t.Fatalf("permanent delete: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, "/api/v1/files/trash", "", nil)
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Items) != 0 {
		t.Fatalf("a permanent delete went to the Trash: %+v", listing.Items)
	}

	// The configured root cannot go to the Trash; the check says why first.
	w = c.do(http.MethodGet, query("/api/v1/files/trash/check", map[string]string{"path": root}), "", nil)
	var plan files.TrashPlan
	if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Available || plan.Reason == "" {
		t.Fatalf("check of a root: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, query("/api/v1/files/delete", map[string]string{"path": root}), "", nil); w.Code != http.StatusConflict {
		t.Fatalf("trashing a root: %d %s", w.Code, w.Body.String())
	}
}

// Replace in the conflict dialog puts what was replaced in the Trash, for a
// move and for a copy, and the answer names it so the page can undo it.
func TestReplacingMoveAndCopyKeepWhatTheyReplace(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o600)

	w := c.do(http.MethodPost, "/api/v1/files/copy", `{"from":"`+a+`","to":"`+b+`","overwrite":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("copy over: %d %s", w.Code, w.Body.String())
	}
	var answer struct {
		Replaced replacedEntry `json:"replaced"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || answer.Replaced.Item == nil {
		t.Fatalf("the copy does not name what it replaced: %s", w.Body.String())
	}
	if got, _ := os.ReadFile(b); string(got) != "a" {
		t.Fatalf("b is %q", got)
	}
	if info, _ := os.Stat(b); info.Mode().Perm() != 0o600 {
		t.Fatalf("the replaced file's mode was not kept: %o", info.Mode().Perm())
	}
	kept := filepath.Join(answer.Replaced.Item.Folder, answer.Replaced.Item.ID, "b.txt")
	if got, _ := os.ReadFile(kept); string(got) != "b" {
		t.Fatalf("the Trash holds %q", got)
	}

	os.WriteFile(a, []byte("moved"), 0o644)
	w = c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+a+`","to":"`+b+`","overwrite":true}`, nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer.Replaced.Item == nil {
		t.Fatalf("move over: %d %s", w.Code, w.Body.String())
	}
	if got, _ := os.ReadFile(b); string(got) != "moved" {
		t.Fatalf("b is %q", got)
	}
	// A move with nothing in the way names what landed and nothing replaced.
	w = c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+b+`","to":"`+a+`","overwrite":true}`, nil)
	var plain movedResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &plain) != nil || plain.Identity == "" || plain.Replaced != nil {
		t.Fatalf("plain move: %d %s", w.Code, w.Body.String())
	}

	var listing files.TrashListing
	json.Unmarshal(c.do(http.MethodGet, "/api/v1/files/trash", "", nil).Body.Bytes(), &listing)
	if len(listing.Items) != 2 || !listing.Items[0].Replaced {
		t.Fatalf("trash after two replacements: %+v", listing.Items)
	}
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	if w := limited.do(http.MethodDelete, "/api/v1/files/trash", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account emptied the Trash: %d", w.Code)
	}
	if w := limited.do(http.MethodDelete, query("/api/v1/files/trash/item", map[string]string{"id": listing.Items[0].ID}), "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account deleted from the Trash: %d", w.Code)
	}
	if w := c.do(http.MethodDelete, query("/api/v1/files/trash/item", map[string]string{"id": listing.Items[0].ID}), "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete from the Trash: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, "/api/v1/files/trash", "", nil); w.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(c.do(http.MethodGet, "/api/v1/files/trash", "", nil).Body.Bytes(), &listing)
	if len(listing.Items) != 0 {
		t.Fatalf("emptied Trash holds %+v", listing.Items)
	}
}

// An undo waits its turn behind the one before it. A copy's undo puts the
// copy in the Trash and a move's moves it back, by name; with the identity
// the operation answered as `expect`, whatever took the name meanwhile is
// left alone.
func TestAQueuedUndoLeavesAnEntryThatTookTheNameAlone(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	a, b, copied := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt"), filepath.Join(root, "a copy.txt")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	identityOf := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var answer movedResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer.Identity == "" {
			t.Fatalf("no identity: %d %s", w.Code, w.Body)
		}
		return answer.Identity
	}
	trash := func(path, expect string) *httptest.ResponseRecorder {
		return c.do(http.MethodDelete, query("/api/v1/files/delete", map[string]string{"path": path, "recursive": "true", "expect": expect}), "", nil)
	}

	ours := identityOf(c.do(http.MethodPost, "/api/v1/files/copy", `{"from":"`+a+`","to":"`+copied+`"}`, nil))
	// Before the undo runs, another copy replaces ours.
	theirs := identityOf(c.do(http.MethodPost, "/api/v1/files/copy", `{"from":"`+b+`","to":"`+copied+`","overwrite":true}`, nil))
	if theirs == ours {
		t.Fatal("the replacing copy has the identity of the one it replaced")
	}
	if w := trash(copied, ours); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "entry_changed") {
		t.Fatalf("the undo trashed what took the name: %d %s", w.Code, w.Body)
	}
	if got, _ := os.ReadFile(copied); string(got) != "b" {
		t.Fatalf("what took the name is %q", got)
	}
	if w := trash(copied, theirs); w.Code != http.StatusOK {
		t.Fatalf("the entry the identity names: %d %s", w.Code, w.Body)
	}

	// A move keeps the entry's identity; a file renamed over it does not.
	moved := filepath.Join(root, "moved.txt")
	before, err := s.modules.hostFiles.Identity(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := identityOf(c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+a+`","to":"`+moved+`"}`, nil)); got != before {
		t.Fatalf("the move answered %q for an entry that was %q", got, before)
	}
	elsewhere := filepath.Join(root, "elsewhere.txt")
	os.WriteFile(elsewhere, []byte("someone else's"), 0o644)
	os.Rename(elsewhere, moved)
	back := `{"from":"` + moved + `","to":"` + a + `","expect":"` + before + `"}`
	if w := c.do(http.MethodPost, "/api/v1/files/move", back, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "entry_changed") {
		t.Fatalf("the undo moved what took the name: %d %s", w.Code, w.Body)
	}
	if _, err := os.Lstat(a); !os.IsNotExist(err) {
		t.Fatal("a refused move moved something")
	}
	if got, _ := os.ReadFile(moved); string(got) != "someone else's" {
		t.Fatalf("what took the name is %q", got)
	}
	// Without expect a move is as it always was.
	if w := c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+moved+`","to":"`+a+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("a move without expect: %d %s", w.Code, w.Body)
	}
}

// The editor's save writes a new file and renames it over the name, so the
// file it saved has another identity than the one an undo was given. The
// write answers both, the one it replaced and the one it left, and an undo
// carried from the first to the second still goes through; the old identity
// is still refused, and so is anything else at the name.
func TestASaveAnswersTheIdentityItReplacedAndTheOneItLeft(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	a, b := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	os.WriteFile(a, []byte("a"), 0o644)
	var moved movedResponse
	w := c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+a+`","to":"`+b+`"}`, nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &moved) != nil || moved.Identity == "" {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]any{"path": b, "content": "edited"})
	w = c.do(http.MethodPut, "/api/v1/files/write", string(body), nil)
	var saved struct {
		Name             string `json:"name"`
		Revision         string `json:"revision"`
		Identity         string `json:"identity"`
		PreviousIdentity string `json:"previousIdentity"`
	}
	// The write answers as it did, the entry and its revision, and now the
	// identities too.
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Name != "b.txt" || saved.Revision == "" {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if saved.PreviousIdentity != moved.Identity || saved.Identity == "" || saved.Identity == moved.Identity {
		t.Fatalf("save answered previous %q and identity %q for a file that was %q", saved.PreviousIdentity, saved.Identity, moved.Identity)
	}
	back := func(expect string) *httptest.ResponseRecorder {
		return c.do(http.MethodPost, "/api/v1/files/move", `{"from":"`+b+`","to":"`+a+`","expect":"`+expect+`"}`, nil)
	}
	if w := back(moved.Identity); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "entry_changed") {
		t.Fatalf("the identity from before the save: %d %s", w.Code, w.Body)
	}
	if w := back(saved.Identity); w.Code != http.StatusOK {
		t.Fatalf("the undo carried through the save: %d %s", w.Code, w.Body)
	}
	if got, _ := os.ReadFile(a); string(got) != "edited" {
		t.Fatalf("a is %q", got)
	}

	// A new file has no identity before it.
	created := filepath.Join(root, "new.txt")
	body, _ = json.Marshal(map[string]any{"path": created, "content": "new"})
	w = c.do(http.MethodPut, "/api/v1/files/write", string(body), nil)
	saved.PreviousIdentity, saved.Identity = "", ""
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Identity == "" || saved.PreviousIdentity != "" {
		t.Fatalf("new file: %d %s", w.Code, w.Body)
	}
}
