// Package selfcfg is the dashboard's own runtime configuration: the ports it
// listens on, the address it answers for, who may reach it, and the two
// buttons that put a change into effect.
//
// Everything here operates on one file — the `.env` beside the compose file in
// the checkout this dashboard was installed from — and on the stack that reads
// it. That is deliberate. The alternative was a second source of truth in the
// database, which would have meant an operator editing .env over ssh and a
// dashboard confidently reporting something else. There is one file, the
// dashboard writes it the same way a person would, and `docker compose up -d`
// is what makes it real.
//
// The dangerous part is not the writing, it is that applying a change restarts
// the process serving the request that asked for it — the same problem
// internal/selfupdate solves, solved the same way: a sibling container carries
// out the work, writes its progress to a file both halves can read, and puts
// the old configuration back if the new one does not come up.
package selfcfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// restoreEnvFile uses paths visible to this process, which may differ from the
// host paths passed to the sibling. Admission remains held until this finishes.
func restoreEnvFile(path, backup string) error {
	if backup == "" {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	body, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".env.jd-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// EnvFile is a parsed .env, kept as lines rather than as a map.
//
// The whole file is retained, comments included, because this one is written
// by install.sh with a paragraph of explanation above nearly every setting and
// then edited by hand for years afterwards. A writer that round-tripped
// through a map would hand the operator back a stripped file the first time
// they changed a port, which is a rude thing to do to somebody's server.
type EnvFile struct {
	path  string
	lines []string
}

// LoadEnv reads a .env. A missing file is not an error: an install that has
// never had one still has settings, they are just all defaults.
func LoadEnv(path string) (*EnvFile, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &EnvFile{path: path}, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(b), "\n")
	f := &EnvFile{path: path}
	if text != "" {
		f.lines = strings.Split(text, "\n")
	}
	return f, nil
}

func (f *EnvFile) Path() string { return f.path }

// Get returns the value recorded for key, or "" when it is absent. Quotes are
// stripped: compose accepts both forms and an operator may well have used
// either.
func (f *EnvFile) Get(key string) string {
	for _, line := range f.lines {
		k, v, ok := splitAssignment(line)
		if ok && k == key {
			return v
		}
	}
	return ""
}

// All returns every assignment in the file.
func (f *EnvFile) All() map[string]string {
	out := map[string]string{}
	for _, line := range f.lines {
		if k, v, ok := splitAssignment(line); ok {
			out[k] = v
		}
	}
	return out
}

// Set records key=value, replacing the assignment in place when it is already
// there and appending it when it is not. In place matters: the comment above a
// setting explains it, and a writer that deleted and re-appended would walk
// every explanation away from the thing it explains.
func (f *EnvFile) Set(key, value string) {
	line := key + "=" + value
	for i, existing := range f.lines {
		if k, _, ok := splitAssignment(existing); ok && k == key {
			f.lines[i] = line
			return
		}
	}
	f.lines = append(f.lines, line)
}

// Apply sets every pair, in a stable order so a diff of the file reads the
// same way twice.
func (f *EnvFile) Apply(values map[string]string) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.Set(k, values[k])
	}
}

// Save writes the file back, atomically, and returns the path of the backup it
// took first.
//
// The backup is the reason this is safe to offer at all: the sibling that
// applies the change restores it when the new configuration does not come up,
// so the worst outcome of a bad edit is a dashboard that went away for a
// minute and came back as it was.
func (f *EnvFile) Save() (string, error) {
	dir := filepath.Dir(f.path)
	var backup string
	if current, err := os.ReadFile(f.path); err == nil {
		backup = f.path + ".jd-previous"
		if err := os.WriteFile(backup, current, 0o600); err != nil {
			return "", fmt.Errorf("back up %s: %w", f.path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	body := strings.Join(f.lines, "\n") + "\n"
	tmp := filepath.Join(dir, ".env.jd-tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// The file holds JD_MASTER_KEY. Compose reads it as root and nothing else
	// has any business doing so, whatever umask this process inherited.
	if err := os.Chmod(f.path, 0o600); err != nil {
		return backup, err
	}
	return backup, nil
}

// splitAssignment parses one line of a .env, ignoring comments and blanks.
func splitAssignment(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "export ")
	k, v, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	if k == "" {
		return "", "", false
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return k, v, true
}
