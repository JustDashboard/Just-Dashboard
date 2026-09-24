package deploy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
)

// GeneratedSecretFormats are the shapes a generated secret can take. A
// framework reads its own secret in one of these, and a value in another
// shape is either refused at boot or silently weaker:
//
//   - "" (alphanumeric): Generate characters from [A-Za-z0-9].
//   - "hex": Generate hexadecimal characters, the shape `rails secret` and
//     `openssl rand -hex` write.
//   - "base64": standard base64 over Generate random bytes, the shape
//     `openssl rand -base64` and `npx auth secret` write.
//   - "laravel": `base64:` and standard base64 over Generate random bytes,
//     which is what `php artisan key:generate` writes.
//   - "keylist": four comma-separated base64 values of Generate random bytes
//     each, Strapi's APP_KEYS rotation list.
var GeneratedSecretFormats = []string{"hex", "base64", "laravel", "keylist"}

func validGeneratedSecretFormat(format string) bool {
	return format == "" || slices.Contains(GeneratedSecretFormats, format)
}

// generatedSecretValue mints a secret of the requested length and shape.
// It is called at commit and by rotation, never in a request that could
// echo it, and its output is sealed before it is stored.
func generatedSecretValue(length int, format string) (string, error) {
	switch format {
	case "hex":
		raw := make([]byte, (length+1)/2)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		return hex.EncodeToString(raw)[:length], nil
	case "base64", "laravel":
		raw := make([]byte, length)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		value := base64.StdEncoding.EncodeToString(raw)
		if format == "laravel" {
			value = "base64:" + value
		}
		return value, nil
	case "keylist":
		keys := make([]string, 4)
		for index := range keys {
			raw := make([]byte, length)
			if _, err := rand.Read(raw); err != nil {
				return "", err
			}
			keys[index] = base64.StdEncoding.EncodeToString(raw)
		}
		return strings.Join(keys, ","), nil
	}
	return generatedSecret(length)
}
