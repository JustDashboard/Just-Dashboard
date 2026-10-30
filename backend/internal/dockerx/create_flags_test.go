package dockerx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

// A deployment's process flags reach the Engine as --ulimit, --sysctl,
// --gpus, --read-only, --tmpfs and --user would put them, and a container
// read back keeps them, so recreating it from its own spec does too.
func TestProcessFlagsReachTheEngineAndAreReadBack(t *testing.T) {
	spec := ContainerSpec{
		Image: "alpine:3.20", User: "1000:1000", ReadOnly: true,
		Mounts:  []MountSpec{{Type: "tmpfs", Target: "/tmp", SizeMB: 64}},
		Ulimits: []Ulimit{{Name: "nofile", Soft: 1024, Hard: 4096}, {Name: "memlock", Soft: -1, Hard: -1}},
		Sysctls: map[string]string{"net.core.somaxconn": "1024"},
		GPUs:    &GPURequest{Driver: "nvidia", Count: -1},
	}
	cfg, host, _, _, err := spec.toEngine()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "1000:1000" || !host.ReadonlyRootfs || len(host.Mounts) != 1 || host.Mounts[0].TmpfsOptions == nil ||
		host.Mounts[0].TmpfsOptions.SizeBytes != 64<<20 {
		t.Fatalf("user %q, read-only %v, mounts %+v", cfg.User, host.ReadonlyRootfs, host.Mounts)
	}
	if len(host.Ulimits) != 2 || host.Ulimits[0].Name != "nofile" || host.Ulimits[0].Soft != 1024 || host.Ulimits[0].Hard != 4096 ||
		host.Ulimits[1].Soft != -1 {
		t.Fatalf("ulimits = %+v", host.Ulimits)
	}
	if host.Sysctls["net.core.somaxconn"] != "1024" {
		t.Fatalf("sysctls = %+v", host.Sysctls)
	}
	spec.Sysctls["net.core.somaxconn"] = "1"
	if host.Sysctls["net.core.somaxconn"] != "1024" {
		t.Fatal("the Engine's sysctls are the spec's own map")
	}
	if len(host.DeviceRequests) != 1 || host.DeviceRequests[0].Driver != "nvidia" || host.DeviceRequests[0].Count != -1 ||
		fmt.Sprint(host.DeviceRequests[0].Capabilities) != "[[gpu]]" {
		t.Fatalf("device requests = %+v", host.DeviceRequests)
	}
	plain, plainHost, _, _, err := (ContainerSpec{Image: "alpine:3.20"}).toEngine()
	if err != nil {
		t.Fatal(err)
	}
	if plain.User != "" || plainHost.ReadonlyRootfs || plainHost.Ulimits != nil || plainHost.Sysctls != nil || plainHost.DeviceRequests != nil {
		t.Fatalf("a spec without flags = %+v", plainHost)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/containers/flagged/json") {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"not found"}`)
			return
		}
		fmt.Fprint(w, `{"Id":"flagged","Name":"/flagged","Config":{"Image":"alpine:3.20","User":"1000:1000"},
			"HostConfig":{"ReadonlyRootfs":true,"Ulimits":[{"Name":"nofile","Soft":1024,"Hard":4096}],
			"Sysctls":{"net.core.somaxconn":"1024"},
			"DeviceRequests":[{"Driver":"","Count":0,"DeviceIDs":["0","1"],"Capabilities":[["compute"]]},{"Driver":"nvidia","Count":0,"DeviceIDs":["0","1"],"Capabilities":[["gpu"]]}],
			"Mounts":[{"Type":"tmpfs","Target":"/tmp","TmpfsOptions":{"SizeBytes":67108864}}]}}`)
	}))
	defer server.Close()
	api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	read, err := (&Client{cli: api}).SpecOf(context.Background(), "flagged")
	if err != nil {
		t.Fatal(err)
	}
	if read.User != "1000:1000" || !read.ReadOnly || len(read.Mounts) != 1 || read.Mounts[0].Type != "tmpfs" || read.Mounts[0].SizeMB != 64 {
		t.Fatalf("read back = %+v", read)
	}
	if len(read.Ulimits) != 1 || read.Ulimits[0] != (Ulimit{Name: "nofile", Soft: 1024, Hard: 4096}) || read.Sysctls["net.core.somaxconn"] != "1024" {
		t.Fatalf("limits read back = %+v %+v", read.Ulimits, read.Sysctls)
	}
	if read.GPUs == nil || read.GPUs.Driver != "nvidia" || !slices.Equal(read.GPUs.DeviceIDs, []string{"0", "1"}) {
		t.Fatalf("GPUs read back = %+v", read.GPUs)
	}
}

// Docker mounts a tmpfs noexec; a helper that runs a tool it downloads into
// one asks for exec, and nothing else gets it.
func TestTmpfsIsExecutableOnlyWhenAsked(t *testing.T) {
	plain, _, err := (MountSpec{Type: "tmpfs", Target: "/tmp", SizeMB: 64}).toEngine()
	if err != nil {
		t.Fatal(err)
	}
	if plain.TmpfsOptions == nil || len(plain.TmpfsOptions.Options) != 0 {
		t.Fatalf("plain tmpfs = %#v", plain.TmpfsOptions)
	}
	executable, _, err := (MountSpec{Type: "tmpfs", Target: "/tmp", Exec: true}).toEngine()
	if err != nil {
		t.Fatal(err)
	}
	if executable.TmpfsOptions == nil || len(executable.TmpfsOptions.Options) != 1 || executable.TmpfsOptions.Options[0][0] != "exec" {
		t.Fatalf("executable tmpfs = %#v", executable.TmpfsOptions)
	}
}
