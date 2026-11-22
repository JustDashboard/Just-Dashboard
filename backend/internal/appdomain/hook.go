package appdomain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// The certificate hook's credential.
//
// certbot runs `jd app-domain challenge add|remove` as root, on the host or
// in the backend's container, and on the host's renewal timer with nobody
// signed in. The hook reaches this process on its own loopback listener and
// shows the token in this file, which the backend writes 0600 into its data
// directory: whoever can read it is root on the server already. The token
// opens one route, which publishes or withdraws a challenge value for this
// server's own handle and does nothing else. Only its SHA-256 is stored.

// HookFile is the credential's name in the data directory.
const HookFile = "app-domain-hook.json"

// hookKey holds the token's SHA-256.
const hookKey = "app.domain.hook"

// HookCredential is the file's content, as jd reads it.
type HookCredential struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// HookPath is where the credential is kept.
func (s *Service) HookPath() string { return filepath.Join(s.dataDir, HookFile) }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ensureHook writes the credential unless the file already holds a token
// whose hash is stored and the address this process listens on.
func (s *Service) ensureHook(ctx context.Context) error {
	if s.dataDir == "" || s.backendURL == "" {
		return errors.New("the hook's credential has nowhere to be written")
	}
	stored := ""
	err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, hookKey).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw, err := os.ReadFile(s.HookPath()); err == nil {
		var cur HookCredential
		if json.Unmarshal(raw, &cur) == nil && cur.URL == s.backendURL && cur.Token != "" &&
			stored != "" && subtle.ConstantTimeCompare([]byte(hashToken(cur.Token)), []byte(stored)) == 1 {
			return nil
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	cred := HookCredential{URL: s.backendURL, Token: "jdh_" + base64.RawURLEncoding.EncodeToString(secret)}
	raw, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	// The hash first: a file whose token is not stored yet is refused, and
	// a stored hash with no file yet is a hook that has nothing to show.
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, hookKey, hashToken(cred.Token)); err != nil {
		return err
	}
	return writePrivate(s.HookPath(), append(raw, '\n'))
}

// writePrivate replaces path in one step with a file that is 0600 from the
// moment it exists.
func writePrivate(path string, content []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(content); err == nil {
		err = f.Chmod(0o600)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// CheckHookToken reports whether token is the hook's.
func (s *Service) CheckHookToken(ctx context.Context, token string) bool {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "jdh_") {
		return false
	}
	stored := ""
	if err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, hookKey).Scan(&stored); err != nil || stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(stored)) == 1
}

// BackendURL is where a loopback client reaches a listener at addr (JD_ADDR):
// the address itself, or loopback for a wildcard one.
func BackendURL(addr string) string {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return ""
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

func splitHostPort(addr string) (string, string, error) { return net.SplitHostPort(addr) }
