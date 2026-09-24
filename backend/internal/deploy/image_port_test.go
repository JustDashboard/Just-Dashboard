package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func imageAnalyzer(t *testing.T, detail *dockerx.ImageDetail) *HostSourceAnalyzer {
	t.Helper()
	return NewHostSourceAnalyzer(nil, nil, t.TempDir(), &planningDockerFake{
		image: &dockerx.DistributionImage{
			Digest: testImageDigest, Platforms: []string{"linux/amd64"},
		},
		imageDetail: detail,
	}, nil)
}

// A registry manifest names platforms and nothing else, so an image plan used
// to start on port zero — with no build to infer one from, a published site
// that answered nothing and no finding that said why. The image's own
// configuration has the answer whenever this host already holds the image.
func TestImageDetectionSeedsThePortTheImageExposes(t *testing.T) {
	analyzer := imageAnalyzer(t, &dockerx.ImageDetail{
		// Deliberately unsorted and mixed-protocol: the lowest TCP port is the
		// answer, so the same image always proposes the same port.
		ExposedPorts: []string{"9000/tcp", "53/udp", "8080/tcp"},
	})
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "ghcr.io/owner/app:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("image candidates = %#v", result.Candidates)
	}
	candidate := result.Candidates[0]
	if candidate.Port != 8080 {
		t.Fatalf("detected port = %d, want the lowest exposed TCP port 8080", candidate.Port)
	}
	evidence := ""
	for _, item := range candidate.Evidence {
		evidence += item.Reason + "\n"
	}
	if !strings.Contains(evidence, "image exposes 8080/tcp") {
		t.Fatalf("port evidence = %q, want the exposure it was read from", evidence)
	}
	// A port read from the image answers the only question the plan asks of an
	// image: the command is the image's own, no finding fires on an empty mount
	// list, and preflight demands a readiness gate of web and static workloads
	// rather than of this one. An image that still owed a decision here landed
	// the reader on the first screen of the sequence to read a sentence with no
	// field under it.
	if len(candidate.NeedsDecision) != 0 {
		t.Fatalf("an image whose port was read still owes decisions: %#v", candidate.NeedsDecision)
	}
}

// A UDP-only image is not something a readiness check or a proxy route can be
// built on, so it stays undecided rather than proposing a port that cannot
// serve HTTP.
func TestImageDetectionIgnoresPortsItCannotRouteOrCheck(t *testing.T) {
	analyzer := imageAnalyzer(t, &dockerx.ImageDetail{ExposedPorts: []string{"19132/udp"}})
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "ghcr.io/owner/udp:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if port := result.Candidates[0].Port; port != 0 {
		t.Fatalf("UDP-only image port = %d, want none", port)
	}
	if !stringSliceContains(result.Candidates[0].NeedsDecision,
		"confirm runtime command, ports, storage, and readiness") {
		t.Fatalf("UDP-only image decisions = %#v", result.Candidates[0].NeedsDecision)
	}
}

// Detection has already succeeded on the registry manifest by the time the
// local image is consulted, so an image this host has never pulled is no
// evidence rather than a failure.
func TestImageDetectionSurvivesAnImageThisHostDoesNotHold(t *testing.T) {
	analyzer := imageAnalyzer(t, nil)
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "ghcr.io/owner/never-pulled:1",
	})
	if err != nil {
		t.Fatalf("an absent local image failed detection: %v", err)
	}
	if result.Source.Digest != testImageDigest {
		t.Fatalf("registry evidence = %#v", result.Source)
	}
	if port := result.Candidates[0].Port; port != 0 {
		t.Fatalf("port without local evidence = %d, want none", port)
	}
}

// Docker gives every container a fresh anonymous volume for a path its image
// declares, so an image plan that mounts nothing there starts empty on every
// release. The declared paths become planned state; scratch space does not.
func TestImageDetectionPlansTheVolumesTheImageDeclares(t *testing.T) {
	analyzer := imageAnalyzer(t, &dockerx.ImageDetail{
		ExposedPorts: []string{"5432/tcp"},
		VolumePaths:  []string{"/var/lib/postgresql/data", "/tmp", "/var/run/postgresql"},
	})
	result, err := analyzer.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "postgres:16",
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := result.Candidates[0].PersistentPaths
	if len(paths) != 1 || paths[0].Kind != PersistentVolume || paths[0].Target != "/var/lib/postgresql/data" ||
		!strings.Contains(paths[0].Reason, "declares VOLUME /var/lib/postgresql/data") {
		t.Fatalf("image persistent paths = %+v", paths)
	}

	// An image this host has not pulled declares nothing yet.
	absent := imageAnalyzer(t, nil)
	result, err = absent.Analyze(context.Background(), DraftSourceConfig{
		Kind: SourceImage, Mode: SourceModeImageReference, Image: "postgres:16",
	})
	if err != nil || len(result.Candidates[0].PersistentPaths) != 0 {
		t.Fatalf("absent image persistent paths = %+v, %v", result.Candidates, err)
	}
}
