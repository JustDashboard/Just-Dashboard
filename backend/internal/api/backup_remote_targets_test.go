package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"golang.org/x/crypto/ssh"
)

// SFTP and rclone destinations through the API: credentials are required to
// create one and never come back, an edit keeps the pinned host key of the
// same server and drops it on request, an rclone remote that would reach
// this server is refused, and point-in-time recovery does not take either.
func TestBackupJobAPISFTPAndRcloneDestinations(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "remote-target-admin", auth.RoleAdmin)}
	source := filepath.Join(s.Cfg.FileRoots[0], "remote-source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	create := func(body map[string]any) (int, string) {
		t.Helper()
		request := map[string]any{"name": body["name"], "sources": []string{source}, "schedule": "0 3 * * *", "retention": 3, "enabled": true}
		for k, v := range body {
			request[k] = v
		}
		encoded, _ := json.Marshal(request)
		response := admin.do(http.MethodPost, "/api/v1/backups/", string(encoded), nil)
		return response.Code, response.Body.String()
	}
	sftpTarget := map[string]any{"host": "backup.example.test", "username": "ops", "path": "/srv/backups"}

	if code, body := create(map[string]any{"name": "no-credentials", "targetKind": "sftp", "target": sftpTarget}); code != http.StatusBadRequest || !strings.Contains(body, "password or a private key") {
		t.Fatalf("SFTP without credentials = %d %s", code, body)
	}
	code, body := create(map[string]any{"name": "sftp", "targetKind": "sftp", "target": sftpTarget,
		"secrets": map[string]string{"password": "fixture-sftp-password"}})
	if code != http.StatusCreated || strings.Contains(body, "fixture-sftp-password") {
		t.Fatalf("SFTP create = %d %s", code, body)
	}
	var job backups.Job
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		t.Fatal(err)
	}
	if job.Target.Port != 22 || !job.HasCredentials || !job.Encrypt {
		t.Fatalf("created = %+v", job)
	}

	// The first contact pins the server; an edit of the same server keeps
	// it, and asking to forget it drops it.
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	if err := s.modules.backupStore.PinHostKey(t.Context(), job.ID, signer.PublicKey()); err != nil {
		t.Fatal(err)
	}
	pinned, _ := s.modules.backupStore.Get(t.Context(), job.ID)
	edit := requestForBackupJob(pinned)
	edit.Target.HostKey, edit.Target.HostKeyFingerprint = "", ""
	edit.Target.Path = "/srv/backups/app"
	encoded, _ := json.Marshal(edit)
	path := fmt.Sprintf("/api/v1/backups/%d", job.ID)
	response := admin.do(http.MethodPut, path, string(encoded), nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), ssh.FingerprintSHA256(signer.PublicKey())) {
		t.Fatalf("edit of the same server = %d %s", response.Code, response.Body.String())
	}
	secrets, err := s.modules.backupStore.Secrets(t.Context(), job.ID)
	if err != nil || secrets.Password != "fixture-sftp-password" {
		t.Fatalf("an edit without credentials replaced them: %+v %v", secrets, err)
	}
	edited, _ := s.modules.backupStore.Get(t.Context(), job.ID)
	forget := requestForBackupJob(edited)
	forget.ForgetHostKey = true
	encoded, _ = json.Marshal(forget)
	response = admin.do(http.MethodPut, path, string(encoded), nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "hostKeyFingerprint") {
		t.Fatalf("forget = %d %s", response.Code, response.Body.String())
	}

	// rclone: the remote's service is named from its configuration, and a
	// remote that would write to this server is refused.
	code, body = create(map[string]any{"name": "rclone", "targetKind": "rclone", "target": map[string]string{"path": "backups"},
		"secrets": map[string]string{"rcloneConfig": "[dropbox]\ntype = dropbox\ntoken = {\"access_token\":\"fixture-token\"}\n"}})
	if code != http.StatusCreated || !strings.Contains(body, `"remoteType":"dropbox"`) || strings.Contains(body, "fixture-token") {
		t.Fatalf("rclone create = %d %s", code, body)
	}
	for _, config := range []string{"type = local\n", "type = alias\nremote = /etc\n", "type = sftp\nhost = h\nkey_file = /root/.ssh/id_ed25519\n"} {
		if code, body := create(map[string]any{"name": "refused", "targetKind": "rclone", "target": map[string]string{"path": "x"},
			"secrets": map[string]string{"rcloneConfig": config}}); code != http.StatusBadRequest {
			t.Fatalf("rclone %q = %d %s", config, code, body)
		}
	}

	// Point-in-time recovery cannot borrow either destination.
	var pitrErr error
	_, _, _, pitrErr = s.pitrDestination(t.Context(), pitrSetupRequest{FromJob: job.ID})
	if pitrErr == nil || !strings.Contains(pitrErr.Error(), "local, s3, b2") {
		t.Fatalf("PITR from an SFTP job = %v", pitrErr)
	}
}
