package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestStickyboardsPersistNormalizeAndRejectStaleSaves(t *testing.T) {
	admin, s := newClient(t)
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "stickyreader", auth.RoleReadOnly)}

	created := admin.do(http.MethodPost, "/api/v1/stickyboards/", `{"name":"  Sprint plan  "}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var board stickyDetail
	if err := json.Unmarshal(created.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	if board.Revision != 1 || board.Name != "Sprint plan" || !strings.Contains(string(board.Document), `"notes":[]`) {
		t.Fatalf("created unexpected board: %+v %s", board, board.Document)
	}
	path := fmt.Sprintf("/api/v1/stickyboards/%d", board.ID)

	if rec := reader.do(http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("reader get = %d %s", rec.Code, rec.Body.String())
	}
	for _, denied := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/stickyboards/", `{"name":"Denied"}`},
		{http.MethodPut, path, `{"revision":1,"document":{"version":1,"notes":[],"links":[]}}`},
		{http.MethodPatch, path, `{"name":"Denied"}`},
		{http.MethodPost, path + "/duplicate", `{"name":"Denied"}`},
		{http.MethodDelete, path, ``},
	} {
		if rec := reader.do(denied.method, denied.path, denied.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("reader %s %s = %d %s", denied.method, denied.path, rec.Code, rec.Body.String())
		}
	}

	// Fields a client leaves out are filled with the editor's defaults, and a
	// link's color and shape likewise, so the served document is complete.
	document := `{"version":1,"notes":[
		{"id":"a","x":0,"y":0,"w":220,"h":200,"text":"Ship it"},
		{"id":"b","kind":"text","x":400,"y":0,"w":300,"h":80,"text":"# Heading","size":"xl"},
		{"id":"c","kind":"section","x":-100,"y":-100,"w":900,"h":600,"text":"This week","color":"teal"}
	],"links":[{"id":"l1","from":"a","to":"b","label":"then"}]}`
	saved := admin.do(http.MethodPut, path, fmt.Sprintf(`{"revision":1,"document":%s}`, document), nil)
	if saved.Code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.Code, saved.Body.String())
	}
	var summary stickySummary
	if err := json.Unmarshal(saved.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Revision != 2 || summary.Notes != 1 || summary.Links != 1 {
		t.Fatalf("saved summary = %+v", summary)
	}
	var preview stickyPreview
	if err := json.Unmarshal(summary.Preview, &preview); err != nil || len(preview.Notes) != 3 || len(preview.Links) != 1 {
		t.Fatalf("preview = %s (%v)", summary.Preview, err)
	}
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"revision":1,"document":%s}`, document), nil); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "stickyboard_conflict") {
		t.Fatalf("stale save = %d %s", rec.Code, rec.Body.String())
	}

	read := admin.do(http.MethodGet, path, "", nil)
	var detail stickyDetail
	if err := json.Unmarshal(read.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	var stored stickyDocument
	if err := json.Unmarshal(detail.Document, &stored); err != nil {
		t.Fatal(err)
	}
	first, heading, section, link := stored.Notes[0], stored.Notes[1], stored.Notes[2], stored.Links[0]
	if stored.Background != "dots" || first.Kind != "sticky" || first.Color != "yellow" || first.Style != "paper" ||
		first.Size != "m" || first.Align != "left" || first.Font != "sans" {
		t.Errorf("sticky defaults = %+v background %q", first, stored.Background)
	}
	if heading.Color != "graphite" || heading.Size != "xl" || section.Color != "teal" {
		t.Errorf("text and section = %+v %+v", heading, section)
	}
	if link.Color != "graphite" || link.Style != "solid" || link.Arrow != "end" || link.Path != "curved" || link.Label != "then" {
		t.Errorf("link defaults = %+v", link)
	}

	// The name is not the document: renaming leaves the revision where a tab
	// editing the board expects it.
	if rec := admin.do(http.MethodPatch, path, `{"name":"Sprint 12"}`, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"revision":2`) || !strings.Contains(rec.Body.String(), "Sprint 12") {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodPatch, path, `{"name":"   "}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank rename = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"revision":2,"document":%s}`, document), nil); rec.Code != http.StatusOK {
		t.Fatalf("save after rename = %d %s", rec.Code, rec.Body.String())
	}

	copied := admin.do(http.MethodPost, path+"/duplicate", `{"name":"Sprint 12 copy"}`, nil)
	if copied.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d %s", copied.Code, copied.Body.String())
	}
	var copy stickyDetail
	if err := json.Unmarshal(copied.Body.Bytes(), &copy); err != nil {
		t.Fatal(err)
	}
	if copy.ID == board.ID || copy.Revision != 1 || copy.Notes != 1 || copy.Links != 1 ||
		!strings.Contains(string(copy.Document), "Ship it") {
		t.Fatalf("duplicate = %+v %s", copy, copy.Document)
	}

	templated := admin.do(http.MethodPost, "/api/v1/stickyboards/", fmt.Sprintf(`{"name":"Kanban","document":%s}`, document), nil)
	if templated.Code != http.StatusCreated || !strings.Contains(templated.Body.String(), `"notes":1`) ||
		!strings.Contains(templated.Body.String(), `"color":"yellow"`) {
		t.Fatalf("create from a template = %d %s", templated.Code, templated.Body.String())
	}
	if rec := admin.do(http.MethodPost, "/api/v1/stickyboards/", `{"name":"Bad","document":{"version":1,"notes":[],"links":[{"id":"l","from":"a","to":"b"}]}}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("create from an invalid template = %d %s", rec.Code, rec.Body.String())
	}

	list := admin.do(http.MethodGet, "/api/v1/stickyboards/", "", nil)
	var boards []stickySummary
	if err := json.Unmarshal(list.Body.Bytes(), &boards); err != nil || len(boards) != 3 {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), `"document"`) || strings.Contains(list.Body.String(), `"locked"`) {
		t.Fatalf("the list carried a document: %s", list.Body.String())
	}

	if rec := reader.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader delete = %d", rec.Code)
	}
	if rec := admin.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	for _, gone := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, fmt.Sprintf(`{"revision":3,"document":%s}`, document)},
		{http.MethodPatch, `{"name":"Back"}`},
		{http.MethodDelete, ""},
	} {
		if rec := admin.do(gone.method, path, gone.body, nil); rec.Code != http.StatusNotFound ||
			!strings.Contains(rec.Body.String(), "stickyboard_not_found") {
			t.Errorf("deleted board %s = %d %s", gone.method, rec.Code, rec.Body.String())
		}
	}
}

func TestStickyboardRefusesDocumentsTheEditorCannotDraw(t *testing.T) {
	admin, _ := newClient(t)
	created := admin.do(http.MethodPost, "/api/v1/stickyboards/", `{"name":"Checks"}`, nil)
	var board stickyDetail
	if err := json.Unmarshal(created.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/stickyboards/%d", board.ID)
	note := func(fields string) string {
		return fmt.Sprintf(`{"version":1,"notes":[{"id":"a","x":0,"y":0,"w":200,"h":200%s}],"links":[]}`, fields)
	}
	two := `{"id":"a","x":0,"y":0,"w":200,"h":200},{"id":"b","x":300,"y":0,"w":200,"h":200}`
	for label, document := range map[string]string{
		"version":        `{"version":2,"notes":[],"links":[]}`,
		"unknown field":  note(`,"rotation":12`),
		"kind":           note(`,"kind":"image"`),
		"color":          note(`,"color":"#ff0000"`),
		"style":          note(`,"style":"glass"`),
		"size":           note(`,"size":"xxl"`),
		"background":     `{"version":1,"background":"stars","notes":[],"links":[]}`,
		"id":             `{"version":1,"notes":[{"id":"a b","x":0,"y":0,"w":200,"h":200}],"links":[]}`,
		"duplicate id":   `{"version":1,"notes":[{"id":"a","x":0,"y":0,"w":200,"h":200},{"id":"a","x":0,"y":0,"w":200,"h":200}],"links":[]}`,
		"far away":       `{"version":1,"notes":[{"id":"a","x":20000000,"y":0,"w":200,"h":200}],"links":[]}`,
		"too small":      `{"version":1,"notes":[{"id":"a","x":0,"y":0,"w":10,"h":200}],"links":[]}`,
		"too long":       note(fmt.Sprintf(`,"text":"%s"`, strings.Repeat("x", maxStickyText+1))),
		"dangling link":  fmt.Sprintf(`{"version":1,"notes":[%s],"links":[{"id":"l","from":"a","to":"z"}]}`, two),
		"self link":      fmt.Sprintf(`{"version":1,"notes":[%s],"links":[{"id":"l","from":"a","to":"a"}]}`, two),
		"duplicate link": fmt.Sprintf(`{"version":1,"notes":[%s],"links":[{"id":"l","from":"a","to":"b"},{"id":"l","from":"b","to":"a"}]}`, two),
		"arrow":          fmt.Sprintf(`{"version":1,"notes":[%s],"links":[{"id":"l","from":"a","to":"b","arrow":"left"}]}`, two),
		"long label":     fmt.Sprintf(`{"version":1,"notes":[%s],"links":[{"id":"l","from":"a","to":"b","label":"%s"}]}`, two, strings.Repeat("x", maxStickyLabel+1)),
		"not an object":  `[]`,
	} {
		rec := admin.do(http.MethodPut, path, fmt.Sprintf(`{"revision":1,"document":%s}`, document), nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %.200s", label, rec.Code, rec.Body.String())
		}
	}
	if rec := admin.do(http.MethodPut, path, `{"revision":1}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing document = %d %s", rec.Code, rec.Body.String())
	}
	big := fmt.Sprintf(`{"revision":1,"document":%s}`, note(fmt.Sprintf(`,"text":"%s"`, strings.Repeat("x", maxStickyDocumentBytes))))
	if rec := admin.do(http.MethodPut, path, big, nil); rec.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(rec.Body.String(), "stickyboard_too_large") {
		t.Errorf("oversized = %d %.200s", rec.Code, rec.Body.String())
	}
	// Nothing refused above may have reached the database.
	if rec := admin.do(http.MethodGet, path, "", nil); !strings.Contains(rec.Body.String(), `"revision":1`) {
		t.Fatalf("a refused save was stored: %s", rec.Body.String())
	}
}

func TestStickyPreviewKeepsTheLargestNotesInPaintOrder(t *testing.T) {
	doc := emptyStickyDocument()
	doc.Notes = append(doc.Notes, stickyNote{ID: "section", Kind: "section", W: 4000, H: 3000})
	for i := 0; i < maxStickyPreview+50; i++ {
		doc.Notes = append(doc.Notes, stickyNote{ID: fmt.Sprintf("n%d", i), Kind: "sticky", X: float64(i), W: 200, H: 200})
	}
	doc.Notes = append(doc.Notes, stickyNote{ID: "wide", Kind: "sticky", W: 900, H: 400})
	doc.Links = []stickyLink{{ID: "l1", From: "section", To: "wide"}, {ID: "l2", From: "wide", To: fmt.Sprintf("n%d", maxStickyPreview+40)}}
	preview := stickyPreviewOf(doc)
	if len(preview.Notes) != maxStickyPreview {
		t.Fatalf("preview kept %d notes", len(preview.Notes))
	}
	if preview.Notes[0].Kind != "section" || preview.Notes[len(preview.Notes)-1].W != 900 {
		t.Fatalf("preview lost the paint order: first %+v last %+v", preview.Notes[0], preview.Notes[len(preview.Notes)-1])
	}
	// The second link's far end did not make the cut, so only the first is drawn.
	if len(preview.Links) != 1 || preview.Links[0] != [2]int{0, maxStickyPreview - 1} {
		t.Fatalf("preview links = %v", preview.Links)
	}
}

func TestStickyPlainText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "Ship it", "Ship it"},
		{"empty", "", ""},
		{"only syntax", "---\n```\n```\n>\n", ""},
		{"heading", "# Sprint 12 #\n### Goals", "Sprint 12\nGoals"},
		{"not a heading", "#hashtag and C# code", "#hashtag and C# code"},
		{"bullets", "- one\n* two\n+ three", "one\ntwo\nthree"},
		{"ordered", "1. first\n2) second\n10. tenth", "first\nsecond\ntenth"},
		{"nested list", "- one\n    - inner", "one\ninner"},
		{"tasks", "- [ ] open\n- [x] done\n- [X] also done\n[ ] bare", "open\ndone\nalso done\nbare"},
		{"quote", "> quoted\n> > deeper\n> - listed", "quoted\ndeeper\nlisted"},
		{"emphasis", "**bold** and __strong__ and ~~gone~~ and `code`", "bold and strong and gone and code"},
		{"italic", "*lean* and _slanted_ but 2*3*4 and snake_case_name", "lean and slanted but 2*3*4 and snake_case_name"},
		{"link", "see [the docs](https://example.com/a_b) now", "see the docs now"},
		{"image", "![diagram](/x.png) caption", "diagram caption"},
		{"rules and fences", "intro\n---\n***\n```go\nx := 1\n```\n~~~\nend", "intro\nx := 1\nend"},
		{"blank lines dropped", "a\n\n\n  \nb\r\nc", "a\nb\nc"},
		{"four lines at most", "1\n2\n3\n4\n5\n6", "1\n2\n3\n4"},
		{"120 runes", strings.Repeat("é", 200), strings.Repeat("é", 120)},
		{"120 runes across lines", strings.Repeat("a", 100) + "\n" + strings.Repeat("b", 100), strings.Repeat("a", 100) + "\n" + strings.Repeat("b", 19)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := stickyPlainText(tc.in); got != tc.want {
				t.Errorf("stickyPlainText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStickyPreviewCarriesEachNotesWords(t *testing.T) {
	t.Parallel()
	doc := emptyStickyDocument()
	doc.Notes = []stickyNote{
		{ID: "s", Kind: "sticky", W: 200, H: 200, Text: "**Ship** it"},
		{ID: "t", Kind: "text", W: 200, H: 80, Text: "# Heading"},
		{ID: "c", Kind: "section", W: 900, H: 600, Text: "This week"},
		{ID: "e", Kind: "sticky", W: 200, H: 200},
	}
	out, err := json.Marshal(stickyPreviewOf(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"text":"Ship it"`, `"text":"Heading"`, `"text":"This week"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("preview %s lacks %s", out, want)
		}
	}
	if strings.Count(string(out), `"text":`) != 3 {
		t.Errorf("an empty note carried a text: %s", out)
	}
}
