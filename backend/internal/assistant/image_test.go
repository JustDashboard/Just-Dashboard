package assistant

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// pngImage is a small PNG as the page sends one.
func pngImage(t *testing.T, side int) Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := range side {
		img.Set(x, x, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return Image{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(b.Bytes())}
}

// An image is read for what it is: a format every provider reads, base64 that
// decodes, bytes of the format it names, within its bound.
func TestCheckImageReadsWhatItIs(t *testing.T) {
	good := pngImage(t, 8)
	if err := CheckImage(good, MaxImageBytes); err != nil {
		t.Fatalf("a PNG = %v", err)
	}
	for _, c := range []struct {
		img  Image
		want string
	}{
		{Image{MediaType: "image/svg+xml", Data: good.Data}, "PNG, JPEG, GIF or WebP"},
		{Image{MediaType: "image/png", Data: "not base64!"}, "did not arrive whole"},
		{Image{MediaType: "image/png", Data: ""}, "did not arrive whole"},
		{Image{MediaType: "image/jpeg", Data: good.Data}, "said it was image/jpeg and is not"},
		{Image{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>"))}, "is not"},
	} {
		if err := CheckImage(c.img, MaxImageBytes); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q = %v, want %q", c.img.MediaType, c.img.Data[:min(len(c.img.Data), 12)], err, c.want)
		}
	}
	if err := CheckImage(pngImage(t, 64), 64); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("past its bound = %v", err)
	}
}

// An image's size is read from its header, for the page to make room for it;
// what is not an image it can read has none.
func TestImageSizeReadsTheHeader(t *testing.T) {
	if w, h, ok := ImageSize(pngImage(t, 24)); !ok || w != 24 || h != 24 {
		t.Fatalf("a 24px PNG = %d×%d %v", w, h, ok)
	}
	for _, img := range []Image{
		{MediaType: "image/png", Data: "not base64!"},
		{MediaType: "image/webp", Data: base64.StdEncoding.EncodeToString([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "))},
	} {
		if _, _, ok := ImageSize(img); ok {
			t.Errorf("%s has a size", img.MediaType)
		}
	}
}

// A question's images go before its words, where a model reads them best; an
// image alone is a question with no empty text beside it; and a chat of many
// screenshots keeps the newest whole and says the older ones were there.
func TestAskCarriesImagesAndLeavesOldOnesOut(t *testing.T) {
	img := pngImage(t, 4)
	c := &Conversation{TurnLimit: AgentTurnLimit}
	if err := c.Ask("what is this error?", img, img); err != nil {
		t.Fatal(err)
	}
	blocks := c.Messages[0].Blocks
	if len(blocks) != 3 || blocks[0].Type != BlockImage || blocks[1].Type != BlockImage || blocks[2].Text != "what is this error?" {
		t.Fatalf("blocks = %+v", blocks)
	}
	c.Messages = append(c.Messages, Message{Role: "assistant", Blocks: []Block{text("It is a 502.")}})
	if err := c.Ask("", img); err != nil {
		t.Fatal(err)
	}
	if last := c.Messages[2].Blocks; len(last) != 1 || last[0].Type != BlockImage {
		t.Fatalf("an image alone = %+v", last)
	}

	for i := 0; i < keptImages; i++ {
		c.Messages = append(c.Messages, Message{Role: "assistant", Blocks: []Block{text("Seen.")}})
		if err := c.Ask("and this?", img); err != nil {
			t.Fatal(err)
		}
	}
	whole, left := 0, 0
	for _, m := range c.Messages {
		for _, b := range m.Blocks {
			switch {
			case b.Type == BlockImage:
				whole++
			case b.Text == LeftOutImage:
				left++
			}
		}
	}
	if whole != keptImages || left != 3 || c.Messages[0].Blocks[0].Text != LeftOutImage {
		t.Fatalf("%d whole and %d left out: %+v", whole, left, c.Messages[0].Blocks)
	}
	if c.size() > 20*imageWeight {
		t.Fatalf("images count for their bytes: %d", c.size())
	}

	// A saved chat keeps its images, and each provider is sent them in its shape.
	saved, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversation(c.ID, saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Messages[len(loaded.Messages)-1].Blocks[0]; got.Image == nil || got.Image.Data != img.Data {
		t.Fatalf("a loaded image = %+v", got)
	}
	request := Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockImage, Image: &img}, text("why?")}}}}
	anthropic, err := (&anthropicClient{model: "claude-opus-5-5"}).body(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"source":{"data":"` + img.Data + `","media_type":"image/png","type":"base64"},"type":"image"}`
	if !strings.Contains(string(anthropic), want) {
		t.Fatalf("Anthropic was sent %s", anthropic)
	}
	openai, err := (&openAIClient{model: "gpt"}).body(request)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(openai, &sent); err != nil {
		t.Fatal(err)
	}
	parts := `[{"image_url":{"url":"data:image/png;base64,` + img.Data + `"},"type":"image_url"},{"text":"why?","type":"text"}]`
	if len(sent.Messages) != 1 || string(sent.Messages[0].Content) != parts {
		t.Fatalf("OpenAI was sent %s", openai)
	}
}
