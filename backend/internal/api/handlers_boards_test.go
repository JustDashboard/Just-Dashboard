package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestBoardsPersistAndRejectStaleSaves(t *testing.T) {
	admin, s := newClient(t)
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "boardreader", auth.RoleReadOnly)}

	created := admin.do(http.MethodPost, "/api/v1/boards/", `{"name":"Server map"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var board boardDetail
	if err := json.Unmarshal(created.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/boards/%d", board.ID)
	if board.Revision != 1 || board.Name != "Server map" {
		t.Fatalf("created unexpected board: %+v", board)
	}
	if rec := reader.do(http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("reader get = %d %s", rec.Code, rec.Body.String())
	}
	if rec := reader.do(http.MethodPost, "/api/v1/boards/", `{"name":"Denied"}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader create = %d %s", rec.Code, rec.Body.String())
	}

	scene := `{"elements":[{"type":"rectangle","id":"x","link":"/deploy/4","customData":{"justDashboard":{"kind":"project","resourceId":4}}}],"appState":{"scrollX":12},"files":{}}`
	request := fmt.Sprintf(`{"name":"Production map","revision":1,"scene":%s}`, scene)
	saved := admin.do(http.MethodPut, path, request, nil)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"revision":2`) {
		t.Fatalf("save = %d %s", saved.Code, saved.Body.String())
	}
	if rec := admin.do(http.MethodPut, path, request, nil); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "board_conflict") {
		t.Fatalf("stale save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := reader.do(http.MethodPut, path, request, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path, "", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"resourceId":4`) || !strings.Contains(rec.Body.String(), "Production map") {
		t.Fatalf("read saved scene = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, "/api/v1/boards/", "", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "Production map") {
		t.Fatalf("board listing = %d %s", rec.Code, rec.Body.String())
	}
	for _, scene := range []string{`null`, `[]`, `{"elements":[],"appState":{}}`} {
		bad := fmt.Sprintf(`{"name":"Invalid","revision":2,"scene":%s}`, scene)
		if rec := admin.do(http.MethodPut, path, bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("scene %s = %d %s", scene, rec.Code, rec.Body.String())
		}
	}
	image := strings.Repeat("A", maxBoardSceneBytes+2<<20)
	for label, size := range map[string]int{"scene": maxBoardSceneBytes, "body": maxBoardSceneBytes + 2<<20} {
		big := fmt.Sprintf(`{"name":"Too big","revision":2,"scene":{"elements":[],"appState":{},"files":{"f":{"dataURL":"%s"}}}}`, image[:size-50])
		if rec := admin.do(http.MethodPut, path, big, nil); rec.Code != http.StatusRequestEntityTooLarge ||
			!strings.Contains(rec.Body.String(), "board_too_large") {
			t.Errorf("oversized %s = %d %.200s", label, rec.Code, rec.Body.String())
		}
	}
	// The editor sends an Excalidraw file, whose envelope carries type, version
	// and source beside the three fields the server checks.
	excalidraw := `{"type":"excalidraw","version":2,"source":"https://excalidraw.com","elements":[{"type":"image","id":"i","fileId":"f"}],"appState":{"gridSize":20},"files":{"f":{"id":"f","mimeType":"image/png","dataURL":"data:image/png;base64,AA=="}}}`
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"name":"Production map","revision":2,"scene":%s}`, excalidraw), nil); rec.Code != http.StatusOK {
		t.Fatalf("excalidraw file save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path, "", nil); !strings.Contains(rec.Body.String(), `"dataURL":"data:image/png;base64,AA=="`) {
		t.Fatalf("saved image missing = %s", rec.Body.String())
	}
	if rec := reader.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodDelete, path, "", nil); rec.Code < 400 || !strings.Contains(rec.Body.String(), "confirmation_required") {
		t.Fatalf("delete without phrase = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodDelete, path, "", map[string]string{"X-Confirm": "Production map"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted board read = %d %s", rec.Code, rec.Body.String())
	}
}
