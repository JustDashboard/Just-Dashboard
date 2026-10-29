package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func flagsPlan(method BuildMethod, mutate func(*RuntimePlanConfig)) PlanConfiguration {
	configuration := limitPlan(mutate)
	configuration.Build.Method = method
	return configuration
}

// Every flag is checked by the plan's own Validate, so a draft, a settings
// save, `jd config set` and a blueprint are refused alike and the refusal
// names the field the Runtime page attaches it to.
func TestRuntimeFlagsAreValidatedWithTheFieldTheyRefuse(t *testing.T) {
	t.Parallel()
	nofile := func(soft, hard int64) []RuntimeUlimit {
		return []RuntimeUlimit{{Name: UlimitNofile, Soft: soft, Hard: hard}}
	}
	cases := []struct {
		name   string
		method BuildMethod
		mutate func(*RuntimePlanConfig)
		field  string
		says   string
	}{
		{"nothing set", BuildImage, func(*RuntimePlanConfig) {}, "", ""},
		{"a numeric user and group", BuildImage, func(r *RuntimePlanConfig) { r.User = "1000:1000" }, "", ""},
		{"a user by name", BuildImage, func(r *RuntimePlanConfig) { r.User = "node" }, "", ""},
		{"a name and a group number", BuildImage, func(r *RuntimePlanConfig) { r.User = "app:0" }, "", ""},
		{"three parts", BuildImage, func(r *RuntimePlanConfig) { r.User = "1:2:3" }, "runtime.user", "group after a colon"},
		{"a leading dash", BuildImage, func(r *RuntimePlanConfig) { r.User = "-root" }, "runtime.user", "name or a number"},
		{"a space", BuildImage, func(r *RuntimePlanConfig) { r.User = "app user" }, "runtime.user", "name or a number"},
		{"the overflow id", BuildImage, func(r *RuntimePlanConfig) { r.User = "4294967295" }, "runtime.user", "4294967294"},
		{"an empty group", BuildImage, func(r *RuntimePlanConfig) { r.User = "1000:" }, "runtime.user", ""},

		{"read-only with the default /tmp", BuildImage, func(r *RuntimePlanConfig) { r.ReadOnlyRootfs = true }, "", ""},
		{"a sized tmpfs", BuildImage, func(r *RuntimePlanConfig) {
			r.ReadOnlyRootfs, r.Tmpfs = true, []RuntimeTmpfs{{Path: "/tmp", SizeMB: 128}, {Path: "/app/cache"}}
		}, "", ""},
		{"a relative tmpfs", BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "tmp"}} }, "runtime.tmpfs[0]", "absolute path"},
		{"the root as tmpfs", BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/"}} }, "runtime.tmpfs[0]", "root itself"},
		{"tmpfs over /proc", BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/proc/sys"}} }, "runtime.tmpfs[0]", "kernel"},
		{"tmpfs twice", BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/tmp"}, {Path: "/tmp/"}} }, "runtime.tmpfs[1]", "twice"},
		{"tmpfs too big", BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/tmp", SizeMB: MaxTmpfsSizeMB + 1}} }, "runtime.tmpfs[0]", "MiB"},
		{"tmpfs where a volume is", BuildImage, func(r *RuntimePlanConfig) {
			r.Mounts = []RuntimeMount{{Source: "data", Target: "/data", Ownership: OwnershipManaged}}
			r.Tmpfs = []RuntimeTmpfs{{Path: "/data"}}
		}, "runtime.tmpfs[0]", "volume is mounted"},
		{"a static site read-only with nowhere for nginx", BuildStatic, func(r *RuntimePlanConfig) { r.ReadOnlyRootfs = true }, "runtime.tmpfs", "/var/cache/nginx and /run"},
		{"a static site with only the cache writable", BuildStatic, func(r *RuntimePlanConfig) {
			r.ReadOnlyRootfs, r.Tmpfs = true, []RuntimeTmpfs{{Path: "/var/cache/nginx"}}
		}, "runtime.tmpfs", "add /run as"},
		{"a static site with both, /run spelled /var/run", BuildStatic, func(r *RuntimePlanConfig) {
			r.ReadOnlyRootfs, r.Tmpfs = true, []RuntimeTmpfs{{Path: "/var/cache/nginx"}, {Path: "/var/run"}}
		}, "", ""},
		{"a static site whose cache is a volume", BuildStatic, func(r *RuntimePlanConfig) {
			r.Mounts = []RuntimeMount{{Source: "cache", Target: "/var/cache", Ownership: OwnershipManaged}}
			r.ReadOnlyRootfs, r.Tmpfs = true, []RuntimeTmpfs{{Path: "/run"}}
		}, "", ""},
		{"a static site whose cache volume is read-only", BuildStatic, func(r *RuntimePlanConfig) {
			r.Mounts = []RuntimeMount{{Source: "cache", Target: "/var/cache", ReadOnly: true, Ownership: OwnershipManaged}}
			r.ReadOnlyRootfs, r.Tmpfs = true, []RuntimeTmpfs{{Path: "/run"}}
		}, "runtime.tmpfs", "/var/cache/nginx"},
		{"a writable static site", BuildStatic, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/tmp"}} }, "", ""},

		{"nofile raised", BuildImage, func(r *RuntimePlanConfig) { r.Ulimits = nofile(65536, 65536) }, "", ""},
		{"memlock unlimited", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = []RuntimeUlimit{{Name: UlimitMemlock, Soft: -1, Hard: -1}}
		}, "", ""},
		{"a soft limit under an unlimited hard one", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = []RuntimeUlimit{{Name: UlimitCore, Soft: 0, Hard: -1}}
		}, "", ""},
		{"an unlimited soft limit under a hard one", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = []RuntimeUlimit{{Name: UlimitStack, Soft: -1, Hard: 8 << 20}}
		}, "runtime.ulimits[0]", "above the hard one"},
		{"nofile unlimited", BuildImage, func(r *RuntimePlanConfig) { r.Ulimits = nofile(-1, -1) }, "runtime.ulimits[0]", "nofile is between 16 and 1048576"},
		{"nofile past nr_open", BuildImage, func(r *RuntimePlanConfig) { r.Ulimits = nofile(1024, MaxUlimitNofile+1) }, "runtime.ulimits[0]", "1048576"},
		{"soft above hard", BuildImage, func(r *RuntimePlanConfig) { r.Ulimits = nofile(4096, 1024) }, "runtime.ulimits[0]", "above the hard one"},
		{"nice past the kernel's range", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = []RuntimeUlimit{{Name: UlimitNice, Soft: 10, Hard: 41}}
		}, "runtime.ulimits[0]", "between 0 and 40"},
		{"a name Docker does not set", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = []RuntimeUlimit{{Name: "files", Soft: 1, Hard: 1}}
		}, "runtime.ulimits[0]", "not a limit Docker sets"},
		{"a limit twice", BuildImage, func(r *RuntimePlanConfig) {
			r.Ulimits = append(nofile(1024, 1024), RuntimeUlimit{Name: UlimitNofile, Soft: 2048, Hard: 2048})
		}, "runtime.ulimits[1]", "set twice"},

		{"the connection backlog", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}}
		}, "", ""},
		{"semaphores", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "kernel.sem", Value: "250 32000 32 128"}, {Name: "fs.mqueue.msg_max", Value: "64"}}
		}, "", ""},
		{"an interface's own setting", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "net.ipv4.conf.eth0.route_localnet", Value: "1"}}
		}, "", ""},
		{"the server's swappiness", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "vm.swappiness", Value: "10"}}
		}, "runtime.sysctls[0]", "the server's, not the container's"},
		{"the server's pid_max", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "kernel.pid_max", Value: "4194304"}}
		}, "runtime.sysctls[0]", "the server's"},
		{"net on the host network", BuildImage, func(r *RuntimePlanConfig) {
			r.HostNetwork = true
			r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}}
		}, "runtime.sysctls[0]", "host network"},
		{"a value with a shell in it", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024; reboot"}}
		}, "runtime.sysctls[0]", "value of net.core.somaxconn"},
		{"a name that is not dotted", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "somaxconn", Value: "1"}}
		}, "runtime.sysctls[0]", "dotted name"},
		{"a parameter twice", BuildImage, func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1"}, {Name: "net.core.somaxconn", Value: "2"}}
		}, "runtime.sysctls[1]", "set twice"},

		{"every GPU", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUAll} }, "", ""},
		{"two GPUs", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUCount, Count: 2} }, "", ""},
		{"GPUs by index and UUID", BuildImage, func(r *RuntimePlanConfig) {
			r.GPUs = &RuntimeGPUs{Mode: GPUDevices, DeviceIDs: []string{"0", "GPU-3a1b2c3d-4e5f-6789-abcd-ef0123456789"}}
		}, "", ""},
		{"no GPUs counted", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUCount} }, "runtime.gpus.count", "1 to 64"},
		{"too many GPUs", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUCount, Count: 65} }, "runtime.gpus.count", "1 to 64"},
		{"devices named none", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUDevices} }, "runtime.gpus.deviceIds", "by index or UUID"},
		{"a device twice", BuildImage, func(r *RuntimePlanConfig) {
			r.GPUs = &RuntimeGPUs{Mode: GPUDevices, DeviceIDs: []string{"0", "0"}}
		}, "runtime.gpus.deviceIds[1]", "named once"},
		{"a device with a space", BuildImage, func(r *RuntimePlanConfig) {
			r.GPUs = &RuntimeGPUs{Mode: GPUDevices, DeviceIDs: []string{"GPU 0"}}
		}, "runtime.gpus.deviceIds[0]", "named once"},
		{"all with a count", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUAll, Count: 1} }, "runtime.gpus", "no count"},
		{"a mode that is not one", BuildImage, func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: "half"} }, "runtime.gpus.mode", "every one"},
	}
	for _, compose := range []struct {
		name, field, key string
		mutate           func(*RuntimePlanConfig)
	}{
		{"user", "runtime.user", "user:", func(r *RuntimePlanConfig) { r.User = "1000" }},
		{"read-only root", "runtime.readOnlyRootfs", "read_only:", func(r *RuntimePlanConfig) { r.ReadOnlyRootfs = true }},
		{"tmpfs", "runtime.tmpfs", "tmpfs:", func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: "/tmp"}} }},
		{"ulimits", "runtime.ulimits", "ulimits:", func(r *RuntimePlanConfig) { r.Ulimits = nofile(1024, 1024) }},
		{"sysctls", "runtime.sysctls", "sysctls:", func(r *RuntimePlanConfig) {
			r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}}
		}},
		{"GPUs", "runtime.gpus", "deploy.resources.reservations.devices", func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{Mode: GPUAll} }},
	} {
		for _, method := range []BuildMethod{BuildCompose, BuildLegacyCompose} {
			cases = append(cases, struct {
				name   string
				method BuildMethod
				mutate func(*RuntimePlanConfig)
				field  string
				says   string
			}{string(method) + " " + compose.name, method, compose.mutate, compose.field, "own file (" + compose.key + ")"})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configuration := canonicalConfiguration(flagsPlan(tc.method, tc.mutate))
			err := configuration.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) || invalid.Field != tc.field || !strings.Contains(invalid.Message, tc.says) {
				t.Fatalf("error = %#v, want %s saying %q", err, tc.field, tc.says)
			}
		})
	}
	// Validate holds a plan nobody canonicalised to the same rule.
	raw := flagsPlan(BuildImage, func(r *RuntimePlanConfig) { r.Tmpfs = []RuntimeTmpfs{{Path: " /tmp/ "}} })
	if err := raw.Validate(); err != nil {
		t.Fatalf("an untrimmed tmpfs path = %v", err)
	}
}

// A plan saved before the flags existed, or one that clears them, keeps the
// bytes and so the digest it had; the flags are spelled one way and the
// caller's plan is not written through.
func TestCanonicalRuntimeFlagsKeepThePlansDigest(t *testing.T) {
	t.Parallel()
	before := canonicalConfiguration(limitPlan(func(*RuntimePlanConfig) {}))
	want := string(mustJSON(before.Runtime))
	for name, edit := range map[string]func(*RuntimePlanConfig){
		"none": func(*RuntimePlanConfig) {},
		"empty lists": func(r *RuntimePlanConfig) {
			r.Tmpfs, r.Ulimits, r.Sysctls = []RuntimeTmpfs{}, []RuntimeUlimit{}, []RuntimeSysctl{}
		},
		"an empty GPU": func(r *RuntimePlanConfig) { r.GPUs = &RuntimeGPUs{} },
		"a blank user": func(r *RuntimePlanConfig) { r.User = "  " },
	} {
		got := string(mustJSON(canonicalConfiguration(limitPlan(edit)).Runtime))
		if got != want {
			t.Fatalf("%s: runtime = %s, want the bytes it had: %s", name, got, want)
		}
		if strings.Contains(got, "ulimits") || strings.Contains(got, "gpus") || strings.Contains(got, "readOnlyRootfs") {
			t.Fatalf("%s: an absent flag was written: %s", name, got)
		}
	}
	plan := limitPlan(func(r *RuntimePlanConfig) {
		r.User = " 1000:1000 "
		r.Tmpfs = []RuntimeTmpfs{{Path: "/var/cache/nginx/", SizeMB: 32}, {Path: " /tmp"}}
		r.Ulimits = []RuntimeUlimit{{Name: "NPROC", Soft: 512, Hard: 1024}, {Name: UlimitNofile, Soft: 1024, Hard: 4096}}
		r.Sysctls = []RuntimeSysctl{{Name: "net.ipv4.tcp_keepalive_time", Value: " 600 "}, {Name: "net.core.somaxconn", Value: "1024"}}
		r.GPUs = &RuntimeGPUs{Mode: " Devices ", DeviceIDs: []string{" 1 ", "0"}}
	})
	canonical := canonicalConfiguration(plan).Runtime
	raw := string(mustJSON(canonical))
	for _, part := range []string{
		`"user":"1000:1000"`,
		`"tmpfs":[{"path":"/tmp"},{"path":"/var/cache/nginx","sizeMb":32}]`,
		`"ulimits":[{"name":"nofile","soft":1024,"hard":4096},{"name":"nproc","soft":512,"hard":1024}]`,
		`"sysctls":[{"name":"net.core.somaxconn","value":"1024"},{"name":"net.ipv4.tcp_keepalive_time","value":"600"}]`,
		`"gpus":{"mode":"devices","deviceIds":["1","0"]}`,
	} {
		if !strings.Contains(raw, part) {
			t.Fatalf("runtime = %s\nwant %s", raw, part)
		}
	}
	if plan.Runtime.Tmpfs[0].Path != "/var/cache/nginx/" || plan.Runtime.Ulimits[0].Name != "NPROC" ||
		plan.Runtime.Sysctls[0].Value != " 600 " || plan.Runtime.GPUs.Mode != " Devices " {
		t.Fatal("canonicalising rewrote the caller's plan")
	}
	if digestBytes(mustJSON(canonicalConfiguration(plan).Runtime)) != digestBytes(mustJSON(canonical)) {
		t.Fatal("the same flags gave two digests")
	}
}

// A read-only root with no writable path named still has /tmp; each path
// without a size gets the default.
func TestAReadOnlyRootHasTmpUnlessItNamesItsPaths(t *testing.T) {
	t.Parallel()
	if got := (RuntimePlanConfig{}).EffectiveTmpfs(); got != nil {
		t.Fatalf("a writable root = %+v", got)
	}
	if got := (RuntimePlanConfig{ReadOnlyRootfs: true}).EffectiveTmpfs(); !slices.Equal(got, []RuntimeTmpfs{{Path: "/tmp", SizeMB: DefaultTmpfsSizeMB}}) {
		t.Fatalf("a read-only root = %+v", got)
	}
	plan := RuntimePlanConfig{ReadOnlyRootfs: true, Tmpfs: []RuntimeTmpfs{{Path: "/run"}, {Path: "/var/cache/nginx", SizeMB: 8}}}
	if got := plan.EffectiveTmpfs(); !slices.Equal(got, []RuntimeTmpfs{{Path: "/run", SizeMB: DefaultTmpfsSizeMB}, {Path: "/var/cache/nginx", SizeMB: 8}}) {
		t.Fatalf("named paths = %+v", got)
	}
	mounts := runtimeMountSpecs(RuntimePlanConfig{
		ReadOnlyRootfs: true, Mounts: []RuntimeMount{{Source: "data", Target: "/data", Ownership: OwnershipManaged}},
	})
	if len(mounts) != 2 || mounts[0].Type != "volume" || mounts[1].Type != "tmpfs" || mounts[1].Target != "/tmp" || mounts[1].SizeMB != DefaultTmpfsSizeMB {
		t.Fatalf("mounts = %+v", mounts)
	}
}

// The flags reach container creation in Docker's own terms: every GPU is a
// count of -1 from the nvidia driver, the others carry what they name.
func TestRuntimeFlagsAreHandedToDockerAsTheyWereSaved(t *testing.T) {
	t.Parallel()
	plan := RuntimePlanConfig{
		Ulimits: []RuntimeUlimit{{Name: UlimitNofile, Soft: 1024, Hard: 4096}},
		Sysctls: []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}},
	}
	if got := plan.dockerUlimits(); len(got) != 1 || got[0].Name != "nofile" || got[0].Soft != 1024 || got[0].Hard != 4096 {
		t.Fatalf("ulimits = %+v", got)
	}
	if got := plan.dockerSysctls(); len(got) != 1 || got["net.core.somaxconn"] != "1024" {
		t.Fatalf("sysctls = %+v", got)
	}
	if plan.dockerGPUs() != nil || (RuntimePlanConfig{}).dockerUlimits() != nil || (RuntimePlanConfig{}).dockerSysctls() != nil {
		t.Fatal("an unset flag was handed to Docker")
	}
	for mode, want := range map[GPUMode]string{GPUAll: "nvidia -1 []", GPUCount: "nvidia 2 []", GPUDevices: "nvidia 0 [0 1]"} {
		plan.GPUs = &RuntimeGPUs{Mode: mode}
		switch mode {
		case GPUCount:
			plan.GPUs.Count = 2
		case GPUDevices:
			plan.GPUs.DeviceIDs = []string{"0", "1"}
		}
		request := plan.dockerGPUs()
		if got := fmt.Sprintf("%s %d %v", request.Driver, request.Count, request.DeviceIDs); got != want {
			t.Fatalf("%s = %s, want %s", mode, got, want)
		}
	}
}

// The release comparison lists each flag as a field of its own, empty when
// it is off, so two releases from before the flags compare unchanged.
func TestTheReleaseComparisonListsEachRuntimeFlag(t *testing.T) {
	t.Parallel()
	plain := runtimeReleaseSnapshot{Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyStopFirst}}
	flagged := plain
	flagged.Plan.User = "1000:1000"
	flagged.Plan.ReadOnlyRootfs = true
	flagged.Plan.Tmpfs = []RuntimeTmpfs{{Path: "/tmp", SizeMB: 128}}
	flagged.Plan.Ulimits = []RuntimeUlimit{{Name: UlimitNofile, Soft: 65536, Hard: 65536}, {Name: UlimitMemlock, Soft: -1, Hard: -1}}
	flagged.Plan.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}}
	flagged.Plan.GPUs = &RuntimeGPUs{Mode: GPUCount, Count: 2, Previews: true}
	fields := func(detail ReleaseDetailComparison) map[string]ReleaseFieldChange {
		out := map[string]ReleaseFieldChange{}
		for _, field := range detail.Fields {
			out[field.Field] = field
		}
		return out
	}
	unchanged := fields(compareRuntimeSnapshots(plain, plain, true, true))
	changed := fields(compareRuntimeSnapshots(plain, flagged, true, true))
	want := map[string]string{
		"user":           "1000:1000",
		"read-only root": "on · writable /tmp (128 MiB)",
		"writable paths": "",
		"ulimits":        "nofile=65536:65536, memlock=unlimited:unlimited",
		"sysctls":        "net.core.somaxconn=1024",
		"gpus":           "2 · previews too",
	}
	for name, to := range want {
		if unchanged[name].Changed || unchanged[name].From != "" {
			t.Fatalf("%s between two plain releases = %+v", name, unchanged[name])
		}
		field, ok := changed[name]
		if !ok || field.From != "" || field.To != to || field.Changed != (to != "") {
			t.Fatalf("%s = %+v, want to %q", name, field, to)
		}
	}
	writable := flagged
	writable.Plan.ReadOnlyRootfs = false
	field := fields(compareRuntimeSnapshots(flagged, writable, true, true))
	if field["read-only root"].To != "" || !field["read-only root"].Changed || field["writable paths"].To != "/tmp (128 MiB)" {
		t.Fatalf("turning the read-only root off = %+v / %+v", field["read-only root"], field["writable paths"])
	}
	every := flagged
	every.Plan.GPUs = &RuntimeGPUs{Mode: GPUAll}
	if got := fields(compareRuntimeSnapshots(flagged, every, true, true))["gpus"]; got.To != "all" || !got.Changed {
		t.Fatalf("every GPU = %+v", got)
	}
}

// Preflight says each flag back, warns when the writable paths can hold more
// than the memory limit and when Docker lists no NVIDIA runtime, counts a
// GPU as host authority, and asks the daemon about runtimes only for a plan
// that wants GPUs.
func TestPreflightStatesTheRuntimeFlags(t *testing.T) {
	t.Parallel()
	configuration := limitPlan(func(r *RuntimePlanConfig) {
		r.User = "1000"
		r.ReadOnlyRootfs = true
		r.Tmpfs = []RuntimeTmpfs{{Path: "/tmp", SizeMB: 512}}
		r.MemoryMB = 256
		r.Ulimits = []RuntimeUlimit{{Name: UlimitNofile, Soft: 4096, Hard: 4096}}
		r.Sysctls = []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}}
		r.GPUs = &RuntimeGPUs{Mode: GPUAll}
	})
	codes := func(observation HostObservation) map[string]PreflightFinding {
		out := map[string]PreflightFinding{}
		for _, item := range runtimeFlagFindings(configuration, observation) {
			out[item.Code] = item
		}
		return out
	}
	missing := codes(HostObservation{GPURuntime: &GPURuntimeObservation{Observed: true, Runtimes: []string{"io.containerd.runc.v2", "runc"}}})
	for code, severity := range map[string]PreflightSeverity{
		"runtime_user": PreflightPass, "runtime_read_only_root": PreflightPass, "runtime_tmpfs_exceeds_memory": PreflightWarning,
		"runtime_ulimits": PreflightPass, "runtime_sysctls": PreflightPass, "runtime_gpu_runtime_missing": PreflightWarning,
	} {
		item, ok := missing[code]
		if !ok || item.Severity != severity || item.FieldID == "" || !strings.HasPrefix(item.FieldID, "runtime.") {
			t.Fatalf("%s = %+v in %+v", code, item, missing)
		}
	}
	if got := missing["runtime_read_only_root"].Measured; got != "writable: /tmp (512 MiB)" {
		t.Fatalf("read-only measured = %q", got)
	}
	if got := missing["runtime_gpu_runtime_missing"].Measured; got != "runtimes: io.containerd.runc.v2, runc" {
		t.Fatalf("missing runtime measured = %q", got)
	}
	if item, ok := codes(HostObservation{GPURuntime: &GPURuntimeObservation{Observed: true, NVIDIA: true, Runtimes: []string{"nvidia", "runc"}}})["runtime_gpus"]; !ok || item.Severity != PreflightPass || item.Measured != "all" {
		t.Fatalf("an NVIDIA runtime = %+v", item)
	}
	if item, ok := codes(HostObservation{GPURuntime: &GPURuntimeObservation{Detail: "docker info could not be read"}})["runtime_gpu_runtime_unknown"]; !ok || item.Severity != PreflightWarning || item.Measured != "docker info could not be read" {
		t.Fatalf("an unread daemon = %+v", item)
	}
	if got := runtimeFlagFindings(limitPlan(func(*RuntimePlanConfig) {}), HostObservation{}); len(got) != 0 {
		t.Fatalf("a plan without flags = %+v", got)
	}

	candidate := newDetectedCandidate("", BuildImage, DetectedCandidate{
		Name: "flags", Profile: ProfileImage, Confidence: ConfidenceHigh, Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	})
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "flags", Profile: ProfileImage},
		Source: &DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "nginx:1.27"},
		Detection: &DetectionResult{
			Source: SourceIdentity{Kind: SourceImage, Repository: "nginx"}, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
	if !preflightObservationRequest(draft, configuration).NeedsGPU || preflightObservationRequest(draft, limitPlan(func(*RuntimePlanConfig) {})).NeedsGPU {
		t.Fatal("the daemon's runtimes are asked about only for a plan with GPUs")
	}
	observation := HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}
	severity := func(admin bool, code string) PreflightSeverity {
		for _, item := range preflightFindings(draft, configuration, observation, admin) {
			if item.Code == code {
				return item.Severity
			}
		}
		return ""
	}
	if severity(false, "advanced_authorization_required") != PreflightBlocked || severity(true, "advanced_runtime") != PreflightWarning {
		t.Fatal("a GPU is not host authority to preflight")
	}
}

// The daemon refuses a GPU, a limit, a kernel parameter or a user only when
// the container starts; each is named rather than left as "did not start".
func TestCandidateStartFailureNamesTheRuntimeFlagsDockerRefuses(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		`Error response from daemon: could not select device driver "nvidia" with capabilities: [[gpu]]`:                                                                                      "runtime_gpu_unavailable",
		`failed to create task for container: OCI runtime create failed: runc create failed: nvidia-container-cli: initialization error: nvml error: driver not loaded: unknown`:              "runtime_gpu_unavailable",
		`OCI runtime create failed: runc create failed: unable to start container process: error during container init: error setting rlimit type 7: operation not permitted: unknown`:        "runtime_ulimit_refused",
		`OCI runtime create failed: runc create failed: unable to start container process: error during container init: open sysctl net.core.nope file: reading from /proc/sys: no such file`: "runtime_sysctl_refused",
		`Error response from daemon: invalid argument: sysctl "vm.swappiness" is not whitelisted`:                                                                                             "runtime_sysctl_refused",
		`OCI runtime create failed: unable to start container process: error during container init: unable to find user app: no matching entries in passwd file: unknown`:                     "runtime_user_unknown",
		`Error response from daemon: something else entirely`: "candidate_start_failed",
	} {
		code, message := candidateStartFailure(errors.New(text))
		if code != want {
			t.Fatalf("%q = %s, want %s", text, code, want)
		}
		if strings.Contains(message, "nvml") || strings.Contains(message, "rlimit type") || strings.Contains(message, "swappiness") {
			t.Fatalf("%s repeated the daemon's error: %s", code, message)
		}
		if want != "candidate_start_failed" && causeTitle(code) == "" {
			t.Fatalf("%s has no title", code)
		}
	}
}

// A write the read-only root refused is named with the path the program
// tried and the directory a writable path would make good — and only on a
// plan whose root is read-only, where the words cannot be a read-only
// volume's.
func TestAWriteToTheReadOnlyRootIsNamed(t *testing.T) {
	t.Parallel()
	readOnly := runtimeCauseContext{runtime: RuntimePlanConfig{ReadOnlyRootfs: true}}
	for line, want := range map[string][2]string{
		`nginx: [emerg] mkdir() "/var/cache/nginx/client_temp" failed (30: Read-only file system)`: {"/var/cache/nginx/client_temp", "/var/cache/nginx"},
		`Error: EROFS: read-only file system, open '/app/.next/cache/fetch-cache/x'`:               {"/app/.next/cache/fetch-cache/x", "/app/.next/cache/fetch-cache"},
		`OSError: [Errno 30] Read-only file system: '/app/static/manifest.json'`:                   {"/app/static/manifest.json", "/app/static"},
		`panic: open /data.db: read-only file system`:                                              {"/data.db", ""},
		`Errno::EROFS: Read-only file system @ rb_sysopen - tmp/pids/server.pid`:                   {"tmp/pids/server.pid", ""},
		`touch: cannot touch 'x': Read-only file system`:                                           {"", ""},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{State: "exited", Lines: []RuntimeLogLine{{Text: line}}}}, readOnly)
		if cause == nil || cause.Code != "runtime_rootfs_read_only" || cause.Fix == nil || cause.Fix.Field != "runtime.tmpfs" {
			t.Fatalf("%q = %+v", line, cause)
		}
		subject := ""
		if len(cause.Subjects) > 0 {
			subject = cause.Subjects[0]
		}
		if subject != want[0] || cause.Detail != want[1] {
			t.Fatalf("%q named %q in %q, want %q in %q", line, subject, cause.Detail, want[0], want[1])
		}
		if sentence := cause.sentence(); !strings.Contains(sentence, "read-only") || (want[1] != "" && !strings.Contains(sentence, "add "+want[1])) {
			t.Fatalf("%q sentence = %q", line, sentence)
		}
	}
	writable := applicationOutputCause([]ContainerDiagnostics{{State: "exited", Lines: []RuntimeLogLine{{Text: `open /data/x: read-only file system`}}}}, runtimeCauseContext{})
	if writable != nil && writable.Code == "runtime_rootfs_read_only" {
		t.Fatalf("a writable root's read-only volume = %+v", writable)
	}
}

// A preview's plan is a copy of the environment's: it runs as the same user
// with the same read-only root, limits and kernel parameters, and gets the
// GPUs only where the environment says its previews do.
func TestAPreviewInheritsTheProcessFlagsAndTheGPUsOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	for _, previews := range []bool{false, true} {
		fixture := newReleaseStoreFixture(t)
		plan := RuntimePlanConfig{
			Strategy: StrategyBlueGreen, InternalPort: 3000, BindAddress: "127.0.0.1",
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			User: "1000:1000", ReadOnlyRootfs: true, Tmpfs: []RuntimeTmpfs{{Path: "/tmp", SizeMB: 32}},
			Ulimits: []RuntimeUlimit{{Name: UlimitNofile, Soft: 4096, Hard: 4096}},
			Sysctls: []RuntimeSysctl{{Name: "net.core.somaxconn", Value: "1024"}},
			GPUs:    &RuntimeGPUs{Mode: GPUAll, Previews: previews},
		}
		fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
		result, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at) VALUES(?, 'PR 9', 'pr-9', 'preview', 0, 'blue_green', 0, 0, ?, ?)",
			fixture.projectID, fixture.now.Unix(), fixture.now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		previewID, _ := result.LastInsertId()
		tx, err := fixture.base.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := createIsolatedPreviewPlansTx(context.Background(), tx, fixture.envID, 1, previewID, 1, fixture.now.Unix()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var raw string
		if err := fixture.base.DB.QueryRow(`SELECT config_json FROM deploy_runtime_plans WHERE environment_id=? AND revision=1`, previewID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var copied RuntimePlanConfig
		if err := json.Unmarshal([]byte(raw), &copied); err != nil {
			t.Fatal(err)
		}
		if copied.User != "1000:1000" || !copied.ReadOnlyRootfs || len(copied.Tmpfs) != 1 || len(copied.Ulimits) != 1 || len(copied.Sysctls) != 1 {
			t.Fatalf("the preview's flags = %s", raw)
		}
		if (copied.GPUs != nil) != previews {
			t.Fatalf("previews=%v: the preview's GPUs = %+v", previews, copied.GPUs)
		}
		if err := validatePreviewPlan(previewID, BuildPlanConfig{Method: BuildDockerfile}, copied); err != nil {
			t.Fatalf("previews=%v: the copy is refused: %v", previews, err)
		}
	}
	held := RuntimePlanConfig{PreviewIsolation: true, BindAddress: "127.0.0.1", GPUs: &RuntimeGPUs{Mode: GPUAll}}
	if err := validatePreviewPlan(4, BuildPlanConfig{Method: BuildDockerfile}, held); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("a preview given GPUs its environment keeps for itself = %v", err)
	}
}

// A container adopted from this server keeps the flags it ran with: its
// tmpfs mounts are writable paths rather than storage to adopt.
func TestAnImportedContainerKeepsItsProcessFlags(t *testing.T) {
	t.Parallel()
	preview := containerImportPreview(DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingContainer, ResourceID: "c"}, &dockerx.ContainerSpec{
		Name: "worker", Image: "alpine:3.20", User: "1000", ReadOnly: true,
		Mounts:  []dockerx.MountSpec{{Type: "volume", Source: "data", Target: "/data"}, {Type: "tmpfs", Target: "/tmp", SizeMB: 32}},
		Ulimits: []dockerx.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}},
		Sysctls: map[string]string{"net.ipv4.ip_forward": "1", "net.core.somaxconn": "512"},
		GPUs:    &dockerx.GPURequest{Driver: "nvidia", Count: -1},
	})
	runtime := preview.Configuration.Runtime
	if runtime.User != "1000" || !runtime.ReadOnlyRootfs || len(runtime.Mounts) != 1 ||
		fmt.Sprint(runtime.Tmpfs) != "[{/tmp 32}]" || fmt.Sprint(runtime.Ulimits) != "[{nofile 1024 2048}]" ||
		fmt.Sprint(runtime.Sysctls) != "[{net.core.somaxconn 512} {net.ipv4.ip_forward 1}]" ||
		runtime.GPUs == nil || runtime.GPUs.Mode != GPUAll {
		t.Fatalf("imported runtime = %+v", runtime)
	}
	plain := containerImportPreview(DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingContainer, ResourceID: "c"}, &dockerx.ContainerSpec{Name: "web", Image: "nginx:1.27"})
	if plain.Configuration.Runtime.hasProcessFlags() {
		t.Fatalf("a container without flags = %+v", plain.Configuration.Runtime)
	}
}
