package dockerx

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildxCommandKeepsSecretsOutOfArgvAndScrubsDashboardEnvironment(t *testing.T) {
	t.Setenv("JD_MASTER_KEY", "must-not-enter-build")
	t.Setenv("VPSD_LEGACY_SECRET", "must-not-enter-build-either")
	options := ImmutableBuildOptions{
		Dockerfile: ".just-dashboard/Dockerfile", Tag: "just-dashboard/test:run-7",
		Platform: "linux/amd64", NoCache: true, Pull: true,
		Secrets: []BuildxSecret{
			{ID: "Z_TOKEN", Value: "z-super-secret"},
			{ID: "A_TOKEN", Value: "a-super-secret"},
		},
	}
	argv, environment, err := BuildxCommand(options, "/tmp/build-metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"buildx", "build", "--progress=plain", "--file", ".just-dashboard/Dockerfile",
		"--tag", "just-dashboard/test:run-7", "--load", "--metadata-file", "/tmp/build-metadata.json",
		"--pull", "--platform", "linux/amd64", "--no-cache",
		"--secret", "id=A_TOKEN,env=JD_DEPLOY_BUILDKIT_SECRET_0",
		"--secret", "id=Z_TOKEN,env=JD_DEPLOY_BUILDKIT_SECRET_1", ".",
	}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %#v\nwant %#v", argv, want)
	}
	joinedArgs := strings.Join(argv, "\x00")
	if strings.Contains(joinedArgs, "super-secret") {
		t.Fatalf("secret entered argv: %q", joinedArgs)
	}
	joinedEnvironment := strings.Join(environment, "\x00")
	for _, absent := range []string{"JD_MASTER_KEY=", "VPSD_LEGACY_SECRET="} {
		if strings.Contains(joinedEnvironment, absent) {
			t.Fatalf("dashboard secret environment survived scrub: %s", absent)
		}
	}
	for _, present := range []string{
		"JD_DEPLOY_BUILDKIT_SECRET_0=a-super-secret",
		"JD_DEPLOY_BUILDKIT_SECRET_1=z-super-secret",
		"BUILDKIT_PROGRESS=plain", "DOCKER_CLI_HINTS=false",
	} {
		if !strings.Contains(joinedEnvironment, present) {
			t.Fatalf("build environment missing %s", present)
		}
	}
}

func TestBuildxCommandRejectsDuplicateOrMalformedSecretAndPlatform(t *testing.T) {
	for name, options := range map[string]ImmutableBuildOptions{
		"duplicate secret": {
			Dockerfile: "Dockerfile", Tag: "test:tag",
			Secrets: []BuildxSecret{{ID: "TOKEN"}, {ID: "TOKEN"}},
		},
		"malformed secret": {
			Dockerfile: "Dockerfile", Tag: "test:tag",
			Secrets: []BuildxSecret{{ID: "BAD,ID"}},
		},
		"multiple platforms": {
			Dockerfile: "Dockerfile", Tag: "test:tag", Platform: "linux/amd64,linux/arm64",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := BuildxCommand(options, "/tmp/metadata"); err == nil {
				t.Fatal("unsafe Buildx command was accepted")
			}
		})
	}
}

func TestBuildxCommandPassesTargetAndBuildArgsThroughTheEnvironment(t *testing.T) {
	t.Setenv("NEXT_PUBLIC_API_URL", "inherited-from-the-dashboard")
	argv, environment, err := BuildxCommand(ImmutableBuildOptions{
		Dockerfile: "Dockerfile", Tag: "just-dashboard/test:run-8", Target: "production",
		BuildArgs: []BuildxArg{{Name: "NEXT_PUBLIC_API_URL", Value: "https://api.example.com"}, {Name: "NODE_VERSION", Value: "22"}},
	}, "/tmp/build-metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--target production") ||
		!strings.Contains(joined, "--build-arg NEXT_PUBLIC_API_URL --build-arg NODE_VERSION") ||
		strings.Contains(joined, "api.example.com") {
		t.Fatalf("argv = %s", joined)
	}
	values := []string{}
	for _, entry := range environment {
		if strings.HasPrefix(entry, "NEXT_PUBLIC_API_URL=") {
			values = append(values, entry)
		}
	}
	if !slices.Equal(values, []string{"NEXT_PUBLIC_API_URL=https://api.example.com"}) {
		t.Fatalf("build argument environment = %v", values)
	}
	for name, options := range map[string]ImmutableBuildOptions{
		"target":         {Dockerfile: "Dockerfile", Tag: "t:1", Target: "prod --push"},
		"argument name":  {Dockerfile: "Dockerfile", Tag: "t:1", BuildArgs: []BuildxArg{{Name: "A=B"}}},
		"duplicate name": {Dockerfile: "Dockerfile", Tag: "t:1", BuildArgs: []BuildxArg{{Name: "A"}, {Name: "A"}}},
	} {
		if _, _, err := BuildxCommand(options, "/tmp/m.json"); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestBuildxCommandBuildsThroughAPinnedFrontendWithTheHashOutOfArgv(t *testing.T) {
	t.Setenv("secrets-hash", "inherited-from-the-dashboard")
	frontend := "ghcr.io/railwayapp/railpack-frontend:v0.43.0@sha256:" + strings.Repeat("a", 64)
	hash := strings.Repeat("b", 64)
	argv, environment, err := BuildxCommand(ImmutableBuildOptions{
		Dockerfile: ".just-dashboard/railpack-plan.json", Tag: "just-dashboard/test:run-9",
		Secrets:  []BuildxSecret{{ID: "API_TOKEN", Value: "token-value"}},
		Frontend: &BuildxFrontend{Image: frontend, CacheKey: "deployment-9", SecretsHash: hash},
	}, "/tmp/build-metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--file .just-dashboard/railpack-plan.json",
		"--build-arg BUILDKIT_SYNTAX=" + frontend,
		"--build-arg cache-key=deployment-9",
		"--build-arg secrets-hash .",
		"--secret id=API_TOKEN,env=JD_DEPLOY_BUILDKIT_SECRET_0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv = %s; want %q in it", joined, want)
		}
	}
	if strings.Contains(joined, hash) || strings.Contains(joined, "token-value") {
		t.Fatalf("a value derived from the secrets entered argv: %s", joined)
	}
	hashes := []string{}
	for _, entry := range environment {
		if strings.HasPrefix(entry, "secrets-hash=") {
			hashes = append(hashes, entry)
		}
	}
	if !slices.Equal(hashes, []string{"secrets-hash=" + hash}) {
		t.Fatalf("secrets-hash environment = %v", hashes)
	}

	for name, options := range map[string]*BuildxFrontend{
		"a frontend by tag alone": {Image: "ghcr.io/railwayapp/railpack-frontend:v0.43.0"},
		"a frontend with a flag":  {Image: "x --push@sha256:" + strings.Repeat("a", 64)},
		"a cache key with spaces": {Image: frontend, CacheKey: "a b"},
		"a hash that is not hex":  {Image: frontend, SecretsHash: "not-a-hash"},
	} {
		if _, _, err := BuildxCommand(ImmutableBuildOptions{Dockerfile: "plan.json", Tag: "t:1", Frontend: options}, "/tmp/m.json"); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
