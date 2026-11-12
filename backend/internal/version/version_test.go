package version

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The version is shown by the frontend and logged by the backend from two
// constants that nothing links at build time. This is the link: a release that
// bumps one and forgets the other fails here, rather than shipping a UI that
// claims a version the server has never heard of.
func TestVersionMatchesFrontend(t *testing.T) {
	const path = "../../../frontend/src/lib/version.ts"
	b, err := os.ReadFile(path)
	if err != nil {
		// The backend module builds on its own, and a checkout without the
		// frontend has nothing to disagree with.
		t.Skipf("no frontend to compare against: %v", err)
	}
	m := regexp.MustCompile(`VERSION\s*=\s*"([^"]+)"`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s no longer declares VERSION as a string literal", path)
	}
	if got := string(m[1]); got != Version {
		t.Fatalf("frontend says %q, backend says %q — a release bumps both", got, Version)
	}
}

// The README says which version this repository is, and it is the
// version somebody reads before they install anything. Nothing linked it to
// the constant, so it sat at 0.5.1 through seven releases — the front page of
// the project describing a build nobody was running. Checked here for the same
// reason as the frontend: a release that forgets it should fail a test run
// rather than mislead a reader.
func TestReadmeMatchesRelease(t *testing.T) {
	const path = "../../../README.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no README to compare against: %v", err)
	}
	claims := regexp.MustCompile(`(?m)^(?:\*\*Version|This is \*\*)\s*\*?\*?([0-9]+\.[0-9]+(?:\.[0-9]+)?)`).
		FindAllSubmatch(b, -1)
	if len(claims) == 0 {
		t.Fatalf("%s no longer states a version; release.sh writes it, and this is the check on it", path)
	}
	for _, m := range claims {
		if got := string(m[1]); got != Version {
			t.Errorf("README says %q, the release is %q — scripts/release.sh writes it", got, Version)
		}
	}
}

// package.json is the one other file carrying a version, because npm demands
// the field. It is checked loosely: the product version has two components and
// npm insists on three, so what must agree is the release, not the string.
func TestPackageVersionMatchesRelease(t *testing.T) {
	const path = "../../../frontend/package.json"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no frontend to compare against: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if pkg.Version != Version && !strings.HasPrefix(pkg.Version, Version+".") {
		t.Fatalf("package.json says %q, which is not a %s release", pkg.Version, Version)
	}
}

// The description of the deployment API is generated from the build and
// carries its version, and the SDKs are generated from that file. The test
// that keeps the file current (internal/api) leaves the version out of its
// comparison, so this is what notices a release that did not regenerate it:
// scripts/release.sh does, and by hand it is
// JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent
func TestTheAPIDescriptionCarriesThisVersion(t *testing.T) {
	const path = "../../../docs/api/deploy-openapi.json"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no API description to compare against: %v", err)
	}
	var description struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(b, &description); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if description.Info.Version != Version {
		t.Fatalf("docs/api/deploy-openapi.json says %q, the release is %q — scripts/release.sh regenerates it",
			description.Info.Version, Version)
	}
}

func TestBuildNamesTheCommitOfAnythingThatIsNotTheRelease(t *testing.T) {
	before := Commit
	t.Cleanup(func() { Commit = before })
	Commit = ""
	if got := Build(); got != Version {
		t.Fatalf("a release build calls itself %q", got)
	}
	Commit = "98c9349"
	if got := Build(); got != Version+"+98c9349" {
		t.Fatalf("a build from 98c9349 calls itself %q", got)
	}
}

// The linker sets Commit by its full name, written in backend/Dockerfile. A
// name that matches no variable is not an error to the linker: it sets
// nothing, and every build then calls itself a release.
func TestTheDockerfileStampsThisPackagesCommit(t *testing.T) {
	b, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Skipf("no Dockerfile to compare against: %v", err)
	}
	dockerfile := string(b)
	type here struct{}
	target := reflect.TypeOf(here{}).PkgPath() + ".Commit"
	if !strings.Contains(dockerfile, "-X "+target+"=${JD_COMMIT}") {
		t.Fatalf("backend/Dockerfile does not build the server with -X %s=${JD_COMMIT}", target)
	}
	if !strings.Contains(dockerfile, "ARG JD_COMMIT") {
		t.Fatal("backend/Dockerfile no longer declares the JD_COMMIT build argument")
	}
	// jd carries the same answer in the one string it has.
	if !strings.Contains(dockerfile, `${JD_COMMIT:++${JD_COMMIT}}`) {
		t.Fatal("backend/Dockerfile no longer adds the commit to jd's version")
	}
}
