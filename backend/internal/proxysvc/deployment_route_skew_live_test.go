package proxysvc

import (
	"context"
	"io"
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

// Skew protection on a real nginx: release 41's route sets the jd_release
// cookie on a visitor's first answer; the cutover to release 42, keeping 41
// for the visitors it answered, loses no request under traffic; a visitor
// with 41's cookie is answered by 41 and the access log says so, while a
// fresh visitor and one whose cookie names nothing served get 42 and 42's
// cookie; and once 41 is taken off the route its visitors are moved to 42.
func TestLiveSkewProtectionPinsAVisitorOnNginx(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to pin visitors on a real nginx")
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
	before := DeploymentRoute{Name: cutoverRouteName, Domains: []string{cutoverDomain}, Upstreams: upstreams(blue), AccessLog: true,
		Skew: &DeploymentSkew{Release: "41", MaxAge: 600}}
	pinned := DeploymentRoute{Name: cutoverRouteName, Domains: []string{cutoverDomain}, Upstreams: upstreams(green), AccessLog: true,
		Skew: &DeploymentSkew{Release: "42", MaxAge: 600, Pinned: []DeploymentPinned{{Release: "41", Upstreams: upstreams(blue)}}}}
	after := pinned
	after.Skew = &DeploymentSkew{Release: "42", MaxAge: 600}

	ctx := t.Context()
	if result, err := service.ApplyDeploymentRoute(ctx, before); err != nil || !result.Verified {
		t.Fatalf("release 41's route = %+v, %v", result, err)
	}
	t.Cleanup(func() { _ = service.RemoveDeploymentRoute(context.WithoutCancel(ctx), cutoverRouteName) })
	waitCutoverRelease(t, endpoint, "blue", 15*time.Second)
	first := skewCutoverGet(t, endpoint, "/", "")
	if first.body != "blue" || first.cookie != "jd_release=41; Path=/; Max-Age=600; HttpOnly; SameSite=Lax" {
		t.Fatalf("release 41's first answer = %+v", first)
	}
	if again := skewCutoverGet(t, endpoint, "/", "41"); again.cookie != "" {
		t.Fatalf("a visitor carrying the cookie was sent it again: %+v", again)
	}

	// The cutover keeping 41 for its visitors, under traffic.
	traffic, stopTraffic := startCutoverTraffic(t, endpoint)
	time.Sleep(300 * time.Millisecond)
	if result, err := service.ApplyDeploymentRoute(ctx, pinned); err != nil || !result.Verified {
		t.Fatalf("the cutover = %+v, %v", result, err)
	}
	if err := service.VerifyDeploymentRoute(ctx, pinned); err != nil {
		t.Fatalf("verify: %v", err)
	}
	waitCutoverRelease(t, endpoint, "green", 15*time.Second)
	time.Sleep(300 * time.Millisecond)
	stopTraffic()
	traffic.fast.report(t, "fast HTTP")
	traffic.long.report(t, "long request")
	traffic.socket.report(t, "new WebSocket")

	const kept = 40
	for index := 0; index < kept; index++ {
		if got := skewCutoverGet(t, endpoint, "/", "41"); got.body != "blue" || got.cookie != "" {
			t.Fatalf("request %d of release 41's visitor = %+v", index, got)
		}
	}
	if fresh := skewCutoverGet(t, endpoint, "/", ""); fresh.body != "green" || !strings.HasPrefix(fresh.cookie, "jd_release=42;") {
		t.Fatalf("a fresh visitor = %+v", fresh)
	}
	if stale := skewCutoverGet(t, endpoint, "/", "7"); stale.body != "green" || !strings.HasPrefix(stale.cookie, "jd_release=42;") {
		t.Fatalf("a visitor whose cookie names nothing served = %+v", stale)
	}
	raw, err := exec.Command("docker", "exec", proxy.name, "cat", nginxAccessLogPath(cutoverRouteName)).Output()
	if err != nil {
		t.Fatalf("read the access log: %v", err)
	}
	recorded := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		if entry, ok := accesslog.Parse(line); ok && entry.Host == cutoverDomain && entry.Path == "/" {
			recorded[entry.Release]++
		}
	}
	if recorded["41"] < kept || recorded["42"] < 2 {
		t.Fatalf("the access log recorded %v", recorded)
	}
	t.Logf("recorded by release: %v", recorded)

	// Release 41 taken off the route: its visitor is moved to 42 and told so.
	if result, err := service.ApplyDeploymentRoute(ctx, after); err != nil || !result.Verified {
		t.Fatalf("the route without 41 = %+v, %v", result, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		moved := skewCutoverGet(t, endpoint, "/", "41")
		if moved.body == "green" && strings.HasPrefix(moved.cookie, "jd_release=42;") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("release 41's visitor after it left the route = %+v", moved)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type skewCutoverAnswer struct {
	status int
	body   string
	cookie string
}

func skewCutoverGet(t *testing.T, endpoint, path, release string) skewCutoverAnswer {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, endpoint+path, nil)
	request.Host = cutoverDomain
	if release != "" {
		request.Header.Set("Cookie", "session=x; jd_release="+release)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return skewCutoverAnswer{status: response.StatusCode, body: strings.TrimSpace(string(body)), cookie: response.Header.Get("Set-Cookie")}
}
