package backups

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Two destinations beside local disk and S3: an SFTP server, and any of the
// storage services rclone speaks (Google Drive, OneDrive, Dropbox, WebDAV,
// pCloud, Azure Blob and the rest). Both are reached only during a transfer,
// with credentials opened for that transfer and nowhere else.

// HostKeyPinner records the host key an SFTP server presented the first time
// it was reached, so every later connection can insist on the same one.
type HostKeyPinner func(ctx context.Context, key ssh.PublicKey) error

// ErrHostKeyChanged is an SFTP server presenting a key other than the one
// pinned for it: a reinstalled server, or somebody in the middle.
var ErrHostKeyChanged = errors.New("the SFTP server's host key changed")

var sftpHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func validSFTPHost(host string) bool {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return true
	}
	return sftpHostRe.MatchString(host)
}

func validRemotePath(p string) bool {
	return !strings.ContainsAny(p, "\x00\r\n") && len(p) <= 1024
}

// sameSFTPServer compares two destinations' servers: the host without regard
// to case, and the port with 22 for none.
func sameSFTPServer(a, b TargetConfig) bool {
	port := func(t TargetConfig) int {
		if t.Port == 0 {
			return 22
		}
		return t.Port
	}
	return strings.EqualFold(strings.Trim(a.Host, "[]"), strings.Trim(b.Host, "[]")) && port(a) == port(b)
}

func (t TargetConfig) sftpAddress() string {
	port := t.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(strings.Trim(t.Host, "[]"), strconv.Itoa(port))
}

// FingerprintHostKey is the form ssh-keygen -l and every SFTP client show.
func FingerprintHostKey(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

func sftpAuth(secrets *TargetSecrets) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if secrets != nil && strings.TrimSpace(secrets.PrivateKey) != "" {
		var signer ssh.Signer
		var err error
		if secrets.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(secrets.PrivateKey), []byte(secrets.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(secrets.PrivateKey))
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, errors.New("the private key is protected by a passphrase; enter it with the key")
			}
			return nil, fmt.Errorf("the private key could not be read: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if secrets != nil && secrets.Password != "" {
		password := secrets.Password
		methods = append(methods, ssh.Password(password),
			// Servers that only offer keyboard-interactive still ask for the
			// same password, one prompt at a time.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}))
	}
	if len(methods) == 0 {
		return nil, errors.New("an SFTP destination needs a password or a private key")
	}
	return methods, nil
}

// hostKeyAlgorithms asks for the pinned key's own kind, the way OpenSSH asks
// for the kind known_hosts holds, so a server holding several keys always
// answers with the pinned one. A server not yet pinned is asked for its
// strongest, which is also the fingerprint an operator usually compares.
func hostKeyAlgorithms(pinned ssh.PublicKey) []string {
	if pinned == nil {
		return []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	if pinned.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{pinned.Type()}
}

// sftpSession is one connection, closed with its client.
type sftpSession struct {
	*sftp.Client
	conn *ssh.Client
	stop func() bool
}

func (s *sftpSession) Close() {
	s.stop()
	_ = s.Client.Close()
	_ = s.conn.Close()
}

// dialSFTP connects with the pinned host key, or pins the key presented when
// none is pinned yet. The pin is recorded only after the server has accepted
// the credentials, so a server that cannot be signed in to pins nothing.
func dialSFTP(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner) (*sftpSession, error) {
	auth, err := sftpAuth(secrets)
	if err != nil {
		return nil, err
	}
	var pinned ssh.PublicKey
	if job.Target.HostKey != "" {
		pinned, _, _, _, err = ssh.ParseAuthorizedKey([]byte(job.Target.HostKey))
		if err != nil {
			return nil, fmt.Errorf("the pinned host key is malformed; edit the destination to pin the server again")
		}
	} else if pin == nil {
		return nil, fmt.Errorf("the host key of %s was never pinned, and the job no longer names that server", job.Target.sftpAddress())
	}
	var presented ssh.PublicKey
	config := &ssh.ClientConfig{
		User:              job.Target.Username,
		Auth:              auth,
		HostKeyAlgorithms: hostKeyAlgorithms(pinned),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			presented = key
			if pinned == nil {
				return nil
			}
			if !bytes.Equal(key.Marshal(), pinned.Marshal()) {
				return fmt.Errorf("%w: pinned %s, now %s. If the server was reinstalled, edit the job and turn on Pin the server again",
					ErrHostKeyChanged, FingerprintHostKey(pinned), FingerprintHostKey(key))
			}
			return nil
		},
		Timeout: 30 * time.Second,
	}
	address := job.Target.sftpAddress()
	dialer := net.Dialer{Timeout: 30 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", address, err)
	}
	// The handshake has its own deadline; a server that accepts the socket
	// and says nothing must not hold a scheduled run for hours.
	_ = raw.SetDeadline(time.Now().Add(45 * time.Second))
	conn, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		_ = raw.Close()
		if errors.Is(err, ErrHostKeyChanged) {
			return nil, err
		}
		return nil, fmt.Errorf("SFTP sign-in to %s as %s failed: %w", address, job.Target.Username, err)
	}
	_ = raw.SetDeadline(time.Time{})
	client := ssh.NewClient(conn, channels, requests)
	if pinned == nil && presented != nil && pin != nil {
		if err := pin(ctx, presented); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("could not pin the server's host key: %w", err)
		}
		job.Target.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(presented)))
		job.Target.HostKeyFingerprint = FingerprintHostKey(presented)
	}
	files, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true), sftp.MaxConcurrentRequestsPerFile(64))
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%s does not offer SFTP: %w", address, err)
	}
	// Cancelling the run closes the connection, which is the only way to
	// interrupt a transfer pkg/sftp is in the middle of.
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	return &sftpSession{Client: files, conn: client, stop: stop}, nil
}

func sftpUpload(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key, localPath string) error {
	session, err := dialSFTP(ctx, job, secrets, pin)
	if err != nil {
		return err
	}
	defer session.Close()
	if dir := path.Dir(key); dir != "." && dir != "/" {
		if err := session.MkdirAll(dir); err != nil {
			return fmt.Errorf("cannot create %s on the server: %w", dir, err)
		}
	}
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()
	info, err := local.Stat()
	if err != nil {
		return err
	}
	// Written under a temporary name and renamed once complete, so an
	// interrupted upload never leaves something that looks like a backup.
	partial := key + ".partial"
	remote, err := session.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("cannot write %s on the server: %w", partial, err)
	}
	if _, err := remote.ReadFrom(local); err != nil {
		_ = remote.Close()
		_ = session.Remove(partial)
		return fmt.Errorf("upload of %s failed: %w", key, err)
	}
	if err := remote.Close(); err != nil {
		_ = session.Remove(partial)
		return fmt.Errorf("upload of %s failed: %w", key, err)
	}
	stat, err := session.Stat(partial)
	if err != nil || stat.Size() != info.Size() {
		_ = session.Remove(partial)
		return fmt.Errorf("the server holds a different size for %s than was sent", key)
	}
	if err := session.PosixRename(partial, key); err != nil {
		// Without the posix-rename extension a rename cannot replace, and
		// an archive name is unique to its run, so a plain rename is enough.
		if err := session.Rename(partial, key); err != nil {
			_ = session.Remove(partial)
			return fmt.Errorf("could not move %s into place: %w", key, err)
		}
	}
	return nil
}

func sftpDownload(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key, destPath string) error {
	session, err := dialSFTP(ctx, job, secrets, pin)
	if err != nil {
		return err
	}
	defer session.Close()
	remote, err := session.Open(key)
	if err != nil {
		return fmt.Errorf("download of %s failed: %w", key, err)
	}
	defer remote.Close()
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := remote.WriteTo(out); err != nil {
		return fmt.Errorf("download of %s failed: %w", key, err)
	}
	return nil
}

func sftpDelete(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key string) error {
	session, err := dialSFTP(ctx, job, secrets, pin)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sftpTest(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner) error {
	session, err := dialSFTP(ctx, job, secrets, pin)
	if err != nil {
		return err
	}
	defer session.Close()
	dir := job.Target.Path
	if err := session.MkdirAll(dir); err != nil {
		return fmt.Errorf("cannot create %s on the server: %w", dir, err)
	}
	probe := path.Join(dir, ".vpsd-probe-"+randomSuffix())
	file, err := session.Create(probe)
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	_, writeErr := file.Write([]byte("probe"))
	_ = file.Close()
	_ = session.Remove(probe)
	if writeErr != nil {
		return fmt.Errorf("%s is not writable: %w", dir, writeErr)
	}
	return nil
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// The rclone backends a destination may name. Each stores data somewhere
// else; none reads or writes this server's own disk. "local" would, and so
// would the wrappers (alias, crypt, union, combine, chunker, hasher, compress,
// cache), which accept a bare path as the remote they wrap.
var rcloneTypes = []string{
	"azureblob", "azurefiles", "b2", "box", "drive", "dropbox", "fichier", "filefabric", "filescom",
	"ftp", "gofile", "google cloud storage", "gcs", "hidrive", "http", "iclouddrive", "internetarchive",
	"jottacloud", "koofr", "linkbox", "mailru", "mega", "netstorage", "onedrive", "opendrive", "oracleobjectstorage",
	"pcloud", "pikpak", "pixeldrain", "premiumizeme", "protondrive", "putio", "qingstor", "quatrix", "s3",
	"seafile", "sftp", "sharefile", "sia", "smb", "storj", "sugarsync", "swift", "ulozto", "webdav",
	"yandex", "zoho",
}

var rcloneKeyRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// RcloneRemote is a remote's configuration as an operator pastes it from
// `rclone config show`, vetted. Only the one remote is kept, under the name
// the dashboard gives it.
type RcloneRemote struct {
	Type    string
	Options [][2]string
}

// ParseRcloneRemote reads one remote's section. It refuses what would let a
// destination reach this server rather than a storage service: the local
// backend and the wrappers, options naming a file on this server, an
// external command, and credentials taken from the dashboard's own
// environment.
func ParseRcloneRemote(text string) (*RcloneRemote, error) {
	if len(text) > 64<<10 {
		return nil, errors.New("the rclone configuration is longer than 64 KiB")
	}
	remote := &RcloneRemote{}
	sections, options := 0, 0
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64<<10), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// A header after options starts a second remote even when the
			// first had none of its own.
			sections++
			if sections > 1 || options > 0 {
				return nil, errors.New("paste one remote: the configuration holds more than one [section]")
			}
			continue
		}
		options++
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("the line %q is not key = value", clipLine(line))
		}
		key, value = strings.TrimSpace(strings.ToLower(key)), strings.TrimSpace(value)
		if !rcloneKeyRe.MatchString(key) {
			return nil, fmt.Errorf("%q is not an rclone option name", clipLine(key))
		}
		if key == "type" {
			if remote.Type != "" {
				return nil, errors.New("the option type is set twice")
			}
			remote.Type = strings.ToLower(value)
			continue
		}
		switch {
		case key == "env_auth":
			if v := strings.ToLower(value); v != "" && v != "false" {
				return nil, errors.New("env_auth would sign in with the dashboard's own environment; put the remote's keys in the configuration instead")
			}
		case strings.Contains(key, "file") || strings.Contains(key, "command") || key == "ssh" || strings.Contains(key, "socket"):
			return nil, fmt.Errorf("the option %s would read a file or run a program on this server; paste the key itself (for example key_pem) instead", key)
		}
		for _, existing := range remote.Options {
			if existing[0] == key {
				return nil, fmt.Errorf("the option %s is set twice", key)
			}
		}
		remote.Options = append(remote.Options, [2]string{key, value})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if remote.Type == "" {
		return nil, errors.New("the configuration has no type = line; paste the output of rclone config show <remote>")
	}
	if !slices.Contains(rcloneTypes, remote.Type) {
		if remote.Type == "local" || remote.Type == "alias" || remote.Type == "crypt" || remote.Type == "union" ||
			remote.Type == "combine" || remote.Type == "chunker" || remote.Type == "hasher" || remote.Type == "compress" || remote.Type == "cache" {
			return nil, fmt.Errorf("an rclone %s remote is not a storage service; back up to the storage it would wrap directly", remote.Type)
		}
		return nil, fmt.Errorf("rclone type %q is not one the dashboard sends backups to", remote.Type)
	}
	return remote, nil
}

func clipLine(line string) string {
	if len(line) > 60 {
		return line[:60] + "…"
	}
	return line
}

// render writes the remote as rclone's configuration file, under the name
// "target".
func (r *RcloneRemote) render() string {
	var b strings.Builder
	b.WriteString("[target]\ntype = " + r.Type + "\n")
	for _, option := range r.Options {
		b.WriteString(option[0] + " = " + option[1] + "\n")
	}
	return b.String()
}

// rcloneRun runs one rclone command against the job's remote, with its
// configuration in a private file that exists only for the command, and
// nothing from the dashboard's own environment that rclone would read.
func rcloneRun(ctx context.Context, secrets *TargetSecrets, stdin io.Reader, args ...string) (string, error) {
	if secrets == nil || strings.TrimSpace(secrets.RcloneConfig) == "" {
		return "", errors.New("the rclone destination has no saved configuration")
	}
	remote, err := ParseRcloneRemote(secrets.RcloneConfig)
	if err != nil {
		return "", err
	}
	// The binary must be this server's own: a host fallback would run in
	// another mount namespace, where the configuration file below is absent.
	if _, err := exec.LookPath("rclone"); err != nil {
		return "", errors.New("rclone is not installed beside the dashboard")
	}
	dir, err := os.MkdirTemp("", "jd-rclone-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	config := filepath.Join(dir, "rclone.conf")
	if err := os.WriteFile(config, []byte(remote.render()), 0o600); err != nil {
		return "", err
	}
	argv := append([]string{
		"--config", config, "--cache-dir", filepath.Join(dir, "cache"),
		"--ask-password=false", "--stats", "0", "--retries", "3", "--low-level-retries", "10",
		"--contimeout", "30s", "--timeout", "10m", "--log-level", "ERROR",
	}, args...)
	command := hostexec.Command(ctx, "rclone", argv...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir}
	command.Stdin = stdin
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &limitedBuffer{buf: &stderr, limit: 16 << 10}
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if lines := strings.Split(message, "\n"); len(lines) > 3 {
			message = strings.Join(lines[len(lines)-3:], "\n")
		}
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("rclone: %s", message)
	}
	return stdout.String(), nil
}

type limitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func rcloneTarget(key string) string {
	return "target:" + key
}

func rcloneUpload(ctx context.Context, secrets *TargetSecrets, key, localPath string) error {
	_, err := rcloneRun(ctx, secrets, nil, "copyto", localPath, rcloneTarget(key))
	return err
}

func rcloneDownload(ctx context.Context, secrets *TargetSecrets, key, destPath string) error {
	_, err := rcloneRun(ctx, secrets, nil, "copyto", rcloneTarget(key), destPath)
	return err
}

func rcloneDelete(ctx context.Context, secrets *TargetSecrets, key string) error {
	_, err := rcloneRun(ctx, secrets, nil, "deletefile", rcloneTarget(key))
	return err
}

func rcloneTest(ctx context.Context, job *Job, secrets *TargetSecrets) error {
	if _, err := rcloneRun(ctx, secrets, nil, "mkdir", rcloneTarget(job.Target.Path)); err != nil {
		return err
	}
	probe := path.Join(job.Target.Path, ".vpsd-probe-"+randomSuffix())
	if _, err := rcloneRun(ctx, secrets, strings.NewReader("probe"), "rcat", rcloneTarget(probe)); err != nil {
		return err
	}
	_, err := rcloneRun(ctx, secrets, nil, "deletefile", rcloneTarget(probe))
	return err
}
