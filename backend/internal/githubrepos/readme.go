package githubrepos

import (
	"cmp"
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// maxReadme is how much of a README is answered. A README is prose; one
	// past this is cut, on a character boundary, and says so.
	maxReadme = 512 << 10
	// maxReadmeSize is the size, as GitHub states it, past which a README is
	// not decoded at all.
	maxReadmeSize = 4 << 20
)

// Readme is GitHubReadme: the repository's README at its default branch, as
// Markdown source. A repository without one is Found false, not an error.
type Readme struct {
	Found bool `json:"found"`
	// Path is where it is in the repository: README.md, docs/README.md.
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	// Truncated is a README cut at 512 KiB, or one GitHub would not send
	// inline at all, which then has no Content.
	Truncated bool   `json:"truncated,omitempty"`
	HTMLURL   string `json:"htmlUrl,omitempty"`
}

// cutUTF8 is text cut to at most limit bytes without splitting a character.
func cutUTF8(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// Readme reads the README GitHub prefers for the repository (the one its
// own page shows). GitHub answers 404 when there is none and for an empty
// repository, which is Found false.
//
// The JSON form is asked for rather than the raw one because it also says
// where the file is. GitHub sends a file inline, as base64, up to 1 MiB; a
// larger README comes with no content, and one it states as over 4 MiB is
// not decoded whatever it came with. Both answer Found and Truncated with
// the path and the address on GitHub and no Content, so the page can say
// there is a README and where to read it.
func (a *RepoAccess) Readme(ctx context.Context) (*Readme, error) {
	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		HTMLURL  string `json:"html_url"`
	}
	path := a.Path("/readme")
	if _, err := a.Client.Do(ctx, http.MethodGet, path, nil, &file); err != nil {
		switch KindOf(err) {
		case KindNotFound, KindConflict:
			return &Readme{}, nil
		}
		return nil, err
	}
	out := &Readme{Found: true, Path: file.Path, HTMLURL: webURL(file.HTMLURL)}
	if file.Encoding != "base64" || file.Size > maxReadmeSize {
		out.Truncated = true
		return out, nil
	}
	content, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(file.Content))
	if err != nil {
		return nil, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub's copy of the README was not readable", Path: path}
	}
	out.Content, out.Truncated = cutUTF8(string(content), maxReadme)
	return out, nil
}

// Language is one language of a repository and the bytes of code in it.
type Language struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Languages is GitHubLanguages.
type Languages struct {
	Languages []Language `json:"languages"`
}

// Languages reads the repository's languages as GitHub counts them, largest
// first, then by name so equal ones keep an order.
func (a *RepoAccess) Languages(ctx context.Context) (*Languages, error) {
	var raw map[string]int64
	if _, err := a.Client.Do(ctx, http.MethodGet, a.Path("/languages"), nil, &raw); err != nil {
		return nil, err
	}
	out := &Languages{Languages: make([]Language, 0, len(raw))}
	for name, bytes := range raw {
		if name != "" {
			out.Languages = append(out.Languages, Language{Name: name, Bytes: bytes})
		}
	}
	slices.SortFunc(out.Languages, func(x, y Language) int {
		return cmp.Or(cmp.Compare(y.Bytes, x.Bytes), cmp.Compare(x.Name, y.Name))
	})
	return out, nil
}
