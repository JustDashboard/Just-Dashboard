package linuxusers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func ed25519Line(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	return line
}

// keyHost is one account whose home is this test's own, so the ownership checks
// on .ssh pass without root.
func keyHost(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	uid, gid := strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	s, _, _ := fixtureHost(t, map[string]string{
		"passwd": "ion:x:" + uid + ":" + gid + "::" + home + ":/bin/bash\n" +
			"mira:x:" + uid + ":" + gid + "::" + filepath.Join(home, "mira") + ":/bin/bash\n",
	})
	if err := os.Mkdir(filepath.Join(home, "mira"), 0o755); err != nil {
		t.Fatal(err)
	}
	return s, home
}

func TestAnOptionCannotSmuggleInASecondKeyOrALine(t *testing.T) {
	raw := ed25519Line(t, "laptop")
	other := ed25519Line(t, "attacker")
	for _, option := range []string{
		"no-pty " + other,
		"no-pty\n" + other,
		`from="1.2.3.4" ` + other,
		`command="x"` + "\n" + other,
		`from="1.2.3.4",` + other,
		`command="a"b"`,
		"made-up-option",
		`from=1.2.3.4`,
		"",
	} {
		if _, _, err := composeKeyLine(raw, "laptop", []string{option}); err == nil {
			t.Errorf("%q was accepted", option)
		}
	}
	if _, _, err := composeKeyLine(raw, "two\nlines", nil); err == nil {
		t.Error("a line break in a label was accepted")
	}
	if _, _, err := composeKeyLine(raw+"\n"+other, "x", nil); err == nil {
		t.Error("a second line in the key was accepted")
	}
}

func TestComposingAKeyKeepsItsFingerprintAndWritesTheOptions(t *testing.T) {
	raw := ed25519Line(t, "laptop")
	before, err := ValidatePublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	key, line, err := composeKeyLine(raw, "work laptop", []string{"restrict", `from="10.0.0.0/8,192.168.1.1"`, `expiry-time="20270101"`})
	if err != nil {
		t.Fatal(err)
	}
	if key.Fingerprint != before.Fingerprint {
		t.Fatalf("the key changed: %s != %s", key.Fingerprint, before.Fingerprint)
	}
	if key.Comment != "work laptop" {
		t.Fatalf("comment = %q", key.Comment)
	}
	if !strings.HasPrefix(line, `restrict,from="10.0.0.0/8,192.168.1.1",expiry-time="20270101" ssh-ed25519 `) {
		t.Fatalf("line = %q", line)
	}
	got := splitKeyOptions(key.Options)
	if len(got) != 3 || got[1] != `from="10.0.0.0/8,192.168.1.1"` {
		t.Fatalf("options = %v", got)
	}
}

func TestSplitKeyOptionsRespectsQuotes(t *testing.T) {
	got := splitKeyOptions(`from="a,b",no-pty,command="x,y z"`)
	if len(got) != 3 || got[0] != `from="a,b"` || got[1] != "no-pty" || got[2] != `command="x,y z"` {
		t.Fatalf("got %v", got)
	}
	if got := splitKeyOptions(""); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func rsaLine(t *testing.T, bits int) string {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " old"
}

func TestAShortRSAKeyIsWeakAndItsSizeIsReported(t *testing.T) {
	weak, err := ValidatePublicKey(rsaLine(t, 1024))
	if err != nil {
		t.Fatal(err)
	}
	if weak.Bits != 1024 || weak.Weak == "" {
		t.Fatalf("weak = %+v", weak)
	}
	strong, err := ValidatePublicKey(rsaLine(t, 2048))
	if err != nil {
		t.Fatal(err)
	}
	if strong.Bits != 2048 || strong.Weak != "" {
		t.Fatalf("strong = %+v", strong)
	}
	modern, err := ValidatePublicKey(ed25519Line(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if modern.Bits != 0 || modern.Weak != "" {
		t.Fatalf("modern = %+v", modern)
	}
}

func TestPrivateKeysAndJunkAreRefused(t *testing.T) {
	for _, bad := range []string{"", "not a key", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"} {
		if _, err := ValidatePublicKey(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAddEditAndRemoveAKeyOnDisk(t *testing.T) {
	s, home := keyHost(t)
	raw := ed25519Line(t, "laptop")
	path := filepath.Join(home, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# my note\nnot a key at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	label := "work"
	key, err := s.AddKeyWith("ion", raw, KeyEdit{Comment: &label, Options: []string{"no-pty"}})
	if err != nil {
		t.Fatal(err)
	}
	if key.Comment != "work" || key.Options != "no-pty" {
		t.Fatalf("key = %+v", key)
	}
	if _, err := s.AddKey("ion", raw); !IsAlreadyAuthorised(err) {
		t.Fatalf("err = %v", err)
	}
	renamed := "personal"
	updated, err := s.UpdateKey("ion", key.Fingerprint, KeyEdit{Comment: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Comment != "personal" || updated.Options != "no-pty" {
		t.Fatalf("the edit lost what it did not name: %+v", updated)
	}
	cleared, err := s.UpdateKey("ion", key.Fingerprint, KeyEdit{ClearOptions: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Options != "" || cleared.Comment != "personal" {
		t.Fatalf("cleared = %+v", cleared)
	}
	if _, err := s.UpdateKey("ion", "SHA256:nope", KeyEdit{Comment: &renamed}); err == nil {
		t.Fatal("edited a key that is not there")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# my note\nnot a key at all\n") {
		t.Fatalf("a line this does not understand was lost: %q", b)
	}
	keys, _, err := s.ListKeys("ion")
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys = %+v, %v", keys, err)
	}
	if err := s.RemoveKey("ion", key.Fingerprint); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "# my note\nnot a key at all\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestAGeneratedKeyIsAuthorisedAndItsPrivateHalfWorksAndIsNotOnDisk(t *testing.T) {
	s, home := keyHost(t)
	generated, err := s.GenerateKey("ion", "phone", "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(generated.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if got := ssh.FingerprintSHA256(signer.PublicKey()); got != generated.Key.Fingerprint {
		t.Fatalf("the private half is not this key's: %s != %s", got, generated.Key.Fingerprint)
	}
	block, _ := pem.Decode([]byte(generated.PrivateKey))
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatalf("block = %+v", block)
	}
	var onDisk strings.Builder
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			onDisk.Write(b)
		}
		return nil
	})
	if strings.Contains(onDisk.String(), "PRIVATE KEY") {
		t.Fatal("the private half was written to the server")
	}
	protected, err := s.GenerateKey("ion", "tablet", "hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParsePrivateKey([]byte(protected.PrivateKey)); err == nil {
		t.Fatal("a passphrase-protected key parsed without it")
	}
	if _, err := ssh.ParsePrivateKeyWithPassphrase([]byte(protected.PrivateKey), []byte("hunter22")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GenerateKey("ion", "two\nlines", ""); err == nil {
		t.Fatal("a line break in the label was accepted")
	}
}

func TestCopyingAKeyReportsEachTarget(t *testing.T) {
	s, _ := keyHost(t)
	key, err := s.AddKey("ion", ed25519Line(t, "laptop"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.CopyKey("ion", key.Fingerprint, []string{"mira", "ion", "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if results["mira"] != "" {
		t.Fatalf("mira: %s", results["mira"])
	}
	if results["ion"] == "" || results["ghost"] == "" {
		t.Fatalf("results = %+v", results)
	}
	if keys, _, _ := s.ListKeys("mira"); len(keys) != 1 || keys[0].Fingerprint != key.Fingerprint {
		t.Fatalf("mira's keys = %+v", keys)
	}
	if _, err := s.CopyKey("ion", "SHA256:nope", []string{"mira"}); err == nil {
		t.Fatal("copied a key that is not there")
	}
}

func pointGitHubAt(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	previous := githubKeysURL
	githubKeysURL = server.URL + "/%s.keys"
	t.Cleanup(func() {
		githubKeysURL = previous
		server.Close()
	})
}

func TestImportingFromGitHubLabelsTheKeysAndCountsWhatItHad(t *testing.T) {
	s, _ := keyHost(t)
	first, second := ed25519Line(t, ""), ed25519Line(t, "")
	pointGitHubAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/octocat.keys" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "%s\n\n%s\n", first, second)
	})
	ctx := context.Background()
	result, err := s.ImportGitHubKeys(ctx, "ion", "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 2 || result.Skipped != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.Added[0].Comment != "github:octocat" {
		t.Fatalf("comment = %q", result.Added[0].Comment)
	}
	again, err := s.ImportGitHubKeys(ctx, "ion", "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Added) != 0 || again.Skipped != 2 {
		t.Fatalf("again = %+v", again)
	}
}

func TestAGitHubImportThatCannotWorkSaysWhy(t *testing.T) {
	s, _ := keyHost(t)
	pointGitHubAt(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty.keys":
		case "/broken.keys":
			http.Error(w, "nope", http.StatusBadGateway)
		case "/junk.keys":
			fmt.Fprintln(w, "this is not a key")
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if _, err := s.ImportGitHubKeys(ctx, "ion", "nobody"); err == nil || !strings.Contains(err.Error(), "no user named") {
		t.Fatalf("404: %v", err)
	}
	if _, err := s.ImportGitHubKeys(ctx, "ion", "empty"); err == nil || !strings.Contains(err.Error(), "no public keys") {
		t.Fatalf("empty: %v", err)
	}
	if _, err := s.ImportGitHubKeys(ctx, "ion", "broken"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("502: %v", err)
	}
	// A line that is not a key is a failure, and not an already-have-it.
	if _, err := s.ImportGitHubKeys(ctx, "ion", "junk"); err == nil {
		t.Fatal("junk: accepted")
	}
}

func TestABadLoginNeverReachesTheNetwork(t *testing.T) {
	called := false
	pointGitHubAt(t, func(http.ResponseWriter, *http.Request) { called = true })
	for _, login := range []string{"", "../etc/passwd", "a/b", "a b", "-leading", "a?x=1", "a#b", strings.Repeat("a", 40)} {
		if _, err := FetchGitHubKeys(context.Background(), login); err == nil {
			t.Errorf("%q was accepted", login)
		}
	}
	if called {
		t.Fatal("a bad login reached the network")
	}
}
