package proxysvc

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// A canary on a real nginx: the route sends its share of the requests to
// the candidate and the rest to the live release, the access log says which
// release answered each one, and moving between the plain route and the
// canary route under traffic loses no request — the canary's start and its
// promotion or abort are each one of those moves.
func TestLiveCanarySplitsTheRouteOnNginx(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to split a real nginx route")
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	network := liveCutoverNetwork(t, stamp)
	root := liveCutoverRoot(t)
	binary := buildCutoverRelease(t)
	blue := startCutoverReplicas(t, network, binary, "blue", stamp, 2)
	green := startCutoverReplicas(t, network, binary, "green", stamp, 1)
	proxy := liveCutoverNginx(t, root, network, stamp)
	endpoint := proxy.endpoint
	service := New(root, "")
	upstreams := func(release []cutoverRelease) []string {
		out := []string{}
		for _, replica := range release {
			out = append(out, "http://"+net.JoinHostPort(replica.name, "8080"))
		}
		return out
	}
	plain := DeploymentRoute{Name: cutoverRouteName, Domains: []string{cutoverDomain}, Upstreams: upstreams(blue), AccessLog: true}
	canary := plain
	canary.Canary = &DeploymentCanary{Percent: 30, Upstreams: upstreams(green), Live: "41", Candidate: "42"}

	ctx := t.Context()
	if result, err := service.ApplyDeploymentRoute(ctx, canary); err != nil || !result.Applied || !result.Verified {
		t.Fatalf("canary route = %+v, %v", result, err)
	}
	t.Cleanup(func() { _ = service.RemoveDeploymentRoute(context.WithoutCancel(ctx), cutoverRouteName) })
	if err := service.VerifyDeploymentRoute(ctx, canary); err != nil {
		t.Fatalf("verify: %v", err)
	}
	waitCutoverRelease(t, endpoint, "blue", 15*time.Second)

	answered := map[string]int{}
	const asked = 600
	for index := 0; index < asked; index++ {
		body, status, err := cutoverGet(endpoint, cutoverDomain, "/", 5*time.Second)
		if err != nil || status != http.StatusOK {
			t.Fatalf("request %d = %d %v", index, status, err)
		}
		answered[body]++
	}
	share := float64(answered["green"]) / asked
	t.Logf("the candidate answered %d of %d requests (%.1f%%)", answered["green"], asked, share*100)
	if share < 0.22 || share > 0.38 || answered["blue"]+answered["green"] != asked {
		t.Fatalf("a 30%% canary answered %v", answered)
	}

	// The record names each request's release, and the counts are the
	// answers': the side-by-side reading stands on this.
	raw, err := exec.Command("docker", "exec", proxy.name, "cat", nginxAccessLogPath(cutoverRouteName)).Output()
	if err != nil {
		t.Fatalf("read the access log: %v", err)
	}
	recorded := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		if entry, ok := accesslog.Parse(line); ok && entry.Host == cutoverDomain {
			recorded[entry.Release]++
		}
	}
	if recorded["42"] < answered["green"] || recorded["41"] < answered["blue"] || recorded[""] != 0 {
		t.Fatalf("the access log recorded %v for answers %v", recorded, answered)
	}

	// Into the canary and out of it again, under traffic: nothing is lost.
	traffic, stopTraffic := startCutoverTraffic(t, endpoint)
	for index := 0; index < 20; index++ {
		next := plain
		if index%2 == 0 {
			next = canary
		}
		if result, err := service.ApplyDeploymentRoute(ctx, next); err != nil || !result.Verified {
			t.Fatalf("move %d = %+v, %v", index, result, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	stopTraffic()
	traffic.fast.report(t, "fast HTTP")
	traffic.long.report(t, "long request")
	traffic.socket.report(t, "new WebSocket")
}
