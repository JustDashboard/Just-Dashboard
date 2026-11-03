package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// pastedPNG is a PNG of side pixels; noisy ones do not compress, so a screenshot's
// size can be made without a screenshot.
func pastedPNG(t *testing.T, side int, noisy bool) assistant.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	random := rand.New(rand.NewPCG(1, 2))
	for y := range side {
		for x := range side {
			if noisy {
				img.Set(x, y, color.RGBA{R: uint8(random.Uint32()), G: uint8(random.Uint32()), B: uint8(random.Uint32()), A: 255})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return assistant.Image{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(b.Bytes())}
}

func pasted(t *testing.T, question string, images ...assistant.Image) string {
	t.Helper()
	thumb := pastedPNG(t, 8, false)
	sent := []map[string]any{}
	for _, img := range images {
		sent = append(sent, map[string]any{"mediaType": img.MediaType, "data": img.Data, "thumb": thumb})
	}
	raw, _ := json.Marshal(map[string]any{"question": question, "images": sent, "mode": "changes"})
	return string(raw)
}

// A screenshot pasted with a question reaches the model as an image before the
// words, the saved chat keeps its thumbnail to draw the question again, and an
// image is a question on its own. Three screenshots are more than the 4 MB an
// ordinary body may be, and are taken.
func TestAPastedImageReachesTheModel(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	shot := pastedPNG(t, 16, false)
	f.provider.queue(claudeAnswer("That is a 502 from the proxy."))
	events := f.ask(t, pasted(t, "What is this error?", shot))
	if len(ofType(events, "error")) != 0 || doneOf(t, events)["kept"] != true {
		t.Fatalf("events = %v", events)
	}
	sent := f.provider.raw[0]
	image := `{"source":{"data":"` + shot.Data + `","media_type":"image/png","type":"base64"},"type":"image"},{"text":"What is this error?","type":"text"}`
	if !strings.Contains(sent, image) {
		t.Fatalf("the model was sent %s", sent)
	}
	id := ofType(events, "start")[0]["conversation"].(string)
	read := f.do(http.MethodGet, "/assistant/chats/"+id, "")
	var got struct {
		Lines []map[string]any `json:"lines"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &got); err != nil || len(got.Lines) == 0 {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	thumbs, _ := got.Lines[0]["images"].([]any)
	if got.Lines[0]["type"] != "question" || len(thumbs) != 1 || strings.Contains(read.Body.String(), shot.Data) {
		t.Fatalf("the question was kept as %v", got.Lines[0])
	}
	if detail, _ := f.audit(t, "deploy.assistant.ask"); !strings.Contains(detail, `"images":1`) {
		t.Fatalf("audit = %s", detail)
	}

	f.provider.queue(claudeAnswer("Three screenshots of noise."))
	large := pastedPNG(t, 640, true)
	body := pasted(t, "", large, large, large)
	if len(body) <= 4<<20 {
		t.Fatalf("the body is %d bytes, not past the ordinary bound", len(body))
	}
	events = f.ask(t, body)
	if len(ofType(events, "error")) != 0 || doneOf(t, events)["kept"] != true {
		t.Fatalf("three screenshots = %v", events)
	}
	if start := ofType(events, "start")[0]; start["title"] != "A pasted image" {
		t.Fatalf("an image-only chat is titled %v", start["title"])
	}
	if sent := f.provider.raw[len(f.provider.raw)-1]; strings.Count(sent, `"type":"image"`) != 3 || strings.Contains(sent, `"text":""`) {
		t.Fatalf("the image-only question was sent as %.400s", sent)
	}
}

// What is not an image a provider reads, or more of them than a question carries,
// is refused before anything is asked, in words that say what was wrong.
func TestAPastedImageThatIsNotOneIsRefused(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	shot := pastedPNG(t, 8, false)
	svg := assistant.Image{MediaType: "image/svg+xml", Data: base64.StdEncoding.EncodeToString([]byte("<svg/>"))}
	lying := assistant.Image{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("<html>not a png</html>"))}
	for _, c := range []struct {
		body, want string
	}{
		{pasted(t, "x", svg), "PNG, JPEG, GIF or WebP"},
		{pasted(t, "x", lying), "said it was image/png and is not"},
		{pasted(t, "x", shot, shot, shot, shot, shot), "at most 4 images"},
		{`{"decisions":{"a":true},"images":[{"mediaType":"image/png","data":"` + shot.Data + `","thumb":{"mediaType":"image/png","data":"` + shot.Data + `"}}]}`, "ask a question, or answer"},
	} {
		rec := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), c.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%.60s = %d %s", c.body, rec.Code, rec.Body.String())
		}
	}
	if len(f.provider.raw) != 0 {
		t.Fatalf("a refused question reached the model: %d", len(f.provider.raw))
	}
}
