package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// A release's seeds are written by helpers against a real daemon: a settings
// file rendered with a sealed value, owned by whoever the image's data
// directory belongs to (PostgreSQL's 70, not root), a nested file whose
// directories are created the same way, a real https download kept because
// its digest matches the pin, and one refused because it does not — with the
// digest it actually had in the message and nothing left in the volume.
func TestLiveReleaseSeedsAreWrittenIntoTheVolumeBeforeStart(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to write seeds through real helper containers")
	}
	client := liveC4Docker(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	const image = "postgres:17-alpine"
	if _, err := liveDockerOutput(ctx, "image", "inspect", image); err != nil {
		t.Skipf("%s is not on this host: %v", image, err)
	}
	stamp := time.Now().UnixNano()
	volume := fmt.Sprintf("jd-seed-live-%d", stamp)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = liveDockerOutput(cleanup, "volume", "rm", "--force", volume)
	})
	const license = "https://raw.githubusercontent.com/golang/go/go1.22.0/LICENSE"
	const licenseDigest = "sha256:2d36597f7117c38b006835ae7f537487207d8ec407aa9d9980794b2030cbc067"
	target := "/var/lib/postgresql/data"
	request := CandidateRuntimeRequest{
		Run: EngineRun{ID: stamp, EnvironmentID: stamp}, Release: Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1},
		Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: RuntimePlanConfig{
			Strategy: StrategyStopFirst,
			Mounts:   []RuntimeMount{{Source: volume, Target: target, Ownership: OwnershipManaged}},
			Seeds: []RuntimeSeed{
				{Mount: target, Path: "seed.properties", Content: "secret={{variable.SEED_SECRET}}\n", Format: "properties"},
				{Mount: target, Path: "conf/nested/app.conf", Content: "nested=yes\n"},
				{Mount: target, Path: "downloads/LICENSE", URL: license, Checksum: licenseDigest, MaxBytes: 1 << 20, TimeoutSeconds: 120},
			},
		}},
		RuntimeVariables: map[string]string{"SEED_SECRET": `s3cr\et`},
	}
	lines := []string{}
	emit := func(line BuildLog) error { lines = append(lines, line.Text); return nil }
	if err := materializeSeeds(ctx, client, request, image, emit); err != nil {
		t.Fatal(err)
	}
	read := func(script string) string {
		output, err := liveDockerOutput(ctx, "run", "--rm", "--network", "none", "--entrypoint", "sh",
			"--mount", "type=volume,src="+volume+",dst=/v", image, "-c", script)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	if got := read("cat /v/seed.properties"); got != `secret=s3cr\\et` {
		t.Fatalf("seed.properties = %q", got)
	}
	if got := read("stat -c '%u:%g' /v/seed.properties /v/conf /v/conf/nested/app.conf /v/downloads/LICENSE | sort -u"); got != "70:70" {
		t.Fatalf("seeded entries are owned by %q, not the image's data owner", got)
	}
	if got := read("sha256sum /v/downloads/LICENSE | cut -d' ' -f1; ls -a /v/downloads"); !strings.Contains(got, strings.TrimPrefix(licenseDigest, "sha256:")) ||
		strings.Contains(got, "jd-partial") {
		t.Fatalf("the verified download = %q", got)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "verified "+licenseDigest) {
		t.Fatalf("transcript = %v", lines)
	}

	// The second release keeps all three, and a pin that does not match is
	// refused with the digest that arrived.
	if err := liveDockerOutputErr(ctx, "run", "--rm", "--network", "none", "--entrypoint", "sh", "--mount",
		"type=volume,src="+volume+",dst=/v", image, "-c", "echo edited > /v/seed.properties"); err != nil {
		t.Fatal(err)
	}
	wrong := "sha256:" + strings.Repeat("0", 64)
	request.Release.Number = 2
	request.Snapshot.Plan.Seeds = append(request.Snapshot.Plan.Seeds,
		RuntimeSeed{Mount: target, Path: "downloads/tampered", URL: license, Checksum: wrong, MaxBytes: 1 << 20, TimeoutSeconds: 120})
	err := materializeSeeds(ctx, client, request, image, nil)
	if !errors.Is(err, ErrSeedRefused) || !strings.Contains(err.Error(), licenseDigest) || !strings.Contains(err.Error(), wrong) {
		t.Fatalf("a mismatched download = %v", err)
	}
	if got := read("cat /v/seed.properties; ls -a /v/downloads"); !strings.HasPrefix(got, "edited") || strings.Contains(got, "tampered") {
		t.Fatalf("after the refusal the volume holds %q", got)
	}
	if output, _ := liveDockerOutput(ctx, "ps", "-a", "--filter", "label=io.just-dashboard.seed=true",
		"--filter", fmt.Sprintf("label=io.just-dashboard.environment-id=%d", stamp), "--format", "{{.ID}}"); strings.TrimSpace(string(output)) != "" {
		t.Fatalf("helpers were left behind: %s", output)
	}
}

func liveDockerOutputErr(ctx context.Context, args ...string) error {
	_, err := liveDockerOutput(ctx, args...)
	return err
}

// A UDP-only primary is published by the runtime owner on the plan's bind
// address and nowhere else, and a udp_probe is answered through it by a real
// game server: Bedrock in RakNet mode answers an unconnected ping with an
// unconnected pong. (The Bedrock template itself runs NetherNet, whose
// gameplay port answers nothing until a client negotiates it; this is the
// probe and the publication proved against the protocol that does answer.)
func TestLiveUDPProbeIsAnsweredThroughAPublishedUDPPort(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to publish a UDP game server and probe it")
	}
	client := liveC4Docker(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	image := liveCatalogueImage(t, ctx, NewDockerArtifactBackend(client), "itzg/minecraft-bedrock-server:2026.9.0")
	stamp := time.Now().UnixNano()
	volume := fmt.Sprintf("jd-udp-live-%d", stamp)
	container := fmt.Sprintf("jd-e%d-r1", stamp)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = liveDockerOutput(cleanup, "rm", "--force", "--volumes", container)
		_, _ = liveDockerOutput(cleanup, "volume", "rm", "--force", volume)
	})
	port := liveC5LoopbackPort(t)
	plan := RuntimePlanConfig{
		Protocol: "udp", InternalPort: 19132, HostPort: port, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		Mounts: []RuntimeMount{{Source: volume, Target: "/data", Ownership: OwnershipManaged}},
	}
	if err := (PlanConfiguration{Build: BuildPlanConfig{Method: BuildImage}, Runtime: plan}).Validate(); err != nil {
		t.Fatal(err)
	}
	started, err := NewDockerRuntimeOwner(client).StartCandidate(ctx, CandidateRuntimeRequest{
		Run: EngineRun{ID: stamp, EnvironmentID: stamp}, Release: Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1},
		Snapshot:         runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image},
		RuntimeVariables: map[string]string{"EULA": "TRUE", "TRANSPORT": "raknet"},
		Host:             "127.0.0.1", Port: port,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	published, err := liveDockerOutput(ctx, "port", container)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(published)); got != fmt.Sprintf("19132/udp -> 127.0.0.1:%d", port) {
		t.Fatalf("published = %q, want the UDP port on loopback alone", got)
	}
	ping := "01" + "0000000000000000" + "00ffff00fefefefefdfdfdfd12345678" + "0000000000000000"
	evidence := NewCheckRunner(client).Run(ctx, PlannedCheck{Name: "RakNet ping", Kind: "udp_probe", Phase: "readiness", Required: true,
		Config: mustJSON(map[string]any{"port": 19132, "payload": ping, "expect": "1c", "attempts": 60, "timeoutSeconds": 3, "intervalSeconds": 3})},
		CheckTarget{ContainerID: started.Target.ContainerID, Host: started.Target.Host, Port: started.Target.Port, OriginalPorts: []int{19132, port}})
	if evidence.Outcome != HealthPassed {
		logs, _ := liveDockerOutput(ctx, "logs", "--tail", "40", container)
		t.Fatalf("udp_probe = %+v\n%s", evidence, logs)
	}
}
