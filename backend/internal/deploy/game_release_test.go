package deploy

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// Both Minecraft templates deploy the way any reviewed image does: the
// source saves, detection resolves the image digest and hands back the
// rendered configuration, preflight explains the loopback default without
// blocking, and commit generates the console password the settings file
// references.
func TestGameBlueprintsDeployThroughTheDraftJourney(t *testing.T) {
	for _, edition := range []struct {
		id, protocol, check string
		port                int
	}{
		{"minecraft-java", "tcp", "game_handshake", 25565},
		// Bedrock's current releases take players over NetherNet: a TCP
		// handshake whose HTTP join answer is the readiness check, and the
		// gameplay UDP it negotiates, on the same number.
		{"minecraft-bedrock", "tcp+udp", "http", 19132},
	} {
		t.Run(edition.id, func(t *testing.T) {
			ctx := context.Background()
			fixture := newPlanningStoreFixture(t)
			fake := &planningDockerFake{image: &dockerx.DistributionImage{
				Digest: "sha256:" + strings.Repeat("9", 64), Platforms: []string{"linux/amd64"},
			}}
			analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, nil)
			draft, err := fixture.plans.Create(ctx, 41, "admin")
			if err != nil {
				t.Fatal(err)
			}
			draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
				Revision: draft.Revision, Step: DraftIntent, Intent: &DraftIntentConfig{Name: "world", Profile: ProfileGame},
			})
			if err != nil {
				t.Fatal(err)
			}
			source := DraftSourceConfig{
				Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: edition.id, BlueprintVersion: "1.0.0",
				BlueprintInputs: map[string]string{"eula": "true", "server-name": "Café \\ Crew"},
			}
			draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &source})
			if err != nil {
				t.Fatalf("a game source is refused: %v", err)
			}
			detection, err := analyzer.Analyze(ctx, source)
			if err != nil || detection.Source.Digest != fake.image.Digest {
				t.Fatalf("detection = %#v, %v", detection.Source, err)
			}
			draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, detection)
			if err != nil {
				t.Fatal(err)
			}
			configuration := draft.Data.Configuration
			runtime := configuration.Runtime
			if runtime.Protocol != edition.protocol || runtime.HostPort != edition.port || runtime.BindAddress != "127.0.0.1" ||
				runtime.Strategy != StrategyStopFirst {
				t.Fatalf("runtime = %#v", runtime)
			}
			if len(runtime.Seeds) != 1 || runtime.Seeds[0].Mount != "/data" || runtime.Seeds[0].Path != "server.properties" ||
				runtime.Seeds[0].Format != "properties" {
				t.Fatalf("seeds = %#v", runtime.Seeds)
			}
			// The input is escaped as the properties file reads it.
			if !strings.Contains(runtime.Seeds[0].Content, `Caf\u00e9 \\ Crew`) {
				t.Fatalf("server name was not escaped: %q", runtime.Seeds[0].Content)
			}
			var probe *PlannedCheck
			for index := range configuration.Checks {
				check := configuration.Checks[index]
				if err := validateCheckConfiguration(check.Kind, check.Config); err != nil {
					t.Fatalf("%s: %v", check.Name, err)
				}
				if reason := unrunnableCheck(check.Kind); reason != "" {
					t.Fatalf("%s cannot run: %s", check.Name, reason)
				}
				if check.Kind == edition.check {
					probe = &configuration.Checks[index]
				}
			}
			if probe == nil || probe.Phase != "readiness" || !probe.Required {
				t.Fatalf("checks = %#v", configuration.Checks)
			}
			preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
				Facilities: map[string]FacilityObservation{"docker": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
			}}, true)
			if err != nil {
				t.Fatal(err)
			}
			acknowledged, explained := []string{}, false
			for _, finding := range preflight.Findings {
				switch finding.Severity {
				case PreflightBlocked, PreflightDecision:
					t.Fatalf("game preflight blocked: %+v", finding)
				case PreflightWarning:
					acknowledged = append(acknowledged, finding.Code)
				}
				if finding.Code == "game_loopback_only" {
					explained = strings.Contains(finding.Measured, fmt.Sprintf("%d/%s", edition.port, edition.protocol)) &&
						finding.FieldID == "runtime.bindAddress"
				}
			}
			if !explained {
				t.Fatalf("preflight does not explain the loopback default: %+v", preflight.Findings)
			}
			draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, true, draft.Revision, preflight)
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: acknowledged})
			if err != nil || !result.Created {
				t.Fatalf("commit = %#v, %v", result, err)
			}
			if edition.id == "minecraft-java" {
				var sealed string
				if err := fixture.store.DB.QueryRow(`SELECT value_enc FROM deploy_variable_revisions WHERE environment_id=? AND key='RCON_PASSWORD' AND active=1`,
					result.EnvironmentID).Scan(&sealed); err != nil {
					t.Fatalf("the console password was not generated: %v", err)
				}
				password, err := fixture.sealer.Open(sealed)
				if err != nil || !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(password) {
					t.Fatalf("console password = %q, %v", password, err)
				}
				if !strings.Contains(runtime.Seeds[0].Content, "rcon.password={{variable.RCON_PASSWORD}}") {
					t.Fatalf("the settings file does not reference the generated password: %q", runtime.Seeds[0].Content)
				}
			}
			if _, _, err := NewOrchestrationStore(fixture.store).Enqueue(ctx, RunRequest{
				ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: OperationDeploy, Trigger: TriggerManual,
				Actor: "admin", RequestDigest: "game-deploy", PlanRevision: 1, SlotClass: SlotHeavy, Steps: DefaultStepKeys,
			}); err != nil {
				t.Fatalf("the queue refused a game server: %v", err)
			}
		})
	}
}

// A routable bind is the operator's choice; once made, the loopback finding
// gives way to the firewall review the plan already gets, which compares the
// right protocol.
func TestAPublishedGameServerIsComparedWithTheFirewallByItsProtocol(t *testing.T) {
	t.Parallel()
	runtime := RuntimePlanConfig{Protocol: "udp", InternalPort: 19132, HostPort: 19132, BindAddress: "0.0.0.0", Strategy: StrategyStopFirst}
	if _, ok := gameReachFinding(ProfileGame, runtime); ok {
		t.Fatal("a published game server is still told it is on loopback")
	}
	runtime.BindAddress = "127.0.0.1"
	if _, ok := gameReachFinding(ProfileWeb, runtime); ok {
		t.Fatal("a web plan was given a game finding")
	}
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "world", Profile: ProfileGame},
		Source:    &DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint},
		Detection: &DetectionResult{},
	}}
	request := preflightObservationRequest(draft, PlanConfiguration{Runtime: RuntimePlanConfig{
		Protocol: "udp", InternalPort: 19132, HostPort: 19132, BindAddress: "0.0.0.0",
	}})
	if !request.NeedsFirewall || len(request.Ports) != 1 || request.Ports[0].Protocol != "udp" {
		t.Fatalf("observation request = %#v", request)
	}
}

func TestAUDPPrimaryIsPublishedOnlyAsAFixedPort(t *testing.T) {
	t.Parallel()
	base := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildImage}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
		Runtime: RuntimePlanConfig{Image: "alpine:3", Protocol: "udp", InternalPort: 19132, HostPort: 19132, Strategy: StrategyStopFirst},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("a fixed UDP publication is refused: %v", err)
	}
	unpinned := base
	unpinned.Runtime.HostPort = 0
	if err := unpinned.Validate(); err == nil {
		t.Fatal("a leased UDP port was accepted")
	}
	clash := base
	clash.Runtime.Ports = []PublishedPort{{HostPort: 19132, ContainerPort: 19133, Protocol: "udp"}}
	if err := clash.Validate(); err == nil {
		t.Fatal("two publications of 19132/udp were accepted")
	}
	beside := base
	beside.Runtime.Ports = []PublishedPort{{HostPort: 19132, ContainerPort: 19132, Protocol: "tcp"}}
	if err := beside.Validate(); err != nil {
		t.Fatalf("19132/tcp beside 19132/udp is refused: %v", err)
	}
	other := base
	other.Runtime.Protocol = "sctp"
	if err := other.Validate(); err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
}

// statusServer answers one Server List Ping the way a Minecraft Java server
// does, after reading the handshake and the status request.
func statusServer(t *testing.T, answer string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				buffer := make([]byte, 512)
				if _, err := connection.Read(buffer); err != nil {
					return
				}
				var body bytes.Buffer
				body.WriteByte(0x00)
				body.Write(binary.AppendUvarint(nil, uint64(len(answer))))
				body.WriteString(answer)
				var packet bytes.Buffer
				packet.Write(binary.AppendUvarint(nil, uint64(body.Len())))
				packet.Write(body.Bytes())
				_, _ = connection.Write(packet.Bytes())
			}()
		}
	}()
	return listener.Addr().String()
}

func TestTheGameHandshakeCheckAsksForTheServerStatus(t *testing.T) {
	t.Parallel()
	runner := NewCheckRunner(nil)
	target := func(address string) CheckTarget {
		host, port, _ := net.SplitHostPort(address)
		var number int
		fmt.Sscanf(port, "%d", &number)
		return CheckTarget{Host: host, Port: number}
	}
	check := PlannedCheck{Name: "handshake", Kind: "game_handshake", Phase: "readiness", Required: true,
		Config: mustJSON(map[string]any{"attempts": 2, "timeoutSeconds": 2})}
	ready := statusServer(t, `{"version":{"name":"1.21.4","protocol":769},"players":{"max":20,"online":0}}`)
	if evidence := runner.Run(t.Context(), check, target(ready)); evidence.Outcome != HealthPassed {
		t.Fatalf("a ready server = %#v", evidence)
	}
	silent := statusServer(t, `not json`)
	evidence := runner.Run(t.Context(), check, target(silent))
	if evidence.Outcome != HealthFailed || evidence.Attempts[len(evidence.Attempts)-1].Code != "handshake_failed" {
		t.Fatalf("a server answering nonsense = %#v", evidence)
	}
	if message := checkFailureMessage("readiness", evidence.Outcome, evidence); !strings.Contains(message, "status handshake") {
		t.Fatalf("message = %q", message)
	}
}

// udpResponder answers each datagram with reply, or with nothing.
func udpResponder(t *testing.T, reply []byte) int {
	t.Helper()
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			_, from, err := connection.ReadFrom(buffer)
			if err != nil {
				return
			}
			if reply != nil {
				_, _ = connection.WriteTo(reply, from)
			}
		}
	}()
	return connection.LocalAddr().(*net.UDPAddr).Port
}

func TestTheUDPProbeNeedsADatagramAnswered(t *testing.T) {
	t.Parallel()
	runner := NewCheckRunner(nil)
	probe := func(port int, expect string) CheckEvidence {
		return runner.Run(t.Context(), PlannedCheck{Name: "ping", Kind: "udp_probe", Phase: "readiness", Required: true,
			Config: mustJSON(map[string]any{"payload": "01", "expect": expect, "attempts": 1, "timeoutSeconds": 1})},
			CheckTarget{Host: "127.0.0.1", Port: port})
	}
	pong := udpResponder(t, []byte{0x1c, 0x00, 0x01})
	if evidence := probe(pong, "1c"); evidence.Outcome != HealthPassed {
		t.Fatalf("an answered ping = %#v", evidence)
	}
	if evidence := probe(pong, "1d"); evidence.Attempts[0].Code != "unexpected_answer" {
		t.Fatalf("a wrong answer = %#v", evidence)
	}
	quiet := udpResponder(t, nil)
	if evidence := probe(quiet, ""); evidence.Outcome != HealthFailed || evidence.Attempts[0].Code != "no_answer" {
		t.Fatalf("silence = %#v", evidence)
	}
	for _, config := range []map[string]any{
		{"payload": "0G"}, {"payload": strings.Repeat("00", 513)}, {"expect": "1"}, {"url": "http://x"},
	} {
		if err := validateCheckConfiguration("udp_probe", mustJSON(config)); err == nil {
			t.Errorf("udp_probe accepted %v", config)
		}
	}
	if err := validateCheckConfiguration("tcp", mustJSON(map[string]any{"payload": "01"})); err == nil {
		t.Error("a TCP check accepted a datagram")
	}
	var decoded CheckConfiguration
	if err := json.Unmarshal(mustJSON(map[string]any{"payload": "01", "expect": "1c"}), &decoded); err != nil || decoded.Payload != "01" {
		t.Fatalf("configuration = %#v, %v", decoded, err)
	}
}

func TestGameReachReadsTheBindAndTheFirewallForTheRightProtocol(t *testing.T) {
	t.Parallel()
	plan := RuntimePlanConfig{Protocol: "udp", InternalPort: 19132, HostPort: 19132, BindAddress: "0.0.0.0"}
	firewall := &netsec.FirewallStatus{Available: true, Enabled: true, Policy: netsec.DefaultPolicy{Incoming: "deny"},
		Capabilities: netsec.FirewallCapabilities{Editable: true},
		Rules:        []netsec.Rule{{Action: "ALLOW", Direction: "IN", Port: "19132", Protocol: "tcp"}}}
	reach, ok := DescribeGameReach(plan, firewall, nil)
	if !ok || reach.Port != 19132 || reach.Protocol != "udp" || !reach.Public || reach.Firewall != "drops" || !reach.Editable {
		t.Fatalf("a TCP rule counted for a UDP port: %#v", reach)
	}
	firewall.Rules = append(firewall.Rules, netsec.Rule{Action: "ALLOW", Direction: "IN", Port: "19132", Protocol: "udp"})
	if reach, _ := DescribeGameReach(plan, firewall, nil); reach.Firewall != "admits" {
		t.Fatalf("an allow rule = %#v", reach)
	}
	firewall.Enabled = false
	if reach, _ := DescribeGameReach(plan, firewall, nil); reach.Firewall != "off" {
		t.Fatalf("a disabled firewall = %#v", reach)
	}
	if reach, _ := DescribeGameReach(plan, nil, errors.New("no ufw")); reach.Firewall != "unknown" || reach.Reason == "" {
		t.Fatalf("an unreadable firewall = %#v", reach)
	}
	plan.BindAddress = ""
	if reach, _ := DescribeGameReach(plan, firewall, nil); reach.Public || reach.Bind != "127.0.0.1" {
		t.Fatalf("an unset bind = %#v", reach)
	}
	if _, ok := DescribeGameReach(RuntimePlanConfig{InternalPort: 8080}, firewall, nil); ok {
		t.Fatal("a routed release described a game port")
	}
}
