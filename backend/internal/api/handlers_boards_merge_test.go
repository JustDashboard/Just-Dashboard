package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func el(id string, version, nonce int64, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"id":%q,"type":"rectangle","version":%d,"versionNonce":%d%s}`, id, version, nonce, extra)
}

func list(elements ...string) string { return "[" + strings.Join(elements, ",") + "]" }

func elementIDs(t *testing.T, c *client, path string) []string {
	t.Helper()
	scene, _ := storedScene(t, c, path)
	ids := make([]string, len(scene.Elements))
	for i, e := range scene.Elements {
		ids[i], _ = e["id"].(string)
	}
	return ids
}

func remoteIDs(t *testing.T, saved boardSaved) []string {
	t.Helper()
	var elements []map[string]any
	if err := json.Unmarshal(saved.RemoteElements, &elements); err != nil {
		t.Fatalf("remoteElements %q: %v", saved.RemoteElements, err)
	}
	ids := []string{}
	for _, e := range elements {
		ids = append(ids, e["id"].(string))
	}
	return ids
}

func TestBoardMergeKeepsTheNewerElement(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		storedV, storedN        int64
		incomingV, incomingN    int64
		storedWins, wantInReply bool
	}{
		{"higher stored version wins", 5, 9, 4, 1, true, true},
		{"higher incoming version wins", 4, 1, 5, 9, false, false},
		{"equal version, lower stored nonce wins", 5, 3, 5, 7, true, true},
		{"equal version, higher stored nonce loses", 5, 7, 5, 3, false, false},
		{"identical element is not sent back", 5, 3, 5, 3, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, _ := newClient(t)
			_, path := newTestBoard(t, admin)
			decodeSaved(t, saveScene(admin, path, 1, list(el("x", tc.storedV, tc.storedN, `"tag":"stored"`)), `{}`, ""))
			saved := decodeSaved(t, saveScene(admin, path, 2, list(el("x", tc.incomingV, tc.incomingN, `"tag":"incoming"`)), `{}`, `,"merge":true`))

			scene, _ := storedScene(t, admin, path)
			want := "incoming"
			if tc.storedWins {
				want = "stored"
			}
			if len(scene.Elements) != 1 || scene.Elements[0]["tag"] != want {
				t.Errorf("kept %v, want the %s copy", scene.Elements, want)
			}
			if got := len(remoteIDs(t, saved)) == 1; got != tc.wantInReply {
				t.Errorf("remoteElements = %s, want it to carry the element: %v", saved.RemoteElements, tc.wantInReply)
			}
		})
	}
}

func TestBoardMergeUnionsElementsAndAnswersWhatTheSaverLacks(t *testing.T) {
	admin, _ := newClient(t)
	_, path := newTestBoard(t, admin)
	decodeSaved(t, saveScene(admin, path, 1, list(
		el("onlyServer", 1, 1, `"index":"a0"`),
		el("shared", 2, 1, `"index":"a1"`),
	), `{}`, ""))

	saved := decodeSaved(t, saveScene(admin, path, 2, list(
		el("shared", 3, 1, `"index":"a1"`),
		el("onlyClient", 1, 1, `"index":"a2"`),
	), `{}`, `,"merge":true`))
	if saved.Revision != 3 {
		t.Errorf("revision = %d", saved.Revision)
	}
	if got := strings.Join(elementIDs(t, admin, path), ","); got != "onlyServer,shared,onlyClient" {
		t.Errorf("elements = %s", got)
	}
	if got := strings.Join(remoteIDs(t, saved), ","); got != "onlyServer" {
		t.Errorf("remoteElements = %s", saved.RemoteElements)
	}

	// Nothing differs: the field is still there, empty.
	rec := saveScene(admin, path, 3, list(
		el("onlyServer", 1, 1, `"index":"a0"`), el("shared", 3, 1, `"index":"a1"`), el("onlyClient", 1, 1, `"index":"a2"`),
	), `{}`, `,"merge":true`)
	if !strings.Contains(rec.Body.String(), `"remoteElements":[]`) {
		t.Errorf("merge answer = %s", rec.Body.String())
	}
}

func TestBoardMergeOrdersByIndex(t *testing.T) {
	admin, _ := newClient(t)
	_, path := newTestBoard(t, admin)
	decodeSaved(t, saveScene(admin, path, 1, list(el("legacy1", 1, 1, ""), el("c", 1, 1, `"index":"a2"`)), `{}`, ""))
	decodeSaved(t, saveScene(admin, path, 2, list(
		el("legacy2", 1, 1, `"index":null`), el("a", 1, 1, `"index":"Zz"`), el("b", 1, 1, `"index":"a1"`), el("empty", 1, 1, `"index":""`),
	), `{}`, `,"merge":true`))
	if got := strings.Join(elementIDs(t, admin, path), ","); got != "a,b,c,legacy1,legacy2,empty" {
		t.Errorf("order = %s", got)
	}
}

func TestBoardTombstones(t *testing.T) {
	admin, s := newClient(t)
	_, path := newTestBoard(t, admin)
	now := time.Now().UnixMilli()
	old := now - int64(25*time.Hour/time.Millisecond)
	recent := now - int64(time.Hour/time.Millisecond)
	dead := func(id string, v int64, updated int64) string {
		return el(id, v, 1, fmt.Sprintf(`"isDeleted":true,"updated":%d`, updated))
	}

	// A strict save drops a tombstone past the timeout and keeps the rest.
	decodeSaved(t, saveScene(admin, path, 1, list(
		dead("stale", 2, old), dead("recent", 2, recent), dead("untimed", 2, 0), el("live", 1, 1, `"updated":1`),
	), `{}`, ""))
	if got := strings.Join(elementIDs(t, admin, path), ","); got != "recent,untimed,live" {
		t.Errorf("after a strict save: %s", got)
	}

	// A deletion beats an older live copy, and is sent back to the saver.
	saved := decodeSaved(t, saveScene(admin, path, 2, list(el("recent", 1, 1, `"tag":"alive"`), dead("stale2", 2, old)), `{}`, `,"merge":true`))
	scene, _ := storedScene(t, admin, path)
	for _, e := range scene.Elements {
		if e["id"] == "recent" && e["isDeleted"] != true {
			t.Errorf("the deletion lost to an older live copy: %v", e)
		}
		if e["id"] == "stale2" {
			t.Errorf("an old tombstone survived a merge")
		}
	}
	if got := strings.Join(remoteIDs(t, saved), ","); got != "recent,untimed,live" {
		t.Errorf("remote = %s", got)
	}

	// A tombstone that was stored when it was young is pruned once it is old.
	oldStored := fmt.Sprintf(`{"elements":%s,"appState":{},"files":{}}`, list(dead("aged", 2, old), el("live", 1, 1, "")))
	if _, err := s.Store.DB.Exec(`UPDATE boards SET scene = ?`, oldStored); err != nil {
		t.Fatal(err)
	}
	decodeSaved(t, saveScene(admin, path, 3, list(el("live", 1, 1, "")), `{}`, `,"merge":true`))
	if got := strings.Join(elementIDs(t, admin, path), ","); got != "live" {
		t.Errorf("after the merge: %s", got)
	}
}

func TestBoardMergeIgnoresTheRevisionButStrictSavesDoNot(t *testing.T) {
	admin, _ := newClient(t)
	_, path := newTestBoard(t, admin)
	for rev := int64(1); rev <= 3; rev++ {
		decodeSaved(t, saveScene(admin, path, rev, list(el("a", rev, 1, "")), `{}`, ""))
	}
	if rec := saveScene(admin, path, 1, list(el("b", 1, 1, "")), `{}`, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "board_conflict") {
		t.Errorf("stale strict save = %d %s", rec.Code, rec.Body.String())
	}
	saved := decodeSaved(t, saveScene(admin, path, 1, list(el("b", 1, 1, "")), `{}`, `,"merge":true`))
	if saved.Revision != 5 {
		t.Errorf("stored revision after a merge = %d, want 5", saved.Revision)
	}
	if got := strings.Join(elementIDs(t, admin, path), ","); got != "a,b" {
		t.Errorf("elements = %s", got)
	}
	// A strict answer carries no remoteElements at all.
	if rec := saveScene(admin, path, 5, list(el("b", 1, 1, "")), `{}`, ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "remoteElements") {
		t.Errorf("strict answer = %d %s", rec.Code, rec.Body.String())
	}
	if rec := saveScene(admin, path, 0, list(), `{}`, `,"merge":true`); rec.Code != http.StatusBadRequest {
		t.Errorf("merge without a revision = %d", rec.Code)
	}
	if rec := saveScene(admin, "/api/v1/boards/9999", 1, list(), `{}`, `,"merge":true`); rec.Code != http.StatusNotFound {
		t.Errorf("merge into a missing board = %d", rec.Code)
	}
	if rec := saveScene(admin, path, 1, `[{"type":"rectangle"}]`, `{}`, `,"merge":true`); rec.Code != http.StatusBadRequest {
		t.Errorf("merge of an element with no id = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBoardMergeKeepsInlineImagesTheStoredSceneStillNeeds(t *testing.T) {
	admin, _ := newClient(t)
	_, path := newTestBoard(t, admin)
	// A picture the server will not move out (not an image type) stays inline.
	legacy := `{"old":{"id":"old","dataURL":"data:text/plain;base64,AA=="}}`
	decodeSaved(t, saveScene(admin, path, 1, list(el("img", 1, 1, `"fileId":"old"`)), legacy, ""))
	decodeSaved(t, saveScene(admin, path, 2, list(el("other", 1, 1, "")), `{}`, `,"merge":true`))
	scene, _ := storedScene(t, admin, path)
	if _, ok := scene.Files["old"]; !ok {
		t.Errorf("an inline image an element still names was dropped: %v", scene.Files)
	}
}

func TestBoardConcurrentMergesLoseNothing(t *testing.T) {
	admin, _ := newClient(t)
	_, path := newTestBoard(t, admin)
	const savers = 8
	var wg sync.WaitGroup
	for i := range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := saveScene(admin, path, 1, list(el(fmt.Sprintf("e%d", i), 1, 1, fmt.Sprintf(`"index":"a%d"`, i))), `{}`, `,"merge":true`)
			if rec.Code != http.StatusOK {
				t.Errorf("saver %d = %d %s", i, rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	if got := len(elementIDs(t, admin, path)); got != savers {
		t.Errorf("elements = %d, want %d", got, savers)
	}
}
