package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// storedConfig is what jd login remembers. The key is a credential for the
// dashboard, so the file holding it is the user's alone.
type storedConfig struct {
	URL string `json:"url"`
	Key string `json:"key"`
}

// configPath is where the URL and key are kept: JD_CONFIG if it is set,
// otherwise jd/config.json in the user's config directory. The environment
// is read through env rather than os.Getenv so a test can point it anywhere.
func configPath(env func(string) string) (path string, ours bool, err error) {
	if p := env("JD_CONFIG"); p != "" {
		return p, false, nil
	}
	dir := ""
	switch runtime.GOOS {
	case "darwin", "windows", "ios", "plan9":
		dir, err = os.UserConfigDir()
	default:
		// The same rule as os.UserConfigDir, read from env.
		if xdg := env("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
			dir = xdg
		} else if home := env("HOME"); home != "" {
			dir = filepath.Join(home, ".config")
		} else {
			dir, err = os.UserConfigDir()
		}
	}
	if err != nil {
		return "", false, fmt.Errorf("no config directory to keep the key in (%v); set JD_CONFIG, or JD_URL and JD_KEY", err)
	}
	return filepath.Join(dir, "jd", "config.json"), true, nil
}

// loadConfig reads the stored URL and key. A missing file is no error: the
// caller decides whether it needs one. A file others can read still works,
// with a warning, because refusing would strand the person who copied it
// with the wrong mode and the fix is theirs to make.
func loadConfig(path string, warn io.Writer) (*storedConfig, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(warn, "jd: warning: %s can be read by other accounts on this machine; run chmod 600 %s\n", path, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c storedConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s is not a jd config file: %v", path, err)
	}
	return &c, nil
}

// saveConfig writes the file in one step, so a reader never meets half a key,
// and with mode 0600 from the moment it exists: os.CreateTemp creates 0600,
// and the chmod only makes that explicit against an unusual umask.
func saveConfig(path string, ours bool, c storedConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Only jd's own directory is narrowed. A JD_CONFIG beside other files
	// is somebody else's directory to set.
	if ours {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(append(raw, '\n')); err == nil {
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

// credentials are the URL and key a command uses: the environment's when it
// names them, which is how CI passes a secret without a file, else the
// stored ones.
func (a *app) credentials() (string, string, error) {
	url, key := strings.TrimSpace(a.env("JD_URL")), strings.TrimSpace(a.env("JD_KEY"))
	if url == "" || key == "" {
		path, _, err := configPath(a.env)
		if err != nil {
			return "", "", err
		}
		stored, err := loadConfig(path, a.stderr)
		if err != nil {
			return "", "", err
		}
		if stored != nil {
			if url == "" {
				url = stored.URL
			}
			if key == "" {
				key = stored.Key
			}
		}
	}
	if url == "" || key == "" {
		return "", "", &notLoggedIn{"not logged in: run jd login https://your-dashboard --key vpsd_…, or set JD_URL and JD_KEY"}
	}
	return url, key, nil
}

// api is the client for the stored or given credentials, made once.
func (a *app) api() (*client, error) {
	if a.client != nil {
		return a.client, nil
	}
	url, key, err := a.credentials()
	if err != nil {
		return nil, err
	}
	c, err := a.newClient(url, key)
	if err != nil {
		return nil, err
	}
	a.client = c
	return c, nil
}

func (a *app) login(args []string) error {
	set := a.newFlags("login", "login URL --key KEY")
	key := set.String("key", "", "the API key; - reads it from stdin")
	positional, err := a.parse(set, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usagef("usage: jd login https://your-dashboard --key vpsd_… (or --key - to read it from stdin)")
	}
	secret := *key
	switch secret {
	case "":
		// Without a terminal library the key cannot be read with echo off,
		// and a key typed visibly is a key on somebody's screen.
		return usagef("give the key with --key, or --key - and pipe it in, so it stays out of your shell history")
	case "-":
		raw, err := io.ReadAll(io.LimitReader(a.stdin, 4096))
		if err != nil {
			return err
		}
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		return usagef("the key is empty")
	}
	c, err := a.newClient(positional[0], secret)
	if err != nil {
		return err
	}
	// The deployment list is what a deploy key reaches; the session route
	// is not, so this is the one request that proves the key does its job.
	var fleet fleetModel
	if _, err := c.getJSON("/deploy/", query("view", "fleet"), &fleet); err != nil {
		var api *apiError
		if errors.As(err, &api) && api.Code == "key_scope" {
			return fmt.Errorf("this key cannot reach the deployments (%s); make a deploy key under Account → API keys", api.Message)
		}
		return err
	}
	path, ours, err := configPath(a.env)
	if err != nil {
		return err
	}
	if err := saveConfig(path, ours, storedConfig{URL: c.base, Key: secret}); err != nil {
		return fmt.Errorf("the key works, but it could not be saved: %v", err)
	}
	a.say("Logged in to %s with key %s; %d %s visible. Saved to %s.", c.base, maskKey(secret),
		len(fleet.Deployments), plural(len(fleet.Deployments), "deployment", "deployments"), path)
	return nil
}

func (a *app) logout(args []string) error {
	set := a.newFlags("logout", "logout")
	if _, err := a.parse(set, args); err != nil {
		return err
	}
	path, _, err := configPath(a.env)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, fs.ErrNotExist) {
		a.say("Not logged in; nothing to forget.")
		return nil
	} else if err != nil {
		return err
	}
	a.say("Forgot the dashboard and key stored in %s. The key itself still works until it is revoked in the dashboard.", path)
	return nil
}

// maskKey is the part of a key it is safe to print: the prefix the dashboard
// itself shows in its list of keys.
func maskKey(key string) string {
	const shown = len("vpsd_") + 8
	if len(key) <= shown {
		return "…"
	}
	return key[:shown] + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
