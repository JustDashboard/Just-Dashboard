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
	// The save moves the embedded image out of the scene into the board's files.
	if rec := admin.do(http.MethodGet, path, "", nil); strings.Contains(rec.Body.String(), "dataURL") ||
		!strings.Contains(rec.Body.String(), `"fileIds":["f"]`) {
		t.Fatalf("image not moved out of the scene = %s", rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path+"/files/f", "", nil); rec.Code != http.StatusOK || rec.Body.String() != "\x00" {
		t.Fatalf("stored image = %d %q", rec.Code, rec.Body.String())
	}
	if rec := reader.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted board read = %d %s", rec.Code, rec.Body.String())
	}
}

func previewOf(t *testing.T, scene string) boardPreview {
	t.Helper()
	p, err := boardPreviewOf(json.RawMessage(scene))
	if err != nil {
		t.Fatalf("boardPreviewOf(%.200s) = %v", scene, err)
	}
	return p
}

func TestBoardPreviewKeepsDrawableElementsOnly(t *testing.T) {
	t.Parallel()
	scene := `{"elements":[
		{"type":"selection","x":0,"y":0,"width":9,"height":9},
		{"type":"rectangle","x":10.4,"y":20.6,"width":100.5,"height":50,"angle":0.12345,"strokeColor":"#1E1E1E","backgroundColor":"#a5d8ff","fillStyle":"hachure","strokeWidth":2,"strokeStyle":"dashed","roundness":{"type":3},"opacity":40},
		{"type":"ellipse","x":0,"y":0,"width":10,"height":10,"isDeleted":true},
		{"type":"diamond","x":1,"y":2,"width":3,"height":4,"strokeColor":"transparent","backgroundColor":"transparent","fillStyle":"solid","strokeStyle":"solid","roundness":null,"opacity":100},
		{"type":"text","x":5,"y":6,"width":70,"height":25,"text":"Hello","originalText":"orig","fontSize":20,"fontFamily":5,"textAlign":"center"},
		{"type":"text","x":5,"y":6,"width":70,"height":25,"text":"","originalText":"fallback","textAlign":"left"},
		{"type":"frame","x":0,"y":0,"width":500,"height":400,"name":"Group A"},
		{"type":"arrow","x":0,"y":0,"width":50,"height":0,"points":[[0,0],[50.4,0.6]],"startArrowhead":null,"endArrowhead":"arrow","opacity":0},
		{"type":"embeddable","x":0,"y":0,"width":1,"height":1,"opacity":0.4},
		{"type":"image","x":0,"y":0,"width":1,"height":1,"fileId":"f","strokeWidth":0}
	],"appState":{"viewBackgroundColor":"#FFF"},"files":{"f":{"dataURL":"data:image/png;base64,AA=="}}}`
	p := previewOf(t, scene)
	if p.Background != "#FFF" || p.Count != 9 {
		t.Fatalf("background %q count %d", p.Background, p.Count)
	}
	var types []string
	for _, e := range p.Elements {
		types = append(types, e.Type)
	}
	if got := strings.Join(types, ","); got != "rectangle,diamond,text,text,frame,arrow,embeddable,image" {
		t.Fatalf("kept types = %s", got)
	}
	rect := p.Elements[0]
	if rect.X != 10 || rect.Y != 21 || rect.W != 101 || rect.H != 50 || rect.Angle != 0.123 ||
		rect.Stroke != "#1E1E1E" || rect.Fill != "#a5d8ff" || rect.FillStyle != "hachure" || rect.StrokeWidth != 2 ||
		rect.StrokeStyle != "dashed" || !rect.Round || rect.Opacity == nil || *rect.Opacity != 40 {
		t.Errorf("rectangle = %+v", rect)
	}
	out, err := json.Marshal(p.Elements[1:])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`{"type":"diamond","x":1,"y":2,"w":3,"h":4}`, // defaults and transparent colours are left out
		`"text":"Hello","fontSize":20,"fontFamily":5,"textAlign":"center"`,
		`"text":"fallback"}`,
		`"text":"Group A"`,
		`"opacity":0,"points":[[0,0],[50,1]],"endArrow":true`,
		`"type":"embeddable","x":0,"y":0,"w":1,"h":1,"opacity":0}`, // 0.4 of 100 rounds to 0
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("elements %s lack %s", out, want)
		}
	}
	if strings.Contains(string(out), "startArrow") || strings.Contains(string(out), "dataURL") {
		t.Errorf("elements carry what they should not: %s", out)
	}
}

func TestBoardPreviewDropsColoursThatAreNotHex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		colour string
		ok     bool
	}{
		{"#fff", true}, {"#FFFF", true}, {"#a1b2c3", true}, {"#A1B2C3D4", true},
		{"transparent", false}, {"red", false}, {"rgb(1,2,3)", false}, {"", false}, {"#", false},
		{"#ff", false}, {"#fffff", false}, {"#fffffff", false}, {"#ggg", false}, {"fff", false},
		{`red" onload="x`, false}, {`#fff" onload="x`, false}, {"#fff\n", false}, {"url(#a)", false},
	} {
		hostile, err := json.Marshal(tc.colour)
		if err != nil {
			t.Fatal(err)
		}
		scene := fmt.Sprintf(`{"elements":[{"type":"rectangle","x":0,"y":0,"width":1,"height":1,"strokeColor":%s,"backgroundColor":%s}],"appState":{"viewBackgroundColor":%s},"files":{}}`, hostile, hostile, hostile)
		p := previewOf(t, scene)
		want := ""
		if tc.ok {
			want = tc.colour
		}
		if p.Background != want || p.Elements[0].Stroke != want || p.Elements[0].Fill != want {
			t.Errorf("colour %q: background %q stroke %q fill %q", tc.colour, p.Background, p.Elements[0].Stroke, p.Elements[0].Fill)
		}
	}
}

func TestBoardPreviewThinsPointsKeepingTheEnds(t *testing.T) {
	t.Parallel()
	for _, n := range []int{2, 64, 65, 100, 1000, 4097} {
		var points []string
		for i := 0; i < n; i++ {
			points = append(points, fmt.Sprintf("[%d,%d]", i, -i))
		}
		scene := fmt.Sprintf(`{"elements":[{"type":"freedraw","x":0,"y":0,"width":9,"height":9,"points":[%s]}],"appState":{},"files":{}}`, strings.Join(points, ","))
		got := previewOf(t, scene).Elements[0].Points
		wantLen := min(n, maxBoardPreviewPoints)
		if len(got) != wantLen || got[0] != [2]int{0, 0} || got[len(got)-1] != [2]int{n - 1, -(n - 1)} {
			t.Errorf("%d points became %d: first %v last %v", n, len(got), got[0], got[len(got)-1])
		}
		for i := 1; i < len(got); i++ {
			if got[i][0] <= got[i-1][0] {
				t.Fatalf("%d points: thinned points out of order at %d", n, i)
			}
		}
	}
	// Only strokes carry points; a rectangle that somehow has them does not.
	scene := `{"elements":[{"type":"rectangle","x":0,"y":0,"width":9,"height":9,"points":[[0,0],[1,1]]}],"appState":{},"files":{}}`
	if got := previewOf(t, scene).Elements[0].Points; got != nil {
		t.Errorf("rectangle points = %v", got)
	}
}

func TestBoardPreviewCapKeepsTheLargestInPaintOrder(t *testing.T) {
	t.Parallel()
	var elements []string
	elements = append(elements, `{"type":"frame","x":0,"y":0,"width":4000,"height":3000,"name":"big"}`)
	for i := 0; i < maxBoardPreviewElements+50; i++ {
		elements = append(elements, fmt.Sprintf(`{"type":"rectangle","x":%d,"y":0,"width":200,"height":200}`, i))
	}
	elements = append(elements,
		`{"type":"rectangle","x":-1,"y":0,"width":900,"height":400}`,
		`{"type":"line","x":-2,"y":0,"width":100000,"height":0,"points":[[0,0],[100000,0]]}`, // flat, but its area is floored above 0
		`{"type":"rectangle","x":0,"y":0,"width":500,"height":500,"isDeleted":true}`)
	scene := fmt.Sprintf(`{"elements":[%s],"appState":{},"files":{}}`, strings.Join(elements, ","))
	p := previewOf(t, scene)
	if len(p.Elements) != maxBoardPreviewElements {
		t.Fatalf("kept %d elements", len(p.Elements))
	}
	if p.Count != maxBoardPreviewElements+50+3 {
		t.Errorf("count = %d, want every live element", p.Count)
	}
	if p.Elements[0].Type != "frame" || p.Elements[len(p.Elements)-2].W != 900 || p.Elements[len(p.Elements)-1].Type != "line" {
		t.Fatalf("paint order lost: first %+v last two %+v %+v", p.Elements[0], p.Elements[len(p.Elements)-2], p.Elements[len(p.Elements)-1])
	}
	// 200x200 squares tie, and a stable sort keeps the earlier ones.
	if last := p.Elements[len(p.Elements)-3]; last.X != maxBoardPreviewElements-4 {
		t.Errorf("tie-breaking kept x=%d", last.X)
	}
}

func TestBoardPreviewResources(t *testing.T) {
	t.Parallel()
	card := func(data string, extra string) string {
		return fmt.Sprintf(`{"type":"rectangle","x":0,"y":0,"width":1,"height":1%s,"customData":{"justDashboard":%s}}`, extra, data)
	}
	elements := []string{
		card(`{"kind":"project","resourceId":12}`, ""),
		card(`{"kind":"server"}`, ""),
		card(`{"kind":"project","resourceId":12}`, ""),
		card(`{"kind":"server"}`, ""),
		card(`{"kind":"database","resourceId":3}`, ""),
		card(`{"kind":"database","resourceId":12}`, ""),
		card(`{"kind":"volume","resourceId":1}`, ""),
		card(`{"kind":"project"}`, ""),
		card(`{"kind":"project","resourceId":"7"}`, ""),
		card(`{"kind":"project","resourceId":1.5}`, ""),
		card(`{"kind":"project","resourceId":0}`, ""),
		card(`{"kind":"project","resourceId":99}`, `,"isDeleted":true`),
		`{"type":"text","x":0,"y":0,"width":1,"height":1,"customData":"nope"}`,
	}
	p := previewOf(t, fmt.Sprintf(`{"elements":[%s],"appState":{},"files":{}}`, strings.Join(elements, ",")))
	out, err := json.Marshal(p.Resources)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"kind":"project","id":12},{"kind":"server"},{"kind":"database","id":3},{"kind":"database","id":12}]`; string(out) != want {
		t.Errorf("resources = %s, want %s", out, want)
	}

	elements = elements[:0]
	for i := 1; i <= maxBoardPreviewResources+10; i++ {
		elements = append(elements, card(fmt.Sprintf(`{"kind":"project","resourceId":%d}`, i), ""))
	}
	p = previewOf(t, fmt.Sprintf(`{"elements":[%s],"appState":{},"files":{}}`, strings.Join(elements, ",")))
	if len(p.Resources) != maxBoardPreviewResources || p.Resources[0].ID != 1 || p.Resources[maxBoardPreviewResources-1].ID != maxBoardPreviewResources {
		t.Errorf("resources were not capped to the first %d: %+v", maxBoardPreviewResources, p.Resources)
	}
}

func TestBoardPreviewTruncatesTextAndSkipsNonFiniteNumbers(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 300)
	scene := fmt.Sprintf(`{"elements":[
		{"type":"text","x":0,"y":0,"width":1,"height":1,"text":"%s"},
		{"type":"rectangle","x":1e300,"y":0,"width":1,"height":1}
	],"appState":{},"files":{}}`, long)
	p := previewOf(t, scene)
	if len(p.Elements) != 1 || p.Count != 2 || p.Elements[0].Text != strings.Repeat("é", maxBoardPreviewText) {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := boardPreviewOf(json.RawMessage(`{"elements":[{"type":"rectangle","x":1e999}],"appState":{},"files":{}}`)); err == nil {
		t.Errorf("an overflowing number was accepted")
	}
}

func TestEmptyBoardPreviewMatchesTheDerivedOne(t *testing.T) {
	t.Parallel()
	if got := boardPreviewJSON(json.RawMessage(emptyBoardScene)); got != emptyBoardPreview {
		t.Errorf("empty scene preview = %s, want %s", got, emptyBoardPreview)
	}
}

func TestBoardListCarriesPreviewsAndBackfillsOldBoards(t *testing.T) {
	admin, s := newClient(t)
	ctx := t.Context()
	list := func() []boardSummary {
		t.Helper()
		rec := admin.do(http.MethodGet, "/api/v1/boards/", "", nil)
		var boards []boardSummary
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &boards) != nil {
			t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
		}
		return boards
	}

	created := admin.do(http.MethodPost, "/api/v1/boards/", `{"name":"Map"}`, nil)
	var board boardDetail
	if err := json.Unmarshal(created.Body.Bytes(), &board); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	path := fmt.Sprintf("/api/v1/boards/%d", board.ID)
	if got := list(); len(got) != 1 || string(got[0].Preview) != emptyBoardPreview {
		t.Fatalf("a new board's preview = %+v", got)
	}

	scene := `{"elements":[
		{"type":"rectangle","x":0,"y":0,"width":200,"height":100,"strokeColor":"#1e1e1e","backgroundColor":"#a5d8ff","customData":{"justDashboard":{"kind":"project","resourceId":4}}},
		{"type":"text","x":20,"y":30,"width":80,"height":25,"text":"api","fontSize":20,"fontFamily":1},
		{"type":"arrow","x":200,"y":50,"width":100,"height":0,"points":[[0,0],[100,0]],"endArrowhead":"arrow"}
	],"appState":{"viewBackgroundColor":"#ffffff"},"files":{"f":{"dataURL":"data:image/png;base64,AA=="}}}`
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"name":"Map","revision":1,"scene":%s}`, scene), nil); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	rec := admin.do(http.MethodGet, "/api/v1/boards/", "", nil)
	if strings.Contains(rec.Body.String(), "dataURL") || strings.Contains(rec.Body.String(), `"scene"`) {
		t.Fatalf("the list carried a scene: %s", rec.Body.String())
	}
	got := list()
	var preview boardPreview
	if err := json.Unmarshal(got[0].Preview, &preview); err != nil || preview.Background != "#ffffff" ||
		preview.Count != 3 || len(preview.Elements) != 3 || len(preview.Resources) != 1 || preview.Resources[0].ID != 4 {
		t.Fatalf("saved preview = %s (%v)", got[0].Preview, err)
	}

	// A board saved before previews existed holds '' and is filled in on sight,
	// without counting as an edit: an editor open on it must not meet a conflict.
	var revision, updated int64
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE boards SET preview = '' WHERE id = ?`, board.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT revision, updated_at FROM boards WHERE id = ?`, board.ID).Scan(&revision, &updated); err != nil {
		t.Fatal(err)
	}
	filled := list()
	if string(filled[0].Preview) != string(got[0].Preview) {
		t.Fatalf("backfilled preview = %s, want %s", filled[0].Preview, got[0].Preview)
	}
	var storedPreview string
	var revisionAfter, updatedAfter int64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT preview, revision, updated_at FROM boards WHERE id = ?`, board.ID).
		Scan(&storedPreview, &revisionAfter, &updatedAfter); err != nil {
		t.Fatal(err)
	}
	if storedPreview != string(got[0].Preview) || revisionAfter != revision || updatedAfter != updated {
		t.Fatalf("backfill stored %q, revision %d -> %d, updated_at %d -> %d", storedPreview, revision, revisionAfter, updated, updatedAfter)
	}
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"name":"Map","revision":%d,"scene":%s}`, revision, scene), nil); rec.Code != http.StatusOK {
		t.Fatalf("save after backfill = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBoardSaveSurvivesElementsThePreviewCannotRead(t *testing.T) {
	admin, s := newClient(t)
	created := admin.do(http.MethodPost, "/api/v1/boards/", `{"name":"Odd"}`, nil)
	var board boardDetail
	if err := json.Unmarshal(created.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/boards/%d", board.ID)
	scene := `{"elements":[{"type":"rectangle","x":"far","y":[],"width":null}, 7],"appState":{"viewBackgroundColor":4},"files":{}}`
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"name":"Odd","revision":1,"scene":%s}`, scene), nil); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	var preview, stored string
	if err := s.Store.DB.QueryRowContext(t.Context(), `SELECT preview, scene FROM boards WHERE id = ?`, board.ID).Scan(&preview, &stored); err != nil {
		t.Fatal(err)
	}
	if preview != emptyBoardPreview || !strings.Contains(stored, `"far"`) {
		t.Fatalf("preview %s, scene %s", preview, stored)
	}
	rec := admin.do(http.MethodGet, "/api/v1/boards/", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"preview":`+emptyBoardPreview) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
}
