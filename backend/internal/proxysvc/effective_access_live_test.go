package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// What the explanation predicts for 127.0.0.1 is what the host's nginx
// binary answers 127.0.0.1 on a private prefix: let through, refused by a
// deny ahead of an allow, asked for a password when satisfy any's addresses
// do not hold it, and let through without one when they do.
func TestLiveAccessExplanationAgreesWithNginx(t *testing.T) {
	root := liveNginx(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "app") }))
	defer app.Close()
	port := freePort(t)
	users := filepath.Join(root, "users")
	if err := os.WriteFile(users, []byte("admin:$2y$05$uHFmtB0b4lV4q6Y5x0mZ3uC3o3HtqQpIJQ4uE0lL3tE7rD9dQ8Vb2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := fmt.Sprintf(`server {
    listen 127.0.0.1:%[1]d;
    server_name access.test;
    location /open/ { proxy_pass %[2]s; }
    location /closed/ { deny 127.0.0.1; allow all; proxy_pass %[2]s; }
    location /office/ { satisfy any; allow 10.0.0.0/8; deny all; auth_basic "Office"; auth_basic_user_file %[3]s; proxy_pass %[2]s; }
    location /local/ { satisfy any; allow 127.0.0.0/8; deny all; auth_basic "Office"; auth_basic_user_file %[3]s; proxy_pass %[2]s; }
}
`, port, app.URL, users)
	if err := os.WriteFile(filepath.Join(root, "sites-enabled", "access"), []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	startNginx(t, root)
	files, err := New(root, filepath.Join(root, "Caddyfile")).EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		verdict string
		status  int
	}{
		{"/open/", AccessAdmitted, http.StatusOK},
		{"/closed/", AccessRefused, http.StatusForbidden},
		{"/office/", AccessCredentials, http.StatusUnauthorized},
		{"/local/", AccessAdmitted, http.StatusOK},
	} {
		explained, err := ExplainAccess(tree, fmt.Sprintf("http://access.test:%d%s", port, tc.path), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		response, _ := siteGet(t, port, "access.test", tc.path)
		if explained.Verdict != tc.verdict || response.StatusCode != tc.status {
			t.Errorf("%s: explained %s (%s), nginx answered %d; want %s and %d",
				tc.path, explained.Verdict, explained.Summary, response.StatusCode, tc.verdict, tc.status)
		}
	}
}
