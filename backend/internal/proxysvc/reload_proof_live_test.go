package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The host's nginx binary on a private prefix: a save it loads is proven from
// its master's own processes and sockets, and a save whose port another
// program holds — which `nginx -t` passes, since its test ignores
// EADDRINUSE — is refused by the master at the reload, put back, and the
// site that was serving still is.
func TestLiveReloadIsProvenFromTheMaster(t *testing.T) {
	l := newLiveSites(t, "app", "other")
	withListeners(t, ListListeners)
	previous := readNginx
	readNginx = func(ctx context.Context, s *Service, files []ConfigFile, master int32) *socketView {
		return s.readNginxSockets(ctx, files, master)
	}
	t.Cleanup(func() { readNginx = previous })
	ctx := context.Background()
	installSite(t, l.root, l.spec("app", "app", "app.test", ""), l.port)
	startNginx(t, l.root)
	l.answers("app.test", "app")

	res, _, err := l.save(l.spec("app", "other", "app.test", ""), SiteSave{Enable: true, Reload: true, Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	proof := res.LoadProof
	if !res.Reloaded || proof == nil || proof.State != LoadLoaded || proof.Workers == 0 || proof.LoadedAt == nil {
		t.Fatalf("result = %+v proof = %+v", res, proof)
	}
	if proof.Listening == nil || !*proof.Listening {
		t.Fatalf("nginx was not seen holding the site's socket: %+v", proof)
	}
	l.answers("app.test", "other")

	// A program holds the port the next save asks for.
	held, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	spec := l.spec("busy", "app", "busy.test", "")
	content := strings.Replace(l.render(spec), fmt.Sprintf("listen 127.0.0.1:%d;", l.port), fmt.Sprintf("listen 127.0.0.1:%d;", port), 1)
	l.service.mu.Lock()
	_, err = l.service.saveSiteLocked(ctx, spec, content, SiteSave{Enable: true, Reload: true})
	l.service.mu.Unlock()
	var refused *SiteLoadRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v, want the reload refused and the site put back", err)
	}
	if !strings.Contains(refused.BindError, fmt.Sprintf("bind() to 127.0.0.1:%d failed (98: Address", port)) || refused.PID != int32(os.Getpid()) {
		t.Fatalf("refusal = %+v, want this test named as the holder", refused)
	}
	if _, err := os.Lstat(filepath.Join(l.root, "sites-enabled", "busy")); !os.IsNotExist(err) {
		t.Fatalf("the refused site is still linked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.root, "sites-available", "busy")); !os.IsNotExist(err) {
		t.Fatalf("the refused site's file is still there: %v", err)
	}
	l.answers("app.test", "other")

	// With the refused file gone, the next reload loads again.
	reload, err := l.service.Reload(ctx, KindNginx)
	if err != nil {
		t.Fatal(err)
	}
	if reload.LoadProof == nil || reload.LoadProof.State != LoadLoaded {
		t.Fatalf("proof = %+v", reload.LoadProof)
	}
}
