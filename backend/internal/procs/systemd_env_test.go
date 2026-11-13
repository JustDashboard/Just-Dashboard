package procs

import (
	"slices"
	"strings"
	"testing"
)

// secretForTest stands in for dockerx.IsSecretEnvKey, which the api package
// passes; the rule itself is tested there.
func secretForTest(name string) bool {
	upper := strings.ToUpper(name)
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET")
}

const hidden = "\u2022\u2022\u2022 hidden"

// The property as systemd 255 printed it for a unit started with
// `-p 'Environment=A=1 "B=has space" C=x\"q D=tab\ttab E=back\\slash F='`.
const showEnvironment = `A=1 "B=has space" "C=x\"q" "D=tab\ttab" "E=back\\slash" F=`

func TestSplitEnvironmentReadsSystemdsQuoting(t *testing.T) {
	got := SplitEnvironment(showEnvironment)
	want := []string{"A=1", "B=has space", `C=x"q`, "D=tab\ttab", `E=back\slash`, "F="}
	if !slices.Equal(got, want) {
		t.Fatalf("split = %q, want %q", got, want)
	}
	if joined := JoinEnvironment(got); joined != showEnvironment {
		t.Fatalf("joined back = %s, want %s", joined, showEnvironment)
	}
	if got := SplitEnvironment(""); len(got) != 0 {
		t.Fatalf("an empty property split into %q", got)
	}
}

// A unit file may quote part of an entry, use single quotes, or escape a
// space; an unterminated quote runs to the end rather than dropping the rest.
func TestSplitEnvironmentReadsUnitFileForms(t *testing.T) {
	for value, want := range map[string][]string{
		`"A=1"  'B=two words'	C="x y" D=a\ b E=\x41\101`: {"A=1", "B=two words", "C=x y", "D=a b", "E=AA"},
		`DB_PASSWORD="unterminated value`:                {"DB_PASSWORD=unterminated value"},
		`A="" B=`:                                        {"A=", "B="},
		`MSG=caf\u00e9 `:                                 {"MSG=café"},
	} {
		if got := SplitEnvironment(value); !slices.Equal(got, want) {
			t.Errorf("split %s = %q, want %q", value, got, want)
		}
	}
}

// Every entry JoinEnvironment writes reads back as itself.
func TestJoinEnvironmentRoundTrips(t *testing.T) {
	entries := []string{"PLAIN=x", "SPACE=a b", `QUOTE=say "hi"`, `DOLLAR=$HOME`, "BELL=\a", "NL=a\nb",
		"UTF=café", "GLOB=*.log", "EMPTY=", "CTRL=\x01", "KEY=" + hidden}
	joined := JoinEnvironment(entries)
	if got := SplitEnvironment(joined); !slices.Equal(got, entries) {
		t.Fatalf("round trip of %s = %q, want %q", joined, got, entries)
	}
	if !strings.HasPrefix(joined, "PLAIN=x \"SPACE=a b\" ") {
		t.Fatalf("joined = %s", joined)
	}
}

func TestMaskEnvironmentHidesSecretValuesInTheSameFormat(t *testing.T) {
	got := MaskEnvironment(`A=1 DB_PASSWORD=hunter2 "API_TOKEN=two words" "GREETING=hello world"`, secretForTest, hidden)
	want := `A=1 "DB_PASSWORD=` + hidden + `" "API_TOKEN=` + hidden + `" "GREETING=hello world"`
	if got != want {
		t.Fatalf("masked = %s, want %s", got, want)
	}
	if entries := SplitEnvironment(got); !slices.Contains(entries, "DB_PASSWORD="+hidden) || strings.Contains(got, "hunter2") {
		t.Fatalf("masked entries = %q", entries)
	}
	// Nothing to hide: the property exactly as systemd wrote it.
	if got := MaskEnvironment(showEnvironment, secretForTest, hidden); got != showEnvironment {
		t.Fatalf("a list with no secret was rewritten: %s", got)
	}
}

func TestMaskUnitFileSecrets(t *testing.T) {
	unit := strings.Join([]string{
		"[Unit]",
		"Description=App with a password in it",
		"",
		"[Service]",
		"Environment=PORT=8080",
		`  Environment="DB_PASSWORD=hunter2" PORT=8081`,
		`Environment = API_TOKEN='tok en' MODE=prod`,
		`Environment=DEBUG=0 \`,
		`  # the secret below`,
		`  SESSION_SECRET=abc123`,
		"EnvironmentFile=/etc/app/secret.env",
		"PassEnvironment=DB_PASSWORD",
		"SetCredential=dbpass:hunter2",
		"SetCredentialEncrypted=dbkey: k6iUCUh0RJCQyvL8k8q1UyAAAAABAAAADAAAABAAAAC",
		"ExecStart=/usr/bin/app --password=hunter2",
		"",
	}, "\n")
	got := MaskUnitFileSecrets(unit, secretForTest, hidden)
	lines := strings.Split(got, "\n")
	if len(lines) != len(strings.Split(unit, "\n")) {
		t.Fatalf("masking changed the line count:\n%s", got)
	}
	want := map[int]string{
		4:  "Environment=PORT=8080",
		5:  `  Environment="DB_PASSWORD=` + hidden + `" PORT=8081`,
		6:  `Environment="API_TOKEN=` + hidden + `" MODE=prod`,
		7:  `Environment=DEBUG=0 "SESSION_SECRET=` + hidden + `"`,
		8:  `  # the secret below`,
		9:  "",
		10: "EnvironmentFile=/etc/app/secret.env",
		11: "PassEnvironment=DB_PASSWORD",
		12: "SetCredential=dbpass:" + hidden,
		13: "SetCredentialEncrypted=dbkey: k6iUCUh0RJCQyvL8k8q1UyAAAAABAAAADAAAABAAAAC",
		14: "ExecStart=/usr/bin/app --password=hunter2",
	}
	for i, line := range want {
		if lines[i] != line {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], line)
		}
	}
	for _, leaked := range []string{"DB_PASSWORD=hunter2", "tok en", "abc123", "dbpass:hunter2"} {
		if strings.Contains(got, leaked) {
			t.Errorf("masked file still holds %q:\n%s", leaked, got)
		}
	}
	// A credential with no ID is still hidden.
	if got := MaskUnitFileSecrets("SetCredential=hunter2", secretForTest, hidden); got != "SetCredential="+hidden {
		t.Errorf("an ID-less credential = %q", got)
	}
	// Nothing to hide: the text exactly as it was, continuation and all.
	plain := "[Service]\nEnvironment=A=1 \\\n  B=2\nExecStart=/bin/true\n"
	if got := MaskUnitFileSecrets(plain, secretForTest, hidden); got != plain {
		t.Errorf("a file with no secret was rewritten:\n%s", got)
	}
}
