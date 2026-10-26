package backups

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestParseRcloneRemoteKeepsStorageAndRefusesWhatReachesThisServer(t *testing.T) {
	remote, err := ParseRcloneRemote(`[gdrive]
type = drive
# a comment
scope = drive
token = {"access_token":"ya29.x","token_type":"Bearer","refresh_token":"1//r","expiry":"2026-10-26T10:00:00Z"}
`)
	if err != nil || remote.Type != "drive" || len(remote.Options) != 2 {
		t.Fatalf("drive remote = %+v, %v", remote, err)
	}
	rendered := remote.render()
	if !strings.HasPrefix(rendered, "[target]\ntype = drive\n") || !strings.Contains(rendered, `refresh_token":"1//r"`) {
		t.Fatalf("rendered = %q", rendered)
	}
	if remote, err := ParseRcloneRemote("type = webdav\nurl = https://dav.example.test/remote.php/dav/files/ops\nvendor = nextcloud\nuser = ops\npass = obscured\n"); err != nil || remote.Type != "webdav" {
		t.Fatalf("a section header is optional: %+v, %v", remote, err)
	}
	refused := map[string]string{
		"type = local\n":                                       "not a storage service",
		"type = alias\nremote = /etc\n":                        "not a storage service",
		"type = crypt\nremote = /root\npassword = x\n":         "not a storage service",
		"type = union\nupstreams = /a /b\n":                    "not a storage service",
		"type = sftp\nhost = h\nkey_file = /root/.ssh/id\n":    "read a file or run a program",
		"type = sftp\nhost = h\nssh = ssh -o ProxyCommand=x\n": "read a file or run a program",
		"type = drive\nservice_account_file = /etc/shadow\n":   "read a file or run a program",
		"type = s3\nenv_auth = true\n":                         "dashboard's own environment",
		"type = drive\n[second]\ntype = s3\n":                  "more than one",
		"[first]\ntype = drive\n[second]\ntype = s3\n":         "more than one",
		"scope = drive\n":                                      "no type",
		"type = somethingelse\n":                               "not one the dashboard",
		"type = drive\ntoken\n":                                "not key = value",
		"type = drive\nscope = a\nscope = b\n":                 "set twice",
		"type = drive\nBAD-KEY = x\n":                          "not an rclone option name",
	}
	for config, want := range refused {
		if _, err := ParseRcloneRemote(config); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseRcloneRemote(%q) = %v, want %q", config, err, want)
		}
	}
	// env_auth spelled out as off is harmless.
	if _, err := ParseRcloneRemote("type = s3\nenv_auth = false\naccess_key_id = a\nsecret_access_key = b\n"); err != nil {
		t.Fatalf("env_auth = false: %v", err)
	}
}

func TestValidateSFTPAndRcloneDestinations(t *testing.T) {
	base := func(kind TargetKind, target TargetConfig) *Job {
		return &Job{Name: "j", Sources: []string{"/srv"}, TargetKind: kind, Target: target}
	}
	ok := base(TargetSFTP, TargetConfig{Host: " backup.example.test ", Username: "ops", Path: "/srv/backups"})
	if err := ok.Validate(); err != nil || ok.Target.Port != 22 || ok.Target.Host != "backup.example.test" {
		t.Fatalf("sftp = %+v, %v", ok.Target, err)
	}
	for _, target := range []TargetConfig{
		{Host: "sftp://backup.example.test", Username: "ops", Path: "/b"},
		{Host: "backup.example.test/x", Username: "ops", Path: "/b"},
		{Host: "backup.example.test", Username: "", Path: "/b"},
		{Host: "backup.example.test", Username: "ops@x", Path: "/b"},
		{Host: "backup.example.test", Username: "ops", Path: ""},
		{Host: "backup.example.test", Port: 70000, Username: "ops", Path: "/b"},
		{Host: "backup.example.test", Username: "ops", Path: "/b\nx"},
	} {
		if err := base(TargetSFTP, target).Validate(); err == nil {
			t.Errorf("accepted %+v", target)
		}
	}
	if err := base(TargetSFTP, TargetConfig{Host: "2001:db8::7", Username: "ops", Path: "b"}).Validate(); err != nil {
		t.Fatalf("an IPv6 address is a host: %v", err)
	}
	rclone := base(TargetRclone, TargetConfig{Path: "/backups/nightly/"})
	if err := rclone.Validate(); err != nil || rclone.Target.Path != "backups/nightly" {
		t.Fatalf("rclone = %+v, %v", rclone.Target, err)
	}
}

func TestPrepareRemoteTargetCarriesWhatAnEditDoesNotRepeat(t *testing.T) {
	signer := testHostSigner(t)
	pinned := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	current := &Job{TargetKind: TargetSFTP, HasCredentials: true, Target: TargetConfig{Host: "backup.example.test", Port: 22, Username: "ops", Path: "/b", HostKey: pinned}}

	// A new SFTP destination cannot work without a way to sign in.
	if err := prepareRemoteTarget(&Job{TargetKind: TargetSFTP, Target: current.Target}, nil, nil); err == nil {
		t.Fatal("an SFTP job was created without credentials")
	}
	if err := prepareRemoteTarget(&Job{TargetKind: TargetSFTP, Target: TargetConfig{Host: "h", Username: "u", Path: "p"}}, &TargetSecrets{PrivateKey: "not a key"}, nil); err == nil {
		t.Fatal("an unreadable private key was accepted")
	}
	// An edit of the same server keeps its pin and its credentials.
	edit := &Job{TargetKind: TargetSFTP, Target: TargetConfig{Host: "BACKUP.example.test", Port: 22, Username: "ops", Path: "/b2"}}
	if err := prepareRemoteTarget(edit, nil, current); err != nil || edit.Target.HostKey != pinned || edit.Target.HostKeyFingerprint != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatalf("same server = %+v, %v", edit.Target, err)
	}
	// Another server, or an explicit forget, is pinned afresh.
	moved := &Job{TargetKind: TargetSFTP, Target: TargetConfig{Host: "other.example.test", Port: 22, Username: "ops", Path: "/b"}}
	if err := prepareRemoteTarget(moved, nil, current); err != nil || moved.Target.HostKey != "" {
		t.Fatalf("other server = %+v, %v", moved.Target, err)
	}
	forget := &Job{TargetKind: TargetSFTP, ForgetHostKey: true, Target: current.Target}
	if err := prepareRemoteTarget(forget, nil, current); err != nil || forget.Target.HostKey != "" || forget.Target.HostKeyFingerprint != "" {
		t.Fatalf("forget = %+v, %v", forget.Target, err)
	}
	// Switching to SFTP from another kind needs credentials for it.
	s3 := &Job{TargetKind: TargetS3, HasCredentials: true}
	if err := prepareRemoteTarget(&Job{TargetKind: TargetSFTP, Target: current.Target}, nil, s3); err == nil {
		t.Fatal("switching to SFTP kept the S3 keys as its credentials")
	}

	// rclone: the configuration names the service, and an edit without one
	// keeps it.
	created := &Job{TargetKind: TargetRclone}
	if err := prepareRemoteTarget(created, &TargetSecrets{RcloneConfig: "type = onedrive\ndrive_type = personal\n"}, nil); err != nil || created.Target.RemoteType != "onedrive" {
		t.Fatalf("rclone create = %+v, %v", created.Target, err)
	}
	if err := prepareRemoteTarget(&Job{TargetKind: TargetRclone}, nil, nil); err == nil {
		t.Fatal("an rclone job was created without a configuration")
	}
	kept := &Job{TargetKind: TargetRclone}
	if err := prepareRemoteTarget(kept, nil, &Job{TargetKind: TargetRclone, HasCredentials: true, Target: TargetConfig{RemoteType: "onedrive"}}); err != nil || kept.Target.RemoteType != "onedrive" {
		t.Fatalf("rclone edit = %+v, %v", kept.Target, err)
	}
	if err := prepareRemoteTarget(&Job{TargetKind: TargetRclone}, &TargetSecrets{RcloneConfig: "type = local\n"}, nil); err == nil {
		t.Fatal("a local rclone remote was accepted")
	}
}

// testSFTPServer is an SSH server on loopback that offers the sftp subsystem
// over a directory, the way OpenSSH's internal-sftp does.
type testSFTPServer struct {
	addr     string
	host     ssh.Signer
	extra    ssh.Signer
	root     string
	password string
	mu       sync.Mutex
	logins   int
}

func testHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func newTestSFTPServer(t *testing.T, host ssh.Signer, clientKey ssh.PublicKey) *testSFTPServer {
	t.Helper()
	server := &testSFTPServer{host: host, root: t.TempDir(), password: "correct horse"}
	config := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == "ops" && string(password) == server.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if clientKey != nil && meta.User() == "ops" && string(key.Marshal()) == string(clientKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	config.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server.addr = listener.Addr().String()
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(raw, config)
		}
	}()
	return server
}

func (s *testSFTPServer) serve(raw net.Conn, config *ssh.ServerConfig) {
	if s.extra != nil {
		// A second host key, as OpenSSH servers hold one of each kind.
		withExtra := *config
		withExtra.AddHostKey(s.extra)
		config = &withExtra
	}
	conn, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		_ = raw.Close()
		return
	}
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	for request := range channels {
		if request.ChannelType() != "session" {
			_ = request.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		channel, sessionRequests, err := request.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range sessionRequests {
				ok := r.Type == "subsystem" && len(r.Payload) > 4 && string(r.Payload[4:]) == "sftp"
				_ = r.Reply(ok, nil)
				if ok {
					server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.root))
					if err == nil {
						_ = server.Serve()
					}
					_ = channel.Close()
				}
			}
		}()
	}
}

func (s *testSFTPServer) job(t *testing.T) *Job {
	t.Helper()
	host, port, _ := net.SplitHostPort(s.addr)
	job := &Job{ID: 1, Name: "sftp", Sources: []string{"/srv"}, TargetKind: TargetSFTP,
		Target: TargetConfig{Host: host, Username: "ops", Path: "nightly/app"}}
	job.Target.Port = mustAtoi(t, port)
	if err := job.Validate(); err != nil {
		t.Fatal(err)
	}
	return job
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type recordedPins struct {
	mu   sync.Mutex
	keys []string
}

func (p *recordedPins) pin(_ context.Context, key ssh.PublicKey) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, ssh.FingerprintSHA256(key))
	return nil
}

func TestSFTPTransfersPinTheServerAndRefuseAnother(t *testing.T) {
	ctx := context.Background()
	host := testHostSigner(t)
	server := newTestSFTPServer(t, host, nil)
	job := server.job(t)
	secrets := &TargetSecrets{Password: "correct horse"}
	pins := &recordedPins{}

	if err := TestTarget(ctx, job, secrets, pins.pin); err != nil {
		t.Fatal(err)
	}
	if len(pins.keys) != 1 || pins.keys[0] != ssh.FingerprintSHA256(host.PublicKey()) || job.Target.HostKeyFingerprint != pins.keys[0] {
		t.Fatalf("pins = %v, target = %+v", pins.keys, job.Target)
	}
	if entries, _ := os.ReadDir(filepath.Join(server.root, "nightly/app")); len(entries) != 0 {
		t.Fatalf("the probe was left behind: %v", entries)
	}

	local := filepath.Join(t.TempDir(), "archive.tar.gz")
	payload := strings.Repeat("backup bytes ", 200000)
	if err := os.WriteFile(local, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	key := remoteKey(job, "app-20261026.tar.gz")
	if key != "nightly/app/app-20261026.tar.gz" {
		t.Fatalf("key = %q", key)
	}
	if err := uploadArtifact(ctx, job, secrets, pins.pin, key, local); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(server.root, key))
	if err != nil || string(stored) != payload {
		t.Fatalf("stored %d bytes, %v", len(stored), err)
	}
	if _, err := os.Stat(filepath.Join(server.root, key+".partial")); !os.IsNotExist(err) {
		t.Fatalf("the partial upload was left: %v", err)
	}
	back := filepath.Join(t.TempDir(), "back")
	if err := downloadArtifact(ctx, job, secrets, pins.pin, key, back); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(back); string(got) != payload {
		t.Fatal("the download differs from the upload")
	}
	if err := deleteArtifact(ctx, job, secrets, pins.pin, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(server.root, key)); !os.IsNotExist(err) {
		t.Fatal("the archive was not deleted")
	}
	if len(pins.keys) != 1 {
		t.Fatalf("a pinned server was pinned again: %v", pins.keys)
	}

	// The same address answering with another key is refused before any
	// password is sent.
	impostor := newTestSFTPServer(t, testHostSigner(t), nil)
	moved := *job
	_, port, _ := net.SplitHostPort(impostor.addr)
	moved.Target.Port = mustAtoi(t, port)
	err = TestTarget(ctx, &moved, secrets, pins.pin)
	if !errors.Is(err, ErrHostKeyChanged) || !strings.Contains(err.Error(), job.Target.HostKeyFingerprint) {
		t.Fatalf("impostor = %v", err)
	}
	impostor.mu.Lock()
	logins := impostor.logins
	impostor.mu.Unlock()
	if logins != 0 {
		t.Fatal("credentials reached a server whose key did not match")
	}

	// A wrong password pins nothing.
	fresh := server.job(t)
	freshPins := &recordedPins{}
	if err := TestTarget(ctx, fresh, &TargetSecrets{Password: "wrong"}, freshPins.pin); err == nil || len(freshPins.keys) != 0 {
		t.Fatalf("wrong password = %v, pins %v", err, freshPins.keys)
	}
	// Nor is an unpinned server reached when nothing may pin it.
	if err := TestTarget(ctx, server.job(t), secrets, nil); err == nil || !strings.Contains(err.Error(), "never pinned") {
		t.Fatalf("unpinnable = %v", err)
	}
}

func TestSFTPSignsInWithAKey(t *testing.T) {
	ctx := context.Background()
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(clientPrivate, "", []byte("key passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, _ := ssh.NewSignerFromKey(clientPrivate)
	server := newTestSFTPServer(t, testHostSigner(t), clientSigner.PublicKey())
	job := server.job(t)
	encoded := string(pem.EncodeToMemory(block))
	pins := &recordedPins{}
	if err := TestTarget(ctx, job, &TargetSecrets{PrivateKey: encoded}, pins.pin); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("a protected key without its passphrase = %v", err)
	}
	if err := TestTarget(ctx, job, &TargetSecrets{PrivateKey: encoded, Passphrase: "key passphrase"}, pins.pin); err != nil {
		t.Fatal(err)
	}
}

// transferJob reads an archive from the server its run wrote it to, with the
// key pinned since, and never pins another server on the job's behalf.
func TestTransferJobUsesThePinForTheSameServerOnly(t *testing.T) {
	store := &Store{}
	current := &Job{ID: 4, TargetKind: TargetSFTP, Target: TargetConfig{Host: "backup.example.test", Port: 22, Username: "ops", Path: "/b", HostKey: "ssh-ed25519 AAAA", HostKeyFingerprint: "SHA256:x"}}
	first := &Run{Manifest: &Manifest{Version: 1, TargetKind: TargetSFTP, Target: TargetConfig{Host: "backup.example.test", Port: 22, Username: "ops", Path: "/b"}}}
	job, pin := store.transferJob(current, first)
	if job.Target.HostKey != "ssh-ed25519 AAAA" || pin == nil {
		t.Fatalf("same server = %+v, pin %v", job.Target, pin != nil)
	}
	elsewhere := &Run{Manifest: &Manifest{Version: 1, TargetKind: TargetSFTP, Target: TargetConfig{Host: "old.example.test", Port: 22, Username: "ops", Path: "/b"}}}
	job, pin = store.transferJob(current, elsewhere)
	if job.Target.HostKey != "" || pin != nil {
		t.Fatalf("another server = %+v, pin %v", job.Target, pin != nil)
	}
}

// A server holding an RSA and an Ed25519 key is pinned to the Ed25519 one,
// and one pinned to its RSA key earlier is still reached with it.
func TestSFTPPinsTheStrongestKeyAndAsksForThePinnedKind(t *testing.T) {
	ctx := context.Background()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, _ := ssh.NewSignerFromKey(rsaKey)
	ed := testHostSigner(t)
	server := newTestSFTPServer(t, rsaSigner, nil)
	server.extra = ed
	job := server.job(t)
	secrets := &TargetSecrets{Password: "correct horse"}
	pins := &recordedPins{}
	if err := TestTarget(ctx, job, secrets, pins.pin); err != nil {
		t.Fatal(err)
	}
	if job.Target.HostKeyFingerprint != ssh.FingerprintSHA256(ed.PublicKey()) {
		t.Fatalf("pinned %s, want the Ed25519 key", job.Target.HostKeyFingerprint)
	}
	older := server.job(t)
	older.Target.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(rsaSigner.PublicKey())))
	if err := TestTarget(ctx, older, secrets, pins.pin); err != nil {
		t.Fatalf("a server pinned to its RSA key was refused: %v", err)
	}
}
