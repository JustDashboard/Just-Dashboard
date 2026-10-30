package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func seedPlan(seeds ...RuntimeSeed) PlanConfiguration {
	return PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildImage}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
		Variables: []PlannedVariable{{Name: "RCON_PASSWORD", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32}},
		Runtime: RuntimePlanConfig{
			Image: "alpine:3", Strategy: StrategyStopFirst, Seeds: seeds,
			Mounts: []RuntimeMount{
				{Source: "world-data", Target: "/data", Ownership: OwnershipManaged},
				{Source: "/srv/shared", Target: "/shared", Ownership: OwnershipLinked},
			},
		},
	}
}

func TestSeedsAreHeldToTheirVolumeAndCarryNoCredential(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	accepted := []RuntimeSeed{
		{Mount: "/data", Path: "server.properties", Content: "rcon.password={{variable.RCON_PASSWORD}}\nmotd=hi\n", Format: "properties"},
		{Mount: "/data", Path: "plugins/x.jar", URL: "https://example.com/x.jar", Checksum: digest, MaxBytes: 1 << 20},
	}
	if err := seedPlan(accepted...).Validate(); err != nil {
		t.Fatalf("valid seeds refused: %v", err)
	}
	for name, seed := range map[string]RuntimeSeed{
		"a literal password":          {Mount: "/data", Path: "a.properties", Content: "rcon.password=hunter2hunter2\n"},
		"an unknown variable":         {Mount: "/data", Path: "a.txt", Content: "x={{variable.NOPE}}"},
		"another placeholder":         {Mount: "/data", Path: "a.txt", Content: "x={{secret.rcon}}"},
		"a linked bind":               {Mount: "/shared", Path: "a.txt", Content: "x"},
		"an undeclared mount":         {Mount: "/etc", Path: "passwd", Content: "x"},
		"an escaping path":            {Mount: "/data", Path: "../etc/passwd", Content: "x"},
		"an absolute path":            {Mount: "/data", Path: "/data/a", Content: "x"},
		"both kinds":                  {Mount: "/data", Path: "a", Content: "x", URL: "https://example.com/a", Checksum: digest, MaxBytes: 1},
		"an unpinned artifact":        {Mount: "/data", Path: "a.jar", URL: "https://example.com/a.jar", MaxBytes: 1},
		"a plain-http artifact":       {Mount: "/data", Path: "a.jar", URL: "http://example.com/a.jar", Checksum: digest, MaxBytes: 1},
		"an artifact with a userinfo": {Mount: "/data", Path: "a.jar", URL: "https://u:p@example.com/a.jar", Checksum: digest, MaxBytes: 1},
		"an unbounded artifact":       {Mount: "/data", Path: "a.jar", URL: "https://example.com/a.jar", Checksum: digest},
	} {
		if err := seedPlan(seed).Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	twice := seedPlan(accepted[0], accepted[0])
	if err := twice.Validate(); err == nil {
		t.Error("the same file seeded twice was accepted")
	}
	compose := seedPlan(accepted[0])
	compose.Build.Method = BuildCompose
	if err := validateRuntimeSeeds(compose); err == nil {
		t.Error("a Compose release was given seeds")
	}
}

// seedDockerFake is the daemon as seeding sees it: a volume's tree with an
// owner per entry, and a record of every helper and run.
type seedDockerFake struct {
	mu       sync.Mutex
	entries  map[string]dockerx.PathOwner
	files    map[string][]byte
	created  []dockerx.ContainerSpec
	removed  []string
	runs     []dockerx.ContainerSpec
	download []byte
}

func newSeedDockerFake() *seedDockerFake {
	return &seedDockerFake{
		entries: map[string]dockerx.PathOwner{"/data": {Exists: true, Dir: true, UID: 1000, GID: 1000}},
		files:   map[string][]byte{},
	}
}

func (f *seedDockerFake) Create(_ context.Context, spec dockerx.ContainerSpec, _ chan<- dockerx.PullProgress) (*dockerx.CreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	return &dockerx.CreateResult{ID: "helper-" + spec.Name, Name: spec.Name}, nil
}

func (f *seedDockerFake) RemoveContainer(_ context.Context, id string, _, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}

func (f *seedDockerFake) PathOwnership(_ context.Context, _, target string) (dockerx.PathOwner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries[target], nil
}

func (f *seedDockerFake) WriteContainerTree(_ context.Context, _, base, relative string, content []byte, _ int64, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(relative, "/")
	for index := range parts[:len(parts)-1] {
		f.entries[base+"/"+strings.Join(parts[:index+1], "/")] = dockerx.PathOwner{Exists: true, Dir: true, UID: uid, GID: gid}
	}
	f.entries[base+"/"+relative] = dockerx.PathOwner{Exists: true, UID: uid, GID: gid}
	f.files[base+"/"+relative] = append([]byte(nil), content...)
	return nil
}

func (f *seedDockerFake) DigestContainerFile(_ context.Context, _, target string, limit int64) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, found := f.files[target]
	if !found {
		return "", 0, errors.New("missing")
	}
	if int64(len(content)) > limit {
		return "", int64(len(content)), dockerx.ErrContainerFileLimit
	}
	return digestOf(content), int64(len(content)), nil
}

// RunToCompletion plays curl, mv and rm against the fake tree, the fetch
// root mapped back onto the volume's /data.
func (f *seedDockerFake) RunToCompletion(_ context.Context, spec dockerx.ContainerSpec, _ chan<- dockerx.LogLine) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, spec)
	local := func(path string) string { return "/data" + strings.TrimPrefix(path, "/jd-seed") }
	arguments := spec.Command
	switch spec.Entrypoint[0] {
	case "curl":
		output := ""
		for index, argument := range arguments {
			if argument == "--output" {
				output = arguments[index+1]
			}
		}
		f.files[local(output)] = append([]byte(nil), f.download...)
	case "mv":
		from, to := local(arguments[1]), local(arguments[2])
		f.files[to] = f.files[from]
		delete(f.files, from)
		f.entries[to] = dockerx.PathOwner{Exists: true}
	case "rm":
		delete(f.files, local(arguments[1]))
	}
	return 0, true, nil
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func seedRequest(seeds ...RuntimeSeed) CandidateRuntimeRequest {
	plan := seedPlan(seeds...).Runtime
	return CandidateRuntimeRequest{
		Run: EngineRun{ID: 7, EnvironmentID: 3}, Release: Release{ID: 11, EnvironmentID: 3, RunID: 7, Number: 2},
		Snapshot:         runtimeReleaseSnapshot{Version: 1, Plan: plan},
		RuntimeVariables: map[string]string{"RCON_PASSWORD": `pass\word`},
	}
}

func TestSeedsAreWrittenOnceByAHelperThatMountsOnlyTheirVolume(t *testing.T) {
	t.Parallel()
	fake := newSeedDockerFake()
	request := seedRequest(
		RuntimeSeed{Mount: "/data", Path: "server.properties", Content: "rcon.password={{variable.RCON_PASSWORD}}\n", Format: "properties"},
		RuntimeSeed{Mount: "/data", Path: "config/paper/global.yml", Content: "verbose: false\n"},
	)
	lines := []string{}
	emit := func(line BuildLog) error { lines = append(lines, line.Text); return nil }
	if err := materializeSeeds(t.Context(), fake, request, "sha256:image", emit); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("helpers = %#v", fake.created)
	}
	helper := fake.created[0]
	if helper.Image != "sha256:image" || helper.NetworkMode != "none" || helper.Start || len(helper.Mounts) != 1 ||
		helper.Mounts[0].Source != "world-data" || helper.Mounts[0].Target != "/data" || len(helper.Env) != 0 {
		t.Fatalf("the helper is more than one volume and the release's image: %#v", helper)
	}
	for _, label := range helper.Labels {
		if label.Name == "io.just-dashboard.managed" {
			t.Fatal("a helper is labelled as one of the release's own containers")
		}
	}
	if got := string(fake.files["/data/server.properties"]); got != "rcon.password=pass\\\\word\n" {
		t.Fatalf("server.properties = %q", got)
	}
	if owner := fake.entries["/data/config/paper/global.yml"]; owner.UID != 1000 || owner.GID != 1000 {
		t.Fatalf("a nested file is owned by %d:%d, not the volume's owner", owner.UID, owner.GID)
	}
	if owner := fake.entries["/data/config"]; !owner.Dir || owner.UID != 1000 {
		t.Fatalf("the directory between them = %#v", owner)
	}
	if len(fake.removed) < 2 || fake.removed[len(fake.removed)-1] != "helper-jd-e3-r2-seed0" {
		t.Fatalf("the helper was not removed: %#v", fake.removed)
	}

	// The next release keeps what is there: an operator's edit survives.
	fake.files["/data/server.properties"] = []byte("motd=edited\n")
	lines = nil
	if err := materializeSeeds(t.Context(), fake, request, "sha256:image", emit); err != nil {
		t.Fatal(err)
	}
	if string(fake.files["/data/server.properties"]) != "motd=edited\n" || !strings.Contains(strings.Join(lines, "\n"), "Kept /data/server.properties") {
		t.Fatalf("a later release rewrote the file: %q (%v)", fake.files["/data/server.properties"], lines)
	}
}

func TestASeedThatNeedsAMissingVariableIsRefused(t *testing.T) {
	t.Parallel()
	fake := newSeedDockerFake()
	request := seedRequest(RuntimeSeed{Mount: "/data", Path: "server.properties", Content: "rcon.password={{variable.RCON_PASSWORD}}\n"})
	request.RuntimeVariables = map[string]string{}
	err := materializeSeeds(t.Context(), fake, request, "sha256:image", nil)
	if !errors.Is(err, ErrSeedRefused) || !strings.Contains(err.Error(), "RCON_PASSWORD") {
		t.Fatalf("err = %v", err)
	}
	if _, written := fake.files["/data/server.properties"]; written {
		t.Fatal("a file with a blank password was written")
	}
}

func TestAnArtifactIsUsedOnlyWhenItsDigestMatches(t *testing.T) {
	t.Parallel()
	payload := []byte("server jar bytes")
	pinned := digestOf(payload)
	artifact := RuntimeSeed{Mount: "/data", Path: "plugins/x.jar", URL: "https://example.com/x.jar", Checksum: pinned, MaxBytes: 1 << 20}

	fake := newSeedDockerFake()
	fake.download = payload
	if err := materializeSeeds(t.Context(), fake, seedRequest(artifact), "sha256:image", nil); err != nil {
		t.Fatal(err)
	}
	if string(fake.files["/data/plugins/x.jar"]) != string(payload) {
		t.Fatalf("the verified artifact was not placed: %#v", fake.files)
	}
	if _, partial := fake.files["/data/plugins/x.jar.jd-partial"]; partial {
		t.Fatal("the partial download was left behind")
	}
	fetch := fake.runs[0]
	if fetch.Image != SeedFetchImage || fetch.User != "1000:1000" || len(fetch.Mounts) != 1 || fetch.Mounts[0].Source != "world-data" {
		t.Fatalf("the fetch helper = %#v", fetch)
	}
	joined := strings.Join(fetch.Command, " ")
	for _, want := range []string{"--proto =https", "--proto-redir =https", "--max-filesize 1048576", "--fail"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("curl runs without %q: %s", want, joined)
		}
	}

	tampered := newSeedDockerFake()
	tampered.download = []byte("something else")
	err := materializeSeeds(t.Context(), tampered, seedRequest(artifact), "sha256:image", nil)
	if !errors.Is(err, ErrSeedRefused) || !strings.Contains(err.Error(), digestOf(tampered.download)) || !strings.Contains(err.Error(), pinned) {
		t.Fatalf("a mismatched artifact = %v", err)
	}
	if len(tampered.files) != 0 {
		t.Fatalf("a mismatched artifact was kept: %#v", tampered.files)
	}
}
