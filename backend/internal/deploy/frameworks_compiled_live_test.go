package deploy

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
)

// livePrepareAndBuildWithin prepares and builds a candidate the way
// prepare_context and build_artifact do: within the checkout at boundary, so
// a module or project builds from the directory that owns it.
func livePrepareAndBuildWithin(
	t *testing.T,
	builder *ArtifactBuilder,
	boundary, root, tag string,
	config BuildPlanConfig,
	variables map[string]string,
	emit func(BuildLog) error,
) BuildArtifactResult {
	t.Helper()
	buildRoot := filepath.Join(boundary, filepath.FromSlash(root))
	prepared, err := builder.PrepareWithin(context.Background(), boundary, buildRoot, config, false, tag, buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContextDirectory != "" {
		buildRoot = filepath.Join(boundary, filepath.FromSlash(prepared.ContextDirectory))
	}
	result, err := builder.Build(context.Background(), buildRoot, tag, config, prepared, variables, "", SourceIdentity{}, nil, emit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// javaCatalogueImages and dotnetCatalogueImages are every image the Java and
// .NET recipes can resolve: the build images of each tool and release, the
// runtimes, and the JDKs a toolchain is provided from.
func javaCatalogueImages() []string {
	var images []string
	for _, release := range javaRecipeReleases {
		version := strconv.Itoa(release)
		images = append(images, "maven:3-eclipse-temurin-"+version, "eclipse-temurin:"+version+"-jdk", "eclipse-temurin:"+version+"-jre")
		if release <= 21 {
			images = append(images, "gradle:8-jdk"+version)
		}
		if release >= 17 {
			images = append(images, "gradle:9-jdk"+version)
		}
	}
	return images
}

func dotnetCatalogueImages() []string {
	var images []string
	for _, version := range dotnetRecipeVersions {
		for _, image := range []string{"sdk", "aspnet", "runtime"} {
			images = append(images, "mcr.microsoft.com/dotnet/"+image+":"+version)
		}
	}
	return images
}

// Every reviewed base must run where the dashboard runs: the Java 11 and
// 17 Alpine JREs the recipe used to run on were published for amd64 alone,
// so an arm64 server failed every such build after Deploy. The images are
// resolved, not pulled.
func TestLiveRecipeBaseCatalogueRunsOnAmd64AndArm64(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to resolve the recipe catalogue")
	}
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	seen := map[string]bool{}
	images := append(javaCatalogueImages(), dotnetCatalogueImages()...)
	for _, bases := range recipeBaseCatalogue {
		images = append(images, bases...)
	}
	sort.Strings(images)
	for _, image := range images {
		if seen[image] {
			continue
		}
		seen[image] = true
		t.Run(image, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			resolved, err := backend.ResolveImage(ctx, image, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, platform := range []string{"linux/amd64", "linux/arm64"} {
				if !platformListContains(resolved.Platforms, platform) {
					t.Fatalf("%s publishes %v, not %s", image, resolved.Platforms, platform)
				}
			}
		})
	}
}
