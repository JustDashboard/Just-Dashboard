package term

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed shell/bashrc shell/.zshenv shell/.zshrc
var shellFiles embed.FS

// SetupShell installs only bundled startup files beneath the existing shared,
// process-owned terminal root. No account dotfiles or global profiles are edited.
func (m *Manager) SetupShell() error {
	if err := m.clipboard.ensureRoot(); err != nil {
		return err
	}
	dir := filepath.Join(m.clipboard.root, ".shell")
	if err := newClipboardStore(dir).ensureRoot(); err != nil {
		return err
	}
	for _, name := range []string{"bashrc", ".zshenv", ".zshrc"} {
		data, err := shellFiles.ReadFile("shell/" + name)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".startup-")
		if err != nil {
			return err
		}
		temp := f.Name()
		if _, err = f.Write(data); err == nil {
			err = f.Chmod(0o644)
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(temp, filepath.Join(dir, name))
		}
		if err != nil {
			os.Remove(temp)
			return fmt.Errorf("install shell startup: %w", err)
		}
	}
	m.shellDir = dir
	return nil
}

// The first shell establishes the account's login environment; exec then starts
// its interactive editor with our final prompt hook. The snippets are constants,
// and shell/path are positional arguments, never interpolated shell source.
func (m *Manager) loginArgv(keepCWD bool) []string {
	return m.startupArgv(keepCWD, "", "")
}

func (m *Manager) validateAgent(agent string) error {
	if agent == "" {
		return nil
	}
	if agent != "codex" && agent != "claude" {
		return ErrUnknownAgent
	}
	shell := m.shell
	if shell == "" {
		shell = m.account.Shell
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	if m.shellDir == "" || (filepath.Base(shell) != "bash" && filepath.Base(shell) != "zsh") {
		return ErrAgentShellUnavailable
	}
	return nil
}

func (m *Manager) startupArgv(keepCWD bool, agent, cwd string) []string {
	shell := m.shell
	if shell == "" {
		shell = m.account.Shell
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	var bootstrap string
	switch filepath.Base(shell) {
	case "bash":
		bootstrap = `exec "$0" --rcfile "$1/bashrc" -i`
		if agent != "" {
			bootstrap = `export JD_TERMINAL_START_AGENT="$2" JD_TERMINAL_START_DIR="$3"; exec "$0" --rcfile "$1/bashrc" -i`
		}
	case "zsh":
		bootstrap = `export JD_ORIGINAL_ZDOTDIR="${ZDOTDIR:-$HOME}"; export ZDOTDIR="$1"; exec "$0" -i`
		if agent != "" {
			bootstrap = `export JD_ORIGINAL_ZDOTDIR="${ZDOTDIR:-$HOME}"; export ZDOTDIR="$1" JD_TERMINAL_START_AGENT="$2" JD_TERMINAL_START_DIR="$3"; exec "$0" -i`
		}
	default:
		return m.account.loginArgv(m.shell, keepCWD)
	}
	if m.shellDir == "" {
		return m.account.loginArgv(m.shell, keepCWD)
	}
	args := []string{shell, "-l"}
	if !m.account.IsRoot() && os.Geteuid() == 0 {
		args = []string{"su", "-s", shell}
		if !keepCWD {
			args = append(args, "-l")
		}
		args = append(args, m.account.Name, "--", "-l")
	}
	args = append(args, "-c", bootstrap, shell, m.shellDir)
	if agent != "" {
		args = append(args, agent, cwd)
	}
	return args
}
