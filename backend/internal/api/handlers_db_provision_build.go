package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// Building a template's image on this server, for the one template no
// published image serves.
//
// The build is a job like a certificate or a package upgrade: it takes
// minutes, it must not die with the tab that asked for it, and its output is
// what an operator reads when it fails. The page starts it, watches it, and
// provisions from the image once it has finished.

// recipeLabel is the label a built image carries the digest of its recipe
// under. A tag can outlive the recipe it was built from — the dashboard is
// updated, the recipe gains an extension — and the label is what tells an
// image built from this release's recipe from one built from an older one.
const recipeLabel = "io.just-dashboard.recipe"

// recipeBuildPrefix is the kind every image build starts with. One runs at a
// time: each is an apt install of a few hundred megabytes, and two of them at
// once on a small server is what makes both slow.
const recipeBuildPrefix = "database.image.build."

// recipeBuildTimeout bounds one build. The longest part is downloading the
// packages, and a slow mirror is still a mirror.
const recipeBuildTimeout = 30 * time.Minute

func recipeDigest(recipe string) string {
	sum := sha256.Sum256([]byte(recipe))
	return hex.EncodeToString(sum[:])[:12]
}

func recipeBuildKind(engine string, version provisionVersion) string {
	return recipeBuildPrefix + engine + "." + version.id
}

// buildRecipeImage runs one build. A variable so the handler's tests can
// stand in for the docker binary, which would otherwise build on whatever
// daemon the machine running them has.
var buildRecipeImage = func(ctx context.Context, docker *dockerx.Client, opts dockerx.BuildOptions, out chan<- dockerx.LogLine) (int, error) {
	return docker.Build(ctx, opts, out)
}

// recipeBuild is whether a release's image is here and from the current
// recipe, and the job building it, if one is.
func (s *Server) recipeBuild(ctx context.Context, tmpl provisionTemplate, version provisionVersion) *provisionBuild {
	build := &provisionBuild{}
	if s.modules.docker != nil {
		labels, err := s.modules.docker.ImageLabels(ctx, version.image)
		build.Ready = err == nil && labels[recipeLabel] == recipeDigest(tmpl.recipe)
	}
	for _, job := range s.modules.jobs.List() {
		if job.Status == jobs.StatusRunning && job.Target == version.image && strings.HasPrefix(job.Kind, recipeBuildPrefix) {
			build.Job = job.ID
			break
		}
	}
	return build
}

type provisionBuildRequest struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
}

// handleDBProvisionBuild builds a template's image and answers 202 with the
// job doing it.
//
// Asking for an image that is already being built answers that build, so a
// second tab, or the page come back to, attaches to the work rather than
// being refused it. A build of a different image waits its turn.
func (s *Server) handleDBProvisionBuild(w http.ResponseWriter, r *http.Request) error {
	var req provisionBuildRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	tmpl, ok := provisionTemplates[req.Engine]
	if !ok {
		return httpx.BadRequest("unknown engine %q", req.Engine)
	}
	if tmpl.recipe == "" {
		return httpx.BadRequest("%s is started from a published image, so there is nothing to build", tmpl.label)
	}
	version, err := tmpl.version(strings.TrimSpace(req.Version))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable",
			"this host has no Docker socket, so an image cannot be built")
	}
	digest := recipeDigest(tmpl.recipe)
	spec := jobs.Spec{
		Capability: auth.CapSystemAdmin,
		Kind:       recipeBuildKind(req.Engine, version), Title: "Building " + version.image, Target: version.image,
		StartedBy: httpx.MustPrincipal(r).Username(), Timeout: recipeBuildTimeout,
	}
	docker := s.modules.docker
	job, started := s.modules.jobs.StartExclusive(recipeBuildPrefix, spec, func(ctx context.Context, out jobs.Emitter) error {
		return runRecipeBuild(ctx, docker, out, tmpl.recipe, version, digest)
	})
	if !started && job.Kind != spec.Kind {
		return httpx.Err(http.StatusConflict, "build_running", fmt.Sprintf(
			"%s is being built now (started by %s); one image is built at a time, so wait for it to finish",
			job.Target, job.StartedBy)).Describe("build", job.ID)
	}
	httpx.SetAudit(r, "database.image.build", version.image, map[string]any{
		"engine": req.Engine, "version": version.id, "recipe": digest, "job": job.ID, "attached": !started,
	})
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

// runRecipeBuild writes the recipe into a directory of its own and builds
// the release's image from it, with the base image pulled fresh: the build
// happens once per recipe, and what it starts from should be the newest
// patch release of that major on the day.
func runRecipeBuild(ctx context.Context, docker *dockerx.Client, out jobs.Emitter, recipe string, version provisionVersion, digest string) error {
	dir, err := os.MkdirTemp("", "jd-recipe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(recipe), 0o600); err != nil {
		return err
	}
	out.Status("Building %s from the dashboard's recipe %s", version.image, digest)
	lines := make(chan dockerx.LogLine, 64)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for line := range lines {
			out.Line(line.Stream, line.Text)
		}
	}()
	code, err := buildRecipeImage(ctx, docker, dockerx.BuildOptions{
		Dir: dir, Tag: version.image, Pull: true,
		BuildArgs: map[string]string{"PG_MAJOR": version.id, "JD_RECIPE": digest},
	}, lines)
	close(lines)
	<-forwarded
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the build of %s exited with status %d; its output above says where it stopped", version.image, code)
	}
	out.Status("Built %s", version.image)
	return nil
}
