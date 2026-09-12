package gameserver

import (
	"strings"
	"testing"
)

// The console is the one field in this product that reaches a process inside a
// container. Whatever it accepts must be something a shell could not act on.
func FuzzValidateCommand(f *testing.F) {
	for _, seed := range []string{
		"list", "/list", "say hello", "whitelist add Notch", "op Notch",
		"say hello; rm -rf /", "say `id`", "say $(id)", "a|b", "a&b", "a>b", "a<b",
		"say a\nstop", "say a\x00stop", "", "   ", strings.Repeat("x", 900),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		command, err := ValidateCommand(input)
		if err != nil {
			return
		}
		if command == "" || len(command) > 512 {
			t.Fatalf("accepted %q as %q", input, command)
		}
		for _, forbidden := range []string{";", "&", "|", "$", "`", ">", "<", "\\", "\n", "\r", "\x00"} {
			if strings.Contains(command, forbidden) {
				t.Fatalf("accepted %q, which carries %q", command, forbidden)
			}
		}
		// The command is split on spaces into argv. No element may be empty or
		// begin with a dash that the console client would read as its own flag.
		for _, field := range strings.Fields(command) {
			if field == "" {
				t.Fatalf("accepted %q, which splits into an empty argument", command)
			}
		}
		if strings.HasPrefix(command, "-") {
			t.Fatalf("accepted %q, which the console client would read as a flag", command)
		}
	})
}

func FuzzPlayerCommand(f *testing.F) {
	for _, seed := range []string{"Notch", "a", "name with space", "rm -rf /", "Bob_99", "", "../etc"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		for _, action := range []PlayerAction{
			PlayerKick, PlayerBan, PlayerPardon, PlayerOp, PlayerDeop, PlayerWhitelist, PlayerUnlist,
		} {
			command, err := PlayerCommand(action, name)
			if err != nil {
				continue
			}
			if _, validateErr := ValidateCommand(command); validateErr != nil {
				t.Fatalf("%s built %q, which the console itself refuses: %v", action, command, validateErr)
			}
			if !strings.HasSuffix(command, " "+name) {
				t.Fatalf("%s built %q for player %q", action, command, name)
			}
		}
	})
}

// A properties file round-trips byte-for-byte, whatever is in it. An editor
// that quietly rewrites somebody's comments is an editor they cannot trust.
func FuzzPropertiesRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"a=1\nb=2\n", "#comment\n\nkey=value", "no-equals", "=novalue",
		"key=value with spaces  ", "\r\nkey=value\r\n", "", "key=",
		strings.Repeat("k=v\n", 500), "key=\x00value",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		// Windows line endings are normalized on read, which is the one
		// documented change; compare against that normalized form.
		normalized := strings.ReplaceAll(content, "\r\n", "\n")
		file := ParseProperties(content)
		if rendered := file.Render(); rendered != normalized {
			t.Fatalf("round trip changed the file:\n%q\n%q", rendered, normalized)
		}
		// Setting a declared key must not disturb any other line.
		before := file.Render()
		if err := file.Set("max-players", "40"); err != nil {
			t.Fatal(err)
		}
		after := file.Render()
		if value, found := file.Get("max-players"); !found || value != "40" {
			t.Fatalf("Set did not take: %q (%v)", value, found)
		}
		if len(after) < len(before)-len("max-players=") {
			t.Fatalf("Set shrank the file from %d to %d bytes", len(before), len(after))
		}
	})
}

// Every archive entry name is checked before anything is extracted.
func FuzzArchiveRoot(f *testing.F) {
	for _, seed := range []string{
		"server/server.properties", "/etc/passwd", "../../etc/passwd",
		"a\\b", "server/world/level.dat", "", "./server.properties",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		root, err := archiveRoot([]ArchiveEntry{{Path: name, Bytes: 1}})
		if err != nil {
			return
		}
		if strings.HasPrefix(root, "/") || strings.Contains(root, "..") ||
			strings.Contains(root, "\\") || root == "" {
			t.Fatalf("accepted %q and chose root %q", name, root)
		}
	})
}
