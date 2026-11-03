package assistant

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
)

// Images a person pastes into a deployment's or the server's chat: a screenshot
// of an error, a page that looks wrong. The page makes each one small before it
// is sent (its longest side at most what the models read at full detail, 1568
// pixels), so the bounds here are generous for a screenshot and tight for
// anything else. An image cannot be masked the way text is: what it shows reaches
// the model as it is, which the page says beside the box.

const (
	// MaxImages is how many images one question carries.
	MaxImages = 4
	// MaxImageBytes bounds one image, decoded. Anthropic takes five megabytes;
	// a screenshot the page has made small is well under one.
	MaxImageBytes = 1536 << 10
	// MaxThumbBytes bounds the small copy the page keeps to draw a saved chat's
	// question again.
	MaxThumbBytes = 96 << 10
	// keptImages and keptImageBytes are how many images, and how much of them as
	// base64, a conversation keeps whole: it is sealed and saved after every step.
	// Older ones are left out, as old tool results are, so a chat of many
	// screenshots stays a size worth sending and saving.
	keptImages     = 8
	keptImageBytes = 6 << 20
	// imageWeight is what an image counts for against maxHistory: about what a
	// model reads one as (some 1,600 tokens at 1568 pixels), not its bytes.
	imageWeight = 6_000
)

// ImageTypes are the formats every provider the assistant asks reads.
var ImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// Image is one image as it is sent: its format and its bytes in base64.
type Image struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

// LeftOutImage is what an older image reads as once a chat has left it out.
const LeftOutImage = "(an image the person pasted earlier, left out to keep the chat short)"

// CheckImage reads an image a page sent: a format a provider reads, base64 that
// decodes, bytes that are that format, within bound. It answers with what was
// wrong, in words for the person.
func CheckImage(img Image, bound int) error {
	if !slices.Contains(ImageTypes, img.MediaType) {
		return fmt.Errorf("an image is PNG, JPEG, GIF or WebP, and %q is none of them", img.MediaType)
	}
	if base64.StdEncoding.DecodedLen(len(img.Data)) > bound+3 {
		return fmt.Errorf("an image is at most %d KB once it is made small, and this one is larger", bound>>10)
	}
	raw, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil || len(raw) == 0 {
		return fmt.Errorf("an image did not arrive whole")
	}
	if len(raw) > bound {
		return fmt.Errorf("an image is at most %d KB once it is made small, and this one is larger", bound>>10)
	}
	if sniffed := http.DetectContentType(raw); sniffed != img.MediaType {
		return fmt.Errorf("an image said it was %s and is not", img.MediaType)
	}
	return nil
}

// leaveOutOldImages keeps the newest images, up to keptImages of them and
// keptImageBytes, and leaves the older ones out, each as a line saying one was
// there. The newest question's are always kept: they are what was just asked.
func (c *Conversation) leaveOutOldImages() {
	seen, size := 0, 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		blocks := c.Messages[i].Blocks
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].Type != BlockImage || blocks[j].Image == nil {
				continue
			}
			seen, size = seen+1, size+len(blocks[j].Image.Data)
			newest := i == len(c.Messages)-1
			if !newest && (seen > keptImages || size > keptImageBytes) {
				blocks[j] = Block{Type: BlockText, Text: LeftOutImage}
			}
		}
	}
}
