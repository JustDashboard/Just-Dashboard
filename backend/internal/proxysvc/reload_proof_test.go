package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// provenSites is a Debian layout behind an nginx shim whose test passes, whose
// -V is nginx.org's build and whose reload runs $root/on-reload. nginx logs
// to $root/error.log, which nginx.conf names.
func provenSites(t *testing.T) (*Service, string) {
	t.Helper()
	root, _ := nginxLayout(t, map[string]string{
		"nginx.conf": "error_log $ROOT/error.log;\nevents {}\nhttp {\n    include $ROOT/sites-enabled/*;\n}\n",
	})
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	version := filepath.Join(root, "version")
	if err := os.WriteFile(version, []byte(alpineNginxV), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
case "$1" in
-V) cat '%[1]s' >&2 ;;
-t) printf '%%s\n' 'nginx: the configuration file %[2]s/nginx.conf syntax is ok' 'nginx: configuration file %[2]s/nginx.conf test is successful' ;;
-s) if [ -x '%[2]s/on-reload' ]; then '%[2]s/on-reload'; fi ;;
esac
exit 0
`, version, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

// loadingNginx is a running nginx with workers 10 and 11 that, once
// $root/reloaded exists, runs the workers after and holds the sockets with
// the inodes held.
func loadingNginx(t *testing.T, root string, after []int32, sockets []socket, held ...uint64) {
	t.Helper()
	withNginx(t, func(int32) *socketView {
		if _, err := os.Stat(filepath.Join(root, "reloaded")); err != nil {
			return &socketView{nginx: &streamNginxProcess{master: 7, workers: []int32{10, 11}}, held: map[uint64]bool{}, local: true}
		}
		inodes := map[uint64]bool{}
		for _, inode := range held {
			inodes[inode] = true
		}
		loaded := time.Now()
		return &socketView{nginx: &streamNginxProcess{master: 7, workers: after, loaded: loaded}, sockets: sockets, held: inodes, local: true}
	})
}

func shortLoadWait(t *testing.T) {
	t.Helper()
	previous := loadWait
	loadWait = 300 * time.Millisecond
	t.Cleanup(func() { loadWait = previous })
}

// A save is live only when the master replaced every worker after the
// signal, the file still held what the save wrote, and nginx holds the
// site's sockets.
func TestSaveSiteProvesNginxLoadedIt(t *testing.T) {
	svc, root := provenSites(t)
	onReload(t, root, "touch '"+root+"/reloaded'")
	loadingNginx(t, root, []int32{12, 13}, listens(80), 5, 6)
	res, err := svc.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{Enable: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	proof := res.LoadProof
	if !res.Reloaded || proof == nil || proof.State != LoadLoaded || proof.Master != 7 || proof.Workers != 2 || proof.LoadedAt == nil {
		t.Fatalf("result = %+v proof = %+v", res, proof)
	}
	if proof.Listening == nil || !*proof.Listening || strings.Join(proof.Listens, ",") != "port 80/tcp" {
		t.Fatalf("listens = %v listening = %v", proof.Listens, proof.Listening)
	}
	if len(proof.Files) != 2 {
		t.Fatalf("files = %+v, want the site's file and its link", proof.Files)
	}
	for _, f := range proof.Files {
		if !f.Held || f.Digest != ContentDigest(res.Content) {
			t.Fatalf("file %+v does not hold what was saved", f)
		}
	}
}

// A reload nginx refused over the site's own socket — a program took the
// port, which `nginx -t` passes — puts the site back as it was: left in
// place, it would fail every later reload on the host.
func TestSaveSitePutsBackASiteNginxCouldNotBind(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit bool
	}{{name: "new"}, {name: "edit", edit: true}} {
		t.Run(tc.name, func(t *testing.T) {
			withListeners(t, func(context.Context) ([]Listener, error) {
				return []Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 8443, PID: 900, Process: "java"}}, nil
			})
			svc, root := provenSites(t)
			ctx := context.Background()
			available := filepath.Join(root, "sites-available", "app")
			before := ""
			if tc.edit {
				if _, err := svc.SaveSite(ctx, plainSpec("app", "app.example.com"), SiteSave{Enable: true}); err != nil {
					t.Fatal(err)
				}
				before = mustRead(t, available)
			}
			onReload(t, root, fmt.Sprintf(`touch '%[1]s/reloaded'
echo '2026/10/09 10:00:00 [emerg] 1#1: bind() to 0.0.0.0:8443 failed (98: Address already in use)' >> '%[1]s/error.log'
echo '2026/10/09 10:00:02 [emerg] 1#1: still could not bind()' >> '%[1]s/error.log'`, root))
			loadingNginx(t, root, []int32{10, 11}, nil)

			spec := plainSpec("app", "app.example.com")
			spec.Custom = "listen 8443;"
			_, err := svc.SaveSite(ctx, spec, SiteSave{Enable: true, Reload: true, Overwrite: tc.edit})
			var refused *SiteLoadRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("got %v, want the save put back", err)
			}
			if refused.BindError != "bind() to 0.0.0.0:8443 failed (98: Address already in use)" || refused.Holder != "java" || refused.PID != 900 {
				t.Fatalf("refusal = %+v", refused)
			}
			if msg := refused.Error(); !strings.Contains(msg, "put back as it was") || !strings.Contains(msg, "held by java (pid 900)") {
				t.Fatalf("message = %s", msg)
			}
			if tc.edit {
				if got := mustRead(t, available); got != before {
					t.Fatalf("the previous file was not put back:\n%s", got)
				}
				if !isLinked(t, root, "app") {
					t.Fatal("the edited site lost its link")
				}
				return
			}
			if _, err := os.Stat(available); !os.IsNotExist(err) {
				t.Fatalf("the new site's file is still there: %v", err)
			}
			if isLinked(t, root, "app") {
				t.Fatal("the new site's link is still there")
			}
		})
	}
}

// The master tries a socket it cannot bind five times before it gives the
// reload up, and a port freed between attempts is bound on the next: the
// reload then loads after all. Until nginx says it gave up, a bind() failure
// is not a refusal, and a site is never put back under a load that happened —
// nor over one nobody saw end either way.
func TestSaveSiteKeepsASiteNginxBoundOnALaterAttempt(t *testing.T) {
	previous := bindAttemptsWait
	bindAttemptsWait = 300 * time.Millisecond
	t.Cleanup(func() { bindAttemptsWait = previous })
	for _, tc := range []struct {
		name  string
		after []int32
		state string
	}{
		{name: "bound", after: []int32{12, 13}, state: LoadLoaded},
		{name: "not seen", after: []int32{10, 11}, state: LoadUnconfirmed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortLoadWait(t)
			svc, root := provenSites(t)
			onReload(t, root, fmt.Sprintf(`touch '%[1]s/reloaded'
echo '2026/10/09 10:00:00 [emerg] 1#1: bind() to 0.0.0.0:8443 failed (98: Address already in use)' >> '%[1]s/error.log'`, root))
			loadingNginx(t, root, tc.after, append(listens(80), listens(8443)...), 5, 6)
			spec := plainSpec("app", "app.example.com")
			spec.Custom = "listen 8443;"
			res, err := svc.SaveSite(context.Background(), spec, SiteSave{Enable: true, Reload: true})
			if err != nil {
				t.Fatalf("a bind() failure nginx did not give up on was a refusal: %v", err)
			}
			proof := res.LoadProof
			if !res.Reloaded || proof == nil || proof.State != tc.state {
				t.Fatalf("result = %+v proof = %+v", res, proof)
			}
			if tc.state == LoadUnconfirmed && !strings.Contains(proof.Note, "could not bind a socket at first (bind() to 0.0.0.0:8443 failed") {
				t.Fatalf("note = %q", proof.Note)
			}
			if !isLinked(t, root, "app") {
				t.Fatal("the site was put back")
			}
		})
	}
}

// A reload refused for something else — another site's port — is a saved
// site that nginx did not load, with nginx's words; the file stays, as a
// failed reload's always has.
func TestSaveSiteReportsAReloadNginxRefusedForSomethingElse(t *testing.T) {
	svc, root := provenSites(t)
	onReload(t, root, fmt.Sprintf(`touch '%[1]s/reloaded'
echo '2026/10/09 10:00:00 [emerg] 1#1: bind() to 0.0.0.0:9999 failed (98: Address already in use)' >> '%[1]s/error.log'
echo '2026/10/09 10:00:02 [emerg] 1#1: still could not bind()' >> '%[1]s/error.log'`, root))
	loadingNginx(t, root, []int32{10, 11}, nil)
	res, err := svc.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{Enable: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || res.ReloadError != "nginx did not take the reload up: bind() to 0.0.0.0:9999 failed (98: Address already in use)" {
		t.Fatalf("result = %+v", res)
	}
	if res.LoadProof == nil || res.LoadProof.State != LoadRefused {
		t.Fatalf("proof = %+v", res.LoadProof)
	}
	if !isLinked(t, root, "app") {
		t.Fatal("a valid site was removed for another port")
	}
}

// A master that never replaces its workers, or replaces one beside the old
// ones (a crashed worker), or loads a file somebody wrote again, is not
// called loaded.
func TestSaveSiteDoesNotCallAnUnprovenReloadLoaded(t *testing.T) {
	shortLoadWait(t)
	for _, tc := range []struct {
		name    string
		after   []int32
		rewrite bool
		sockets []socket
		note    string
	}{
		{name: "no new workers", after: []int32{10, 11}, sockets: listens(80), note: "had not taken the reload up"},
		{name: "one replaced worker", after: []int32{10, 12}, sockets: listens(80), note: "crashed and was replaced"},
		{name: "file written again", after: []int32{12, 13}, rewrite: true, sockets: listens(80), note: "written again"},
		{name: "no socket", after: []int32{12, 13}, note: "holds no socket for port 80/tcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := provenSites(t)
			script := "touch '" + root + "/reloaded'"
			if tc.rewrite {
				script += "\necho '# changed' >> '" + filepath.Join(root, "sites-available", "app") + "'"
			}
			onReload(t, root, script)
			loadingNginx(t, root, tc.after, tc.sockets, 5, 6)
			res, err := svc.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{Enable: true, Reload: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Reloaded || res.LoadProof == nil || res.LoadProof.State != LoadUnconfirmed || !strings.Contains(res.LoadProof.Note, tc.note) {
				t.Fatalf("result = %+v proof = %+v", res, res.LoadProof)
			}
		})
	}
}

// A host whose running nginx cannot be read says so rather than claiming
// either outcome.
func TestSaveSiteSaysWhenItCouldNotProveTheLoad(t *testing.T) {
	svc, _ := provenSites(t)
	withNginx(t, func(int32) *socketView { return listenerView("no running nginx reads nginx.conf") })
	res, err := svc.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{Enable: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reloaded || res.LoadProof == nil || res.LoadProof.State != LoadUnchecked ||
		res.LoadProof.Note != "Whether nginx took the reload up could not be checked: no running nginx reads nginx.conf." {
		t.Fatalf("result = %+v proof = %+v", res, res.LoadProof)
	}
}

// The engine's own reload is watched the same way, and a refused one is an
// error carrying nginx's words rather than a reload that "worked".
func TestEngineReloadIsProvenFromTheMaster(t *testing.T) {
	t.Run("loaded", func(t *testing.T) {
		svc, root := provenSites(t)
		onReload(t, root, "touch '"+root+"/reloaded'")
		loadingNginx(t, root, []int32{12, 13}, nil)
		res, err := svc.Reload(context.Background(), KindNginx)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Reloaded || res.LoadProof == nil || res.LoadProof.State != LoadLoaded || res.LoadProof.Listening != nil {
			t.Fatalf("result = %+v proof = %+v", res, res.LoadProof)
		}
	})
	t.Run("refused", func(t *testing.T) {
		svc, root := provenSites(t)
		onReload(t, root, fmt.Sprintf(`echo '2026/10/09 10:00:00 [emerg] 1#1: cannot load certificate "/etc/ssl/gone.pem"' >> '%[1]s/error.log'`, root))
		loadingNginx(t, root, []int32{10, 11}, nil)
		res, err := svc.Reload(context.Background(), KindNginx)
		if !errors.Is(err, ErrLoadRefused) || !strings.Contains(err.Error(), `cannot load certificate "/etc/ssl/gone.pem"`) {
			t.Fatalf("got %v, want the refusal", err)
		}
		if res.Reloaded || res.LoadProof == nil || res.LoadProof.State != LoadRefused {
			t.Fatalf("result = %+v proof = %+v", res, res.LoadProof)
		}
	})
}

func TestSiteBindsReadEveryServerBlock(t *testing.T) {
	content := "server {\n listen 80;\n listen [::]:80;\n server_name a;\n}\n" +
		"server {\n listen 127.0.0.1:8443 ssl;\n server_name a;\n}\n" +
		"server {\n server_name b;\n}\n"
	got := siteBinds("/etc/nginx/sites-available/a", content)
	want := []bind{{"0.0.0.0", 80, false}, {"::", 80, false}, {"127.0.0.1", 8443, false}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("binds = %v, want %v", got, want)
	}
}
