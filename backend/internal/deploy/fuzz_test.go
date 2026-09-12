package deploy

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fuzz targets below assert bounds rather than merely finishing. A target
// that only proves "did not panic" would pass on a parser that happily accepted
// a traversal, so each one states what the accepted output may contain.

func FuzzParseDotenv(f *testing.F) {
	for _, seed := range []string{
		"A=1\nB=2\n", "# comment\nKEY=\"quoted value\"\n", "KEY='single'\n",
		"MULTI=\"line one\nline two\"\n", "=novalue\n", "NO_EQUALS\n",
		"KEY=value # trailing\n", "\x00=nul\n", strings.Repeat("K=v\n", 300),
		"export KEY=value\n", "KEY=\n", "  SPACED  =  value  \n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		values, err := ParseDotenv(input)
		if err != nil {
			return
		}
		if len(values) > 256 {
			t.Fatalf("accepted %d variables; the documented cap is 256", len(values))
		}
		for name, value := range values {
			if ValidateEnvKey(name) != nil {
				t.Fatalf("accepted invalid variable name %q", name)
			}
			if strings.ContainsRune(value, '\x00') {
				t.Fatalf("accepted a NUL inside the value of %q", name)
			}
			if len(value) > maxDeploymentVariableValue {
				t.Fatalf("accepted a %d byte value for %q", len(value), name)
			}
		}
	})
}

func FuzzParseVariableReference(f *testing.F) {
	for _, seed := range []string{
		"${{credential.api-token}}", "${{database.1.url}}", "${{secret.name}}",
		"${{}}", "${{ credential.x }}", "$${{credential.x}}", "${{credential." + strings.Repeat("a", 500) + "}}",
		"plain value", "${{credential.../../etc/passwd}}", "${{deployment.7.endpoint}}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		reference, err := ParseVariableReference(input)
		if err != nil {
			return
		}
		if reference.Kind == "" {
			t.Fatalf("accepted %q with no kind", input)
		}
		if strings.ContainsAny(reference.Kind+reference.Target, "\x00\r\n/\\") {
			t.Fatalf("accepted a reference carrying a separator: %#v", reference)
		}
		if strings.Contains(reference.Target, "..") {
			t.Fatalf("accepted a parent reference: %#v", reference)
		}
	})
}

func FuzzNormalizeImageReference(f *testing.F) {
	for _, seed := range []string{
		"nginx", "nginx:1.27", "ghcr.io/owner/app:tag",
		"app@sha256:" + strings.Repeat("a", 64), "UPPER/Case:tag", "",
		"registry.test:5000/team/app:v1", "app:tag@sha256:short",
		"-leading-dash", "a" + strings.Repeat("/b", 200), "nginx:tag with space",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		normalized, err := normalizeImageReference(input)
		if err != nil {
			return
		}
		if normalized == "" {
			t.Fatalf("accepted %q and returned nothing", input)
		}
		if strings.ContainsAny(normalized, " \t\r\n\x00") {
			t.Fatalf("accepted whitespace in %q", normalized)
		}
		if !utf8.ValidString(normalized) {
			t.Fatalf("accepted invalid UTF-8 from %q", input)
		}
		// Normalization must be a fixed point: running it twice cannot keep
		// changing what the release will pin.
		again, againErr := normalizeImageReference(normalized)
		if againErr != nil || again != normalized {
			t.Fatalf("normalizing %q twice gave %q then %q (%v)", input, normalized, again, againErr)
		}
	})
}

func FuzzRedactSecrets(f *testing.F) {
	for _, seed := range []string{
		"token=hunter2", "nothing to hide", "hunter2 in the middle",
		"HUNTER2", "hun\nter2", strings.Repeat("hunter2", 100), "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		secret := "hunter2"
		var cleaned string
		emit := redactBuildEmitter(map[string]string{"TOKEN": secret}, func(log BuildLog) error {
			cleaned = log.Text
			return nil
		})
		if err := emit(BuildLog{Stream: "stdout", Text: line}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(cleaned, secret) {
			t.Fatalf("redaction left %q in %q", secret, cleaned)
		}
		// Redaction replaces; it never grows without bound.
		if len(cleaned) > len(line)*len("[REDACTED]")+len("[REDACTED]") {
			t.Fatalf("redaction grew %d bytes into %d", len(line), len(cleaned))
		}
	})
}

func FuzzSafeRelativePath(f *testing.F) {
	for _, seed := range []string{
		"compose.yml", "deploy/compose.yaml", "../escape.yml", "/absolute.yml",
		"a/../b.yml", "./compose.yml", "", "a//b.yml", "c:\\windows\\x.yml",
		strings.Repeat("a/", 200) + "compose.yml", "com\x00pose.yml",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if !safeRelativePath(path) {
			return
		}
		if strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") || path == "" {
			t.Fatalf("accepted unsafe relative path %q", path)
		}
		// safeRelativePath promises containment after cleaning, not that the
		// caller already wrote a clean path: every caller joins it onto a root.
		clean := cleanComposePath(path)
		if strings.HasPrefix(clean, "/") || clean == ".." ||
			strings.HasPrefix(clean, "../") || clean == "." || clean == "" {
			t.Fatalf("accepted %q, which cleans to %q", path, clean)
		}
	})
}

// A run's persisted metadata is read back from the database and must not be
// able to widen what an operation targets.
func FuzzOperationTargetReleaseID(f *testing.F) {
	for _, seed := range []string{
		`{"targetReleaseId":7}`, `{"targetReleaseId":-1}`, `{"targetReleaseId":"7"}`,
		`{}`, `null`, `{"targetReleaseId":9223372036854775807}`, `[`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, metadata string) {
		if !json.Valid([]byte(metadata)) {
			return
		}
		id, err := operationTargetReleaseID(EngineRun{Metadata: json.RawMessage(metadata)})
		if err != nil {
			return
		}
		if id <= 0 {
			t.Fatalf("accepted target release %d from %q", id, metadata)
		}
	})
}
