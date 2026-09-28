package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Against the real nginx: a list is tested with every site that includes it,
// an edit one of them cannot take is refused in the list's own file and put
// back, and nginx still loads afterwards.
func TestLiveAccessListIsTestedWithEverySiteThatIncludesIt(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	if _, err := svc.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	writeFile(t, available("app"), "server {\n    listen 127.0.0.1:18191;\n    server_name app.test;\n    include jd-access/office.conf;\n}\n")
	writeFile(t, available("docs"), "server {\n    listen 127.0.0.1:18191;\n    server_name docs.test;\n    auth_basic \"Docs\";\n    auth_basic_user_file "+
		filepath.Join(root, "jd-auth", "staging")+";\n    include jd-access/office.conf;\n}\n")
	if _, err := svc.SaveAccessList(ctx, "office", AccessListSpec{Allow: []string{"127.0.0.1"}}, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "docs"} {
		if err := svc.SetVHostEnabled(ctx, name, true); err != nil {
			t.Fatalf("enabling %s: %v", name, err)
		}
	}
	list := filepath.Join(root, "jd-access", "office.conf")
	before, _ := os.ReadFile(list)

	_, err := svc.SaveAccessList(ctx, "office", AccessListSpec{Allow: []string{"127.0.0.1"}, AuthFile: "staging"}, true)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("a password docs already has: %v", err)
	}
	if want := `nginx refuses office where a site includes it: "auth_basic" directive is duplicate in ` + list + ":"; !strings.HasPrefix(refused.Reason(), want) {
		t.Errorf("reason = %q\nwant it to start %q", refused.Reason(), want)
	}
	if after, _ := os.ReadFile(list); string(after) != string(before) {
		t.Errorf("the refused list stayed:\n%s", after)
	}
	if res := svc.Test(ctx, KindNginx); !res.Valid {
		t.Fatalf("nginx no longer loads after a refused list:\n%s", res.Output)
	}
	lists, err := svc.ListAccessLists()
	if err != nil || len(lists) != 1 || len(lists[0].UsedBy) != 2 || !lists[0].UsedBy[0].Enabled || !lists[0].UsedBy[1].Enabled {
		t.Fatalf("lists = %+v, %v", lists, err)
	}
	if err := svc.DeleteAccessList(ctx, "office"); err == nil {
		t.Fatal("a list two sites include was deleted")
	}
}

// Against a running nginx: each save reaches the site that includes the list
// at the next request — an allowed address, a denied one, a password, and
// what "either one" really lets in.
func TestLiveAccessListDecidesWhoGetsIn(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	if _, err := svc.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	www := filepath.Join(root, "www")
	writeFile(t, filepath.Join(www, "index.html"), "hello")
	writeFile(t, filepath.Join(www, ".env"), "secret")
	// The list at the server's level, a static root, and the site form's
	// own fence around dotfiles: a location that denies everyone.
	writeFile(t, filepath.Join(root, "sites-available", "lists"), fmt.Sprintf(`server {
    listen 127.0.0.1:%d;
    server_name lists.test;
    root %s;
    include jd-access/office.conf;
    location / {
        try_files $uri $uri/index.html =404;
    }
    location ~ /\.env {
        deny all;
    }
}
`, port, www))

	save := func(spec AccessListSpec) {
		t.Helper()
		res, err := svc.SaveAccessList(ctx, "office", spec, true)
		if err != nil {
			t.Fatalf("saving %+v: %v", spec, err)
		}
		if res.Reload != nil && res.Reload.Err != nil {
			t.Fatalf("reloading after %+v: %v", spec, res.Reload.Err)
		}
	}
	if _, err := svc.SaveAccessList(ctx, "office", AccessListSpec{Allow: []string{"127.0.0.1"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetVHostEnabled(ctx, "lists", true); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	daemon := exec.Command("nginx", "-g", "daemon off;")
	daemon.Stdout, daemon.Stderr = &output, &output
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Signal(syscall.SIGTERM)
		_ = daemon.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})

	client := &http.Client{Timeout: 2 * time.Second}
	get := func(path string, login bool) (int, string) {
		request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		request.Host = "lists.test"
		if login {
			request.SetBasicAuth("admin", "correcthorsebattery")
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, err.Error()
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	// nginx answers `-s reload` before its new workers take over, so each
	// expectation waits for them.
	expect := func(what, path string, login bool, want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, body := get(path, login)
			if code == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: GET %s answered %d %q, want %d", what, path, code, body, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	expect("an allowed address", "/", false, http.StatusOK)

	save(AccessListSpec{Deny: []string{"127.0.0.1"}})
	expect("a denied address", "/", false, http.StatusForbidden)

	save(AccessListSpec{Allow: []string{"10.0.0.0/8"}, AuthFile: "staging", Satisfy: "any"})
	expect("either one, with neither", "/", false, http.StatusUnauthorized)
	expect("either one, with the password", "/", true, http.StatusOK)
	// What the form warns about: the password also opens a path the site
	// refuses everyone with deny all.
	expect("either one, a denied path with the password", "/.env", true, http.StatusOK)

	save(AccessListSpec{Allow: []string{"10.0.0.0/8"}, AuthFile: "staging", Satisfy: "all"})
	expect("both, with only the password", "/", true, http.StatusForbidden)

	save(AccessListSpec{Allow: []string{"127.0.0.1"}, AuthFile: "staging", Satisfy: "all"})
	expect("both, with no password", "/", false, http.StatusUnauthorized)
	expect("both, with both", "/", true, http.StatusOK)
	expect("both, a denied path", "/.env", true, http.StatusForbidden)

	save(AccessListSpec{Allow: []string{"::1"}})
	expect("an IPv4 client against an IPv6-only allow list", "/", false, http.StatusForbidden)
}
