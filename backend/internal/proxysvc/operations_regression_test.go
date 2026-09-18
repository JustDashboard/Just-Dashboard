package proxysvc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnabledLinkRollbackRestoresPreviousTarget(t *testing.T) {
	link := filepath.Join(t.TempDir(), "enabled")
	if err := os.Symlink("../old-site", link); err != nil {
		t.Fatal(err)
	}
	undo, err := linkEnabled(link, "../new-site")
	if err != nil {
		t.Fatal(err)
	}
	undo()
	target, err := os.Readlink(link)
	if err != nil || target != "../old-site" {
		t.Fatalf("rollback link = %q: %v", target, err)
	}
}
func TestRoute53CredentialsBecomeAValidDefaultProfile(t *testing.T) {
	normalized, err := normalizeRoute53Credentials("aws_secret_access_key = synthetic-secret\naws_access_key_id = synthetic-id")
	if err != nil || !strings.HasPrefix(normalized, "[default]\n") {
		t.Fatalf("profile %q: %v", normalized, err)
	}
	for _, bad := range []string{"[another]\naws_access_key_id = x\naws_secret_access_key = y", "aws_access_key_id = x", "aws_access_key_id = x\naws_access_key_id = y\naws_secret_access_key = z"} {
		if _, err := normalizeRoute53Credentials(bad); err == nil {
			t.Fatalf("accepted malformed credentials %q", bad)
		}
	}
}

func TestCertbotEnvironmentSelectsSavedProfileWithoutExposingKeys(t *testing.T) {
	env := strings.Join(route53Environment("/private/route53.ini"), "\n")
	for _, required := range []string{"AWS_SHARED_CREDENTIALS_FILE=/private/route53.ini", "AWS_PROFILE=default", "AWS_ACCESS_KEY_ID=\n", "AWS_SECRET_ACCESS_KEY=\n"} {
		if !strings.Contains(env, required) {
			t.Fatalf("missing %s in %s", required, env)
		}
	}
}

func TestExistingRoute53CredentialsAreNormalizedBeforeCertbot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "route53.ini")
	if err := os.WriteFile(path, []byte("aws_access_key_id = synthetic-id\naws_secret_access_key = synthetic-secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env, err := route53EnvironmentAt(path)
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "AWS_SHARED_CREDENTIALS_FILE="+path) {
		t.Fatalf("environment %v: %v", env, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(content), "[default]\n") {
		t.Fatalf("migration %q: %v", content, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("migrated credentials are not private")
	}
	if err := os.WriteFile(path, []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := route53EnvironmentAt(path); err == nil {
		t.Fatal("silently fell back to an unrelated AWS identity")
	}
}
