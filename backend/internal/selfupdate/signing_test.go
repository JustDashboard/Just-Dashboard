package selfupdate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Signature checking against a real git and a real ssh-keygen, with a key made
// here. The project has no release key yet, so this is the only place the
// check runs until it does, and every case below is a release the updater
// would otherwise have built and started as root.

// signingKey makes an SSH key and returns its private half's path and the
// allowed-signers line that trusts it.
func signingKey(t *testing.T, name string) (private, allowed string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed on this machine")
	}
	private = filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", private).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	public, err := os.ReadFile(private + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(public))
	return private, name + `@example.invalid namespaces="git" ` + fields[0] + " " + fields[1] + "\n"
}

// ship makes signers the keys this build was compiled with, for one test.
func ship(t *testing.T, signers string) {
	t.Helper()
	if err := checkSigners([]byte(signers)); err != nil {
		t.Fatalf("the test's own signers file is malformed: %v", err)
	}
	before := releaseSigners
	releaseSigners = []byte(signers)
	t.Cleanup(func() { releaseSigners = before })
}

// signedTag tags rev in repo as name, signed with key.
func signedTag(t *testing.T, repo, key, name, rev string) {
	t.Helper()
	git(t, repo, "-c", "gpg.format=ssh", "-c", "user.signingkey="+key, "tag", "-s", name, "-m", name, rev)
}

func fromOwnRepository(checkout, version string, log *bytes.Buffer) (landing, error) {
	return fastForward(context.Background(), checkout, request{ref: "main", version: version, tagOnly: true}, log)
}

func TestAReleaseSignedWithTheShippedKeyIsInstalled(t *testing.T) {
	key, allowed := signingKey(t, "release")
	upstream, checkout := installed(t)
	ship(t, "# the release key\n"+allowed)
	tagged := head(t, upstream, "HEAD")
	signedTag(t, upstream, key, "v0.6", "HEAD")
	release(t, upstream, "0.7")

	var log bytes.Buffer
	landed, err := fromOwnRepository(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if landed.commit != tagged || !landed.tagged {
		t.Fatalf("landed %+v, want the signed commit %s", landed, tagged)
	}
	// The transcript is where an operator sees who signed what they are running.
	for _, want := range []string{"$ git verify-tag v0.6", `Good "git" signature for release@example.invalid`} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the transcript does not show %q:\n%s", want, log.String())
		}
	}
}

// Everything that is not that: each of these was, until now, installed.
func TestAReleaseNotSignedWithTheShippedKeyIsRefused(t *testing.T) {
	key, allowed := signingKey(t, "release")
	stranger, _ := signingKey(t, "stranger")

	cases := map[string]struct {
		tag  func(t *testing.T, upstream string)
		want string
	}{
		"signed with a key that was not shipped": {
			func(t *testing.T, upstream string) { signedTag(t, upstream, stranger, "v0.6", "HEAD") },
			"is not signed by a Just Dashboard release key",
		},
		"annotated and not signed": {
			func(t *testing.T, upstream string) { git(t, upstream, "tag", "-a", "v0.6", "-m", "0.6") },
			"is not signed by a Just Dashboard release key",
		},
		"lightweight": {
			func(t *testing.T, upstream string) { git(t, upstream, "tag", "v0.6") },
			"lightweight tag",
		},
		// A real signature, made by the real key, for something else. The ref
		// says v0.6 and the object it points at says otherwise.
		"a signed tag pushed again under this release's name": {
			func(t *testing.T, upstream string) {
				signedTag(t, upstream, key, "nightly", "HEAD")
				git(t, upstream, "update-ref", "refs/tags/v0.6", strings.TrimSpace(git(t, upstream, "rev-parse", "nightly")))
			},
			`made for "nightly"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			upstream, checkout := installed(t)
			ship(t, allowed)
			before := head(t, checkout, "HEAD")
			tc.tag(t, upstream)

			var log bytes.Buffer
			_, err := fromOwnRepository(checkout, "0.6", &log)
			if err == nil {
				t.Fatalf("installed it:\n%s", log.String())
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Errorf("error = %v", err)
			}
			if head(t, checkout, "HEAD") != before {
				t.Fatal("the checkout moved")
			}
		})
	}
}

// A setting in the checkout's own git configuration must not be able to stand
// in for the shipped keys.
func TestTheCheckoutsOwnConfigurationCannotVouchForARelease(t *testing.T) {
	_, allowed := signingKey(t, "release")
	stranger, strangerAllowed := signingKey(t, "stranger")
	upstream, checkout := installed(t)
	ship(t, allowed)
	signedTag(t, upstream, stranger, "v0.6", "HEAD")

	theirs := filepath.Join(t.TempDir(), "theirs")
	write(t, theirs, strangerAllowed)
	git(t, checkout, "config", "gpg.ssh.allowedSignersFile", theirs)
	git(t, checkout, "config", "gpg.minTrustLevel", "undefined")

	var log bytes.Buffer
	if _, err := fromOwnRepository(checkout, "0.6", &log); err == nil {
		t.Fatalf("a key named in .git/config signed a release:\n%s", log.String())
	}
}

// The project's key did not sign a fork's tags, so a fork is not held to it.
func TestAForkIsNotHeldToTheProjectsKey(t *testing.T) {
	_, allowed := signingKey(t, "release")
	upstream, checkout := installed(t)
	ship(t, allowed)
	git(t, upstream, "tag", "v0.6")

	var log bytes.Buffer
	if _, _, err := forward(checkout, "0.6", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if strings.Contains(log.String(), "verify-tag") {
		t.Fatalf("a fork's tag was checked against the project's key:\n%s", log.String())
	}
}

// With no key in the file nothing is signed yet, and nothing is asked for.
func TestWithNoKeyShippedNoSignatureIsAskedFor(t *testing.T) {
	upstream, checkout := installed(t)
	ship(t, "# no key yet\n\n")
	if SignaturesRequired() {
		t.Fatal("a file of comments turned signature checking on")
	}
	git(t, upstream, "tag", "v0.6")
	var log bytes.Buffer
	if _, err := fromOwnRepository(checkout, "0.6", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
}

// One mistyped line in allowed_signers is every install refusing every release
// from the one after it, so the file that ships is held to the format here.
func TestTheShippedSignersFileIsWellFormed(t *testing.T) {
	if err := checkSigners(shippedSigners); err != nil {
		t.Fatalf("backend/internal/selfupdate/allowed_signers: %v", err)
	}
	for name, file := range map[string]string{
		"no namespace":      "release@example.invalid ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKPGlXnuEX3CspybKa5vtg7bQRQyHXDzwbx5qr3wr4Th\n",
		"another namespace": `release@example.invalid namespaces="file" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKPGlXnuEX3CspybKa5vtg7bQRQyHXDzwbx5qr3wr4Th` + "\n",
		"no key":            `release@example.invalid namespaces="git" ssh-ed25519` + "\n",
		"a key cut short":   `release@example.invalid namespaces="git" ssh-ed25519 AAAA…` + "\n",
		"a bare key":        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKPGlXnuEX3CspybKa5vtg7bQRQyHXDzwbx5qr3wr4Th release\n",
	} {
		if checkSigners([]byte(file)) == nil {
			t.Errorf("%s: accepted %q", name, file)
		}
	}
}

// The header of the transcript says what this update will accept, before the
// updater has started.
func TestTheTranscriptSaysASignatureIsRequired(t *testing.T) {
	_, allowed := signingKey(t, "release")
	ship(t, allowed)
	srv, _ := repository(t, threeReleases, "v0.6")
	service := serviceFor(t, DefaultRepo, srv.URL, "0.5")
	stubDocker(t)
	rep := service.Report(context.Background(), Forced)
	if _, err := service.Install(context.Background(), rep.Latest, "tester"); err != nil {
		t.Fatal(err)
	}
	if got := service.Transcript(); !strings.Contains(got, "origin's tag v0.6, signed with a release key, or nothing") {
		t.Fatalf("transcript begins %q", got)
	}
}
