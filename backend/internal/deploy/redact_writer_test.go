package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretWriterRedactsEveryChunkBoundaryAndSharedPrefix(t *testing.T) {
	secret := "synthetic-secret-value"
	for split := 0; split <= len(secret); split++ {
		var output bytes.Buffer
		writer := newSecretWriter(&output, map[string]string{"TOKEN": secret, "OTHER": "synthetic-secret-value-longer"})
		for _, part := range []string{"prefix ", secret[:split], secret[split:], " suffix synthetic-secret-value-longer"} {
			if _, err := writer.Write([]byte(part)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), secret) || !strings.Contains(output.String(), "[REDACTED]") {
			t.Fatalf("split %d: %s", split, output.String())
		}
	}
}

func TestLegacyEnvFileCannotFollowPredictableTemporarySymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sensitive")
	if err := os.WriteFile(target, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(root, ".env")
	if err := os.Symlink(target, env+".vpsd-tmp"); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvFile(env, map[string]string{"TOKEN": "synthetic$value"}); err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(target); string(contents) != "unchanged" {
		t.Fatal("followed predictable staging symlink")
	}
	info, err := os.Stat(env)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("env mode: %v %v", info, err)
	}
	contents, _ := os.ReadFile(env)
	if !strings.Contains(string(contents), "synthetic$$value") {
		t.Fatal("literal dollar will interpolate")
	}
}

func TestSecretWriterDefersShortMatchWhenLongerSecretIsIncomplete(t *testing.T) {
	for split := 1; split < 12; split++ {
		var output bytes.Buffer
		writer := newSecretWriter(&output, map[string]string{"SHORT": "abc", "LONG": "abcdefghijkl"})
		_, _ = writer.Write([]byte("abcdefghijkl"[:split]))
		_, _ = writer.Write([]byte("abcdefghijkl"[split:]))
		_ = writer.Close()
		if output.String() != "[REDACTED]" {
			t.Fatalf("split %d disclosed suffix: %q", split, output.String())
		}
	}
}
