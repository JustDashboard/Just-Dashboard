package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A stickyboard is one document the server validates field by field, unlike
// a board's Excalidraw scene, which is opaque: the editor's links name notes
// by id, and a link left pointing at a note that is gone would be drawn from
// nowhere. Checking the shape here keeps every stored board one the editor
// can draw, whichever client wrote it.
const (
	maxStickyDocumentBytes = 4 << 20
	maxStickyNotes         = 5000
	maxStickyLinks         = 10000
	maxStickyText          = 20000
	maxStickyLabel         = 200
	// Beyond this the canvas is no longer one board anyone pans across, and a
	// coordinate this large is a client bug rather than a place.
	maxStickyCoordinate = 10_000_000
	minStickySide       = 24
	maxStickySide       = 20000
	// The thumbnail is a picture of a board's shape, not of every note: the
	// largest items, so sections and the notes in them are what it shows.
	maxStickyPreview = 300
)

var (
	stickyID          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	stickyKinds       = map[string]bool{"sticky": true, "text": true, "section": true}
	stickyColors      = map[string]bool{"yellow": true, "orange": true, "red": true, "pink": true, "violet": true, "blue": true, "teal": true, "green": true, "graphite": true}
	stickyStyles      = map[string]bool{"paper": true, "tint": true}
	stickySizes       = map[string]bool{"s": true, "m": true, "l": true, "xl": true}
	stickyAligns      = map[string]bool{"left": true, "center": true}
	stickyFonts       = map[string]bool{"sans": true, "mono": true}
	stickyLinkStyles  = map[string]bool{"solid": true, "dashed": true}
	stickyArrows      = map[string]bool{"none": true, "end": true, "both": true}
	stickyPaths       = map[string]bool{"curved": true, "straight": true, "elbow": true}
	stickyBackgrounds = map[string]bool{"dots": true, "lines": true, "none": true}
)

type stickyNote struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	Text   string  `json:"text"`
	Color  string  `json:"color"`
	Style  string  `json:"style"`
	Size   string  `json:"size"`
	Align  string  `json:"align"`
	Font   string  `json:"font"`
	Locked bool    `json:"locked"`
}

type stickyLink struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	Color string `json:"color"`
	Style string `json:"style"`
	Arrow string `json:"arrow"`
	Path  string `json:"path"`
}

type stickyDocument struct {
	Version    int          `json:"version"`
	Background string       `json:"background"`
	Notes      []stickyNote `json:"notes"`
	Links      []stickyLink `json:"links"`
}

type stickyPreviewNote struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Kind  string  `json:"kind"`
	Color string  `json:"color"`
	Style string  `json:"style"`
	// The note's words without their Markdown, so the thumbnail can show what
	// is written rather than only where.
	Text string `json:"text,omitempty"`
}

type stickyPreview struct {
	Notes []stickyPreviewNote `json:"notes"`
	// Pairs of indexes into Notes, for the links whose ends both made it in.
	Links [][2]int `json:"links"`
}

type stickySummary struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Revision  int64           `json:"revision"`
	Notes     int             `json:"notes"`
	Links     int             `json:"links"`
	Preview   json.RawMessage `json:"preview"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type stickyDetail struct {
	stickySummary
	Document json.RawMessage `json:"document"`
}

func emptyStickyDocument() stickyDocument {
	return stickyDocument{Version: 1, Background: "dots", Notes: []stickyNote{}, Links: []stickyLink{}}
}

func (s *Server) mountStickyboardRoutes(r chi.Router) {
	r.Route("/stickyboards", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleStickyList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleStickyGet))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/", s.handle(s.handleStickyCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleStickyPut))
			r.Method(http.MethodPatch, "/{id}", s.handle(s.handleStickyRename))
			r.Method(http.MethodPost, "/{id}/duplicate", s.handle(s.handleStickyDuplicate))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleStickyDelete))
		})
	})
}

func errStickyNotFound() error {
	return httpx.Err(http.StatusNotFound, "stickyboard_not_found", "stickyboard not found")
}

func errStickyTooLarge() error {
	return httpx.Err(http.StatusRequestEntityTooLarge, "stickyboard_too_large",
		"this board is larger than 4 MiB; split it into more than one board")
}

func stickyName(name string) (string, error) {
	name, err := boardName(name)
	if err != nil {
		return "", httpx.BadRequest("stickyboard name must be between 1 and 100 characters")
	}
	return name, nil
}

// decodeStickyPut reads a write the way a board's save is read, at a stickyboard's
// own limit: a body past it is refused as too big rather than as malformed,
// and a field the document does not have is refused rather than dropped.
func decodeStickyPut(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return httpx.Err(http.StatusUnsupportedMediaType, "json_content_type_required", "request body must use application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStickyDocumentBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errStickyTooLarge()
		}
		return httpx.BadRequest("malformed stickyboard: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return httpx.BadRequest("stickyboard request must contain one JSON object")
	}
	return nil
}

func stickyFinite(v, limit float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= -limit && v <= limit
}

// stickyEnum fills a field a client left empty, so a document written before a
// field existed still opens, and refuses a value the editor would not know
// how to draw.
func stickyEnum(value, fallback, field string, allowed map[string]bool) (string, error) {
	if value == "" {
		return fallback, nil
	}
	if !allowed[value] {
		return "", fmt.Errorf("%s %q is not one this board can draw", field, value)
	}
	return value, nil
}

// normalizeSticky checks a document and returns it with every default filled
// in, which is the form it is stored and served in.
func normalizeSticky(doc stickyDocument) (stickyDocument, error) {
	if doc.Version != 1 {
		return doc, fmt.Errorf("document version must be 1")
	}
	var err error
	if doc.Background, err = stickyEnum(doc.Background, "dots", "background", stickyBackgrounds); err != nil {
		return doc, err
	}
	if doc.Notes == nil {
		doc.Notes = []stickyNote{}
	}
	if doc.Links == nil {
		doc.Links = []stickyLink{}
	}
	if len(doc.Notes) > maxStickyNotes {
		return doc, fmt.Errorf("a board holds at most %d notes", maxStickyNotes)
	}
	if len(doc.Links) > maxStickyLinks {
		return doc, fmt.Errorf("a board holds at most %d links", maxStickyLinks)
	}
	ids := make(map[string]bool, len(doc.Notes))
	for i := range doc.Notes {
		n := &doc.Notes[i]
		if !stickyID.MatchString(n.ID) {
			return doc, fmt.Errorf("note %d has an invalid id", i)
		}
		if ids[n.ID] {
			return doc, fmt.Errorf("note id %q is used twice", n.ID)
		}
		ids[n.ID] = true
		if n.Kind, err = stickyEnum(n.Kind, "sticky", "kind", stickyKinds); err != nil {
			return doc, err
		}
		if !stickyFinite(n.X, maxStickyCoordinate) || !stickyFinite(n.Y, maxStickyCoordinate) {
			return doc, fmt.Errorf("note %q is placed outside the board", n.ID)
		}
		if !stickyFinite(n.W, maxStickySide) || !stickyFinite(n.H, maxStickySide) ||
			n.W < minStickySide || n.H < minStickySide {
			return doc, fmt.Errorf("note %q must be between %d and %d units on each side", n.ID, minStickySide, maxStickySide)
		}
		if utf8.RuneCountInString(n.Text) > maxStickyText {
			return doc, fmt.Errorf("note %q is longer than %d characters", n.ID, maxStickyText)
		}
		fallbackColor := "yellow"
		if n.Kind != "sticky" {
			fallbackColor = "graphite"
		}
		if n.Color, err = stickyEnum(n.Color, fallbackColor, "color", stickyColors); err != nil {
			return doc, err
		}
		if n.Style, err = stickyEnum(n.Style, "paper", "style", stickyStyles); err != nil {
			return doc, err
		}
		if n.Size, err = stickyEnum(n.Size, "m", "size", stickySizes); err != nil {
			return doc, err
		}
		if n.Align, err = stickyEnum(n.Align, "left", "align", stickyAligns); err != nil {
			return doc, err
		}
		if n.Font, err = stickyEnum(n.Font, "sans", "font", stickyFonts); err != nil {
			return doc, err
		}
	}
	links := make(map[string]bool, len(doc.Links))
	for i := range doc.Links {
		l := &doc.Links[i]
		if !stickyID.MatchString(l.ID) {
			return doc, fmt.Errorf("link %d has an invalid id", i)
		}
		if links[l.ID] {
			return doc, fmt.Errorf("link id %q is used twice", l.ID)
		}
		links[l.ID] = true
		if !ids[l.From] || !ids[l.To] {
			return doc, fmt.Errorf("link %q joins a note that is not on the board", l.ID)
		}
		if l.From == l.To {
			return doc, fmt.Errorf("link %q joins a note to itself", l.ID)
		}
		if utf8.RuneCountInString(l.Label) > maxStickyLabel {
			return doc, fmt.Errorf("link %q has a label longer than %d characters", l.ID, maxStickyLabel)
		}
		if l.Color, err = stickyEnum(l.Color, "graphite", "link color", stickyColors); err != nil {
			return doc, err
		}
		if l.Style, err = stickyEnum(l.Style, "solid", "link style", stickyLinkStyles); err != nil {
			return doc, err
		}
		if l.Arrow, err = stickyEnum(l.Arrow, "end", "arrow", stickyArrows); err != nil {
			return doc, err
		}
		if l.Path, err = stickyEnum(l.Path, "curved", "path", stickyPaths); err != nil {
			return doc, err
		}
	}
	return doc, nil
}

func stickyPreviewOf(doc stickyDocument) stickyPreview {
	order := make([]int, len(doc.Notes))
	for i := range order {
		order[i] = i
	}
	if len(order) > maxStickyPreview {
		sort.SliceStable(order, func(a, b int) bool {
			na, nb := doc.Notes[order[a]], doc.Notes[order[b]]
			return na.W*na.H > nb.W*nb.H
		})
		order = order[:maxStickyPreview]
		// Back into the board's own paint order, so a section the thumbnail
		// keeps is still drawn under the notes on it.
		sort.Ints(order)
	}
	preview := stickyPreview{Notes: make([]stickyPreviewNote, 0, len(order)), Links: [][2]int{}}
	index := make(map[string]int, len(order))
	for _, i := range order {
		n := doc.Notes[i]
		index[n.ID] = len(preview.Notes)
		preview.Notes = append(preview.Notes, stickyPreviewNote{
			X: math.Round(n.X), Y: math.Round(n.Y), W: math.Round(n.W), H: math.Round(n.H),
			Kind: n.Kind, Color: n.Color, Style: n.Style, Text: stickyPlainText(n.Text),
		})
	}
	for _, l := range doc.Links {
		if len(preview.Links) == maxStickyPreview {
			break
		}
		from, okFrom := index[l.From]
		to, okTo := index[l.To]
		if okFrom && okTo {
			preview.Links = append(preview.Links, [2]int{from, to})
		}
	}
	return preview
}

const (
	maxStickyPreviewLines = 4
	maxStickyPreviewText  = 120
)

var (
	// Go's regexp has no backreferences, so a rule is one pattern per marker.
	stickyRule     = regexp.MustCompile(`^(-\s*){3,}$|^(\*\s*){3,}$|^(_\s*){3,}$`)
	stickyFence    = regexp.MustCompile("^(```|~~~)")
	stickyHeading  = regexp.MustCompile(`^#{1,6}\s+`)
	stickyBullet   = regexp.MustCompile(`^([-*+]|\d{1,9}[.)])\s+`)
	stickyTaskBox  = regexp.MustCompile(`^\[[ xX]\]\s+`)
	stickyImage    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	stickyStarEm   = regexp.MustCompile(`(^|[^\w*])\*([^\s*](?:[^*]*[^\s*])?)\*`)
	stickyUnderEm  = regexp.MustCompile(`(^|\W)_([^\s_](?:[^_]*[^\s_])?)_(\W|$)`)
	stickyMarkup   = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")
	stickyHeadTail = regexp.MustCompile(`\s+#+$`)
)

// stickyPlainText reduces a note's Markdown to the words a thumbnail can show:
// at most four lines and 120 characters, with the syntax the notes are drawn
// from (headings, lists, tasks, quotes, emphasis, code, links) taken out. It
// reads lines rather than parsing blocks because it only has to be readable,
// and a stray marker left in a thumbnail costs nothing.
func stickyPlainText(markdown string) string {
	lines := make([]string, 0, maxStickyPreviewLines)
	for _, line := range strings.Split(markdown, "\n") {
		if len(lines) == maxStickyPreviewLines {
			break
		}
		line = stickyPlainLine(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return truncateRunes(strings.Join(lines, "\n"), maxStickyPreviewText)
}

func stickyPlainLine(line string) string {
	line = strings.TrimSpace(line)
	if stickyRule.MatchString(line) || stickyFence.MatchString(line) {
		return ""
	}
	// A quote can hold a list and a list item a task, so each marker is
	// peeled in the order they nest.
	for strings.HasPrefix(line, ">") {
		line = strings.TrimSpace(line[1:])
	}
	if stickyHeading.MatchString(line) {
		line = stickyHeadTail.ReplaceAllString(stickyHeading.ReplaceAllString(line, ""), "")
	}
	line = stickyBullet.ReplaceAllString(line, "")
	line = stickyTaskBox.ReplaceAllString(line, "")
	line = stickyImage.ReplaceAllString(line, "$1")
	line = stickyMarkup.Replace(line)
	line = stickyStarEm.ReplaceAllString(line, "$1$2")
	line = stickyUnderEm.ReplaceAllString(line, "$1$2$3")
	return strings.TrimSpace(line)
}

func countStickies(doc stickyDocument) int {
	count := 0
	for _, n := range doc.Notes {
		if n.Kind == "sticky" {
			count++
		}
	}
	return count
}

// encodeSticky is what is stored for a document: the document itself, how
// many sticky notes and links it holds, and its thumbnail.
func encodeSticky(doc stickyDocument) (document, preview []byte, err error) {
	document, err = json.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	preview, err = json.Marshal(stickyPreviewOf(doc))
	return document, preview, err
}

const stickySummaryColumns = `id, name, revision, notes, links, preview, created_at, updated_at`

type stickyRow interface {
	Scan(dest ...any) error
}

func scanStickySummary(row stickyRow, extra ...any) (stickySummary, error) {
	var b stickySummary
	var preview string
	var created, updated int64
	dest := append([]any{&b.ID, &b.Name, &b.Revision, &b.Notes, &b.Links, &preview, &created, &updated}, extra...)
	if err := row.Scan(dest...); err != nil {
		return b, err
	}
	b.Preview = json.RawMessage(preview)
	b.CreatedAt = time.Unix(created, 0).UTC()
	b.UpdatedAt = time.Unix(updated, 0).UTC()
	return b, nil
}

func (s *Server) stickySummary(r *http.Request, id int64) (stickySummary, error) {
	b, err := scanStickySummary(s.Store.DB.QueryRowContext(r.Context(),
		`SELECT `+stickySummaryColumns+` FROM sticky_boards WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return b, errStickyNotFound()
	}
	if err != nil {
		return b, httpx.Internal(err)
	}
	return b, nil
}

func (s *Server) handleStickyList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT `+stickySummaryColumns+` FROM sticky_boards ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	boards := []stickySummary{}
	for rows.Next() {
		b, err := scanStickySummary(rows)
		if err != nil {
			return httpx.Internal(err)
		}
		boards = append(boards, b)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, boards)
	return nil
}

func (s *Server) handleStickyGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var document string
	b, err := scanStickySummary(s.Store.DB.QueryRowContext(r.Context(),
		`SELECT `+stickySummaryColumns+`, document FROM sticky_boards WHERE id = ?`, id), &document)
	if errors.Is(err, sql.ErrNoRows) {
		return errStickyNotFound()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, stickyDetail{stickySummary: b, Document: json.RawMessage(document)})
	return nil
}

func (s *Server) insertSticky(r *http.Request, name string, doc stickyDocument) (stickyDetail, error) {
	document, preview, err := encodeSticky(doc)
	if err != nil {
		return stickyDetail{}, httpx.Internal(err)
	}
	now := time.Now().Unix()
	result, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO sticky_boards(name, document, notes, links, preview, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		name, string(document), countStickies(doc), len(doc.Links), string(preview), now, now)
	if err != nil {
		return stickyDetail{}, httpx.Internal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return stickyDetail{}, httpx.Internal(err)
	}
	return stickyDetail{
		stickySummary: stickySummary{ID: id, Name: name, Revision: 1,
			Notes: countStickies(doc), Links: len(doc.Links), Preview: preview,
			CreatedAt: time.Unix(now, 0).UTC(), UpdatedAt: time.Unix(now, 0).UTC()},
		Document: document,
	}, nil
}

// A board may start from a document — a template the editor offers — which
// is checked exactly as a save is.
func (s *Server) handleStickyCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name     string          `json:"name"`
		Document *stickyDocument `json:"document"`
	}
	if err := decodeStickyPut(w, r, &req); err != nil {
		return err
	}
	name, err := stickyName(req.Name)
	if err != nil {
		return err
	}
	doc := emptyStickyDocument()
	if req.Document != nil {
		if doc, err = normalizeSticky(*req.Document); err != nil {
			return httpx.BadRequest("%v", err)
		}
	}
	created, err := s.insertSticky(r, name, doc)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "stickyboard.create", name, nil)
	httpx.JSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) handleStickyPut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		Document *stickyDocument `json:"document"`
		Revision int64           `json:"revision"`
	}
	if err := decodeStickyPut(w, r, &req); err != nil {
		return err
	}
	if req.Revision < 1 || req.Document == nil {
		return httpx.BadRequest("stickyboard revision and document are required")
	}
	doc, err := normalizeSticky(*req.Document)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	document, preview, err := encodeSticky(doc)
	if err != nil {
		return httpx.Internal(err)
	}
	now := time.Now().Unix()
	result, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE sticky_boards SET document = ?, notes = ?, links = ?, preview = ?,
		 revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?`,
		string(document), countStickies(doc), len(doc.Links), string(preview), now, id, req.Revision)
	if err != nil {
		return httpx.Internal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return httpx.Internal(err)
	}
	if changed == 0 {
		if _, err := s.stickySummary(r, id); err != nil {
			return err
		}
		return httpx.Err(http.StatusConflict, "stickyboard_conflict",
			"this board changed in another tab; reload it before saving")
	}
	saved, err := s.stickySummary(r, id)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "stickyboard.save", saved.Name, nil)
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}

// A rename leaves the revision alone: the name is not part of the document,
// so renaming a board in the list never puts a tab editing it into a conflict.
func (s *Server) handleStickyRename(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name, err := stickyName(req.Name)
	if err != nil {
		return err
	}
	result, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE sticky_boards SET name = ?, updated_at = ? WHERE id = ?`, name, time.Now().Unix(), id)
	if err != nil {
		return httpx.Internal(err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return httpx.Internal(err)
	} else if changed == 0 {
		return errStickyNotFound()
	}
	saved, err := s.stickySummary(r, id)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "stickyboard.rename", name, nil)
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}

func (s *Server) handleStickyDuplicate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name, err := stickyName(req.Name)
	if err != nil {
		return err
	}
	var document string
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT document FROM sticky_boards WHERE id = ?`, id).Scan(&document); errors.Is(err, sql.ErrNoRows) {
		return errStickyNotFound()
	} else if err != nil {
		return httpx.Internal(err)
	}
	var doc stickyDocument
	if err := json.Unmarshal([]byte(document), &doc); err != nil {
		return httpx.Internal(err)
	}
	created, err := s.insertSticky(r, name, doc)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "stickyboard.duplicate", name, nil)
	httpx.JSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) handleStickyDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	b, err := s.stickySummary(r, id)
	if err != nil {
		return err
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM sticky_boards WHERE id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "stickyboard.delete", b.Name, nil)
	httpx.NoContent(w)
	return nil
}
