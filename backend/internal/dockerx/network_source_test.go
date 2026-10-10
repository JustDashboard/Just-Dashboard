package dockerx

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestContainerNativeResolversRetainEmbeddedDNSOnlyFromConfig(t *testing.T) {
	config := "search corp.private\noptions ndots:0\nnameserver 127.0.0.11\nnameserver bad\nnameserver 224.0.0.1\nnameserver 0.0.0.0\nnameserver 127.0.0.11\nnameserver 2001:db8::53 # native\n"
	if got := configuredNameservers(config); !reflect.DeepEqual(got, []string{"127.0.0.11", "2001:db8::53"}) {
		t.Fatalf("invented/leaked resolver: %v", got)
	}
	if got := configuredNameservers("search private.corp\n"); len(got) != 0 {
		t.Fatalf("missing native DNS fell back: %v", got)
	}
}

func TestContainerNamespaceIdentityBypassesReadSnapshot(t *testing.T) {
	id := strings.Repeat("a", 64)
	reads := 0
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		reads++
		fmt.Fprintf(w, `{"Id":%q,"State":{"Running":true,"Pid":%d,"StartedAt":"now"}}`, id, 100+reads)
	})
	ctx := c.WithReadSnapshot(context.Background())
	initial, err := c.inspectContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	source := &NetworkSource{ID: id, PID: initial.State.Pid, StartedAt: initial.State.StartedAt}
	if err := c.checkNetworkSource(ctx, source); err == nil || !strings.Contains(err.Error(), "restarted") || reads != 2 {
		t.Fatalf("stale inventory accepted for namespace entry: %v reads=%d", err, reads)
	}
}

func TestContainerAttributionRequiresExactCgroupIdentity(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, raw := range []string{"0::/system.slice/docker-" + id + ".scope\n", "1:name=systemd:/docker/" + id + "\n"} {
		if !containerCgroup(raw, id) {
			t.Fatalf("legitimate owner unreadable %s", raw)
		}
	}
	for _, raw := range []string{"0::/docker/" + id + "bad", "0::/system.slice/docker-" + id[:12] + ".scope", "0::/unrelated/"} {
		if containerCgroup(raw, id) {
			t.Fatalf("unrelated process attributed %s", raw)
		}
	}
}

func TestContainerStartIdentityHandlesSpacesAndRejectsPartialStat(t *testing.T) {
	fields := append([]string{"S"}, strings.Fields(strings.Repeat("0 ", 18))...)
	fields = append(fields, "123456")
	if got, err := processStartTicks("123 (name with ) parentheses) " + strings.Join(fields, " ")); err != nil || got != "123456" {
		t.Fatalf("bad process identity %s %v", got, err)
	}
	for _, raw := range []string{"bad", "123 (name) S 0", "123 (name) " + strings.Join(fields[:19], " ") + " invalid"} {
		if _, err := processStartTicks(raw); err == nil {
			t.Fatalf("partial/invalid identity accepted %s", raw)
		}
	}
}
