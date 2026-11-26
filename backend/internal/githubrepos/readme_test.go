package githubrepos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// readmeAnswer is GitHub's contents answer for a README, its base64 broken
// into lines as GitHub sends it.
func readmeAnswer(content string, size int) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	var lines []string
	for len(encoded) > 60 {
		lines = append(lines, encoded[:60])
		encoded = encoded[60:]
	}
	lines = append(lines, encoded)
	raw, _ := json.Marshal(map[string]any{
		"type": "file", "encoding": "base64", "size": size, "name": "README.md", "path": "docs/README.md",
		"content": strings.Join(lines, "\n") + "\n", "html_url": "https://github.com/octo-org/widgets/blob/main/docs/README.md",
	})
	return string(raw)
}

func TestTheReadmeIsDecodedWithWhereItIs(t *testing.T) {
	fake := newFake(t)
	source := "# Widgets\n\nGrüße, 世界. " + strings.Repeat("Long enough to wrap GitHub's base64 lines. ", 4)
	fake.json("GET /repos/octo-org/widgets/readme", 200, readmeAnswer(source, len(source)))
	readme, err := testAccess(t, fake).Readme(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !readme.Found || readme.Content != source || readme.Truncated || readme.Path != "docs/README.md" ||
		readme.HTMLURL != "https://github.com/octo-org/widgets/blob/main/docs/README.md" {
		t.Fatalf("readme = %+v", readme)
	}
	if raw, _ := json.Marshal(readme); strings.Contains(string(raw), "truncated") {
		t.Fatalf("readme as JSON = %s", raw)
	}
}

func TestARepositoryWithoutAReadmeIsNotAnError(t *testing.T) {
	for status, body := range map[int]string{404: `{"message":"Not Found"}`, 409: `{"message":"Git Repository is empty."}`} {
		fake := newFake(t)
		fake.json("GET /repos/octo-org/widgets/readme", status, body)
		readme, err := testAccess(t, fake).Readme(context.Background())
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		if raw, _ := json.Marshal(readme); string(raw) != `{"found":false}` {
			t.Fatalf("%d: %s", status, raw)
		}
	}
	// Anything else GitHub refuses is still a refusal.
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/readme", 403, `{"message":"Resource not accessible by integration"}`)
	if _, err := testAccess(t, fake).Readme(context.Background()); KindOf(err) != KindForbidden {
		t.Fatalf("403: %v", err)
	}
	fake = newFake(t)
	fake.json("GET /repos/octo-org/widgets/readme", 200, `{"type":"file","encoding":"base64","size":3,"path":"README","content":"!!not base64!!"}`)
	if _, err := testAccess(t, fake).Readme(context.Background()); KindOf(err) != KindUnavailable {
		t.Fatalf("unreadable content: %v", err)
	}
}

func TestALongReadmeIsCutOnACharacter(t *testing.T) {
	// The cut falls inside a three-byte character whichever of these it is:
	// the content is two-, three- and four-byte characters end to end.
	for _, unit := range []string{"é", "世", "🙂", "a"} {
		source := "#" + strings.Repeat(unit, maxReadme/len(unit)+10)
		fake := newFake(t)
		fake.json("GET /repos/octo-org/widgets/readme", 200, readmeAnswer(source, len(source)))
		readme, err := testAccess(t, fake).Readme(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !readme.Found || !readme.Truncated || len(readme.Content) > maxReadme || len(readme.Content) <= maxReadme-utf8.UTFMax ||
			!utf8.ValidString(readme.Content) || !strings.HasPrefix(source, readme.Content) {
			t.Fatalf("%q: found %v truncated %v, %d bytes, valid %v", unit, readme.Found, readme.Truncated, len(readme.Content), utf8.ValidString(readme.Content))
		}
	}
	if text, cut := cutUTF8("héllo", 2); text != "h" || !cut {
		t.Fatalf("cutUTF8 = %q %v", text, cut)
	}
	if text, cut := cutUTF8("héllo", 6); text != "héllo" || cut {
		t.Fatalf("cutUTF8 whole = %q %v", text, cut)
	}

	// A README GitHub will not send inline, or states as larger than is ever
	// decoded, is there but has no content here.
	for name, body := range map[string]string{
		"not inline": `{"type":"file","encoding":"none","size":2097152,"path":"README.md","content":"","html_url":"https://github.com/octo-org/widgets/blob/main/README.md"}`,
		"too large":  `{"type":"file","encoding":"base64","size":4194305,"path":"README.md","content":"aGk=","html_url":"https://github.com/octo-org/widgets/blob/main/README.md"}`,
	} {
		fake := newFake(t)
		fake.json("GET /repos/octo-org/widgets/readme", 200, body)
		readme, err := testAccess(t, fake).Readme(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, _ := json.Marshal(readme)
		if string(raw) != `{"found":true,"path":"README.md","truncated":true,"htmlUrl":"https://github.com/octo-org/widgets/blob/main/README.md"}` {
			t.Fatalf("%s: %s", name, raw)
		}
	}
}

func TestLanguagesAreLargestFirstThenByName(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/languages", 200, `{"Shell":120,"TypeScript":98000,"Go":250000,"CSS":120,"Makefile":120}`)
	languages, err := testAccess(t, fake).Languages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(languages)
	want := `{"languages":[{"name":"Go","bytes":250000},{"name":"TypeScript","bytes":98000},{"name":"CSS","bytes":120},{"name":"Makefile","bytes":120},{"name":"Shell","bytes":120}]}`
	if string(raw) != want {
		t.Fatalf("languages = %s", raw)
	}
	empty := newFake(t)
	empty.json("GET /repos/octo-org/widgets/languages", 200, `{}`)
	languages, err = testAccess(t, empty).Languages(context.Background())
	if raw, _ := json.Marshal(languages); err != nil || string(raw) != `{"languages":[]}` {
		t.Fatalf("empty = %s %v", raw, err)
	}
}
