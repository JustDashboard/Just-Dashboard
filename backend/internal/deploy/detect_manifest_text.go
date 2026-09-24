package deploy

import (
	"bytes"
	"unicode/utf16"
	"unicode/utf8"
)

// Manifests written on Windows often start with a byte-order mark: PowerShell
// 5.1's Set-Content and Out-File write one by default, and Visual Studio saves
// some files as UTF-16. npm, Composer and Deno read such a file without
// complaint, while Go's JSON decoder stops at the first byte, so the same
// package.json that `npm ci` installs was "malformed" to detection and to the
// recipe. Every manifest detection reads as data goes through decodeManifest.

var (
	utf8ByteOrderMark    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEByteOrderMark = []byte{0xFF, 0xFE}
	utf16BEByteOrderMark = []byte{0xFE, 0xFF}
)

// decodeManifest returns the manifest's text as UTF-8 without a byte-order
// mark, and reports whether one was removed. UTF-16 is transcoded only when
// it announces itself with a mark; text that is neither is returned as it is.
func decodeManifest(content []byte) ([]byte, bool) {
	switch {
	case bytes.HasPrefix(content, utf8ByteOrderMark):
		return content[len(utf8ByteOrderMark):], true
	case bytes.HasPrefix(content, utf16LEByteOrderMark), bytes.HasPrefix(content, utf16BEByteOrderMark):
		littleEndian := bytes.HasPrefix(content, utf16LEByteOrderMark)
		body := content[2:]
		if len(body)%2 != 0 {
			return content, false
		}
		units := make([]uint16, 0, len(body)/2)
		for index := 0; index+1 < len(body); index += 2 {
			if littleEndian {
				units = append(units, uint16(body[index])|uint16(body[index+1])<<8)
			} else {
				units = append(units, uint16(body[index])<<8|uint16(body[index+1]))
			}
		}
		decoded := make([]byte, 0, len(units))
		for _, r := range utf16.Decode(units) {
			decoded = utf8.AppendRune(decoded, r)
		}
		return decoded, true
	}
	return content, false
}

// manifestText is decodeManifest for the callers that only need the text.
func manifestText(content []byte) []byte {
	text, _ := decodeManifest(content)
	return text
}
