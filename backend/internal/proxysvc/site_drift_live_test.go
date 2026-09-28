package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Against the host's real nginx: a hand edit the form has no field for does
// something, the save the form would make drops it, and the lines the form
// offers to move into its extra configuration go on doing what they did
// there. The one it does not offer to move stays dropped, as it said.
func TestLiveMovedLinesKeepWhatTheyDid(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	tenants := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenants <- r.Header.Get("X-Tenant")
		fmt.Fprint(w, "app")
	}))
	defer upstream.Close()
	spec := plainSpec("app", "app.test")
	spec.Upstream = upstream.URL
	first, second := freePort(t), freePort(t)
	content := installSite(t, root, spec, first)

	// Text and a variable in one value is what the headers section cannot
	// hold, so both headers stay the file's; $is_args is empty on "/".
	edited := strings.Replace(content, "    server_name app.test;\n",
		fmt.Sprintf("    server_name app.test;\n    listen 127.0.0.1:%d;\n    add_header X-Moved yes$is_args always;\n", second), 1)
	edited = strings.Replace(edited, "        proxy_set_header X-Forwarded-Host  $host;\n",
		"        proxy_set_header X-Forwarded-Host  $host;\n        proxy_set_header X-Tenant acme$is_args;\n", 1)
	full := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(full, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := runValidator(context.Background(), "nginx", "-t"); !v.Valid {
		t.Fatalf("the hand-edited site failed its test:\n%s", v.Output)
	}

	dropped, err := service.SiteDrift("app", full, edited, nil)
	if err != nil {
		t.Fatal(err)
	}
	var moved []string
	for _, d := range dropped {
		if d.Movable {
			moved = append(moved, d.Text)
		}
	}
	// The test's own listen on its first port is one of them: the form
	// writes listen 80 there, which is why it is the one line swapped back
	// below.
	want := []string{fmt.Sprintf("listen 127.0.0.1:%d;", first), fmt.Sprintf("listen 127.0.0.1:%d;", second), "add_header X-Moved yes$is_args always;"}
	if strings.Join(moved, "|") != strings.Join(want, "|") || len(dropped) != 4 {
		t.Fatalf("dropped %+v", dropped)
	}

	nginxAnswers := func(port int, wantMoved string, wantTenant string) {
		t.Helper()
		response, body := siteGet(t, port, "app.test", "/")
		if body != "app" || response.Header.Get("X-Moved") != wantMoved {
			t.Fatalf("port %d answered %q with X-Moved %q, want %q", port, body, response.Header.Get("X-Moved"), wantMoved)
		}
		select {
		case got := <-tenants:
			if got != wantTenant {
				t.Fatalf("the application was sent X-Tenant %q, want %q", got, wantTenant)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the request never reached the application")
		}
	}
	startNginx(t, root)
	nginxAnswers(first, "yes", "acme")
	nginxAnswers(second, "yes", "acme")

	// The save with the movable lines moved: what the form writes, with only
	// its own port-80 listens left out, since this test cannot bind 80.
	read, _ := ParseSiteSpec("app", edited)
	read.Custom = strings.Join(moved, "\n")
	if left, err := service.SiteDrift("app", full, edited, read); err != nil || len(left) != 1 || !strings.Contains(left[0].Text, "X-Tenant") {
		t.Fatalf("after the move: %+v %v", left, err)
	}
	saved, err := RenderNginx(read)
	if err != nil {
		t.Fatal(err)
	}
	saved = strings.ReplaceAll(saved, "    listen 80;\n", "")
	saved = strings.ReplaceAll(saved, "    listen [::]:80;\n", "")
	if err := os.WriteFile(full, []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := service.Reload(context.Background(), KindNginx); err != nil {
		t.Fatalf("the saved site did not reload: %v %+v", err, res)
	}
	// nginx swaps workers on a reload; the old ones may answer once more.
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, _ := siteGet(t, second, "app.test", "/")
		got := <-tenants
		if got == "" && response.Header.Get("X-Moved") == "yes" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the save: X-Tenant %q, X-Moved %q", got, response.Header.Get("X-Moved"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	nginxAnswers(first, "yes", "")
}

// An include the form's own TLS settings repeat is not offered as a move,
// because nginx refuses the result; the line beside it is, and nginx takes it.
func TestLiveAnIncludeThatRepeatsTheFormsTLSStaysOut(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	certPEM, keyPEM, _ := selfSigned(t, "app.test", time.Now().Add(24*time.Hour))
	certPath, keyPath := filepath.Join(root, "app.pem"), filepath.Join(root, "app.key")
	for path, content := range map[string]string{certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The shape of certbot's options-ssl-nginx.conf.
	options := filepath.Join(root, "options-ssl-nginx.conf")
	if err := os.WriteFile(options, []byte("ssl_session_cache shared:le_nginx_SSL:10m;\nssl_session_timeout 1440m;\nssl_session_tickets off;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	hand := fmt.Sprintf(`server {
    listen 127.0.0.1:%d ssl;
    server_name app.test;
    ssl_certificate %s;
    ssl_certificate_key %s;
    include %s;
    ssl_buffer_size 4k;
    access_log off;
    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
`, port, certPath, keyPath, options)
	full := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(full, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(full, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	if v := runValidator(context.Background(), "nginx", "-t"); !v.Valid {
		t.Fatalf("the hand-written site failed its test:\n%s", v.Output)
	}
	dropped, err := service.SiteDrift("app", full, hand, nil)
	if err != nil {
		t.Fatal(err)
	}
	var include, buffer *DroppedLine
	for i := range dropped {
		switch {
		case strings.HasPrefix(dropped[i].Text, "include"):
			include = &dropped[i]
		case strings.HasPrefix(dropped[i].Text, "ssl_buffer_size"):
			buffer = &dropped[i]
		}
	}
	if include == nil || include.Movable || !strings.Contains(include.Reason, "ssl_session_") || buffer == nil || !buffer.Movable {
		t.Fatalf("dropped %+v", dropped)
	}
	test := func(custom string) *ValidationResult {
		t.Helper()
		read, _ := ParseSiteSpec("app", hand)
		read.Custom = custom + fmt.Sprintf("\nlisten 127.0.0.1:%d ssl;", port)
		saved, err := RenderNginx(read)
		if err != nil {
			t.Fatal(err)
		}
		for _, own := range []string{"    listen 80;\n", "    listen [::]:80;\n", "    listen 443 ssl;\n", "    listen [::]:443 ssl;\n"} {
			saved = strings.ReplaceAll(saved, own, "")
		}
		if err := os.WriteFile(full, []byte(saved), 0o644); err != nil {
			t.Fatal(err)
		}
		return runValidator(context.Background(), "nginx", "-t")
	}
	if v := test(buffer.Text); !v.Valid {
		t.Fatalf("the movable line was refused:\n%s", v.Output)
	}
	if v := test(buffer.Text + "\n" + include.Text); v.Valid || !strings.Contains(v.Output, "is duplicate") {
		t.Fatalf("the include moved as well passed its test:\n%s", v.Output)
	}
}
