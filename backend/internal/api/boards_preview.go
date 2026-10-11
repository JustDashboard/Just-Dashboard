package api

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"unicode/utf8"
)

const (
	// The thumbnail is a picture of a board's shape, not of every element: the
	// largest ones, so frames and the shapes in them are what it shows.
	maxBoardPreviewElements  = 400
	maxBoardPreviewPoints    = 64
	maxBoardPreviewResources = 24
	maxBoardPreviewText      = 200
	// Excalidraw coordinates are canvas pixels; anything past this is a client
	// bug, and keeps the rounding below inside an int on every platform.
	maxBoardPreviewCoordinate = 1e12

	emptyBoardPreview = `{"background":"","count":0,"elements":[],"resources":[]}`
)

// boardPreviewTypes are the element types a thumbnail can draw. The rest
// (selection boxes, anything a later Excalidraw adds) are skipped.
var boardPreviewTypes = map[string]bool{
	"rectangle": true, "ellipse": true, "diamond": true, "line": true, "arrow": true,
	"freedraw": true, "text": true, "image": true, "frame": true, "magicframe": true,
	"embeddable": true, "iframe": true,
}

type boardPreviewElement struct {
	Type        string   `json:"type"`
	X           int      `json:"x"`
	Y           int      `json:"y"`
	W           int      `json:"w"`
	H           int      `json:"h"`
	Angle       float64  `json:"angle,omitempty"`
	Stroke      string   `json:"stroke,omitempty"`
	Fill        string   `json:"fill,omitempty"`
	FillStyle   string   `json:"fillStyle,omitempty"`
	StrokeWidth float64  `json:"strokeWidth,omitempty"`
	StrokeStyle string   `json:"strokeStyle,omitempty"`
	Round       bool     `json:"round,omitempty"`
	Opacity     *int     `json:"opacity,omitempty"`
	Points      [][2]int `json:"points,omitempty"`
	Text        string   `json:"text,omitempty"`
	FontSize    float64  `json:"fontSize,omitempty"`
	FontFamily  int      `json:"fontFamily,omitempty"`
	TextAlign   string   `json:"textAlign,omitempty"`
	StartArrow  bool     `json:"startArrow,omitempty"`
	EndArrow    bool     `json:"endArrow,omitempty"`
}

type boardPreviewResource struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id,omitempty"`
}

type boardPreview struct {
	Background string                 `json:"background"`
	Count      int                    `json:"count"`
	Elements   []boardPreviewElement  `json:"elements"`
	Resources  []boardPreviewResource `json:"resources"`
}

// boardSceneElement is the part of an Excalidraw element the thumbnail reads.
// Pointers mark the fields whose absence means something other than zero.
type boardSceneElement struct {
	Type            string          `json:"type"`
	IsDeleted       bool            `json:"isDeleted"`
	X               float64         `json:"x"`
	Y               float64         `json:"y"`
	Width           float64         `json:"width"`
	Height          float64         `json:"height"`
	Angle           float64         `json:"angle"`
	StrokeColor     string          `json:"strokeColor"`
	BackgroundColor string          `json:"backgroundColor"`
	FillStyle       string          `json:"fillStyle"`
	StrokeWidth     float64         `json:"strokeWidth"`
	StrokeStyle     string          `json:"strokeStyle"`
	Roundness       json.RawMessage `json:"roundness"`
	Opacity         *float64        `json:"opacity"`
	Points          [][]float64     `json:"points"`
	Text            string          `json:"text"`
	OriginalText    string          `json:"originalText"`
	Name            string          `json:"name"`
	FontSize        float64         `json:"fontSize"`
	FontFamily      float64         `json:"fontFamily"`
	TextAlign       string          `json:"textAlign"`
	StartArrowhead  *string         `json:"startArrowhead"`
	EndArrowhead    *string         `json:"endArrowhead"`
	CustomData      json.RawMessage `json:"customData"`
}

// boardPreviewOf reads a scene for its thumbnail. It decodes the elements and
// the background colour and nothing else: a scene is up to 16 MiB, nearly all
// of it the base64 images under `files`, which a thumbnail never needs.
func boardPreviewOf(scene json.RawMessage) (boardPreview, error) {
	preview := boardPreview{Elements: []boardPreviewElement{}, Resources: []boardPreviewResource{}}
	var doc struct {
		Elements []json.RawMessage `json:"elements"`
		AppState json.RawMessage   `json:"appState"`
	}
	if err := json.Unmarshal(scene, &doc); err != nil {
		return boardPreview{}, err
	}
	var appState struct {
		ViewBackgroundColor string `json:"viewBackgroundColor"`
	}
	// The background is decoration; a mistyped one is no reason to lose the
	// shapes.
	_ = json.Unmarshal(doc.AppState, &appState)
	if validBoardColor(appState.ViewBackgroundColor) {
		preview.Background = appState.ViewBackgroundColor
	}

	var area []float64
	seen := map[boardPreviewResource]bool{}
	for _, raw := range doc.Elements {
		var e boardSceneElement
		if err := json.Unmarshal(raw, &e); err != nil {
			return boardPreview{}, err
		}
		if e.IsDeleted {
			continue
		}
		preview.Count++
		if r, ok := boardPreviewResourceOf(e.CustomData); ok && !seen[r] && len(preview.Resources) < maxBoardPreviewResources {
			seen[r] = true
			preview.Resources = append(preview.Resources, r)
		}
		kept, ok := boardPreviewElementOf(e)
		if !ok {
			continue
		}
		preview.Elements = append(preview.Elements, kept)
		area = append(area, math.Max(math.Abs(e.Width), 1)*math.Max(math.Abs(e.Height), 1))
	}

	if len(preview.Elements) > maxBoardPreviewElements {
		order := make([]int, len(preview.Elements))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return area[order[a]] > area[order[b]] })
		order = order[:maxBoardPreviewElements]
		// Back into the scene's paint order, so a frame the thumbnail keeps is
		// still drawn under the shapes in it.
		sort.Ints(order)
		largest := make([]boardPreviewElement, len(order))
		for i, at := range order {
			largest[i] = preview.Elements[at]
		}
		preview.Elements = largest
	}
	return preview, nil
}

func boardPreviewElementOf(e boardSceneElement) (boardPreviewElement, bool) {
	if !boardPreviewTypes[e.Type] {
		return boardPreviewElement{}, false
	}
	for _, v := range []float64{e.X, e.Y, e.Width, e.Height} {
		if !finiteWithin(v) {
			return boardPreviewElement{}, false
		}
	}
	out := boardPreviewElement{
		Type: e.Type,
		X:    int(math.Round(e.X)), Y: int(math.Round(e.Y)),
		W: int(math.Round(e.Width)), H: int(math.Round(e.Height)),
		StrokeWidth: e.StrokeWidth,
	}
	if finiteWithin(e.Angle) {
		out.Angle = math.Round(e.Angle*1000) / 1000
	}
	if validBoardColor(e.StrokeColor) {
		out.Stroke = e.StrokeColor
	}
	if validBoardColor(e.BackgroundColor) {
		out.Fill = e.BackgroundColor
		switch e.FillStyle {
		case "hachure", "cross-hatch", "zigzag":
			out.FillStyle = e.FillStyle
		}
	}
	switch e.StrokeStyle {
	case "dashed", "dotted":
		out.StrokeStyle = e.StrokeStyle
	}
	if r := bytes.TrimSpace(e.Roundness); len(r) > 0 && r[0] == '{' {
		out.Round = true
	}
	if e.Opacity != nil && finiteWithin(*e.Opacity) {
		if opacity := int(math.Round(math.Min(math.Max(*e.Opacity, 0), 100))); opacity != 100 {
			out.Opacity = &opacity
		}
	}

	switch e.Type {
	case "line", "arrow", "freedraw":
		out.Points = boardPreviewPoints(e.Points)
		out.StartArrow = e.StartArrowhead != nil
		out.EndArrow = e.EndArrowhead != nil
	case "text":
		out.Text = e.Text
		if out.Text == "" {
			out.Text = e.OriginalText
		}
		out.FontSize = e.FontSize
		if finiteWithin(e.FontFamily) {
			out.FontFamily = int(e.FontFamily)
		}
		switch e.TextAlign {
		case "center", "right":
			out.TextAlign = e.TextAlign
		}
	case "frame", "magicframe":
		out.Text = e.Name
	}
	out.Text = truncateRunes(out.Text, maxBoardPreviewText)
	return out, true
}

// boardPreviewPoints rounds an element's points and thins them to at most
// maxBoardPreviewPoints by an even stride, always keeping the first and last:
// a hand-drawn stroke is thousands of points and a thumbnail needs its shape.
func boardPreviewPoints(points [][]float64) [][2]int {
	rounded := make([][2]int, 0, len(points))
	for _, p := range points {
		if len(p) < 2 || !finiteWithin(p[0]) || !finiteWithin(p[1]) {
			continue
		}
		rounded = append(rounded, [2]int{int(math.Round(p[0])), int(math.Round(p[1]))})
	}
	if len(rounded) <= maxBoardPreviewPoints {
		return rounded
	}
	thinned := make([][2]int, maxBoardPreviewPoints)
	last := len(rounded) - 1
	for i := range thinned {
		thinned[i] = rounded[i*last/(maxBoardPreviewPoints-1)]
	}
	return thinned
}

// boardPreviewResourceOf reads the card an operator inserted from the
// dashboard (`customData.justDashboard`). It is best-effort: customData is
// whatever a scene carries, so anything unexpected is not a resource.
func boardPreviewResourceOf(raw json.RawMessage) (boardPreviewResource, bool) {
	if len(raw) == 0 {
		return boardPreviewResource{}, false
	}
	var data struct {
		JustDashboard struct {
			Kind       string  `json:"kind"`
			ResourceID float64 `json:"resourceId"`
		} `json:"justDashboard"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return boardPreviewResource{}, false
	}
	jd := data.JustDashboard
	switch jd.Kind {
	case "server":
		return boardPreviewResource{Kind: "server"}, true
	case "project", "database":
		if jd.ResourceID >= 1 && jd.ResourceID <= maxBoardPreviewCoordinate && jd.ResourceID == math.Trunc(jd.ResourceID) {
			return boardPreviewResource{Kind: jd.Kind, ID: int64(jd.ResourceID)}, true
		}
	}
	return boardPreviewResource{}, false
}

func finiteWithin(v float64) bool {
	return !math.IsNaN(v) && math.Abs(v) <= maxBoardPreviewCoordinate
}

// validBoardColor accepts only #rgb, #rgba, #rrggbb and #rrggbbaa. A colour is
// written into an SVG attribute on other operators' screens, so a name,
// rgb(...) or anything with a quote in it is dropped rather than escaped.
func validBoardColor(c string) bool {
	switch len(c) {
	case 4, 5, 7, 9:
	default:
		return false
	}
	if c[0] != '#' {
		return false
	}
	for i := 1; i < len(c); i++ {
		switch b := c[i]; {
		case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		default:
			return false
		}
	}
	return true
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// boardPreviewJSON is what is stored beside a scene. The scene is opaque data
// the server keeps for the editor and the preview is a convenience, so a scene
// whose elements do not decode into the preview's shape saves with an empty
// one instead of failing.
func boardPreviewJSON(scene json.RawMessage) string {
	preview, err := boardPreviewOf(scene)
	if err != nil {
		return emptyBoardPreview
	}
	out, err := json.Marshal(preview)
	if err != nil {
		return emptyBoardPreview
	}
	return string(out)
}
