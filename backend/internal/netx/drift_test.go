package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func driftSave(t *testing.T, s *Service, sp *Spec) {
	t.Helper()
	rtSaveSpec(t, s, sp)
	files, err := s.renderAll(sp)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func driftIdleNFT() string {
	return `{"nftables":[{"table":{"family":"inet","name":"jd_gateway"}},{"set":{"family":"inet","table":"jd_gateway","name":"trusted4"}},{"set":{"family":"inet","table":"jd_gateway","name":"trusted6"}}]}`
}

func driftIdleHost(t *testing.T) *recorder {
	return record(t).on("nft -t -j list ruleset", driftIdleNFT()).on("systemctl show", "LoadState=not-found\nResult=success\nExecMainStartTimestampMonotonic=0\nExecMainStatus=0\n")
}

func driftFind(t *testing.T, objects []DriftObservation, domain, resource string) DriftObservation {
	t.Helper()
	for _, o := range objects {
		if o.Domain == domain && o.Resource == resource {
			return o
		}
	}
	t.Fatalf("missing observation %s/%s in %+v", domain, resource, objects)
	return DriftObservation{}
}

func TestDriftSeparatesSavedRecoveredAndRenderedGenerationsWithoutWriting(t *testing.T) {
	s := testService(t)
	sp := emptySpec()
	driftSave(t, s, sp)
	j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "candidate", Generation: strings.Repeat("b", 64), Phase: "recovered", Persistence: "restored", Runtime: "restored"}, VerificationDigest: "private-challenge", Commands: []recoveryCommand{{Tool: "ip", Args: []string{"private-undo"}}}}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	beforeSpec, _ := os.ReadFile(s.specPath())
	beforeJournal, _ := os.ReadFile(filepath.Join(s.paths.Dir, recoveryFile))
	rec := driftIdleHost(t)
	r := s.Drift(context.Background())
	if !r.Consistent || r.Spec.Status != "matching" || r.SavedGeneration != digestBytes(beforeSpec) || r.Change == nil || r.Change.Generation == r.SavedGeneration || r.Change.Phase != "recovered" {
		t.Fatalf("generations conflated: %+v", r)
	}
	for _, f := range r.Files {
		if f.Status != "matching" {
			t.Fatalf("recovered candidate marked saved render drift: %+v", f)
		}
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "private-challenge") || strings.Contains(string(data), "private-undo") {
		t.Fatal("inspection leaked private recovery data")
	}
	afterSpec, _ := os.ReadFile(s.specPath())
	afterJournal, _ := os.ReadFile(filepath.Join(s.paths.Dir, recoveryFile))
	if string(afterSpec) != string(beforeSpec) || string(afterJournal) != string(beforeJournal) {
		t.Fatal("inspection wrote managed state")
	}
	if _, err := os.Stat(filepath.Join(s.paths.Dir, ".change.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created recovery lock")
	}
	for _, cmd := range rec.commands() {
		if !strings.HasPrefix(cmd, "nft -t -j list") && !strings.HasPrefix(cmd, "systemctl show") {
			t.Fatalf("read performed a mutation: %s", cmd)
		}
	}
	if r.RepairPlan.Executable || r.RepairPlan.Generation != r.SavedGeneration {
		t.Fatal("plan is executable or not generation bound")
	}
}

func TestDriftDistinguishesMissingChangedForeignAndUnreadableRenders(t *testing.T) {
	s := testService(t)
	driftSave(t, s, emptySpec())
	driftIdleHost(t)
	missing := filepath.Join(s.paths.Dir, linksFile)
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(s.paths.Dir, rules6File)
	if err := os.WriteFile(changed, []byte(generatedHeader+"rule add priority 10001 table 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.paths.Unit, []byte("[Unit]\nDescription=Foreign owner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(s.paths.Dir, shapingFile)
	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(changed, unreadable); err != nil {
		t.Fatal(err)
	}
	r := s.Drift(context.Background())
	for path, status := range map[string]string{missing: "missing", changed: "drift", s.paths.Unit: "conflict", unreadable: "unreadable"} {
		if got := driftFind(t, r.Files, "render", path); got.Status != status {
			t.Fatalf("%s: %+v", status, got)
		}
	}
	if len(r.RepairPlan.Items) != 2 || len(r.RepairPlan.Excluded) < 2 {
		t.Fatalf("plan repaired foreign or unreadable state: %+v", r.RepairPlan)
	}
}

func TestDriftUnknownInventoriesNeverBecomeMissingOrRepairable(t *testing.T) {
	for _, answer := range []string{"", "null", `{}`, `[{}]`, `[{"ifname":"d0","addr_info":null}]`} {
		t.Run(answer, func(t *testing.T) {
			s := testService(t)
			sp := emptySpec()
			sp.Links = []LinkSpec{{Name: "d0", Kind: "dummy", Up: true}}
			driftSave(t, s, sp)
			driftIdleHost(t).on("ip -j -d link show", answer).on("ip -j addr show", answer)
			r := s.Drift(context.Background())
			o := driftFind(t, r.Runtime, "link", "d0")
			if o.Status == "missing" || o.Repairable {
				t.Fatalf("unknown inventory authorized repair: %+v", o)
			}
		})
	}
}

func TestDriftKernelReadsSeparateOwnedMismatchForeignCollisionAndNativeDependencies(t *testing.T) {
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "d0", Kind: "dummy", MTU: 1400, Up: true}, {Name: "lost", Kind: "vlan", Parent: "provider0", VLANID: 7, Up: true}, {Name: "occupied", Kind: "dummy", Up: true}}
	sp.Addresses = []AddressSpec{{ID: 1, Link: "d0", CIDR: "192.0.2.7/24"}}
	sp.Routes = []RouteSpec{{ID: 2, Family: "inet", Type: "unicast", Destination: "198.51.100.0/24", Device: "d0", Table: 100, Source: "192.0.2.7"}}
	sp.Rules = []RuleSpec{{ID: 3, Family: "inet", Priority: 10001, Action: "lookup", Table: 100}}
	driftSave(t, s, sp)
	driftIdleHost(t).on("ip -j -d link show", `[{"ifname":"d0","flags":["UP"],"mtu":1500,"linkinfo":{"info_kind":"dummy"}},{"ifname":"occupied","flags":["UP"],"mtu":1500,"linkinfo":{"info_kind":"bridge"}}]`).on("ip -j addr show", `[{"ifname":"d0","addr_info":[]}]`).on("ip -j -4 route show table all", `[{"dst":"198.51.100.0/24","dev":"d0","table":100,"prefsrc":"192.0.2.8"}]`).on("ip -j -4 rule show", `[{"priority":10001,"src":"all","table":200}]`)
	r := s.Drift(context.Background())
	for domain, resource := range map[string]string{"link": "d0", "address": "d0/192.0.2.7/24"} {
		o := driftFind(t, r.Runtime, domain, resource)
		if !o.Repairable {
			t.Fatalf("known owned repair absent: %+v", o)
		}
	}
	if o := driftFind(t, r.Runtime, "link", "lost"); o.Status != "missing" || o.Repairable || !strings.Contains(o.Reason, "native") {
		t.Fatalf("native parent would be repaired: %+v", o)
	}
	for domain, resource := range map[string]string{"link": "occupied", "route": "2", "rule": "3"} {
		o := driftFind(t, r.Runtime, domain, resource)
		if o.Status != "conflict" || o.Repairable {
			t.Fatalf("foreign collision accepted: %+v", o)
		}
	}
}

func TestDriftRulesRejectUnknownSelectorsAndDuplicatePriorities(t *testing.T) {
	want := RuleSpec{ID: 1, Family: "inet", Priority: 10001, Action: "lookup", Table: 100}
	unknown := driftRule(want, []json.RawMessage{json.RawMessage(`{"priority":10001,"src":"all","table":100,"ipproto":"tcp"}`)}, nil)
	if unknown.Status != "unknown" || unknown.Repairable {
		t.Fatalf("extra selector ignored: %+v", unknown)
	}
	duplicate := driftRule(want, []json.RawMessage{json.RawMessage(`{"priority":10001,"src":"all","table":100}`), json.RawMessage(`{"priority":10001,"src":"all","table":200}`)}, nil)
	if duplicate.Status != "conflict" {
		t.Fatalf("duplicate priority silently matched: %+v", duplicate)
	}
}

func TestDriftRefusesPlanDuringPendingJournalAndConcurrentRecovery(t *testing.T) {
	for _, phase := range []string{"awaiting_confirmation", "degraded"} {
		t.Run(phase, func(t *testing.T) {
			s := testService(t)
			driftSave(t, s, emptySpec())
			if err := os.Remove(filepath.Join(s.paths.Dir, linksFile)); err != nil {
				t.Fatal(err)
			}
			j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "pending", Generation: strings.Repeat("a", 64), Phase: phase}}
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			driftIdleHost(t)
			r := s.Drift(context.Background())
			if r.RepairPlan.Status != "blocked" || r.RepairPlan.Executable {
				t.Fatalf("unresolved journal did not block plan: %+v", r.RepairPlan)
			}
		})
	}
	t.Run("concurrent", func(t *testing.T) {
		s := testService(t)
		driftSave(t, s, emptySpec())
		rec := driftIdleHost(t)
		previous := run
		run = func(ctx context.Context, name string, args ...string) (string, error) {
			if name == "systemctl" {
				if err := os.WriteFile(s.specPath(), []byte("{bad"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			return previous(ctx, name, args...)
		}
		_ = rec
		r := s.Drift(context.Background())
		if r.Consistent || r.Status != "unknown" || r.RepairPlan.Status != "blocked" {
			t.Fatalf("concurrent recovery falsely certified: %+v", r)
		}
	})
}

func TestDriftBootNeedsMeasuredExecutionAndDetectsIgnoredFailure(t *testing.T) {
	t.Run("never", func(t *testing.T) {
		s := testService(t)
		driftIdleHost(t)
		b := s.driftBoot(context.Background())
		if b.Execution.Status != "unrecorded" || b.Execution.StartedAt != "" || b.Execution.Result != "" || b.Execution.ExitStatus != nil {
			t.Fatalf("default success fabricated a boot: %+v", b)
		}
	})
	for _, status := range []string{"0", "2"} {
		t.Run(status, func(t *testing.T) {
			s := testService(t)
			if err := writeFileAtomic(s.paths.Unit, []byte(generatedHeader+"[Service]\nExecStart=-ip -batch fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			record(t).on("systemctl show", "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\nFragmentPath="+s.paths.Unit+"\nDropInPaths=\nNeedDaemonReload=no\nResult=success\nExecMainStartTimestamp=Thu 2026-10-08 10:00:00 UTC\nExecMainExitTimestamp=Thu 2026-10-08 10:00:01 UTC\nExecMainStartTimestampMonotonic=1000000\nExecMainExitTimestampMonotonic=2000000\nExecMainStatus=0\nExecStart={ path=/usr/bin/ip ; argv[]=/usr/bin/ip -batch fixture ; ignore_errors=yes ; start_time=[Thu 2026-10-08 10:00:00 UTC] ; stop_time=[Thu 2026-10-08 10:00:01 UTC] ; pid=123 ; code=exited ; status="+status+" }\n")
			b := s.driftBoot(context.Background())
			expected := "succeeded"
			if status != "0" {
				expected = "failed"
			}
			if b.Execution.Status != expected || b.Execution.BootTrigger != "unknown" || b.Execution.StartedMonotonicUS != 1000000 {
				t.Fatalf("measured command outcome lost: %+v", b)
			}
		})
	}
	t.Run("partial commands", func(t *testing.T) {
		s := testService(t)
		if err := writeFileAtomic(s.paths.Unit, []byte(generatedHeader+"ExecStart=-ip one\nExecStart=-ip two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		record(t).on("systemctl show", "LoadState=loaded\nResult=success\nExecMainStartTimestamp=measured\nExecMainStartTimestampMonotonic=1\nExecMainExitTimestampMonotonic=2\nExecStart={ path=/usr/bin/ip ; start_time=[measured] ; stop_time=[measured] ; code=exited ; status=0 }\n")
		b := s.driftBoot(context.Background())
		if b.Execution.Status != "unknown" {
			t.Fatalf("partial results claimed success: %+v", b)
		}
	})
}

func TestDriftBootUnreadableMetadataDoesNotInventAnUnrecordedOrOwnedActivation(t *testing.T) {
	for _, monotonic := range []string{"", "bad", "1"} {
		t.Run(monotonic, func(t *testing.T) {
			s := testService(t)
			if err := writeFileAtomic(s.paths.Unit, []byte(renderUnit(s.paths, false)), 0o644); err != nil {
				t.Fatal(err)
			}
			out := "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\nFragmentPath=" + s.paths.Unit + "\nNeedDaemonReload=no\nResult=success\n"
			if monotonic != "" {
				out += "ExecMainStartTimestampMonotonic=" + monotonic + "\n"
			}
			record(t).on("systemctl show", out)
			b := s.driftBoot(context.Background())
			if b.Status != "unknown" || b.Execution.Status != "unknown" || b.Execution.Result != "" || b.Execution.ExitStatus != nil {
				t.Fatalf("incomplete metadata fabricated certainty: %+v", b)
			}
		})
	}
}

func TestDriftGatewayNeverCertifiesRulesFromPreservedComments(t *testing.T) {
	s := testService(t)
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "192.0.2.7", TargetPort: "80", SourceNAT: "never", Enabled: true}}
	render, err := s.driftGatewayRender(sp)
	if err != nil {
		t.Fatal(err)
	}
	listing := `{"nftables":[{"table":{"family":"inet","name":"jd_gateway"}},{"set":{"family":"inet","table":"jd_gateway","name":"trusted4"}},{"set":{"family":"inet","table":"jd_gateway","name":"trusted6"}},{"chain":{"family":"inet","table":"jd_gateway","name":"nat_pre","type":"nat","hook":"prerouting","prio":-110,"policy":"accept"}},{"rule":{"family":"inet","table":"jd_gateway","chain":"nat_pre","comment":"forward:1","expr":[{"dnat":{"addr":"203.0.113.99"}}]}}]}`
	record(t).on("nft -t -j list ruleset", listing)
	o := s.driftGateway(context.Background(), sp, render, nil)[0]
	if o.Status != "unknown" || o.Repairable || !strings.Contains(o.Reason, "expression") {
		t.Fatalf("comments certified modified DNAT: %+v", o)
	}
}

func TestDriftCacheLossDoesNotHideIndependentRenderEvidence(t *testing.T) {
	s := testService(t)
	sp := emptySpec()
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "feed", Kind: "feed", URL: "https://example.com/list", Enabled: true}}
	rtSaveSpec(t, s, sp)
	for _, name := range []string{linksFile, rules6File, shapingFile} {
		content := renderLinks(sp)
		if name == rules6File {
			content = renderIPv6Rules(sp)
		}
		if name == shapingFile {
			content = renderShaping(sp)
		}
		if err := writeFileAtomic(filepath.Join(s.paths.Dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	driftIdleHost(t).on("nft -j list set", `{"nftables":[]}`)
	r := s.Drift(context.Background())
	if driftFind(t, r.Files, "render", filepath.Join(s.paths.Dir, linksFile)).Status != "matching" {
		t.Fatal("cache failure erased unrelated render evidence")
	}
	if len(r.Blocklists) != 1 || r.Blocklists[0].Cache.Status != "missing" {
		t.Fatalf("cache absence lost: %+v", r.Blocklists)
	}
	if slices.ContainsFunc(r.RepairPlan.Items, func(p OwnedRepair) bool { return p.Resource == filepath.Join(s.paths.Dir, gatewayFile) }) {
		t.Fatal("proposed empty gateway render after cache loss")
	}
}

func TestDriftReusesShapingEvidenceAndSeparatesUnreadableKernelSettings(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed queue", true: "unreadable queue"}[failed], func(t *testing.T) {
			s := testService(t)
			rtProc(t, nil, "0", "")
			sp := emptySpec()
			sp.Sysctls[sysctlForwardV4] = "1"
			sp.Sysctls["vm.swappiness"] = "60"
			sp.Shaping = []ShapeSpec{{Device: "eth0", EgressKbit: 50000}}
			rec := record(t).on("ip -j -d link show", `[{"ifname":"eth0","flags":["UP"],"mtu":1500}]`).on("ip -j addr show", `[{"ifname":"eth0","addr_info":[]}]`)
			if failed {
				rec.fail("tc -j qdisc show dev eth0", "permission denied")
			} else {
				rec.on("tc -j qdisc show dev eth0", `[]`)
			}
			out := s.driftRuntime(context.Background(), sp)
			if o := driftFind(t, out, "sysctl", sysctlForwardV4); o.Status != "drift" || !o.Repairable {
				t.Fatalf("sysctl drift missed: %+v", o)
			}
			if o := driftFind(t, out, "sysctl", "vm.swappiness"); o.Status != "unreadable" || o.Repairable {
				t.Fatalf("unreadable setting proposed: %+v", o)
			}
			want := "drift"
			if failed {
				want = "unknown"
			}
			if o := driftFind(t, out, "shaping", "eth0"); o.Status != want || o.Repairable == failed {
				t.Fatalf("shaping evidence lost: %+v", o)
			}
		})
	}
}

func TestDriftDetectsMissingRecoveryHelperWithoutExecutingReplacements(t *testing.T) {
	s := testService(t)
	if o := s.driftRecoveryHelper(); o.Status != "missing" || !o.Repairable {
		t.Fatalf("missing boot dependency missed: %+v", o)
	}
	if err := writeFileAtomic(filepath.Join(s.paths.Dir, recoveryBinary), []byte("foreign script"), 0o700); err != nil {
		t.Fatal(err)
	}
	if o := s.driftRecoveryHelper(); o.Status != "conflict" || o.Owned || o.Repairable {
		t.Fatalf("foreign executable accepted: %+v", o)
	}
}
