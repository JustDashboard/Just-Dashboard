package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

type unrepresentedImageRecoveryReader struct {
	*adoptionReaderFake
	imageRecoveries int
}

func (r *unrepresentedImageRecoveryReader) RecoverAdoptionImage(_ context.Context, _ *dockerx.AdoptionContainer, _ string, _ []string) (*dockerx.ImageDetail, error) {
	r.imageRecoveries++
	return nil, errors.New("image recovery must not run for unknown original settings")
}

func TestRecoverDockerUnknownEffectiveOptionsBlockBeforeImageRecovery(t *testing.T) {
	root := t.TempDir()
	capture := adoptionCaptureFixture(t, "web", true)
	capture.Image, capture.MissingImage = nil, true
	capture.UnrepresentedOptions = []string{"HostConfig.FutureMode"}
	candidate := WorkloadCandidate{Key: "container:" + capture.Inspection.ID, Kind: "container", ResourceID: capture.Inspection.ID, Name: "original-web", Total: 1, Running: 1, Services: []WorkloadService{{Name: "original-web", ResourceID: capture.Inspection.ID}}}
	reader := &unrepresentedImageRecoveryReader{adoptionReaderFake: &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}}}
	result, err := RecoverDockerWorkload(t.Context(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
	if !errors.Is(err, ErrRecoveryBlocked) || reader.imageRecoveries != 0 {
		t.Fatal("unknown original settings did not block before image export/import")
	}
	for _, issue := range result.Adoption.Issues {
		if issue.Code == "unknown_engine_configuration" && issue.Field == "HostConfig.FutureMode" && issue.Blocking {
			return
		}
	}
	t.Fatal("unknown original settings have no actionable blocker")
}
