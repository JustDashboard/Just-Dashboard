package selfupdate

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

// Checking who made a release.
//
// A tag on the repository says a version was released. It does not say by
// whom: whoever can push to the repository can push a tag, and an update
// builds and runs what the tag names as root on the host. A stolen token, a
// compromised workflow secret or a mistaken force-push is then code execution
// on every server that updates next. A signature closes that, because the key
// that verifies it is not on the repository at the moment of the update: it is
// in the binary doing the updating, put there by the release before.
//
// SSH signatures rather than OpenPGP, for what has to be in the image to check
// one: git and ssh-keygen, which are both there already, against a keyring and
// a trust database that are not.
//
// It ships switched off. With no key in allowed_signers nothing is verified,
// because nothing is signed yet: making and guarding the key, and having the
// release workflow sign with it, are the project owner's to do. The file says
// where the steps are.

// shippedSigners is the allowed-signers file this build was compiled with.
//
//go:embed allowed_signers
var shippedSigners []byte

// releaseSigners is the file release tags are verified against. A variable so
// a test can verify against a key it made.
var releaseSigners = shippedSigners

// signerLines are the lines of an allowed-signers file that name a key.
func signerLines(file []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

// checkSigners says what is wrong with an allowed-signers file, or nil.
//
// ssh-keygen stops at the first line it cannot parse, and a file it cannot
// parse verifies nothing, so one mistyped line here is every install refusing
// every release. The test suite runs this over the shipped file.
func checkSigners(file []byte) error {
	for _, line := range signerLines(file) {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return fmt.Errorf("%q is not `principal namespaces=\"git\" key-type key`", line)
		}
		if fields[1] != `namespaces="git"` {
			return fmt.Errorf("%q does not limit the key to namespaces=\"git\"", line)
		}
		if !strings.HasPrefix(fields[2], "ssh-") && !strings.HasPrefix(fields[2], "ecdsa-") && !strings.HasPrefix(fields[2], "sk-") {
			return fmt.Errorf("%q: %q is not a key type", line, fields[2])
		}
		if blob, err := base64.StdEncoding.DecodeString(fields[3]); err != nil || len(blob) < 16 {
			return fmt.Errorf("%q: the key is not base64", line)
		}
	}
	return nil
}

// SignaturesRequired reports whether this build installs only releases signed
// by a key it carries.
func SignaturesRequired() bool { return len(signerLines(releaseSigners)) > 0 }

// verifyReleaseTag checks that object is the tag named tag, signed by one of
// signers, and returns nil only then.
//
// Three things, each of which alone lets something else through. It must be a
// tag object, since a lightweight tag is a bare commit with nothing to sign.
// Its own name must be the one asked for: a signature covers the tag's
// contents, name included, so a real signed tag for an older release pushed
// again under a newer name still verifies, and only the name gives it away.
// And the signature must be good and made by a key in signers, which is what
// git verify-tag reports with the settings below.
func verifyReleaseTag(ctx context.Context, dir, tag, object string, signers []byte, out io.Writer) error {
	refused := func(why string) error {
		return fmt.Errorf("the tag %s on origin %s, so it was not installed; nothing was changed. "+
			"This version installs only a release signed with one of the keys it was built with", tag, why)
	}
	kind, err := gitOutput(ctx, dir, "cat-file", "-t", object)
	if err != nil {
		return refused("could not be read")
	}
	if strings.TrimSpace(kind) != "tag" {
		return refused("is a lightweight tag, which carries no signature")
	}
	body, err := gitOutput(ctx, dir, "cat-file", "tag", object)
	if err != nil {
		return refused("could not be read")
	}
	if named := tagName(body); named != tag {
		return refused(fmt.Sprintf("is a signed tag made for %q, not for this release", named))
	}

	file, err := os.CreateTemp("", "jd-release-signers-*")
	if err != nil {
		return fmt.Errorf("could not write the release keys to check the signature against: %w", err)
	}
	defer os.Remove(file.Name())
	_, werr := file.Write(signers)
	if cerr := file.Close(); werr != nil || cerr != nil {
		return fmt.Errorf("could not write the release keys to check the signature against")
	}

	fmt.Fprintf(out, "\n$ git verify-tag %s\n", tag)
	verdict, err := gitOutput(ctx, dir,
		// Every setting that decides the verdict is given here, so nothing in
		// the checkout's own configuration can stand in for it.
		"-c", "gpg.ssh.allowedSignersFile="+file.Name(),
		"-c", "gpg.ssh.program=ssh-keygen",
		// Fully trusted is what git calls a signature whose key is in the
		// allowed-signers file. A good signature by any other key is not.
		"-c", "gpg.minTrustLevel=fully",
		// Only an SSH signature is accepted: there is no keyring here for an
		// OpenPGP or X.509 one to be checked against.
		"-c", "gpg.program=false", "-c", "gpg.openpgp.program=false", "-c", "gpg.x509.program=false",
		"verify-tag", object)
	fmt.Fprintln(out, strings.TrimSpace(verdict))
	if err != nil {
		return refused("is not signed by a Just Dashboard release key")
	}
	return nil
}

// tagName reads the name a tag object gives itself, from its header.
func tagName(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			break
		}
		if name, ok := strings.CutPrefix(line, "tag "); ok {
			return name
		}
	}
	return ""
}
