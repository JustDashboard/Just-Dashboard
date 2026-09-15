package gameserver

import (
	"strings"
	"testing"
)

func TestPublicPropertiesNeverReturnUndeclaredSecrets(t *testing.T) {
	values, preview := PublicProperties("motd=hello\nrcon.password=synthetic-secret\nplugin-token=other-secret\n", []string{"motd"})
	if len(values) != 1 || values["motd"] != "hello" || strings.Contains(preview, "secret") || strings.Contains(preview, "rcon") {
		t.Fatalf("public properties: %#v %q", values, preview)
	}
}
func TestPropertyDuplicatesEscapesAndContinuationsMatchServerMeaning(t *testing.T) {
	file := ParseProperties("# keep me\nmax-players=5\nmax\\u002dplayers: 10\nmotd=hello\\\n world\nrcon.password=private\n")
	if value, _ := file.Get("max-players"); value != "10" {
		t.Fatalf("last declaration = %q", value)
	}
	if value, _ := file.Get("motd"); value != "helloworld" {
		t.Fatalf("continuation = %q", value)
	}
	if err := file.Set("max-players", "20"); err != nil {
		t.Fatal(err)
	}
	if err := file.Set("motd", `text\`); err != nil {
		t.Fatal(err)
	}
	result := ParseProperties(file.Render())
	if value, _ := result.Get("max-players"); value != "20" {
		t.Fatalf("duplicate shadows edit: %q", value)
	}
	if value, _ := result.Get("rcon.password"); value != "private" {
		t.Fatal("trailing backslash swallowed next key")
	}
	if value, _ := result.Get("motd"); value != `text\` {
		t.Fatalf("backslash changed: %q", value)
	}
	if !strings.HasPrefix(file.Render(), "# keep me\n") {
		t.Fatal("lost comment")
	}
}
func TestNumericPropertyHonorsZeroMinimum(t *testing.T) {
	if err := validatePropertyValue(KnownProperty{Key: "spawn-protection", Kind: "number", Minimum: 0, Maximum: 100}, "-1"); err == nil {
		t.Fatal("negative value bypassed zero minimum")
	}
}

func TestPropertyUnicodeSurrogatePair(t *testing.T) {
	file := ParseProperties(`motd=Hello \uD83D\uDE00`)
	if value, _ := file.Get("motd"); value != "Hello 😀" {
		t.Fatalf("surrogate pair = %q", value)
	}
}
