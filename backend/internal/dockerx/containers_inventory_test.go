package dockerx

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dtypes "github.com/docker/docker/api/types"
)

func inventoryTestClient(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	var inspections atomic.Int32
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.47")
		switch {
		case path == "/containers/json":
			running := `{"Id":"running","Names":["/web"],"Image":"example/app:1","ImageID":"sha256:abc","State":"running","Labels":{"com.docker.compose.project":"site"},"Mounts":[{"Type":"volume","Name":"data","Destination":"/data"}],"NetworkSettings":{"Networks":{"private":{}}}}`
			stopped := `{"Id":"stopped","Names":["/old"],"Image":"example/app:1","ImageID":"sha256:abc","State":"exited","Mounts":[{"Type":"volume","Name":"data","Destination":"/data"}],"NetworkSettings":{"Networks":{"private":{}}}}`
			if r.URL.Query().Get("all") == "1" {
				fmt.Fprintf(w, "[%s,%s]", running, stopped)
			} else {
				fmt.Fprintf(w, "[%s]", running)
			}
		case path == "/networks":
			fmt.Fprint(w, `[{"Id":"network","Name":"private","IPAM":{}}]`)
		case path == "/containers/running/stats":
			fmt.Fprint(w, `{}`)
		case path == "/containers/running/json":
			inspections.Add(1)
			fmt.Fprint(w, `{"Id":"running","State":{"Running":true,"StartedAt":"2026-09-01T00:00:00Z","Health":{"Status":"healthy"}},"Config":{"Image":"example/app:1","Healthcheck":{"Test":["CMD","true"]}},"HostConfig":{"Memory":1048576,"NanoCpus":1000000000,"RestartPolicy":{"Name":"always"}}}`)
		case path == "/containers/stopped/json":
			inspections.Add(1)
			fmt.Fprint(w, `{"Id":"stopped","State":{"Running":false,"ExitCode":0},"Config":{},"HostConfig":{}}`)
		default:
			t.Errorf("unexpected request: %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c.duVal, c.duAt = &dtypes.DiskUsage{}, time.Now()
	c.hostMemory, c.hostCPUs = 1<<30, 2
	return c, &inspections
}

func TestContainerMembershipAndSamplesSkipUnusedInspections(t *testing.T) {
	for _, kind := range []string{"volumes", "images", "networks", "image refs", "stats"} {
		t.Run(kind, func(t *testing.T) {
			c, inspections := inventoryTestClient(t)
			ctx := cacheTestContext(t)
			switch kind {
			case "volumes":
				users, err := c.volumeUsers(ctx)
				if err != nil || len(users["data"]) != 2 {
					t.Fatalf("volume users lost a stopped container: %+v, %v", users, err)
				}
			case "images":
				if users := c.imageUsers(ctx, "sha256:abc"); len(users) != 2 {
					t.Fatalf("image users lost a stopped container: %+v", users)
				}
			case "networks":
				networks, err := c.ListNetworks(ctx)
				if err != nil || len(networks) != 1 || networks[0].Containers != 2 {
					t.Fatalf("network membership = %+v, %v", networks, err)
				}
			case "image refs":
				if refs := c.containerImageRefs(ctx); len(refs) != 1 || refs[0] != "example/app:1" {
					t.Fatalf("image references = %+v", refs)
				}
			case "stats":
				stats, err := c.NewStatsSampler().SampleAll(ctx)
				if err != nil || len(stats) != 1 || stats[0].ID != "running" {
					t.Fatalf("stats = %+v, %v", stats, err)
				}
			}
			if got := inspections.Load(); got != 0 {
				t.Fatalf("membership read performed %d unused inspections", got)
			}
		})
	}
}

func TestContainerListingRetainsHealthUptimeAndLimits(t *testing.T) {
	c, inspections := inventoryTestClient(t)
	list, err := c.ListContainers(cacheTestContext(t), true)
	if err != nil || len(list) != 2 {
		t.Fatalf("listing = %+v, %v", list, err)
	}
	running := list[0]
	if running.ID != "running" || !running.Inspected || running.Health != "healthy" ||
		running.StartedAt == nil || running.MemoryLimit != 1048576 || running.CPULimit != 1 ||
		!running.HasHealthchk || running.RestartPolicy != "always" || inspections.Load() != 1 || list[1].Inspected {
		t.Fatalf("enriched listing changed: %+v, inspections=%d", list, inspections.Load())
	}
}

func TestDiagnoseInspectsEachContainerOnce(t *testing.T) {
	c, inspections := inventoryTestClient(t)
	diagnosis, err := c.Diagnose(cacheTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if inspections.Load() != 2 || diagnosis.Checked != 2 || diagnosis.Runtime.Running != 1 ||
		diagnosis.Runtime.Healthy != 1 || diagnosis.Runtime.Exited != 1 {
		t.Fatalf("diagnosis changed or repeated inspections: %+v, inspections=%d", diagnosis, inspections.Load())
	}
}
